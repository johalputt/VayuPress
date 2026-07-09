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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/dbbatch"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/brain"
	"github.com/johalputt/vayupress/internal/vayushield/calibrate"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/fingerprint"
	"github.com/johalputt/vayupress/internal/vayushield/prefilter"
	"github.com/johalputt/vayupress/internal/vayushield/resilience"
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

	// PressureFn reports external saturation (e.g. the Aegis L0 sovereignty
	// lane nearing its cap). When it returns true the L2 pre-filter fair-sheds
	// heavy hitters even if no operator toggle is on — the shield defends
	// availability automatically, with no configuration. Optional; when set,
	// the pre-filter observes every request (a few atomic ops) so its sketch is
	// already warm the moment pressure appears.
	PressureFn func() bool

	// OffloadFn exports a jail verdict to the L1 kernel offload (an IP that
	// should be dropped by nftables/XDP for the given TTL). Optional and
	// best-effort: when nil, or when the root agent is not installed, the
	// in-binary gates keep enforcing on their own. Never called for verified
	// visitors — only for sources the shield has actively jailed.
	OffloadFn func(ip string, ttl time.Duration)
}

// liveConfig is the runtime-tunable subset of the shield, swapped atomically so
// the request hot path reads it lock-free. Operators change these from the
// VayuOS panel with no restart.
type liveConfig struct {
	enabled        bool    // bot classification + challenge ladder
	pow, js, block float64 // challenge score thresholds
	tarpit         bool    // Block -> Tarpit for the worst offenders
	rateLimit      bool    // per-IP token-bucket rate limiting
	loadShed       bool    // in-flight concurrency cap (503 when saturated)
	autoBlock      bool    // auto-jail IPs that keep breaching the rate limit
	underAttack    bool    // adaptive: tighten thresholds during a flood
}

// Settings is the full operator-tunable configuration, persisted by the cmd
// layer and applied live via ApplySettings.
type Settings struct {
	Enabled                                   bool
	PoWThreshold, JSThreshold, BlockThreshold float64
	Tarpit                                    bool
	RateLimit                                 bool
	RatePerMinute                             int // sustained per-IP requests/min
	Burst                                     int // per-IP burst ceiling
	LoadShed                                  bool
	MaxInFlight                               int // 0 = unlimited
	AutoBlock                                 bool
	AutoBlockJailMinutes                      int
	UnderAttack                               bool
	UnderAttackRPS                            int // global RPS that trips attack mode
}

// Manager is the live bot-protection + resilience engine.
type Manager struct {
	cfg Config

	liveCfg     atomic.Pointer[liveConfig]
	curSettings atomic.Pointer[Settings]

	// Tier-1 resilience primitives (cheap, always allocated; only consulted when
	// their toggle is on).
	limiter    *resilience.Limiter // per-IP request rate
	violation  *resilience.Limiter // per-IP rate-limit-breach meter -> auto-jail
	inflight   *resilience.InFlight
	blocklist  *resilience.Blocklist
	controller *resilience.Controller
	jailTTLns  atomic.Int64

	// prefilter is the Aegis L2 probabilistic fair-shed engine: a 256 KiB
	// windowed Count-Min Sketch that identifies heavy hitters (per IP and per
	// /24 / /48 group) in fixed memory and, under attack pressure, sheds their
	// excess traffic proportionally — while a client within its fair budget can
	// never be shed. Lock-free, zero-allocation hot path.
	prefilter *prefilter.Prefilter

	// brain is the Aegis L5 online reputation engine: every enforcement event
	// lowers a source's standing, every positive proof raises it; collapsed
	// standings auto-jail with escalating sentences, decay/rehab/pardons make
	// false positives self-heal, and only suspects are ever tracked.
	brain *brain.Brain

	// calib is the Aegis L4 feedback controller: it watches the challenge pass
	// rate and, when almost everyone challenged proves to be a real browser,
	// loosens the effective thresholds automatically (loosen-only, capped) so
	// the ladder stays silent-first and self-heals its own false positives.
	calib *calibrate.Calibrator

	ipMu   sync.Mutex
	ipSalt []byte
	ipDay  string

	// asyncW batches VayuShield's best-effort DB writes (learning upserts,
	// challenge/block records) off the request path so a bot flood — the very
	// situation the shield exists for — cannot saturate the single SQLite writer
	// and take VayuOS down. nil until StartAsyncWrites; writes then fall back to
	// synchronous execution.
	asyncW *dbbatch.Writer
}

