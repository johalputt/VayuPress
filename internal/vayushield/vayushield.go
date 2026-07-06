// Package vayushield is the sovereign, enterprise-grade bot protection engine
// for VayuPress. It ties together the transport/TLS fingerprint (fingerprint),
// the static + adaptive bot database (botdb), the composite scorer (scorer) and
// the challenge engine (challenge) into a single net/http middleware plus a
// background learning reporter.
//
// Design principles, all enforced here:
//   - Pure Go, zero CGo, zero third-party runtime services, zero external calls.
//   - Dependency-injected side channels: country lookup, error-budget charging
//     and the CSP nonce are passed in as functions from the cmd layer, so this
//     package never imports the SQLite-bound governance/geoip packages and stays
//     unit-testable with net/http/httptest alone.
//   - Fail-open: any internal error or a nil dependency degrades to Allow. Bot
//     protection must never take the site down.
//   - Good bots and AI agents are always allowed and counted, never challenged.
package vayushield

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/fingerprint"
	"github.com/johalputt/vayupress/internal/vayushield/scorer"
)

// Action is the middleware's decision for a request.
type Action int

const (
	ActionAllow        Action = iota // serve normally
	ActionChallengePoW               // Level 1 — silent proof-of-work
	ActionChallengeJS                // Level 2 — interstitial JS challenge
	ActionBlock                      // Level 3 — hard 403
	ActionTarpit                     // Level 4 — operator-enabled deliberate delay
)

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "allow"
	case ActionChallengePoW:
		return "pow"
	case ActionChallengeJS:
		return "js"
	case ActionBlock:
		return "block"
	case ActionTarpit:
		return "tarpit"
	default:
		return "unknown"
	}
}

// Config configures a Manager. Only Enabled + at least a StaticDB are required;
// everything else degrades gracefully.
type Config struct {
	Enabled bool

	Static  *botdb.StaticDB
	Bots    *botdb.Store       // adaptive learning DB (nil disables learning)
	Signer  *challenge.Signer  // required for challenges; nil disables them
	Capture *fingerprint.Store // TLS ClientHello capture (nil = HTTP-only signals)
	DB      *sql.DB            // for recording challenges/blocks (nil disables recording)

	// Score thresholds (0..1). Defaults: PoW 0.4, JS 0.6, Block 0.8.
	PoWThreshold   float64
	JSThreshold    float64
	BlockThreshold float64

	// TarpitEnabled turns Block into Tarpit for aggressive scrapers.
	TarpitEnabled bool

	// BypassPrefixes are path prefixes always allowed without a challenge
	// (feeds, health, the admin panel, the shield's own endpoints).
	BypassPrefixes []string

	SessionCookieName string        // default "vayushield"
	SessionTTL        time.Duration // default 12h
	ChallengeTTL      time.Duration // default 5m
	CookieSecure      bool

	// Injected side channels (all optional).
	CountryFn func(ip string) string        // e.g. geoip.Country
	ClientIP  func(r *http.Request) string  // trusted-proxy-aware client IP
	OnEvent   func(a Action, score float64) // charge the error budget, emit metrics
	Now       func() time.Time
}

// Manager is the live bot-protection engine.
type Manager struct {
	cfg Config

	ipMu   sync.Mutex
	ipSalt []byte
	ipDay  string
}

