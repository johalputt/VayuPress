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

// verifiedBotManager builds a shield whose VerifiedBotFn is driven by the UA:
// a UA containing "verified" is IP-verified (allow), one containing "spoof" is a
// spoof suspect, anything else is not a recognised crawler.
func verifiedBotManager(surge bool) *Manager {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return "203.0.113.9:1" },
		VerifiedBotFn: func(_ netip.Addr, ua string) (bool, bool) {
			switch {
			case strings.Contains(ua, "verified"):
				return true, false
			case strings.Contains(ua, "spoof"):
				return false, true
			default:
				return false, false
			}
		},
	})
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8, Surge: surge})
	return m
}

func uaReq(ua string) *http.Request {
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", ua)
	return req
}

// TestVerifiedBotFastPathUnderSurge: an IP-verified crawler is served content
// even under surge, exactly like the UA-only Phase-1 path but spoof-proof.
func TestVerifiedBotFastPathUnderSurge(t *testing.T) {
	m := verifiedBotManager(true)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq("Mozilla/5.0 (compatible; Googlebot/2.1; verified)"))
	if rr.Code != 200 {
		t.Fatalf("IP-verified crawler must be served under surge, got %d", rr.Code)
	}
}

// TestSpoofSuspectDowngraded: a UA claiming a crawler from an IP that is NOT the
// vendor's must NOT be auto-allowed — it falls to behavioural scoring and, being
// a static good-bot score (0.7) with no session, is challenged rather than
// served. This is the spoof-proofing: a fake "Googlebot" gets no free pass.
func TestSpoofSuspectDowngraded(t *testing.T) {
	m := verifiedBotManager(false) // no surge — isolate the Decide downgrade
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq("Mozilla/5.0 (compatible; Googlebot/2.1; spoof)"))
	if rr.Code == 200 {
		t.Fatal("a spoofed Googlebot UA (unverified IP) must not be auto-allowed")
	}
}

// TestVerifiedBotBeatsSpoofToken: the allow path wins even if the UA also mentions
// a challenge-y token — verification is authoritative, not substring heuristics.
func TestVerifiedBotNotACrawlerFallsThrough(t *testing.T) {
	m := verifiedBotManager(false)
	// A plain browser: VerifiedBotFn returns (false,false) -> normal scoring -> allow.
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq(realBrowserUA))
	if rr.Code != 200 {
		t.Fatalf("a normal browser must pass normally, got %d", rr.Code)
	}
}

// TestSpoofSuspectHonouredOnlyForGoodBotUA: the spoof flag only strips the
// good-bot allow; a spoof flag on a request the scorer would allow anyway (human)
// still passes — the downgrade can never turn a real human away.
func TestSpoofDoesNotBlockHuman(t *testing.T) {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return "203.0.113.9:1" },
		// Force spoof for everything, including the human browser UA.
		VerifiedBotFn: func(_ netip.Addr, _ string) (bool, bool) { return false, true },
	})
	m.ApplySettings(Settings{Enabled: true})
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, uaReq(realBrowserUA))
	if rr.Code != 200 {
		t.Fatalf("a real browser must still pass even if flagged spoof (human classification), got %d", rr.Code)
	}
}