// StartAsyncWrites launches the batched background writer for VayuShield's
// telemetry. Call once at boot with the process shutdown channel. Until called,
// telemetry writes run synchronously (as before).
func (m *Manager) StartAsyncWrites(done <-chan struct{}) {
	if m == nil || m.cfg.DB == nil || m.asyncW != nil {
		return
	}
	m.asyncW = dbbatch.New(m.cfg.DB, 8192, 256, time.Second)
	go m.asyncW.Run(done)
}

// AsyncWritesDropped reports telemetry writes shed under overload (observability).
func (m *Manager) AsyncWritesDropped() int64 {
	if m == nil {
		return 0
	}
	return m.asyncW.Dropped()
}

// recordExec runs a best-effort telemetry write: batched off the request path
// when the async writer is running (using the batch's own context, since the
// request context is gone by flush time), else synchronously on the caller's
// context. Errors are ignored — telemetry must never fail a request.
func (m *Manager) recordExec(ctx context.Context, query string, args ...any) {
	if m.cfg.DB == nil {
		return
	}
	if m.asyncW != nil {
		m.asyncW.Submit(func(bctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(bctx, query, args...)
			return err
		})
		return
	}
	_, _ = m.cfg.DB.ExecContext(ctx, query, args...)
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
	m.limiter = resilience.NewLimiter(2, 60, 200000)           // 120 rpm, burst 60
	m.violation = resilience.NewLimiter(20.0/60.0, 20, 200000) // ~20 breaches/min -> jail
	m.inflight = &resilience.InFlight{}
	m.blocklist = resilience.NewBlocklist()
	m.controller = &resilience.Controller{}
	m.prefilter = prefilter.New()
	m.brain = brain.New()
	m.calib = calibrate.New()
	m.jailTTLns.Store(int64(10 * time.Minute))
	// Seed the live config from the constructor Config (challenge tunables on,
	// resilience toggles off — all opt-in).
	m.ApplySettings(Settings{
		Enabled:              cfg.Enabled,
		PoWThreshold:         cfg.PoWThreshold,
		JSThreshold:          cfg.JSThreshold,
		BlockThreshold:       cfg.BlockThreshold,
		Tarpit:               cfg.TarpitEnabled,
		RatePerMinute:        120,
		Burst:                60,
		AutoBlockJailMinutes: 10,
		UnderAttackRPS:       200,
	})
	m.rotateIPSalt(cfg.Now().UTC())
	return m
}

// ApplySettings atomically swaps in a new runtime configuration — no restart.
// Numeric parameters are pushed into the resilience primitives; the boolean
// snapshot is published for the hot path to read lock-free.
// defaultMaxInFlight is the capacity-derived concurrency ceiling used when
// load-shedding is enabled without an explicit cap. It scales with the host so a
// bigger box tolerates a larger legitimate burst, with a generous floor so small
// sites are not throttled under normal traffic. Only saturating floods hit it.
func defaultMaxInFlight() int {
	n := runtime.NumCPU() * 64
	if n < 256 {
		n = 256
	}
	if n > 2048 {
		n = 2048
	}
	return n
}