// New constructs a Manager, applying defaults.
func New(cfg Config) *Manager {
	if cfg.Static == nil {
		cfg.Static = botdb.NewStaticDB()
	}
	if cfg.PoWThreshold == 0 {
		cfg.PoWThreshold = 0.4
	}
	if cfg.JSThreshold == 0 {
		cfg.JSThreshold = 0.6
	}
	if cfg.BlockThreshold == 0 {
		cfg.BlockThreshold = 0.8
	}
	if cfg.SessionCookieName == "" {
		cfg.SessionCookieName = "vayushield"
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.ChallengeTTL == 0 {
		cfg.ChallengeTTL = 5 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ClientIP == nil {
		cfg.ClientIP = func(r *http.Request) string { return r.RemoteAddr }
	}
	m := &Manager{cfg: cfg}
	m.rotateIPSalt(cfg.Now().UTC())
	return m
}

// Verdict is the full classification result for a request, exposed so the
// analytics layer can reuse the same client-type/bot-score decision.
type Verdict struct {
	Result     scorer.Result
	Composite  fingerprint.Composite
	Signals    fingerprint.Signals
	HasTLS     bool
	AIReferrer string // AI system name if the referrer indicates AI-assisted browsing
}

// Classify runs the full fingerprint → static/adaptive lookup → score pipeline
// for a request. It never mutates state (learning happens in the middleware),
// so analytics can call it freely.
func (m *Manager) Classify(r *http.Request) Verdict {
	sig, hasTLS := m.signals(r)
	comp := sig.Fingerprint()

	in := scorer.Input{Signals: sig, Composite: comp, HasTLS: hasTLS}
	if m.cfg.Static != nil {
		if s, ok := m.cfg.Static.MatchUA(sig.UserAgent); ok {
			in.StaticMatch = &s
		}
	}
	if m.cfg.Bots != nil && comp.FingerprintHash != "" {
		if learned, ok := m.cfg.Bots.Lookup(r.Context(), comp.FingerprintHash); ok {
			in.Learned = learned
		}
	}
	v := Verdict{Result: scorer.Score(in), Composite: comp, Signals: sig, HasTLS: hasTLS}
	if ref := r.Referer(); ref != "" && m.cfg.Static != nil {
		if host := hostOf(ref); host != "" {
			if name, ok := m.cfg.Static.MatchReferrerAI(host); ok {
				v.AIReferrer = name
			}
		}
	}
	return v
}

// signals builds a Signals value, preferring captured TLS data for the
// connection, and always folding in the HTTP request.
func (m *Manager) signals(r *http.Request) (fingerprint.Signals, bool) {
	var sig fingerprint.Signals
	hasTLS := false
	if m.cfg.Capture != nil {
		if s, ok := m.cfg.Capture.Get(r.RemoteAddr); ok {
			sig = s
			hasTLS = true
		}
	}
	if !hasTLS && r.TLS != nil {
		// In-process TLS termination: we at least know a handshake occurred.
		hasTLS = true
	}
	return sig.ApplyRequest(r), hasTLS
}

// Decide maps a verdict to an Action, honouring bypass rules and an existing
// valid session token.
func (m *Manager) Decide(r *http.Request, v Verdict) Action {
	if !m.cfg.Enabled {
		return ActionAllow
	}
	if m.isBypassed(r) {
		return ActionAllow
	}
	switch v.Result.ClientType {
	case botdb.TypeGoodBot, botdb.TypeAIAgent, botdb.TypeHuman:
		return ActionAllow
	}
	if v.AIReferrer != "" {
		// A human arriving via an AI assistant — allow.
		return ActionAllow
	}
	// A previously-verified visitor (valid signed session token) skips challenges.
	if m.hasValidSession(r) {
		return ActionAllow
	}
	score := v.Result.BotScore
	switch {
	case score >= m.cfg.BlockThreshold:
		if m.cfg.TarpitEnabled {
			return ActionTarpit
		}
		return ActionBlock
	case score >= m.cfg.JSThreshold:
		return ActionChallengeJS
	case score >= m.cfg.PoWThreshold:
		return ActionChallengePoW
	default:
		return ActionAllow
	}
}

// isBypassed reports whether the path is always allowed.
func (m *Manager) isBypassed(r *http.Request) bool {
	p := r.URL.Path
	for _, pre := range m.cfg.BypassPrefixes {
		if pre != "" && strings.HasPrefix(p, pre) {
			return true
		}
	}
	// Feeds are machine endpoints legit clients hit without JS.
	switch p {
	case "/feed.xml", "/rss.xml", "/atom.xml", "/sitemap.xml", "/robots.txt":
		return true
	}
	return false
}

func (m *Manager) hasValidSession(r *http.Request) bool {
	if m.cfg.Signer == nil {
		return false
	}
	c, err := r.Cookie(m.cfg.SessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return m.cfg.Signer.VerifySession(c.Value)
}

// ctxKey carries the Verdict on the request context for downstream handlers
// (analytics reads it to classify traffic and record bot_score/client_type).
type ctxKey struct{}

// VerdictFrom returns the Verdict stashed by the middleware, if any.
func VerdictFrom(ctx context.Context) (Verdict, bool) {
	v, ok := ctx.Value(ctxKey{}).(Verdict)
	return v, ok
}

// Middleware wraps next with bot classification, challenge issuance and
// learning. When disabled it is a transparent pass-through.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.cfg.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		v := m.Classify(r)
		action := m.Decide(r, v)
		if m.cfg.OnEvent != nil {
			m.cfg.OnEvent(action, v.Result.BotScore)
		}

		// Learn: a strongly bot-like but as-yet unclassified fingerprint becomes
		// an auto-learned candidate for later operator review / auto-promotion.
		m.maybeLearn(r.Context(), v)

		ctx := context.WithValue(r.Context(), ctxKey{}, v)
		r = r.WithContext(ctx)

		switch action {
		case ActionAllow:
			next.ServeHTTP(w, r)
		case ActionChallengePoW:
			m.serveChallenge(w, r, v, challenge.DefaultDifficulty)
		case ActionChallengeJS:
			m.serveChallenge(w, r, v, challenge.HardDifficulty)
		case ActionTarpit:
			m.serveTarpit(w, r, v, next)
		case ActionBlock:
			m.serveBlock(w, r, v)
		}
	})
}

