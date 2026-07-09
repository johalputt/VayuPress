package vayushield

import (
	"context"
	"encoding/json"
	htmlpkg "html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/scorer"
)

func newTestManager(enabled bool) *Manager {
	return New(Config{
		Enabled:  enabled,
		Signer:   challenge.NewSigner([]byte("test-secret")),
		Now:      time.Now,
		ClientIP: func(r *http.Request) string { return "203.0.113.7:5555" },
	})
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("real content"))
	})
}

func TestMiddlewareDisabledPassThrough(t *testing.T) {
	m := newTestManager(false)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "python-requests/2.31")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "real content") {
		t.Fatalf("disabled shield must pass through, got %d", rr.Code)
	}
}

func TestMiddlewareAllowsRealBrowser(t *testing.T) {
	m := newTestManager(true)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("real browser should be allowed, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMiddlewareBlocksKnownBadBot(t *testing.T) {
	m := newTestManager(true)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "sqlmap/1.7")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("sqlmap should be blocked (403), got %d", rr.Code)
	}
	if rr.Header().Get("X-VayuShield") != "block" {
		t.Fatalf("missing block header")
	}
}

func TestMiddlewareAllowsGoodBot(t *testing.T) {
	m := newTestManager(true)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("Googlebot must be allowed, got %d", rr.Code)
	}
}

func TestMiddlewareChallengesUncertain(t *testing.T) {
	m := newTestManager(true)
	// An empty UA scores ~0.7 → JS challenge band. Serve interstitial (503).
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("uncertain client should get challenge interstitial (503), got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Verifying your browser") {
		t.Fatalf("expected interstitial body, got: %s", rr.Body.String()[:minInt(120, len(rr.Body.String()))])
	}
	if !strings.Contains(rr.Body.String(), "/__vayushield/challenge.js") {
		t.Fatal("interstitial must reference the same-origin solver script")
	}
}

func TestPoWEndToEndSessionBypass(t *testing.T) {
	m := newTestManager(true)
	// 1) Issue a challenge (simulate what serveChallenge embeds).
	pow, err := m.cfg.Signer.IssuePoW(challenge.DefaultDifficulty, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	nonce, ok := challenge.Solve(pow.Salt, pow.Difficulty, 5_000_000)
	if !ok {
		t.Fatal("solve failed")
	}
	// 2) Verify and mint a session token.
	tok, ok := m.VerifyPoW(context.Background(), pow, nonce)
	if !ok {
		t.Fatal("VerifyPoW should succeed for a valid solution")
	}
	// 3) A subsequent uncertain request carrying the session cookie is allowed.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "") // would otherwise be challenged
	req.AddCookie(m.SessionCookie(tok))
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("verified session should bypass challenge, got %d", rr.Code)
	}
}

func TestBypassPrefixes(t *testing.T) {
	m := New(Config{Enabled: true, Signer: challenge.NewSigner([]byte("s")), BypassPrefixes: []string{"/os", "/__vayushield"}})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/os/dashboard", nil)
	req.Header.Set("User-Agent", "sqlmap/1.7") // even a bad bot is bypassed on /os
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("bypass prefix should allow, got %d", rr.Code)
	}
	// Feeds are always bypassed.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/feed.xml", nil)
	req2.Header.Set("User-Agent", "")
	m.Middleware(okHandler()).ServeHTTP(rr2, req2)
	if rr2.Code != 200 {
		t.Fatalf("feed should be bypassed, got %d", rr2.Code)
	}
}

func TestChallengeJSServable(t *testing.T) {
	js := ChallengeJS()
	if !strings.Contains(js, "crypto.subtle.digest") || !strings.Contains(js, "/__vayushield/pow") {
		t.Fatal("challenge JS missing core logic")
	}
}

func TestVerdictOnContext(t *testing.T) {
	m := newTestManager(true)
	var gotType string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := VerdictFrom(r.Context()); ok {
			gotType = string(v.Result.ClientType)
		}
		w.WriteHeader(200)
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/130 Safari/537.36")
	m.Middleware(h).ServeHTTP(rr, req)
	if gotType == "" {
		t.Fatal("verdict should be stashed on context for downstream handlers")
	}
}