func (m *Manager) ApplySettings(s Settings) {
	clamp := func(f, def float64) float64 {
		if f <= 0 || f > 1 {
			return def
		}
		return f
	}
	rpm := s.RatePerMinute
	if rpm <= 0 {
		rpm = 120
	}
	burst := s.Burst
	if burst <= 0 {
		burst = 60
	}
	m.limiter.SetRate(float64(rpm)/60.0, float64(burst))
	// L2 fair share follows the operator's per-IP rate: rpm/6 requests fit in
	// one 10 s sketch window; 3x headroom on top means fair-shedding starts only
	// at ~3x the configured sustained rate (default 120 rpm -> budget 60/window).
	m.prefilter.SetFairShare(rpm / 2)
	// In-flight load-shed cap. If shedding is enabled but the operator left the
	// cap at 0, do NOT leave the gate disabled (0 = unlimited = no protection) —
	// derive a generous, capacity-based ceiling so a flood is shed with a cheap
	// 503 before the render/SQLite path collapses. It is high enough that normal
	// bursts pass untouched, and only expensive uncached public work ever reaches
	// this gate (verified sessions and bypassed prefixes like /os are exempt).
	maxInFlight := int64(s.MaxInFlight)
	if s.LoadShed && maxInFlight <= 0 {
		maxInFlight = int64(defaultMaxInFlight())
	}
	m.inflight.SetMax(maxInFlight)
	rps := int64(0)
	if s.UnderAttack {
		rps = int64(s.UnderAttackRPS)
		if rps <= 0 {
			rps = 200
		}
	}
	m.controller.SetThreshold(rps)
	jm := s.AutoBlockJailMinutes
	if jm <= 0 {
		jm = 10
	}
	m.jailTTLns.Store(int64(time.Duration(jm) * time.Minute))

	m.liveCfg.Store(&liveConfig{
		enabled:     s.Enabled,
		pow:         clamp(s.PoWThreshold, 0.4),
		js:          clamp(s.JSThreshold, 0.6),
		block:       clamp(s.BlockThreshold, 0.8),
		tarpit:      s.Tarpit,
		rateLimit:   s.RateLimit,
		loadShed:    s.LoadShed,
		autoBlock:   s.AutoBlock,
		underAttack: s.UnderAttack,
	})
	// Preserve the resolved numeric fields for the dashboard.
	s.PoWThreshold = clamp(s.PoWThreshold, 0.4)
	s.JSThreshold = clamp(s.JSThreshold, 0.6)
	s.BlockThreshold = clamp(s.BlockThreshold, 0.8)
	s.RatePerMinute, s.Burst, s.AutoBlockJailMinutes = rpm, burst, jm
	m.curSettings.Store(&s)
}

// CurrentSettings returns the effective settings (for rendering the panel).
func (m *Manager) CurrentSettings() Settings {
	if s := m.curSettings.Load(); s != nil {
		return *s
	}
	return Settings{}
}

func (m *Manager) live() *liveConfig {
	if lc := m.liveCfg.Load(); lc != nil {
		return lc
	}
	return &liveConfig{}
}

func (m *Manager) jailTTL() time.Duration {
	if d := m.jailTTLns.Load(); d > 0 {
		return time.Duration(d)
	}
	return 10 * time.Minute
}

// Status is a live snapshot of the resilience meters for the dashboard.
type Status struct {
	UnderAttack bool  `json:"under_attack"`
	RPS         int64 `json:"rps"`
	InFlight    int64 `json:"in_flight"`
	Blocklisted int   `json:"blocklisted"`
	FairShed    int64 `json:"fair_shed"`  // cumulative L2 fair-shed count
	Suspects    int   `json:"suspects"`   // L5: sources currently under suspicion
	RepJailed   int   `json:"rep_jailed"` // L5: sources serving a reputation sentence
	Pardons     int64 `json:"pardons"`    // L5: cumulative challenge-proof pardons

	// L4 self-calibration: the loosen-only threshold bias currently applied
	// (0 = running exactly at the operator's thresholds) and the live window's
	// challenge counters it is derived from.
	CalibrationBias  float64 `json:"calibration_bias"`
	ChallengesServed int64   `json:"challenges_served"`
	ChallengesPassed int64   `json:"challenges_passed"`
}