// maybeLearn records a bot-like unknown fingerprint as an auto-learned candidate.
func (m *Manager) maybeLearn(ctx context.Context, v Verdict) {
	if m.cfg.Bots == nil || v.Composite.FingerprintHash == "" {
		return
	}
	if v.Result.BotScore > 0.75 && v.Result.ClientType == botdb.TypeUnknown {
		_ = m.cfg.Bots.Observe(ctx, botdb.Observation{
			FingerprintHash:   v.Composite.FingerprintHash,
			JA3:               v.Composite.JA3,
			JA4:               v.Composite.JA4,
			HTTP2SettingsHash: v.Composite.HTTP2SettingsHash,
			HeaderOrderHash:   v.Composite.HeaderOrderHash,
			UserAgentPattern:  uaFamily(v.Signals.UserAgent),
			PostQuantum:       v.Composite.PostQuantum,
			Classification:    botdb.ClassUnknown,
			Confidence:        0.6,
			AutoLearned:       true,
		})
	}
}

// hashIP returns a daily-rotating salted hash of ip — never a plaintext IP.
func (m *Manager) hashIP(ip string) string {
	if ip == "" {
		return ""
	}
	now := m.cfg.Now().UTC()
	day := now.Format("2006-01-02")
	m.ipMu.Lock()
	if day != m.ipDay {
		m.rotateIPSaltLocked(now)
	}
	salt := m.ipSalt
	m.ipMu.Unlock()
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(ip))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func (m *Manager) rotateIPSalt(now time.Time) {
	m.ipMu.Lock()
	defer m.ipMu.Unlock()
	m.rotateIPSaltLocked(now)
}

func (m *Manager) rotateIPSaltLocked(now time.Time) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	m.ipSalt = b
	m.ipDay = now.Format("2006-01-02")
}

// ── Challenge / block rendering ───────────────────────────────────────────────

// serveChallenge issues a signed PoW and returns the branded interstitial page.
// The solver runs from a same-origin script (/__vayushield/challenge.js), so no
// inline script is needed and the strict script-src 'self' CSP is satisfied.
func (m *Manager) serveChallenge(w http.ResponseWriter, r *http.Request, v Verdict, difficulty int) {
	if m.cfg.Signer == nil {
		// Cannot challenge without a signer — fail open.
		return
	}
	pow, err := m.cfg.Signer.IssuePoW(difficulty, m.cfg.ChallengeTTL)
	if err != nil {
		return
	}
	m.recordChallenge(r, v, actionType(difficulty), "issued", 0)
	payload, _ := json.Marshal(pow)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VayuShield", "challenge")
	w.WriteHeader(http.StatusServiceUnavailable) // 503: retry after solving
	_, _ = w.Write([]byte(interstitialHTML(string(payload))))
}