// OnEvent must be invoked for observability/budget integration.
func TestOnEventInvoked(t *testing.T) {
	var called bool
	var lastAction Action
	m := New(Config{Enabled: true, Signer: challenge.NewSigner([]byte("s")),
		OnEvent: func(a Action, score float64) { called = true; lastAction = a }})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("User-Agent", "sqlmap/1.7")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if !called || lastAction != ActionBlock {
		t.Fatalf("OnEvent not invoked with block action (called=%v action=%v)", called, lastAction)
	}
}

// Sanity: the PoW payload embedded in the interstitial (via html/template) is
// valid JSON once the browser decodes the attribute.
func TestInterstitialEmbedsValidChallenge(t *testing.T) {
	p := challenge.PoW{Salt: "abc", Difficulty: 4, Expires: 123, Sig: "sig"}
	b, _ := json.Marshal(p)
	htmlOut := interstitialHTML(string(b))
	const attr = `data-challenge="`
	i := strings.Index(htmlOut, attr)
	if i < 0 {
		t.Fatal("no challenge attribute")
	}
	rest := htmlOut[i+len(attr):]
	j := strings.Index(rest, `"`) // first raw quote is the attribute terminator
	if j < 0 {
		t.Fatal("unterminated data-challenge attribute")
	}
	payload := htmlpkg.UnescapeString(rest[:j]) // browsers decode HTML entities
	var got challenge.PoW
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("embedded challenge not valid JSON: %v (%s)", err, payload)
	}
	if got.Salt != "abc" {
		t.Fatalf("challenge round-trip mismatch: %+v", got)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── Live config + Tier-1 resilience ──────────────────────────────────────────

func TestApplySettingsLiveToggle(t *testing.T) {
	m := New(Config{Enabled: false, Signer: challenge.NewSigner([]byte("s"))})
	if m.Enabled() {
		t.Fatal("should start disabled")
	}
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8})
	if !m.Enabled() {
		t.Fatal("live enable failed")
	}
	m.ApplySettings(Settings{Enabled: false})
	if m.Enabled() {
		t.Fatal("live disable failed")
	}
}

func TestRateLimitBlocksFlood(t *testing.T) {
	m := New(Config{ClientIP: func(r *http.Request) string { return "9.9.9.9:1" }})
	// Challenges OFF; only per-IP rate limiting on: burst 2, 1/min sustained.
	m.ApplySettings(Settings{Enabled: false, RateLimit: true, RatePerMinute: 1, Burst: 2})
	var ok, throttled int
	for i := 0; i < 6; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/post", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/130 Safari/537.36")
		m.Middleware(okHandler()).ServeHTTP(rr, req)
		switch rr.Code {
		case 200:
			ok++
		case http.StatusTooManyRequests:
			throttled++
		}
	}
	if ok != 2 {
		t.Fatalf("burst should allow exactly 2, got %d", ok)
	}
	if throttled != 4 {
		t.Fatalf("rest should be 429, got %d throttled", throttled)
	}
}

func TestVerifiedSessionBypassesRateLimit(t *testing.T) {
	signer := challenge.NewSigner([]byte("s"))
	m := New(Config{Signer: signer, ClientIP: func(r *http.Request) string { return "8.8.8.8:2" }})
	m.ApplySettings(Settings{Enabled: false, RateLimit: true, RatePerMinute: 1, Burst: 1})
	tok, _ := signer.IssueSession(time.Hour)
	for i := 0; i < 10; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/post", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/130 Safari/537.36")
		req.AddCookie(m.SessionCookie(tok))
		m.Middleware(okHandler()).ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("verified visitor must never be rate-limited, got %d on request %d", rr.Code, i)
		}
	}
}

func TestLoadShedRejectsWhenSaturated(t *testing.T) {
	m := New(Config{ClientIP: func(r *http.Request) string { return "7.7.7.7:3" }})
	m.ApplySettings(Settings{Enabled: false, LoadShed: true, MaxInFlight: 1})

	inHandler := make(chan struct{})
	release := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inHandler)
		<-release
		w.WriteHeader(200)
	})
	mw := m.Middleware(slow)

	// Request 1 occupies the single in-flight slot.
	done := make(chan int, 1)
	go func() {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/post", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/130 Safari/537.36")
		mw.ServeHTTP(rr, req)
		done <- rr.Code
	}()
	<-inHandler // ensure request 1 is inside the handler holding the slot

	// Request 2 must be shed with 503.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/post", nil)
	req2.Header.Set("User-Agent", "Mozilla/5.0 Chrome/130 Safari/537.36")
	mw.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated server should shed with 503, got %d", rr2.Code)
	}

	close(release)
	if code := <-done; code != 200 {
		t.Fatalf("request 1 should complete 200, got %d", code)
	}
}

