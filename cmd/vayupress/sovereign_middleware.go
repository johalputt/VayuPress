// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// trustedSessionTTL bounds how long a validated (or rejected) operator session
// decision is cached, so the hot path does not run a SQLite session lookup for
// every cookie-bearing request. 30s is short enough that a logout revokes
// shield immunity almost immediately, and long enough that a logged-in operator
// browsing many public pages — or an attacker spamming a garbage session cookie
// — costs at most one DB read per token per window.
const trustedSessionTTL = 30 * time.Second

type trustedEntry struct {
	ok      bool
	expires time.Time
}

// trustedSessionCache is a tiny, bounded, mutex-guarded cache of session-token
// validation results keyed by the token's hash. It exists purely to keep the
// operator-immunity check off the SQLite read path under load.
type trustedSessionCache struct {
	mu sync.Mutex
	m  map[string]trustedEntry
}

func newTrustedSessionCache() *trustedSessionCache {
	return &trustedSessionCache{m: make(map[string]trustedEntry)}
}

func (c *trustedSessionCache) get(key string, now time.Time) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.After(e.expires) {
		return false, false
	}
	return e.ok, true
}

func (c *trustedSessionCache) put(key string, ok bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Bound memory: a flood of unique garbage tokens can't grow this without
	// limit. When the cap is hit, drop expired entries; if still full, reset —
	// correctness is unaffected (a reset just re-validates on the next request).
	if len(c.m) >= 8192 {
		for k, e := range c.m {
			if now.After(e.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= 8192 {
			c.m = make(map[string]trustedEntry, 1024)
		}
	}
	c.m[key] = trustedEntry{ok: ok, expires: now.Add(trustedSessionTTL)}
}

// sovereign_middleware.go — the request-side of the Aegis L0 Admin Sovereignty
// Lane. It mounts the lock-free sovereign.Gate (internal/vayushield/sovereign)
// as the very first middleware so that, under a volumetric flood, PUBLIC traffic
// is capped and the overflow is shed with a couple of atomic ops BEFORE any
// expensive work runs — leaving guaranteed CPU/goroutine headroom for the admin
// control plane (VayuOS, Save, refresh) and verified readers. This is the fix
// for "Save button + refresh die when a big bot hit lands".

// sovereignPrefixes are the paths that are ALWAYS priority (never shed): the
// admin/operator control plane, the API, health/metrics, and the shield's own
// endpoints. These mirror VayuShield's BypassPrefixes so the two lanes agree on
// what "control plane" means. A request under one of these — or one carrying a
// verified signed session — is admitted unconditionally and never consumes the
// public budget.
var sovereignPrefixes = []string{
	"/os",
	"/api",
	"/admin",
	"/debug",
	"/health",
	"/metrics",
	"/__vayushield",
	"/.well-known",
	// Machine-protocol endpoints (VayuMCP + OAuth, ADR-0139/0140): same
	// treatment as /api — authenticated/rate-limited traffic that must not be
	// shed with the anonymous public pool. Mirrors VayuShield's BypassPrefixes.
	"/mcp",
	"/oauth",
}

// isSovereignLane reports whether a request belongs to the always-admit priority
// lane: an admin/control-plane path, any visitor holding a verified signed
// session (a real logged-in reader), or a trusted operator (valid admin login
// session — so the operator's asset loads and public-page views keep working
// even while a flood saturates the public lane). The session checks are cheap:
// HMAC verification, and a header parse when no admin cookie is present.
func (a *App) isSovereignLane(r *http.Request) bool {
	p := r.URL.Path
	for _, pre := range sovereignPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	// robots.txt / sitemap.xml / feeds are priority (never shed). A 503 on
	// robots.txt pauses ALL crawling and a 503 on sitemap.xml drops URLs from the
	// index, so these machine endpoints must always reach the handler even under a
	// public-traffic flood. The set is small, cacheable and unspoofable (path
	// shape only), so priority-admitting it can never be abused to bypass the
	// public-lane cap. (The shield's own bypass already covers them one layer in;
	// this closes the gap at L0, which runs before the shield.)
	if vayushield.IsFeedLikePath(p) {
		return true
	}
	// The IndexNow key file belongs to the same class: a 503 there fails the
	// engine's key validation, which silently voids every URL submitted with that
	// key. It is a single exact path and a 32-byte response, so admitting it costs
	// nothing and cannot be used to bypass the public-lane cap.
	if a.isIndexNowKeyPath(r) {
		return true
	}
	if a.vayuShield != nil && a.vayuShield.HasVerifiedSession(r) {
		return true
	}
	return a.isTrustedOperator(r)
}

// isTrustedOperator reports whether the request carries a valid operator login
// session (the admin console cookie). Used by the L0 sovereignty lane and
// injected into VayuShield as TrustedFn, so a logged-in operator is
// structurally immune to every availability gate on every path — they can
// never be locked out of their own site, not even after jailing their own IP
// with a load test.
//
// Cheap on the flood path in three stages: (1) no session cookie → no work at
// all (real bots never carry one); (2) a recently-seen token → answered from
// the TTL cache with no DB access (bounds both a logged-in operator browsing
// many pages and adversarial cookie-spam to one lookup per token per 30s); (3)
// only a fresh, cookie-bearing token reaches the SQLite lookup — and that runs
// on the isolated public read pool, never the writer or the VayuOS admin pool.
func (a *App) isTrustedOperator(r *http.Request) bool {
	if a.sessions == nil {
		return false
	}
	token := auth.SessionTokenFromRequest(r)
	if token == "" {
		return false
	}
	now := time.Now()
	sum := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(sum[:])
	if a.trustedSessions != nil {
		if ok, hit := a.trustedSessions.get(key, now); hit {
			return ok
		}
	}
	_, err := a.sessions.Validate(r.Context(), token)
	ok := err == nil
	if a.trustedSessions != nil {
		a.trustedSessions.put(key, ok, now)
	}
	return ok
}

// sovereignMiddleware admits every request through the L0 gate. Priority
// (sovereign-lane) requests are always let through; public requests are admitted
// only while public concurrency is under the CPU-derived cap, and the overflow
// gets a cheap, cacheable 503 + Retry-After — no handler, no render, no SQLite.
// When the cap is not being approached this is a single atomic increment, so it
// adds no measurable latency to normal traffic.
func (a *App) sovereignMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.sovereign == nil {
			next.ServeHTTP(w, r)
			return
		}
		// The weight is only consulted for public traffic, and only when the
		// operator has actually written route rules — a priority request never
		// touches the public budget, and an install with no policy short-circuits
		// on an empty rule set.
		priority := a.isSovereignLane(r)
		cost := 1
		if !priority {
			cost = a.vayuShield.RouteCost(r)
		}
		release, ok := a.sovereign.AdmitCost(priority, cost)
		if !ok {
			// Public overflow during saturation. Shed cheaply so the CPU we save
			// keeps the admin plane and verified readers responsive. 503 (not a
			// hard block) with a short Retry-After tells well-behaved clients and
			// crawlers to come back — it does not harm SEO the way a 4xx would.
			w.Header().Set("Retry-After", "2")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Aegis", "shed")
			http.Error(w, "Server busy — please retry in a moment.", http.StatusServiceUnavailable)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}
