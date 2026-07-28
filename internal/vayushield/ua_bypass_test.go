// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// Gate 0's crawler fast path used to treat "identity confirmed" and "identity
// unconfirmable" as the same verdict, returning `next.ServeHTTP; return` for
// both — ahead of the blocklist, the reputation jail, load shedding, fair
// shedding, the rate limiter and Sovereign Surge.
//
// BotUnprovable is reachable by claiming one of ~34 UA-only vendor strings from
// ANY address, by an in-flight reverse-DNS lookup, and — on a fresh boot, before
// the published-range feeds have loaded — by a plain "Googlebot" UA from any
// address. So any client could opt out of every gate in the shield by choosing a
// User-Agent, which is the one input an attacker fully controls.
//
// The exemption BotUnprovable legitimately needs is narrow: a non-JS crawler
// cannot solve a proof-of-work, and a search engine reads sustained non-200s as
// a crawl error. That argues for skipping CHALLENGES. It says nothing about
// availability gates, which answer 429/503 with Retry-After — a signal real
// crawlers honour and impostors gain nothing from.

// rateLimitedManager builds a shield with a per-IP rate limit tight enough to
// trip deterministically, and a UA-driven verdict.
func rateLimitedManager() *Manager {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return "203.0.113.9:1" },
		VerifiedBotFn: func(_ netip.Addr, ua string) BotFastPath {
			switch {
			case strings.Contains(ua, "verified"):
				return BotVerified
			case strings.Contains(ua, "unprovable"):
				return BotUnprovable
			case strings.Contains(ua, "spoof"):
				return BotSpoof
			default:
				return BotNotRecognised
			}
		},
	})
	m.ApplySettings(Settings{
		Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8,
		RateLimit: true, RatePerMinute: 1, Burst: 2,
	})
	return m
}

// hammer sends n requests with the given UA and reports how many were throttled.
func hammer(m *Manager, ua string, n int) (throttled int) {
	h := m.Middleware(okHandler())
	for i := 0; i < n; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, uaReq(ua))
		if rr.Code == http.StatusTooManyRequests || rr.Code == http.StatusServiceUnavailable {
			throttled++
		}
	}
	return throttled
}

// TestUnprovableCrawlerIsRateLimited is the regression test for the bypass. A UA
// string must not buy exemption from the rate limiter.
func TestUnprovableCrawlerIsRateLimited(t *testing.T) {
	m := rateLimitedManager()
	if got := hammer(m, "Mozilla/5.0 (compatible; Googlebot/2.1; unprovable)", 40); got == 0 {
		t.Error("an unconfirmable crawler UA was never throttled in 40 requests — " +
			"choosing a User-Agent still opts a client out of the rate limiter")
	}
}

// TestVerifiedCrawlerKeepsTheFullFastPath — the SEO guarantee that justifies the
// path at all must survive the fix. A confirmed crawler's identity is a fact
// about the network, not a claim in a string, so it is served regardless.
func TestVerifiedCrawlerKeepsTheFullFastPath(t *testing.T) {
	m := rateLimitedManager()
	if got := hammer(m, "Mozilla/5.0 (compatible; Googlebot/2.1; verified)", 40); got != 0 {
		t.Errorf("an IP-verified crawler was throttled %d times in 40 — the fast path regressed", got)
	}
}

// TestUnprovableCrawlerUnderSurgeGetsABoundedBackoff documents the tradeoff
// rather than pretending it away, because the comment at vayushield.go:1004
// records that serving crawlers a 503 is "the exact mechanism that de-indexed
// the site" — a real incident, not a hypothetical.
//
// The resolution is in the same file at :247: a *sustained multi-day* 503
// de-indexes even with Retry-After. Surge is not sustained — a forced surge
// auto-expires at maxForcedSurge specifically so a forgotten toggle cannot serve
// 503s forever, and an automatic surge relaxes when the flood does. So an
// unconfirmable crawler meeting an active flood should get a bounded 503 that
// tells it when to come back, which every real crawler honours.
//
// What it must NOT get is a 200 bought with a User-Agent string. That is the
// bypass; capacity pressure is real whoever is asking.
func TestUnprovableCrawlerUnderSurgeGetsABoundedBackoff(t *testing.T) {
	m := verifiedBotManager(true) // surge on
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq("Mozilla/5.0 (compatible; Googlebot/2.1; unprovable)"))

	if rr.Code == http.StatusOK {
		t.Fatal("an unconfirmable crawler was served during an active flood — a UA string still buys capacity")
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Errorf("status %d carries no Retry-After — without it a crawler cannot tell a transient "+
			"flood from a dead site, which is what turns shedding into de-indexing", rr.Code)
	}
}

// TestUnprovableCrawlerUAIsStillServedWithoutAChallenge pins the SEO half — and
// pins it at the place it actually happens.
//
// The first version of this fix added a "challenge exempt" marker at gate 0 and
// honoured it in Decide. A probe showed that code changed no outcome for any
// User-Agent: it was dead. The exemption a crawler needs already exists further
// down, in Decide's TypeGoodBot branch, and it is better placed there — it
// belongs to "looks like a crawler", a classification question, not to "we could
// not verify it", an identity question. The dead code was removed rather than
// left with a comment describing behaviour it did not produce.
func TestUnprovableCrawlerUAIsStillServedWithoutAChallenge(t *testing.T) {
	m := verifiedBotManager(false) // no surge: isolate the challenge decision
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq("Mozilla/5.0 (compatible; Googlebot/2.1; unprovable)"))
	if rr.Code != http.StatusOK {
		t.Errorf("an unconfirmable crawler UA got %d with no flood in progress — handing a "+
			"proof-of-work to a client that cannot run JavaScript is how a site de-indexes itself", rr.Code)
	}
}

// TestSpoofSuspectGetsNoExemption — a UA claiming a crawler from an address that
// is demonstrably not the vendor's earns nothing at all, not even the challenge
// exemption. It is the one case we can positively disprove.
func TestSpoofSuspectGetsNoExemption(t *testing.T) {
	m := verifiedBotManager(true)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq("Mozilla/5.0 (compatible; Googlebot/2.1; spoof)"))
	if rr.Code == http.StatusOK {
		t.Error("a demonstrably spoofed crawler UA was served under surge")
	}
}

// TestUnprovableCrawlerStillBlockableOnScore — an unconfirmable client that is
// not crawler-shaped stays fully blockable. "We could not verify it" must never
// become a reason to serve something that has earned a block on its behaviour.
func TestUnprovableCrawlerStillBlockableOnScore(t *testing.T) {
	m := New(Config{
		Enabled:       true,
		Signer:        challenge.NewSigner([]byte("s")),
		ClientIP:      func(r *http.Request) string { return "203.0.113.9:1" },
		VerifiedBotFn: func(_ netip.Addr, _ string) BotFastPath { return BotUnprovable },
	})
	// Block everything scoring above zero, so any classified client is blocked.
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.0, JSThreshold: 0.0, BlockThreshold: 0.0})

	// A UA that is NOT a recognised good bot. A crawler-shaped UA would be
	// classified TypeGoodBot and allowed by Decide well before the exemption is
	// consulted, so testing with one would prove nothing about this code path.
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq("python-requests/2.31"))
	if rr.Code == http.StatusOK {
		t.Error("the challenge exemption was allowed to override a block verdict")
	}
}