func TestAutoBlockJailsFloodingIP(t *testing.T) {
	m := New(Config{ClientIP: func(r *http.Request) string { return "6.6.6.6:4" }})
	m.ApplySettings(Settings{Enabled: false, RateLimit: true, RatePerMinute: 1, Burst: 1, AutoBlock: true, AutoBlockJailMinutes: 5})
	// Hammer well past the ~20-breach jail meter.
	for i := 0; i < 40; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/post", nil)
		req.Header.Set("User-Agent", "curl/8")
		m.Middleware(okHandler()).ServeHTTP(rr, req)
	}
	if m.Status().Blocklisted == 0 {
		t.Fatal("a relentlessly flooding IP should have been auto-jailed")
	}
}

func TestUnderAttackTightensThresholds(t *testing.T) {
	m := New(Config{Signer: challenge.NewSigner([]byte("s"))})
	m.ApplySettings(Settings{Enabled: true, PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8, UnderAttack: true, UnderAttackRPS: 1})
	// A borderline unknown client scoring 0.30 — below the normal 0.40 PoW gate.
	v := Verdict{Result: scorer.Result{BotScore: 0.30, ClientType: botdb.TypeUnknown}}
	req := httptest.NewRequest("GET", "/post", nil)
	if a := m.Decide(req, v); a != ActionAllow {
		t.Fatalf("at normal load a 0.30 score should be allowed, got %v", a)
	}
	// Trip the attack meter (threshold 1 rps).
	m.controller.Observe()
	m.controller.Observe()
	if a := m.Decide(req, v); a != ActionChallengePoW {
		t.Fatalf("under attack a 0.30 score should be challenged, got %v", a)
	}
}

func TestResilienceOffIsPassThrough(t *testing.T) {
	m := New(Config{ClientIP: func(r *http.Request) string { return "5.5.5.5:5" }})
	// Everything off — even a blatant bot UA passes (nothing is active).
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/post", nil)
	req.Header.Set("User-Agent", "sqlmap/1.7")
	m.Middleware(okHandler()).ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("all-off shield must pass through, got %d", rr.Code)
	}
}

// TestBadBotAutoJailedOnBlock proves the "detect once, block thereafter"
// behaviour: a bad bot is hard-blocked (403) AND its IP is jailed, so the very
// next request from that IP is dropped by the O(1) blocklist gate (429) without
// re-running classification.
func TestBadBotAutoJailedOnBlock(t *testing.T) {
	m := newTestManager(true)
	m.ApplySettings(Settings{Enabled: true, AutoBlock: true})
	h := m.Middleware(okHandler())

	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/post", nil)
	req1.Header.Set("User-Agent", "sqlmap/1.7")
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusForbidden {
		t.Fatalf("first bad-bot request should be blocked (403), got %d", rr1.Code)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/post", nil)
	req2.Header.Set("User-Agent", "sqlmap/1.7")
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from a jailed bad-bot IP should be dropped by the blocklist (429), got %d", rr2.Code)
	}
}

// TestBadBotNotJailedWhenAutoBlockOff confirms the jail respects the operator's
// auto-block toggle: with auto-block off, a bad bot is still blocked per request
// but its IP is not added to the persistent blocklist.
func TestBadBotNotJailedWhenAutoBlockOff(t *testing.T) {
	m := newTestManager(true)
	m.ApplySettings(Settings{Enabled: true, AutoBlock: false})
	h := m.Middleware(okHandler())

	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/post", nil)
	req1.Header.Set("User-Agent", "sqlmap/1.7")
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusForbidden {
		t.Fatalf("bad bot should still be blocked (403), got %d", rr1.Code)
	}
	if m.blocklist.Len() != 0 {
		t.Fatalf("with auto-block off, the bad-bot IP must not be jailed; blocklist len=%d", m.blocklist.Len())
	}
}

// ── Aegis L2 fair-shed integration ───────────────────────────────────────────

// browserReq builds a real-browser request from the given client IP.
func browserReq(path string) *http.Request {
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	return req
}