func actionType(difficulty int) string {
	if difficulty >= challenge.HardDifficulty {
		return "js"
	}
	return "pow"
}

// serveBlock returns a clean 403 page and records the block for operator review.
func (m *Manager) serveBlock(w http.ResponseWriter, r *http.Request, v Verdict) {
	m.recordBlock(r, v)
	m.recordChallenge(r, v, "block", "blocked", 0)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VayuShield", "block")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(blockHTML))
}

// serveTarpit accepts the request but delays it, wasting scraper compute. The
// content is still served (so detection is not revealed) after the delay.
func (m *Manager) serveTarpit(w http.ResponseWriter, r *http.Request, v Verdict, next http.Handler) {
	m.recordChallenge(r, v, "tarpit", "delayed", 0)
	delay := 5 * time.Second
	select {
	case <-time.After(delay):
	case <-r.Context().Done():
		return
	}
	next.ServeHTTP(w, r)
}

// VerifyPoW verifies a solved challenge submitted to the PoW endpoint and, on
// success, returns a freshly-minted session token to set as the cookie. The
// caller (cmd) owns the HTTP surface and cookie writing.
func (m *Manager) VerifyPoW(ctx context.Context, pow challenge.PoW, nonce string) (token string, ok bool) {
	if m.cfg.Signer == nil {
		return "", false
	}
	if err := m.cfg.Signer.VerifyPoW(pow, nonce); err != nil {
		return "", false
	}
	tok, err := m.cfg.Signer.IssueSession(m.cfg.SessionTTL)
	if err != nil {
		return "", false
	}
	return tok, true
}

// SessionCookie builds the httpOnly, signed session cookie for a verified token.
// This cookie is security-essential (not tracking): it carries no PII, only a
// signed verification token, so it needs no consent banner under GDPR.
func (m *Manager) SessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     m.cfg.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(m.cfg.SessionTTL.Seconds()),
	}
}

// ── Recording ─────────────────────────────────────────────────────────────────

func (m *Manager) recordChallenge(r *http.Request, v Verdict, ctype, outcome string, solveMS int) {
	if m.cfg.DB == nil {
		return
	}
	ip := m.cfg.ClientIP(r)
	country := ""
	if m.cfg.CountryFn != nil {
		country = m.cfg.CountryFn(ip)
	}
	_, _ = m.cfg.DB.ExecContext(r.Context(), `INSERT INTO vayushield_challenges
(session_hash,challenge_type,bot_score,fingerprint_hash,outcome,time_to_solve_ms,ip_hash,country_code)
VALUES('',?,?,?,?,?,?,?)`,
		ctype, v.Result.BotScore, v.Composite.FingerprintHash, outcome, solveMS, m.hashIP(ip), country)
}

func (m *Manager) recordBlock(r *http.Request, v Verdict) {
	if m.cfg.DB == nil {
		return
	}
	ip := m.cfg.ClientIP(r)
	country := ""
	if m.cfg.CountryFn != nil {
		country = m.cfg.CountryFn(ip)
	}
	reason := "bot_score>=block_threshold"
	if len(v.Result.Reasons) > 0 {
		reason = strings.Join(v.Result.Reasons, "; ")
	}
	ua := v.Signals.UserAgent
	if len(ua) > 512 {
		ua = ua[:512]
	}
	_, _ = m.cfg.DB.ExecContext(r.Context(), `INSERT INTO vayushield_blocked
(fingerprint_hash,ja3_hash,ip_hash,user_agent,request_path,block_reason,bot_score,country_code)
VALUES(?,?,?,?,?,?,?,?)`,
		v.Composite.FingerprintHash, v.Composite.JA3, m.hashIP(ip), ua, r.URL.Path, reason, v.Result.BotScore, country)
}

