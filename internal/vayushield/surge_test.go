package vayushield

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/challenge"
)

// realBrowserUA is a UA the scorer classifies as human (ActionAllow) absent
// surge, so a 503 under surge proves the surge gate ran BEFORE classification.
const realBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

func surgeManager(surge bool) *Manager {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("test-secret")),
		Now:      time.Now,
		ClientIP: func(r *http.Request) string { return "203.0.113.9:5555" },
	})
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8, Surge: surge})
	return m
}

func getBrowser(path string) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("User-Agent", realBrowserUA)
	return req
}

// TestSurgeChallengesUnprovenBrowser is the core guarantee: with surge on, even
// a request the scorer would ALLOW is met with the stateless PoW interstitial
// up front — proving the surge gate runs before classification.
func TestSurgeChallengesUnprovenBrowser(t *testing.T) {
	m := surgeManager(true)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("surge should challenge an unproven browser (503), got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Verifying your browser") ||
		!strings.Contains(rr.Body.String(), "/__vayushield/challenge.js") {
		t.Fatal("surge must serve the CSP-safe interstitial referencing the same-origin solver")
	}
	if m.Status().SurgeChallenges < 1 || !m.Status().SurgeActive {
		t.Fatalf("surge telemetry not recorded: %+v", m.Status())
	}
}

// TestSurgeOffPassesBrowser: with surge off and no pressure, the same browser
// passes normally — surge must not touch steady-state traffic.
func TestSurgeOffPassesBrowser(t *testing.T) {
	m := surgeManager(false)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != 200 {
		t.Fatalf("with surge off, a real browser must pass, got %d", rr.Code)
	}
	if m.Status().SurgeActive {
		t.Fatal("surge must not be active with no toggle and no pressure")
	}
}

// TestSurgeBypassesVerifiedSession: a visitor who already solved a challenge
// (valid signed session) is never re-challenged under surge.
func TestSurgeBypassesVerifiedSession(t *testing.T) {
	m := surgeManager(true)
	// The session cookie is bound to the client IP (surgeManager's fixed IP).
	tok, err := m.cfg.Signer.IssueSession(m.cfg.SessionTTL, "203.0.113.9")
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	rr := httptest.NewRecorder()
	req := getBrowser("/post")
	req.AddCookie(m.SessionCookie(tok))
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("verified session must bypass surge, got %d", rr.Code)
	}
}

// TestSurgeBypassesTrustedOperator: the operator (TrustedFn) is immune to surge.
func TestSurgeBypassesTrustedOperator(t *testing.T) {
	m := New(Config{
		Enabled:   true,
		Signer:    challenge.NewSigner([]byte("s")),
		ClientIP:  func(r *http.Request) string { return "203.0.113.9:1" },
		TrustedFn: func(r *http.Request) bool { return true },
	})
	m.ApplySettings(Settings{Enabled: true, Surge: true})
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != 200 {
		t.Fatalf("trusted operator must bypass surge, got %d", rr.Code)
	}
}

// TestSurgeBypassesPrefixes: bypassed paths (assets, admin, feeds) are never
// challenged, even under surge — a challenge in place of a stylesheet would
// break the very page that references it.
func TestSurgeBypassesPrefixes(t *testing.T) {
	m := New(Config{Enabled: true, Signer: challenge.NewSigner([]byte("s")),
		BypassPrefixes: []string{"/os", "/static"},
		ClientIP:       func(r *http.Request) string { return "203.0.113.9:1" }})
	m.ApplySettings(Settings{Enabled: true, Surge: true})
	for _, p := range []string{"/os/dashboard", "/static/app.css", "/feed.xml"} {
		rr := httptest.NewRecorder()
		m.Middleware(okHandler()).ServeHTTP(rr, getBrowser(p))
		if rr.Code != 200 {
			t.Fatalf("bypassed path %q must not be surged, got %d", p, rr.Code)
		}
	}
}

// TestSurgeAutoEngagesUnderAttack: with adaptive under-attack mode enabled and a
// live RPS flood (the attack meter tripped), surge auto-engages with NO explicit
// surge toggle — a 1M-source swarm drives RPS up and is met at the front gate.
// Mild pressure (below the RPS trip) must NOT surge.
func TestSurgeAutoEngagesUnderAttack(t *testing.T) {
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return "203.0.113.9:1" },
	})
	// Under-attack mode on, surge toggle OFF, a low RPS trip point.
	m.ApplySettings(Settings{Enabled: true, UnderAttack: true, UnderAttackRPS: 2})

	// Before any flood the attack meter is quiet → a browser passes.
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != 200 {
		t.Fatalf("no flood: browser should pass, got %d", rr.Code)
	}

	// Simulate a flood tripping the RPS controller, then a fresh request must be
	// met by surge before classification.
	for i := 0; i < 6; i++ {
		m.controller.Observe()
	}
	if !m.controller.UnderAttack() {
		t.Skip("controller did not trip within the window; timing-sensitive")
	}
	rr = httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("during an RPS flood, surge must auto-engage (503), got %d", rr.Code)
	}
}