// Status returns the current resilience meters.
func (m *Manager) Status() Status {
	bs := m.brain.Stats()
	served, passed, bias := m.calib.Snapshot()
	return Status{
		UnderAttack:      m.controller.UnderAttack(),
		RPS:              m.controller.RPS(),
		InFlight:         m.inflight.Current(),
		Blocklisted:      m.blocklist.Len(),
		FairShed:         m.prefilter.Shed(),
		Suspects:         bs.Tracked,
		RepJailed:        bs.Jailed,
		Pardons:          bs.Redeems,
		CalibrationBias:  bias,
		ChallengesServed: served,
		ChallengesPassed: passed,
	}
}

func (m *Manager) onEvent(a Action, score float64) {
	if m.cfg.OnEvent != nil {
		m.cfg.OnEvent(a, score)
	}
}

// ipOnly strips a port from a host:port client address so the rate limiter and
// blocklist key on the IP alone.
func ipOnly(s string) string {
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
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
	lc := m.live()
	if !lc.enabled {
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
	pow, js, block := lc.pow, lc.js, lc.block
	// Aegis L4 self-calibration: when the observed challenge pass rate says we
	// have been challenging real browsers, a loosen-only bias raises the
	// challenge thresholds so borderline humans are allowed instead. It can
	// never tighten below the operator's settings, and it never touches the
	// block threshold — self-heal must not open the door to confirmed bots.
	if bias := m.calib.Bias(); bias > 0 {
		pow += bias
		js += bias
	}
	// Adaptive "under attack" mode: during a flood, tighten the thresholds so
	// borderline traffic is challenged rather than served, and relax the moment
	// the flood subsides. Verified visitors and good bots already returned above,
	// so this only ever tightens the screening of unknown clients.
	if lc.underAttack && m.controller.UnderAttack() {
		if pow > 0.25 {
			pow = 0.25
		}
		if js > 0.5 {
			js = 0.5
		}
		if block > 0.7 {
			block = 0.7
		}
	}
	score := v.Result.BotScore
	switch {
	case score >= block:
		if lc.tarpit {
			return ActionTarpit
		}
		return ActionBlock
	case score >= js:
		return ActionChallengeJS
	case score >= pow:
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

// HasVerifiedSession reports whether the request carries a valid, signed
// session cookie. It is the exported form of hasValidSession, used by the L0
// Aegis sovereignty gate to grant priority admission to verified readers so a
// real logged-in visitor is never shed during a public-traffic flood. The
// check is unspoofable (HMAC-verified) and allocation-free.
func (m *Manager) HasVerifiedSession(r *http.Request) bool {
	return m.hasValidSession(r)
}

// RewardProof feeds the L5 reputation engine the strongest positive signal we
// have: this client solved a challenge, proving a real browser/human. Called
// by the PoW verification endpoint on success. It instantly pardons any
// reputation sentence the source was serving (false-positive self-heal for
// shared/NAT IPs).
func (m *Manager) RewardProof(r *http.Request) {
	m.brain.Observe(ipOnly(m.cfg.ClientIP(r)), brain.SignalProof)
}

// ctxKey carries the Verdict on the request context for downstream handlers
// (analytics reads it to classify traffic and record bot_score/client_type).
type ctxKey struct{}

// VerdictFrom returns the Verdict stashed by the middleware, if any.
func VerdictFrom(ctx context.Context) (Verdict, bool) {
	v, ok := ctx.Value(ctxKey{}).(Verdict)
	return v, ok
}

// Middleware wraps next with the resilience gates and the bot classification /
// challenge ladder. It is ordered cheapest-first so that under load the vast
// majority of abusive requests are dropped with an O(1) check *before* any
// expensive work (fingerprinting, rendering, SQLite):
//
//	blocklist → load-shed → rate-limit → (classify → challenge)
//
// Verified visitors (a valid signed session token, unspoofable) bypass the
// rate-limit and load-shed gates so a real reader is never throttled. When
// everything is disabled it is a transparent pass-through with near-zero cost.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lc := m.live()
		// With PressureFn wired, the L2 pre-filter stays live even when every
		// operator toggle is off: it observes each public request (a few atomic
		// ops) and fair-sheds heavy hitters automatically when the L0 lane
		// saturates — the shield's zero-configuration self-defense floor.
		resilienceActive := lc.rateLimit || lc.loadShed || lc.autoBlock || lc.underAttack ||
			m.cfg.PressureFn != nil
		if !lc.enabled && !resilienceActive {
			next.ServeHTTP(w, r)
			return
		}
		if m.isBypassed(r) {
			next.ServeHTTP(w, r)
			return
		}

		ipKey := ipOnly(m.cfg.ClientIP(r))

		// 1. Blocklist — the cheapest possible gate for a known-abusive IP.
		if lc.autoBlock && m.blocklist.Blocked(ipKey) {
			m.onEvent(ActionBlock, 1.0)
			m.serveThrottled(w, http.StatusTooManyRequests, "blocked", "10")
			return
		}

		// A previously-verified visitor is exempt from the availability gates —
		// their signed token cannot be spoofed, so they can never be throttled.
		verified := m.hasValidSession(r)

		// 1b. Aegis L5 — reputation jail. A source whose standing collapsed is
		// serving an automatic, escalating, self-expiring sentence: O(1) map
		// read, no classification. A verified session always passes (and a
		// solved challenge pardons the sentence — see RewardProof), so a real
		// user behind a shared/NAT IP is never locked out.
		if !verified && m.brain.Jailed(ipKey) {
			m.onEvent(ActionBlock, 1.0)
			m.serveThrottled(w, http.StatusTooManyRequests, "reputation", "60")
			return
		}

		// 2. Load shedding — protect the process from collapse under saturation.
		if lc.loadShed && !verified {
			if !m.inflight.Acquire() {
				m.serveThrottled(w, http.StatusServiceUnavailable, "load-shed", "5")
				return
			}
			defer m.inflight.Release()
		}

		// 3. Attack meter (lock-free) drives adaptive under-attack mode.
		if lc.underAttack {
			m.controller.Observe()
		}

		// 3b. Aegis L2 — probabilistic fair-shed pre-filter. Always observes
		// (fixed 256 KiB sketch, a few atomic ops) so heavy hitters are already
		// known the instant pressure appears; sheds ONLY under genuine pressure
		// (attack meter tripped, or the L0 sovereignty lane near its cap), and
		// only the traffic a source sends beyond its fair budget. A client at or
		// under budget — every real reader — can never be shed here.
		if !verified {
			pressure := m.controller.UnderAttack() ||
				(m.cfg.PressureFn != nil && m.cfg.PressureFn())
			if m.prefilter.Check(ipKey, pressure) {
				m.onEvent(ActionBlock, 1.0)
				m.serveThrottled(w, http.StatusTooManyRequests, "fair-shed", "5")
				return
			}
		}

		// 4. Per-IP rate limit — generous burst, so a normal reader never trips it.
		if lc.rateLimit && !verified && !m.limiter.Allow(ipKey) {
			if lc.autoBlock && !m.violation.Allow(ipKey) {
				// This IP keeps breaching the limit — jail it for a while.
				m.blocklist.Block(ipKey, m.jailTTL())
			}
			m.brain.Observe(ipKey, brain.SignalRateBurst)
			// If the breaches collapsed this source's reputation, escalate to
			// the O(1) blocklist jail immediately — subsequent requests are then
			// dropped by the very first gate (the brain reached the verdict the
			// legacy 20-breach violation meter would take 4x longer to reach).
			if lc.autoBlock && m.brain.Jailed(ipKey) {
				m.blocklist.Block(ipKey, m.jailTTL())
				// L1 offload: drop this flooding source in-kernel too.
				if m.cfg.OffloadFn != nil {
					m.cfg.OffloadFn(ipKey, m.jailTTL())
				}
			}
			m.onEvent(ActionBlock, 1.0)
			m.serveThrottled(w, http.StatusTooManyRequests, "rate-limited", "10")
			return
		}

		// 5. Bot classification + challenge ladder (only when enabled).
		if !lc.enabled {
			next.ServeHTTP(w, r)
			return
		}
		v := m.Classify(r)
		action := m.Decide(r, v)
		m.onEvent(action, v.Result.BotScore)

		// Learn: a strongly bot-like but as-yet unclassified fingerprint becomes
		// an auto-learned candidate for later operator review / auto-promotion.
		m.maybeLearn(r.Context(), v)

		ctx := context.WithValue(r.Context(), ctxKey{}, v)
		r = r.WithContext(ctx)

		switch action {
		case ActionAllow:
			// Sustained human-scored browsing slowly restores a suspect's
			// standing (no-op for untracked sources — the innocent are never
			// tracked), so a mis-scored real user heals automatically.
			if v.Result.BotScore < 0.3 {
				m.brain.Observe(ipKey, brain.SignalHuman)
			}
			next.ServeHTTP(w, r)
		case ActionChallengePoW:
			// Reputation-scaled difficulty (L5→L4): an unknown client works a
			// light, silent PoW; a source already under suspicion works harder.
			diff := challenge.DefaultDifficulty
			if m.brain.Standing(ipKey) < 0.3 {
				diff = challenge.HardDifficulty
			}
			m.serveChallenge(w, r, v, diff)
		case ActionChallengeJS:
			m.serveChallenge(w, r, v, challenge.HardDifficulty)
		case ActionTarpit:
			// A confirmed bad actor (score ≥ block threshold, tarpit variant).
			m.brain.Observe(ipKey, brain.SignalBlock)
			m.jailBadActor(r.Context(), ipKey, lc.autoBlock, v)
			m.serveTarpit(w, r, v, next)
		case ActionBlock:
			// A confirmed bad actor: jail the IP so the VERY NEXT request from it
			// is dropped by the O(1) blocklist gate above — without re-running
			// classification — and fold its fingerprint into the signature
			// knowledge base so the bot database grows from real detections.
			m.brain.Observe(ipKey, brain.SignalBlock)
			m.jailBadActor(r.Context(), ipKey, lc.autoBlock, v)
			m.serveBlock(w, r, v)
		}
	})
}

