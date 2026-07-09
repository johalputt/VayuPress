package main

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/auth"
)

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
// with a load test. Cheap on the flood path: no cookie means no store lookup.
func (a *App) isTrustedOperator(r *http.Request) bool {
	if a.sessions == nil {
		return false
	}
	token := auth.SessionTokenFromRequest(r)
	if token == "" {
		return false
	}
	_, err := a.sessions.Validate(r.Context(), token)
	return err == nil
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
		release, ok := a.sovereign.Admit(a.isSovereignLane(r))
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