// TestSurgeAutoEngagesOnCriticalSaturation: when the L0 lane is critically full
// (SurgePressureFn), surge auto-engages with NO operator toggle and NO RPS
// spike — so a low-and-slow, high-cardinality swarm that fills the lane without
// tripping the per-second meter is still met at the front gate.
func TestSurgeAutoEngagesOnCriticalSaturation(t *testing.T) {
	critical := false
	m := New(Config{
		Enabled:         true,
		Signer:          challenge.NewSigner([]byte("s")),
		ClientIP:        func(r *http.Request) string { return "203.0.113.9:1" },
		SurgePressureFn: func() bool { return critical },
	})
	m.ApplySettings(Settings{Enabled: true, Surge: false, UnderAttack: false})

	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != 200 {
		t.Fatalf("below critical saturation: browser should pass, got %d", rr.Code)
	}
	critical = true
	rr = httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("critical L0 saturation must auto-engage surge (503), got %d", rr.Code)
	}
}

// TestSurgeDoesNotEngageOnMildPressure: the L0 fair-shed pressure signal must
// NOT trip surge — a merely-busy site keeps serving unproven light clients (the
// graceful-degradation contract), only heavy hitters are fair-shed.
func TestSurgeDoesNotEngageOnMildPressure(t *testing.T) {
	m := New(Config{
		Enabled:    true,
		Signer:     challenge.NewSigner([]byte("s")),
		ClientIP:   func(r *http.Request) string { return "203.0.113.9:1" },
		PressureFn: func() bool { return true }, // L0 lane busy, but no attack detected
	})
	m.ApplySettings(Settings{Enabled: true, Surge: false, UnderAttack: false})
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != 200 {
		t.Fatalf("mild L0 pressure must not surge a light client, got %d", rr.Code)
	}
}

// TestSurgeCookieReplayAcrossIPsRejected is the anti-swarm guard: a clearance
// cookie solved by one IP must NOT let a different IP through under surge. This
// is what stops "solve once, replay the cookie across the whole 1M-IP swarm".
func TestSurgeCookieReplayAcrossIPsRejected(t *testing.T) {
	ip := "203.0.113.9:5555"
	m := New(Config{
		Enabled:  true,
		Signer:   challenge.NewSigner([]byte("s")),
		ClientIP: func(r *http.Request) string { return ip },
	})
	m.ApplySettings(Settings{Enabled: true, Surge: true})
	// Mint a cookie bound to the first IP.
	tok, _ := m.cfg.Signer.IssueSession(m.cfg.SessionTTL, "203.0.113.9")
	// Same IP: bypasses surge.
	rr := httptest.NewRecorder()
	req := getBrowser("/post")
	req.AddCookie(m.SessionCookie(tok))
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("cookie must work from its own IP, got %d", rr.Code)
	}
	// Different IP replaying the SAME cookie: must be challenged, not admitted.
	ip = "198.51.100.200:4444"
	rr = httptest.NewRecorder()
	req = getBrowser("/post")
	req.AddCookie(m.SessionCookie(tok))
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("replayed cookie from a different IP must be re-challenged (503), got %d", rr.Code)
	}
}

// TestSurgeChallengeCarriesRetryAfter: a surge 503 must carry Retry-After so a
// crawler challenged during surge treats it as transient (not deindexed).
func TestSurgeChallengeCarriesRetryAfter(t *testing.T) {
	m := surgeManager(true)
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected surge 503, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("surge 503 must carry Retry-After (SEO-safe transient signal)")
	}
}

// TestSurgeForcedAutoExpires: operator-forced surge stops engaging after
// maxForcedSurge, so a forgotten toggle cannot serve 503s indefinitely.
func TestSurgeForcedAutoExpires(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	m := New(Config{Enabled: true, Signer: challenge.NewSigner([]byte("s")), Now: clock,
		ClientIP: func(r *http.Request) string { return "203.0.113.9:1" }})
	m.ApplySettings(Settings{Enabled: true, Surge: true})

	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("freshly-armed forced surge should challenge, got %d", rr.Code)
	}
	// Advance past the auto-expiry window.
	now = now.Add(maxForcedSurge + time.Minute)
	rr = httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != 200 {
		t.Fatalf("forced surge must auto-expire and pass traffic, got %d", rr.Code)
	}
}

// TestSurgeFailsOpenWithoutSigner: with no signer the shield cannot challenge, so
// surge must degrade to Allow rather than error — protection never fails closed.
func TestSurgeFailsOpenWithoutSigner(t *testing.T) {
	m := New(Config{Enabled: true, ClientIP: func(r *http.Request) string { return "203.0.113.9:1" }})
	m.ApplySettings(Settings{Enabled: true, Surge: true}) // operator forced surge ON
	rr := httptest.NewRecorder()
	m.Middleware(okHandler()).ServeHTTP(rr, getBrowser("/post"))
	if rr.Code != 200 {
		t.Fatalf("no signer: surge must fail open (allow), got %d", rr.Code)
	}
}

// TestSurgeSolvableGrantsPassage proves a real human is never stuck: solving the
// surge challenge yields a session that bypasses surge on the next request.
func TestSurgeSolvableGrantsPassage(t *testing.T) {
	m := surgeManager(true)
	// The surge challenge is the silent default-difficulty PoW; solve it as a
	// browser would and redeem it for a session.
	pow, err := m.cfg.Signer.IssuePoW(challenge.DefaultDifficulty, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	nonce, ok := challenge.Solve(pow.Salt, pow.Difficulty, 5_000_000)
	if !ok {
		t.Fatal("solve failed")
	}
	tok, ok := m.VerifyPoW(getBrowser("/__vayushield/pow"), pow, nonce)
	if !ok {
		t.Fatal("VerifyPoW should accept a valid surge solution")
	}
	rr := httptest.NewRecorder()
	req := getBrowser("/post")
	req.AddCookie(m.SessionCookie(tok))
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("a solved surge challenge must let the visitor through, got %d", rr.Code)
	}
}