func TestMiddlewareFairShedsHeavyHitterUnderPressure(t *testing.T) {
	pressure := false
	ip := "203.0.113.99:1234"
	m := New(Config{
		Enabled:    false, // classification off — L2 must defend on its own
		Signer:     challenge.NewSigner([]byte("test-secret")),
		Now:        time.Now,
		ClientIP:   func(r *http.Request) string { return ip },
		PressureFn: func() bool { return pressure },
	})
	h := m.Middleware(okHandler())

	// Calm conditions: a flood is observed but nothing is shed.
	for i := 0; i < 2000; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, browserReq("/post"))
		if rr.Code != 200 {
			t.Fatalf("without pressure nothing sheds; got %d at request %d", rr.Code, i)
		}
	}

	// Pressure on: the same heavy source now sheds most of its excess.
	pressure = true
	shed := 0
	for i := 0; i < 500; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, browserReq("/post"))
		if rr.Code == http.StatusTooManyRequests {
			if got := rr.Header().Get("X-VayuShield"); got != "fair-shed" {
				t.Fatalf("shed reason = %q, want fair-shed", got)
			}
			shed++
		}
	}
	if shed < 300 {
		t.Fatalf("heavy hitter shed only %d/500 under pressure", shed)
	}
	if m.Status().FairShed == 0 {
		t.Fatal("FairShed status metric should be positive")
	}

	// A light client from another network is never shed at the same moment.
	ip = "198.51.100.44:2222"
	for i := 0; i < 30; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, browserReq("/post"))
		if rr.Code != 200 {
			t.Fatalf("light client must never be fair-shed; got %d", rr.Code)
		}
	}
}

func TestMiddlewareFairShedExemptsVerifiedSession(t *testing.T) {
	signer := challenge.NewSigner([]byte("test-secret"))
	m := New(Config{
		Enabled:    false,
		Signer:     signer,
		Now:        time.Now,
		ClientIP:   func(r *http.Request) string { return "203.0.113.77:1000" },
		PressureFn: func() bool { return true },
	})
	h := m.Middleware(okHandler())
	tok, err := signer.IssueSession(12 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Saturate the IP far past its budget with anonymous traffic.
	for i := 0; i < 3000; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, browserReq("/post"))
	}
	// The verified visitor on the SAME saturated IP is never shed.
	for i := 0; i < 50; i++ {
		rr := httptest.NewRecorder()
		req := browserReq("/post")
		req.AddCookie(&http.Cookie{Name: "vayushield", Value: tok})
		h.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("verified session must bypass fair-shed; got %d", rr.Code)
		}
	}
}

// ── Aegis L5 reputation integration ──────────────────────────────────────────

func TestMiddlewareReputationJailAndVerifiedExemption(t *testing.T) {
	signer := challenge.NewSigner([]byte("test-secret"))
	m := New(Config{
		Enabled:  false,
		Signer:   signer,
		ClientIP: func(r *http.Request) string { return "6.6.6.7:9" },
	})
	m.ApplySettings(Settings{RateLimit: true, RatePerMinute: 1, Burst: 1})
	h := m.Middleware(okHandler())

	// Breach the rate limit until reputation collapses into a jail sentence.
	var last string
	for i := 0; i < 12; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, browserReq("/post"))
		if rr.Code == http.StatusTooManyRequests {
			last = rr.Header().Get("X-VayuShield")
		}
	}
	if last != "reputation" {
		t.Fatalf("flooding source should end up reputation-jailed, last reason %q", last)
	}
	st := m.Status()
	if st.Suspects == 0 || st.RepJailed == 0 {
		t.Fatalf("status = %+v, want suspect + jailed counts", st)
	}

	// A verified session from the SAME jailed IP always passes.
	tok, err := signer.IssueSession(12 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := browserReq("/post")
	req.AddCookie(&http.Cookie{Name: "vayushield", Value: tok})
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("verified session must bypass the reputation jail, got %d", rr.Code)
	}

	// RewardProof (a solved challenge) pardons the sentence for everyone else.
	m.RewardProof(browserReq("/__vayushield/pow"))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, browserReq("/post"))
	if rr.Header().Get("X-VayuShield") == "reputation" {
		t.Fatal("solved challenge must pardon the reputation sentence")
	}
	if m.Status().Pardons == 0 {
		t.Fatal("pardon telemetry should be positive")
	}
}