// hostOf extracts the lowercase host from a referrer URL.
func hostOf(ref string) string {
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "://"); i >= 0 {
		ref = ref[i+3:]
	}
	if i := strings.IndexAny(ref, "/?#"); i >= 0 {
		ref = ref[:i]
	}
	return strings.ToLower(ref)
}

// uaFamily mirrors the coarse UA bucketing used elsewhere (kept local).
func uaFamily(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case l == "":
		return "none"
	case strings.Contains(l, "edg/"):
		return "edge"
	case strings.Contains(l, "chrome/") && !strings.Contains(l, "chromium"):
		return "chrome"
	case strings.Contains(l, "firefox/"):
		return "firefox"
	case strings.Contains(l, "safari/") && !strings.Contains(l, "chrome"):
		return "safari"
	case strings.Contains(l, "python") || strings.Contains(l, "go-http") || strings.Contains(l, "curl/"):
		return "http-lib"
	default:
		return "other"
	}
}

// ── Static challenge/block HTML ───────────────────────────────────────────────

func interstitialHTML(challengeJSON string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Verifying your browser…</title>
<meta name="robots" content="noindex">
</head><body>
<main style="max-width:32rem;margin:16vh auto;font-family:system-ui,sans-serif;text-align:center;color:#e5e7eb;background:#0b0f14;padding:2rem;border-radius:12px">
<h1 style="font-size:1.25rem">Verifying your browser…</h1>
<p style="color:#94a3b8">This automatic check protects the site from bots. It takes a moment and requires no interaction.</p>
<noscript><p style="color:#f59e0b">JavaScript is required to complete this check.</p></noscript>
<div id="vayushield-pow" data-challenge='` + html.EscapeString(challengeJSON) + `'></div>
<p id="vayushield-status" style="color:#64748b;font-size:.85rem">Working…</p>
</main>
<script src="/__vayushield/challenge.js"></script>
</body></html>`
}

const blockHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Access denied</title><meta name="robots" content="noindex"></head>
<body><main style="max-width:32rem;margin:16vh auto;font-family:system-ui,sans-serif;text-align:center;color:#e5e7eb;background:#0b0f14;padding:2rem;border-radius:12px">
<h1 style="font-size:1.25rem">Access denied</h1>
<p style="color:#94a3b8">Your request was blocked by VayuShield bot protection. If you believe this is a mistake, please contact the site operator.</p>
</main></body></html>`

// ChallengeJS returns the same-origin PoW solver script. It reads the signed
// challenge from the interstitial's data attribute, brute-forces a nonce with
// SHA-256 (Web Crypto), posts the solution, and reloads on success. Served at
// /__vayushield/challenge.js (script-src 'self', no nonce needed).
func ChallengeJS() string {
	return `(function(){'use strict';
var el=document.getElementById('vayushield-pow');
var st=document.getElementById('vayushield-status');
if(!el){return;}
var ch;try{ch=JSON.parse(el.getAttribute('data-challenge'));}catch(e){return;}
function hex(buf){var b=new Uint8Array(buf),s='';for(var i=0;i<b.length;i++){s+=('0'+b[i].toString(16)).slice(-2);}return s;}
function leadingZeros(h,n){for(var i=0;i<n;i++){if(h[i]!=='0')return false;}return true;}
async function sha(str){var enc=new TextEncoder().encode(str);var d=await crypto.subtle.digest('SHA-256',enc);return hex(d);}
async function solve(){
  var i=0,max=8000000;
  while(i<max){
    var h=await sha(ch.salt+':'+i);
    if(leadingZeros(h,ch.difficulty)){return ''+i;}
    i++;
    if(i%20000===0){await new Promise(function(r){setTimeout(r,0);});}
  }
  return null;
}
solve().then(function(nonce){
  if(nonce===null){if(st)st.textContent='Verification failed. Please refresh.';return;}
  fetch('/__vayushield/pow',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({challenge:ch,nonce:nonce}),credentials:'same-origin'})
   .then(function(r){if(r.ok){location.reload();}else{if(st)st.textContent='Verification rejected.';}})
   .catch(function(){if(st)st.textContent='Network error during verification.';});
}).catch(function(){if(st)st.textContent='Verification error.';});
})();`
}