// serveThrottled writes a small, cheap rejection (429/503) with a Retry-After.
func (m *Manager) serveThrottled(w http.ResponseWriter, code int, reason, retryAfter string) {
	w.Header().Set("Retry-After", retryAfter)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VayuShield", reason)
	w.WriteHeader(code)
	_, _ = w.Write([]byte("Request rejected by VayuShield (" + reason + "). Please retry shortly."))
}

// maybeLearn records a bot-like unknown fingerprint as an auto-learned candidate.
func (m *Manager) maybeLearn(ctx context.Context, v Verdict) {
	if m.cfg.Bots == nil || v.Composite.FingerprintHash == "" {
		return
	}
	if v.Result.BotScore > 0.75 && v.Result.ClientType == botdb.TypeUnknown {
		obs := botdb.Observation{
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
		}
		// Batch the learning upsert off the request path when the async writer is
		// running (so a bot flood cannot serialise on the single writer).
		if m.asyncW != nil {
			m.asyncW.Submit(func(bctx context.Context, tx *sql.Tx) error {
				return m.cfg.Bots.ObserveTx(bctx, tx, obs)
			})
			return
		}
		_ = m.cfg.Bots.Observe(ctx, obs)
	}
}

// jailBadActor reacts to a confirmed bad-actor verdict (score >= block). When
// auto-block is enabled it jails the source IP in the O(1) in-memory blocklist,
// so the NEXT request from that IP is dropped immediately by the blocklist gate
// without re-running the classifier — "detect once, block thereafter". It also
// folds the client's fingerprint into the signature knowledge base as an
// auto-learned bad_bot candidate (operator-reviewable), so the bot database
// grows from real detections instead of only from the static seed list.
//
// GDPR: the jailed IP lives only in memory on a short, auto-expiring TTL (a
// security legitimate-interest measure, never written to disk); the learned
// signature stores only derived sub-hashes plus a coarse UA family — never an IP
// or the full User-Agent.
func (m *Manager) jailBadActor(ctx context.Context, ipKey string, autoBlock bool, v Verdict) {
	if autoBlock && ipKey != "" {
		m.blocklist.Block(ipKey, m.jailTTL())
		// L1 offload: a jailed confirmed bad actor is also dropped in-kernel,
		// so its follow-up packets never even create a connection.
		if m.cfg.OffloadFn != nil {
			m.cfg.OffloadFn(ipKey, m.jailTTL())
		}
	}
	if m.cfg.Bots == nil || v.Composite.FingerprintHash == "" {
		return
	}
	obs := botdb.Observation{
		FingerprintHash:   v.Composite.FingerprintHash,
		JA3:               v.Composite.JA3,
		JA4:               v.Composite.JA4,
		HTTP2SettingsHash: v.Composite.HTTP2SettingsHash,
		HeaderOrderHash:   v.Composite.HeaderOrderHash,
		UserAgentPattern:  uaFamily(v.Signals.UserAgent),
		PostQuantum:       v.Composite.PostQuantum,
		Classification:    botdb.ClassBadBot,
		Confidence:        0.7,
		AutoLearned:       true,
	}
	if m.asyncW != nil {
		m.asyncW.Submit(func(bctx context.Context, tx *sql.Tx) error {
			return m.cfg.Bots.ObserveTx(bctx, tx, obs)
		})
		return
	}
	_ = m.cfg.Bots.Observe(ctx, obs)
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
	m.calib.Served()
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
	// A solved challenge is the L4 calibrator's ground truth: this client was
	// a real browser. If the pass rate says we mostly challenge real browsers,
	// the thresholds loosen automatically.
	m.calib.Passed()
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
	m.recordExec(r.Context(), `INSERT INTO vayushield_challenges
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
	m.recordExec(r.Context(), `INSERT INTO vayushield_blocked
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

// interstitialTmpl renders the challenge interstitial. Using html/template
// (context-aware auto-escaping) rather than manual string quoting means the
// signed challenge JSON is escaped correctly for the data-attribute context —
// no possibility of breaking out of the attribute regardless of its contents.
var interstitialTmpl = template.Must(template.New("vayushield-interstitial").Parse(
	`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Verifying your browser…</title>
<meta name="robots" content="noindex">
</head><body>
<main style="max-width:32rem;margin:16vh auto;font-family:system-ui,sans-serif;text-align:center;color:#e5e7eb;background:#0b0f14;padding:2rem;border-radius:12px">
<h1 style="font-size:1.25rem">Verifying your browser…</h1>
<p style="color:#94a3b8">This automatic check protects the site from bots. It takes a moment and requires no interaction.</p>
<noscript><p style="color:#f59e0b">JavaScript is required to complete this check.</p></noscript>
<div id="vayushield-pow" data-challenge="{{.Challenge}}"></div>
<p id="vayushield-status" style="color:#64748b;font-size:.85rem">Working…</p>
</main>
<script src="/__vayushield/challenge.js"></script>
</body></html>`))

func interstitialHTML(challengeJSON string) string {
	var buf bytes.Buffer
	if err := interstitialTmpl.Execute(&buf, struct{ Challenge string }{Challenge: challengeJSON}); err != nil {
		return ""
	}
	return buf.String()
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
