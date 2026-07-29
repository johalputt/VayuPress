// SPDX-License-Identifier: Apache-2.0

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
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/dbbatch"
	"github.com/johalputt/vayupress/internal/reqclass"
	"github.com/johalputt/vayupress/internal/vayushield/behaviour"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/brain"
	"github.com/johalputt/vayupress/internal/vayushield/calibrate"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/fingerprint"
	"github.com/johalputt/vayupress/internal/vayushield/gossip"
	"github.com/johalputt/vayupress/internal/vayushield/inspect"
	"github.com/johalputt/vayupress/internal/vayushield/policy"
	"github.com/johalputt/vayupress/internal/vayushield/prefilter"
	"github.com/johalputt/vayupress/internal/vayushield/resilience"
	"github.com/johalputt/vayupress/internal/vayushield/scorer"
	"github.com/johalputt/vayupress/internal/vayushield/sigcache"
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

	Static *botdb.StaticDB
	Bots   *botdb.Store      // adaptive learning DB (nil disables learning)
	Signer *challenge.Signer // required for challenges; nil disables them
	// Capture is TLS ClientHello capture. It is nil in this binary, and that is a
	// structural fact rather than an oversight — recorded here because a declared
	// field with no assignment reads as a wiring bug and invites someone to
	// "fix" it by wiring it to nothing.
	//
	// Three reasons it stays nil:
	//
	//   • This process never terminates TLS. The web listener is plain HTTP behind
	//     a reverse proxy, so there is no handshake here to capture. No change in
	//     Go can produce a ClientHello that nginx already consumed.
	//   • crypto/tls cannot expose the raw extension list or its ORDER, which is
	//     most of what a JA3/JA4 value is. Values derived without it would never
	//     match public corpora, which removes the main reason to compute them.
	//   • Reading the handshake at the edge would mean an nginx JA3 module — a
	//     third-party dependency in precisely the place this product claims
	//     sovereignty.
	//
	// The consequence is measured, not asserted: see the cardinality test in
	// internal/vayushield/fingerprint, where 320 genuinely distinct clients
	// produce 16 distinct composite hashes. Two scorer branches are dead as a
	// result (the Go-default-HTTP/2-SETTINGS contradiction and the post-quantum
	// key share), which is also why the anti-poisoning guards around the adaptive
	// signature database are load-bearing rather than belt-and-braces.
	//
	// The derivation code is kept because it is correct and would run unchanged
	// wherever TLS IS terminated in-process. If that day comes, assign this field
	// and the fingerprint package's reachability test will fail and tell you to
	// restore the operator-facing copy that was retired for exactly this reason.
	Capture *fingerprint.Store
	DB      *sql.DB // for recording challenges/blocks (nil disables recording)

	// Score thresholds (0..1). Defaults: PoW 0.4, JS 0.6, Block 0.8.
	PoWThreshold   float64
	JSThreshold    float64
	BlockThreshold float64

	// TarpitEnabled turns Block into Tarpit for aggressive scrapers.
	TarpitEnabled bool

	// BypassPrefixes are path prefixes always allowed without a challenge
	// (feeds, health, the admin panel, the shield's own endpoints).
	BypassPrefixes []string

	// BypassFn is an optional predicate for machine endpoints whose path is not a
	// fixed prefix, evaluated after BypassPrefixes. It exists for paths derived
	// from runtime state — the IndexNow verification file is named after the
	// operator's rotatable key — where a static list would either go stale or have
	// to be widened into a shape an attacker could ride. Implementations must
	// match exactly and stay cheap: this runs on every request that reaches the
	// shield, so it must not perform a database read or any unbounded work.
	// nil disables it.
	BypassFn func(*http.Request) bool

	SessionCookieName string        // default "vayushield"
	SessionTTL        time.Duration // default 12h
	ChallengeTTL      time.Duration // default 5m
	CookieSecure      bool

	// OnionMode marks a Tor Space install (VAYUOS_MODE=tor): the site is served
	// over plain-http .onion, where the browser leaves window.crypto.subtle
	// undefined (it is a secure-context-only API), so the Web-Worker SHA-256
	// challenge solver cannot run and NO visitor could ever pass a challenge.
	// When set, the shield never issues a browser challenge (it fails open to the
	// content); the non-crypto gates — blocklist, reputation jail, rate limit,
	// load-shed, fair-shed — still enforce. This prevents a total lockout of a
	// Tor Space the moment any challenge band is reached.
	OnionMode bool

	// GroupIPv4 extends prefix-keyed enforcement to IPv4 /24. OFF by default and
	// deliberately opt-in: behind carrier-grade NAT a /24 can be thousands of
	// unrelated subscribers, so one jailed scraper takes a town's worth of real
	// readers with it. IPv6 /64 grouping is unconditional because an attacker
	// there gets 2^64 free identities and the exact-address key is no defence at
	// all — see enforcementKey.
	GroupIPv4 bool

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

	// SurgePressureFn reports CRITICAL saturation — the L0 lane at/near full, i.e.
	// the site is genuinely being overwhelmed. It is a higher bar than PressureFn
	// (which drives the gentler fair-shed): when it trips, Sovereign Surge
	// auto-engages so unproven traffic is bounced by a cheap challenge instead of
	// blindly shed. This catches an overwhelming swarm even when it does not spike
	// per-second RPS (a low-and-slow, high-cardinality flood that fills the lane).
	// Optional; a couple of atomic loads.
	SurgePressureFn func() bool

	// OffloadFn exports a jail verdict to the L1 kernel offload (an IP that
	// should be dropped by nftables/XDP for the given TTL). Optional and
	// best-effort: when nil, or when the root agent is not installed, the
	// in-binary gates keep enforcing on their own. Never called for verified
	// visitors — only for sources the shield has actively jailed.
	OffloadFn func(ip string, ttl time.Duration)

	// VerifiedBotFn authoritatively classifies a request's (client IP, UA) as a
	// legitimate crawler by IDENTITY, not by the spoofable UA string. See
	// BotFastPath for what each verdict earns.
	//
	// Optional; when nil the shield falls back to UA-only recognition
	// (IsGoodBotCandidate). Must be cheap and non-blocking (see internal/
	// vayushield/verifiedbot, which fronts everything with a sharded cache and
	// runs reverse-DNS off the hot path).
	VerifiedBotFn func(ip netip.Addr, ua string) BotFastPath

	// TrustedFn reports whether the request carries a valid OPERATOR login
	// session (the admin console cookie — not the shield's own PoW cookie).
	// A trusted request is structurally immune to every gate on EVERY path:
	// blocklist, reputation jail, load-shed, rate-limit, fair-shed and the
	// challenge ladder. This is the guarantee that the operator can never be
	// locked out of their own site — not even after load-testing it from
	// their own IP and getting that IP jailed. Optional; must be cheap when
	// the request carries no session cookie (a header parse).
	TrustedFn func(r *http.Request) bool
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
	surge          bool    // Aegis L3: operator-forced surge (challenge all unproven)
	groupIPv4      bool    // extend prefix-keyed enforcement to IPv4 /24 (opt-in)
	observe        bool    // observe-only: count what every gate WOULD do, enforce nothing
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

	// Surge is the operator's "Sovereign Surge" (a.k.a. Under-Attack) switch. When
	// on, every unproven visitor is met with the stateless PoW interstitial up
	// front — before any classification, fingerprint or SQLite read — so a
	// distributed swarm is absorbed at ~one HMAC per request. Surge ALSO engages
	// automatically (no toggle) whenever the L0 sovereignty lane nears saturation
	// or an active flood trips the attack meter, so the site self-defends even if
	// the operator never touches this.
	Surge bool

	// GroupIPv4 extends prefix-keyed enforcement to IPv4 /24. IPv6 /64 grouping
	// is unconditional (an attacker there gets 2^64 free identities and the
	// exact-address key is no defence at all); IPv4 is opt-in because a /24
	// behind carrier-grade NAT can be thousands of unrelated subscribers, so one
	// jailed scraper would take a town's worth of real readers with it. See
	// enforcementKey.
	GroupIPv4 bool

	// ObserveOnly turns every gate into a counter. Nothing is blocked, shed,
	// throttled or challenged; the shield reports what it WOULD have done to this
	// traffic. It is the primitive that makes a threshold change safe to roll out
	// — and while it is on the site is undefended, which the panel, the posture
	// report and the boot log all say plainly.
	ObserveOnly bool

	// Policy is the operator's own rules — allow/deny networks, country
	// verdicts, route weights. It rides in Settings rather than in a separate
	// setter so there is exactly ONE path that applies operator configuration:
	// two paths would eventually mean a save that updated the thresholds and
	// silently left the deny list on its previous value.
	Policy policy.Config
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
	redeem     *resilience.Limiter // per-IP redemption-challenge budget for jailed IPs
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

	// sigCache is the Aegis L6 signature-lookup cache: a fixed-memory, negative-
	// caching layer in front of the adaptive-DB Lookup SELECT, so classification
	// under load does not hit SQLite per request. Keyed on fingerprint hash only.
	sigCache *sigcache.Cache

	// surgeServed counts stateless surge-mode PoW interstitials served (telemetry).
	surgeServed atomic.Int64

	// redeemer makes a solved proof of work spendable exactly once.
	redeemer *challenge.Redeemer

	// clusterFields carries the multi-node verdict-sharing surface. Zero-valued
	// and inert on a single-node install, which is nearly all of them.
	clusterFields

	// policy holds the operator's own rules — allow/deny networks, country
	// verdicts, per-route cost. Swapped atomically because it is read on every
	// request and written only when an operator saves.
	policy atomic.Pointer[policy.Rules]

	// policyIssues are the entries the last compile could not parse, kept so the
	// panel can name them. A rule an operator wrote and the shield silently threw
	// away is worse than one it refused loudly.
	policyIssues atomic.Pointer[[]string]

	// inspectHits counts compiled-in inspection findings by tier (foreign-stack,
	// traversal, payload). Split rather than totalled because the tiers mean
	// very different things: a count that is nearly all payload is most likely
	// the site's own search box, and reporting one number would hide that.
	inspectHits [3]atomic.Int64

	// behaviour scores a client by what it DOES, which is the one signal a
	// client cannot fake for free. Every other input is either transport-derived
	// (unavailable behind a TLS-terminating proxy) or a header the client sets
	// itself — which is why a scraper that sets a Chrome User-Agent scores 0.15
	// and is classified as a human.
	behaviour *behaviour.Tracker

	// wouldHave counts, per gate, the requests observe-only mode let through that
	// enforcement would have stopped. Cumulative since boot, like every other
	// counter here — the audit-trail aggregates carry the time dimension.
	wouldHave [gateCount]atomic.Int64
	// surgeArmedNs is when operator-forced surge (the toggle) was last armed, in
	// unix nanoseconds; 0 = not armed. Operator-forced surge auto-expires after
	// maxForcedSurge so a forgotten toggle can never serve 503s indefinitely (a
	// sustained multi-day 503 deindexes URLs even with Retry-After). The RPS
	// auto-engage path needs no timer — it already relaxes when the flood passes.
	surgeArmedNs atomic.Int64

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
	// Redemption budget: a jailed IP is offered the (expensive) solvable
	// challenge at most ~once per 30s; every other request from it gets the
	// O(1) flat rejection. This keeps a real user behind a shared/jailed IP
	// able to redeem within seconds, while a hammering bot costs almost nothing.
	m.redeem = resilience.NewLimiter(1.0/30.0, 1, 200000)
	m.inflight = &resilience.InFlight{}
	m.blocklist = resilience.NewBlocklist()
	m.controller = &resilience.Controller{}
	m.prefilter = prefilter.New()
	m.brain = brain.New()
	m.calib = calibrate.New()
	m.redeemer = challenge.NewRedeemer()
	m.behaviour = behaviour.New()
	m.sigCache = sigcache.New(sigCacheTTL)
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

// maxForcedSurge bounds how long the operator's manual surge switch stays
// active. After this, surge auto-disarms even if the toggle is still on, so a
// forgotten switch cannot serve 503s (and risk deindexing) forever. A genuine
// ongoing attack is still covered by the RPS auto-engage path (no timer).
const maxForcedSurge = 12 * time.Hour

// sigCacheTTL bounds how long a cached signature-lookup result is served. Short,
// so a freshly-learned or pardoned signature takes effect within seconds even
// without an explicit invalidation; operator actions Clear the cache at once.
const sigCacheTTL = 45 * time.Second

// InvalidateSigCache drops every cached signature-lookup result. The cmd layer
// calls it after an operator verify / dismiss / report-false-positive so a
// verdict flip is never masked by a stale cache entry.
func (m *Manager) InvalidateSigCache() {
	if m != nil {
		m.sigCache.Clear()
	}
}

func (m *Manager) ApplySettings(s Settings) {
	// Arm / disarm the operator-forced surge timer on a genuine off->on edge, so
	// re-saving other settings while surge is on does not keep resetting (and
	// thus indefinitely extending) the auto-expiry window.
	prev := m.liveCfg.Load()
	switch {
	case s.Surge && (prev == nil || !prev.surge):
		m.surgeArmedNs.Store(m.cfg.Now().UnixNano())
	case !s.Surge:
		m.surgeArmedNs.Store(0)
	}
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

	// The operator's own rules. Malformed entries are skipped and REMEMBERED
	// rather than dropped: a silently discarded allow entry means the source it
	// was protecting starts getting challenged with no visible cause, which is
	// close to undiagnosable from the operator's side of the screen.
	rules, badPolicy := policy.Compile(s.Policy)
	m.policy.Store(&rules)
	m.policyIssues.Store(&badPolicy)

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
		surge:       s.Surge,
		groupIPv4:   s.GroupIPv4,
		observe:     s.ObserveOnly,
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

// underSurge reports whether Sovereign Surge (Aegis L3) is currently engaged, so
// the middleware meets an unproven visitor with the stateless PoW interstitial
// BEFORE any classification/fingerprint/SQLite work. Surge is a deliberate
// escalation — "challenge every unproven visitor" — so it engages only when:
//   - the operator forced it on (the explicit "Under Attack" switch), or
//   - adaptive under-attack mode is enabled AND the RPS meter says a flood is
//     live (a 1M-source swarm drives global RPS through the roof, so this fires
//     automatically during a real attack and relaxes the moment it passes).
//
// It deliberately does NOT engage on mild pressure (the L0 lane's PressureFn):
// that signal drives the graceful L2 fair-shed, which sheds only heavy hitters
// and never touches a client within its fair budget. Surge stays a higher tier
// so a merely-busy site (a legitimate traffic spike) never challenges everyone.
// Requires a signer (no signer => no surge, fail-open); both checks are a couple
// of atomic loads, so this is negligible on the hot path.
func (m *Manager) underSurge(lc *liveConfig) bool {
	if m.cfg.Signer == nil || m.cfg.OnionMode {
		// No signer → cannot challenge. Tor Space → crypto.subtle is unavailable,
		// so surge could only lock everyone out; never engage it there.
		return false
	}
	if lc.surge {
		// Operator-forced surge auto-expires after maxForcedSurge so a forgotten
		// switch cannot serve 503s forever. armed==0 is defensive (treat as live).
		armed := m.surgeArmedNs.Load()
		if armed == 0 || m.cfg.Now().Sub(time.Unix(0, armed)) < maxForcedSurge {
			return true
		}
	}
	// Critical L0 saturation: the site is being overwhelmed (a high-RPS flood OR a
	// low-and-slow distinct-IP swarm that fills the lane without spiking RPS). Both
	// auto-engage — no operator toggle, no configuration. Relaxes when it clears.
	if m.cfg.SurgePressureFn != nil && m.cfg.SurgePressureFn() {
		return true
	}
	return lc.underAttack && m.controller.UnderAttack()
}

// Status is a live snapshot of the resilience meters for the dashboard.
type Status struct {
	UnderAttack bool  `json:"under_attack"`
	RPS         int64 `json:"rps"`
	InFlight    int64 `json:"in_flight"`
	Blocklisted int   `json:"blocklisted"`
	FairShed    int64 `json:"fair_shed"`   // cumulative L2 fair-shed count
	WindowRate  int64 `json:"window_rate"` // L2: requests seen in the busiest recent sketch window
	Suspects    int   `json:"suspects"`    // L5: sources currently under suspicion
	RepJailed   int   `json:"rep_jailed"`  // L5: sources serving a reputation sentence
	Pardons     int64 `json:"pardons"`     // L5: cumulative challenge-proof pardons

	// L3 Sovereign Surge: whether surge is engaged right now, and the cumulative
	// count of up-front surge interstitials served (each ~one HMAC, no DB/render).
	SurgeActive     bool  `json:"surge_active"`
	SurgeChallenges int64 `json:"surge_challenges"`

	// L6 signature-lookup cache: hits served from memory vs misses that fell
	// through to the adaptive-DB SELECT.
	SigCacheHits   int64 `json:"sig_cache_hits"`
	SigCacheMisses int64 `json:"sig_cache_misses"`

	// L4 self-calibration: the loosen-only threshold bias currently applied
	// (0 = running exactly at the operator's thresholds) and the live window's
	// challenge counters it is derived from.
	CalibrationBias  float64 `json:"calibration_bias"`
	ChallengesServed int64   `json:"challenges_served"`
	ChallengesPassed int64   `json:"challenges_passed"`

	// L7 compiled-in request inspection: cumulative findings, split by tier so a
	// number that is almost all ClassPayload reads differently from one that is
	// almost all ClassForeignStack — the first is probably the site's own search
	// box, the second is unambiguously scanners.
	InspectFindings [3]int64 `json:"inspect_findings"` // foreign-stack, traversal, payload
	InspectRules    int      `json:"inspect_rules"`
	InspectRuleset  int      `json:"inspect_ruleset"`

	// Observe-only mode: whether it is engaged, and how many requests each gate
	// would have acted on but did not. Indexed by Gate; GateNames labels them.
	ObserveOnly bool             `json:"observe_only"`
	WouldHave   [gateCount]int64 `json:"would_have"`
}

// Status returns the current resilience meters.
func (m *Manager) Status() Status {
	bs := m.brain.Stats()
	served, passed, bias := m.calib.Snapshot()
	return Status{
		ObserveOnly:      m.live().observe,
		WouldHave:        m.WouldHave(),
		UnderAttack:      m.controller.UnderAttack(),
		RPS:              m.controller.RPS(),
		InFlight:         m.inflight.Current(),
		Blocklisted:      m.blocklist.Len(),
		FairShed:         m.prefilter.Shed(),
		WindowRate:       m.prefilter.WindowRate(),
		Suspects:         bs.Tracked,
		RepJailed:        bs.Jailed,
		Pardons:          bs.Redeems,
		CalibrationBias:  bias,
		ChallengesServed: served,
		ChallengesPassed: passed,
		SurgeActive:      m.underSurge(m.live()),
		SurgeChallenges:  m.surgeServed.Load(),
		SigCacheHits:     m.sigCache.Hits(),
		SigCacheMisses:   m.sigCache.Misses(),
		InspectFindings:  m.InspectFindings(),
		InspectRules:     inspect.RuleCount(),
		InspectRuleset:   inspect.RulesetVersion,
	}
}

func (m *Manager) onEvent(a Action, score float64) {
	if m.cfg.OnEvent != nil {
		m.cfg.OnEvent(a, score)
	}
}

// CaptureWired reports whether TLS ClientHello capture is available to this
// manager. Without it, classification runs on HTTP signals alone — which an
// attacker controls entirely — so the posture report states it rather than
// leaving an operator to assume the deeper signal is in play.
func (m *Manager) CaptureWired() bool { return m != nil && m.cfg.Capture != nil }

// Gate identifies one enforcement point in the middleware, for observe-only
// mode's would-have counters.
//
// There are eight of them and they are NOT reducible to Decide's Action. Only
// the classification ladder goes through Decide; the blocklist, the reputation
// jail, load shedding, fair shedding, the rate limiter and Sovereign Surge are
// inline early returns that never call it. That is why observe mode is a change
// to the hottest function in the codebase rather than a flag on one switch.
type Gate uint8

const (
	GateBlocklist  Gate = iota // the O(1) jail
	GateReputation             // the self-learning sentence
	GateLoadShed               // the in-flight cap
	GateFairShed               // the L2 probabilistic pre-filter
	GateRateLimit              // the per-source token bucket
	GateSurge                  // Sovereign Surge's up-front interstitial
	GateChallenge              // the classification ladder's PoW/JS challenge
	GateBlock                  // the ladder's hard block or tarpit
	GatePolicyDeny             // an operator's own allow/deny list
	GateGeoDeny                // an operator's own country rules
	gateCount
)

// GateNames are stable identifiers for metrics labels and the panel. Changing
// one silently breaks an operator's dashboard, so they are written once here.
var GateNames = [gateCount]string{
	"blocklist", "reputation", "load_shed", "fair_shed",
	"rate_limit", "surge", "challenge", "block",
	"policy_deny", "geo_deny",
}

// SetPolicy installs the operator's own rules. Safe to call at any time.
// ApplySettings is the normal path; this exists for tests and for callers that
// compile a rule set themselves.
func (m *Manager) SetPolicy(r policy.Rules) { m.policy.Store(&r) }

// PolicyIssues returns the operator-written entries the last compile could not
// parse, so the panel can name them instead of leaving a typo to be discovered
// by its effect.
func (m *Manager) PolicyIssues() []string {
	if p := m.policyIssues.Load(); p != nil {
		return *p
	}
	return nil
}

// Policy returns the live rules. Never nil.
func (m *Manager) Policy() policy.Rules {
	if p := m.policy.Load(); p != nil {
		return *p
	}
	return policy.Rules{}
}

// InspectFindings returns the inspection counters by tier, so the panel can
// show the split rather than a single number that conflates a scanner sweep
// with a reader searching for "union select".
func (m *Manager) InspectFindings() [3]int64 {
	var out [3]int64
	for i := range m.inspectHits {
		out[i] = m.inspectHits[i].Load()
	}
	return out
}

// RouteCost reports how many sovereignty-lane slots a request should reserve,
// per the operator's route weights. Always at least 1.
//
// Costs nothing on an install that has configured no policy, which is nearly
// all of them: the compiled rule set reports itself empty and the path returns
// before touching the request at all.
func (m *Manager) RouteCost(r *http.Request) int {
	if m == nil || r == nil || r.URL == nil {
		return 1
	}
	pol := m.Policy()
	if pol.Empty() {
		return 1
	}
	return pol.CostOf(r.Host, r.URL.Path, r.Method)
}

// observing reports whether observe-only mode is engaged and, when it is,
// records that this gate WOULD have acted.
//
// Callers put it last in the condition so the counter only moves when the gate
// would genuinely have fired:
//
//	if gateWouldFire && !m.observing(lc, GateX) { …enforce…; return }
//
// The point of the mode is to make a threshold change safe to roll out: an
// operator can see what a setting would have done to their own traffic before it
// does it. The cost is real and stated in the panel — while it is on, none of
// these gates enforce anything.
func (m *Manager) observing(lc *liveConfig, g Gate) bool {
	if lc == nil || !lc.observe || g >= gateCount {
		return false
	}
	m.wouldHave[g].Add(1)
	return true
}

// offload pushes a ban to the kernel, unless observe mode is engaged.
//
// This suppression is the sharpest correctness point in the whole mode.
// Everything else the middleware decides is an in-memory verdict it can simply
// decline to act on, and a request that was let through is still served. A
// kernel drop happens OUTSIDE this process and cannot be un-dropped: an
// "observe-only" mode that still installed nftables bans would be enforcing,
// silently, in the one layer the operator cannot see from the panel.
//
// In-memory state (the blocklist, the reputation brain) is still updated in
// observe mode on purpose, so the counters reflect what a real rollout would
// look like — including escalation — rather than a first-request-only sample.
func (m *Manager) offload(lc *liveConfig, ip string, ttl time.Duration) {
	if lc != nil && lc.observe {
		return
	}
	if m.cfg.OffloadFn != nil {
		m.cfg.OffloadFn(ip, ttl)
	}
}

// WouldHave returns the observe-mode counters, indexed by Gate.
func (m *Manager) WouldHave() [gateCount]int64 {
	var out [gateCount]int64
	for i := range m.wouldHave {
		out[i] = m.wouldHave[i].Load()
	}
	return out
}

// Observing reports whether observe-only mode is currently engaged.
//
// Deliberately read from the LIVE manager rather than from the saved setting.
// The setting says what an operator wrote; this says what is in force, and the
// whole point of the posture report is to prefer the second.
func (m *Manager) Observing() bool { return m != nil && m.live().observe }

// ipOnly strips a port from a host:port client address so the rate limiter and
// blocklist key on the IP alone.
func ipOnly(s string) string {
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

// enforcementKey is the identity the rate limiter, violation meter, blocklist and
// reputation brain count against. It is NOT always the exact address.
//
// IPv6 is the reason. A routed /64 is the standard allocation for one network,
// and every address inside it is free to use — so an attacker on a single /64 has
// 2^64 identities. Keyed on the exact address, none of the durable gates can ever
// catch up: each request arrives as a first-time visitor, no bucket is ever
// drained twice, no jail sentence is ever served, and reputation never
// accumulates. The exact-address key is not a weak defence there, it is no
// defence at all.
//
// Why /64 and not the /48 the L2 pre-filter groups on: those two layers do
// different jobs and the granularity should follow the job.
//
//   - The pre-filter is DETECTION. It sheds probabilistically, only under
//     pressure, and forgets. A wide group is right: it is looking for a pattern
//     spread across a site's whole allocation, and a false positive costs one
//     shed request.
//   - This key is ENFORCEMENT. A jail sentence is durable and punitive. A /48 can
//     be an entire company or a whole residential allocation, so jailing one
//     would punish thousands of unrelated people for one machine. /64 is the
//     smallest unit that closes the 2^64 hole, and it is the unit a single
//     network actually controls.
//
// IPv4 is deliberately NOT grouped by default. A /24 behind carrier-grade NAT can
// be thousands of unrelated subscribers, so grouping it trades a real
// availability guarantee for a marginal gain against an attacker who, unlike the
// IPv6 case, has to pay for every address. Operators who know their traffic can
// turn it on; the collateral is theirs to accept, not ours to impose.
//
// The exact address is still used for the kernel offload and the L2 sketch — see
// the call site.
func (m *Manager) enforcementKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		// Not an address at all. Pass it through verbatim rather than folding
		// every unknown source onto one shared key, which would make a single
		// malformed client able to jail all of them.
		return ip
	}
	// An IPv4-mapped IPv6 literal is an IPv4 client. Unmapping is not cosmetic:
	// treating ::ffff:a.b.c.d as v6 would group it into a /64 spanning the whole
	// of IPv4, and returning the string as given would let one host hold two
	// identities by arriving in either notation.
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		if !m.live().groupIPv4 && !m.cfg.GroupIPv4 {
			return addr.String()
		}
		if p, err := addr.Prefix(24); err == nil {
			return p.String()
		}
		return addr.String()
	}
	if p, err := addr.Prefix(64); err == nil {
		return p.String()
	}
	return addr.String()
}

// BotFastPath is how much benefit the verified-bot recogniser's verdict earns.
//
// The distinction between BotVerified and BotUnprovable is the whole point of
// this type, and collapsing the two was a bypass of the entire shield. Both used
// to return a single allow=true, which took gate 0's `next.ServeHTTP; return` —
// ahead of the blocklist, the reputation jail, load shedding, fair shedding, the
// rate limiter and Sovereign Surge. BotUnprovable is reached by claiming one of
// ~34 UA-only vendor strings from ANY address, by an in-flight FCrDNS lookup,
// and — on a fresh boot, before the published-range feeds have loaded — by a
// plain "Googlebot" UA from any address. So any client could opt out of every
// gate by choosing a User-Agent.
type BotFastPath uint8

const (
	// BotNotRecognised — not a crawler this build knows. Handle normally.
	BotNotRecognised BotFastPath = iota

	// BotVerified — identity confirmed against the vendor's published ranges or
	// by forward-confirmed reverse DNS. Earns the full gate-0 fast path, because
	// the identity is a fact about the network, not a claim in a string.
	BotVerified

	// BotUnprovable — a recognised crawler UA that can be neither confirmed nor
	// disproved. Earns exemption from CHALLENGES ONLY.
	//
	// That is the actual SEO reason this path exists: a non-JS crawler cannot
	// solve a proof-of-work, and a search engine reads sustained non-200s as a
	// crawl error and de-indexes. It is NOT a reason to skip availability gates,
	// which answer 429/503 with Retry-After — a back-off signal every real
	// crawler handles correctly, and one an impostor gains nothing by receiving.
	BotUnprovable

	// BotSpoof — the UA claims a crawler whose real network this address is not.
	// Earns nothing, and additionally forfeits the good-bot allow it would
	// otherwise get from the classifier.
	BotSpoof
)

// spoofSuspectKey marks a request whose User-Agent claims a crawler that the
// verified-bot engine could not confirm (the IP is not the vendor's real
// network). Decide reads it so the spoofed UA cannot short-circuit to a good-bot
// allow — the request falls to behavioural scoring instead.
type spoofSuspectKey struct{}

func withSpoofSuspect(ctx context.Context) context.Context {
	return context.WithValue(ctx, spoofSuspectKey{}, true)
}

func isSpoofSuspect(ctx context.Context) bool {
	v, _ := ctx.Value(spoofSuspectKey{}).(bool)
	return v
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

	// Behavioural signals, as a second independent input.
	//
	// Independent is the operative word: every other input is either
	// transport-derived — and empty behind a TLS-terminating proxy — or a header
	// the client sets itself. Behaviour is the only one a client cannot fake
	// without paying the cost of actually behaving like a browser.
	//
	// It is bounded on purpose (behaviour.MaxDelta) so it can move an unknown
	// client into a solvable challenge and never, on its own, into a block. Each
	// signal has a legitimate client that trips it — a feed reader fetches no
	// assets, a broken link produces 404s — so a verdict from heuristics alone
	// would be a verdict on the wrong person often enough to matter.
	if m.behaviour != nil {
		bs := m.behaviour.Observe(m.enforcementKey(ipOnly(m.cfg.ClientIP(r))), r.URL.Path, 0)
		in.BehaviourDelta, in.BehaviourReasons = bs.Score()
	}

	// Compiled-in request inspection, as a third independent input.
	//
	// It answers a question behaviour cannot answer quickly: behaviour needs a
	// sample before any ratio means anything, and even then a high 404 ratio
	// cannot tell a scanner from a site with broken links. A single request for
	// /wp-login.php can — this binary serves no PHP, so no reader's browser
	// produces one.
	//
	// Only the path and the query, never a body or a header. The editor submits
	// bodies, and an author writing an article about SQL injection has to be able
	// to publish it. The contribution shares one clamped budget with behaviour
	// (scorer.HeuristicBudget) rather than carrying its own, because two bounds
	// that are individually correct still add up to a hard block.
	if f, ok := inspect.Scan(r.URL.Path, r.URL.RawQuery); ok {
		in.InspectDelta = f.Delta()
		in.InspectReasons = []string{f.Reason()}
		if i := int(f.Class) - 1; i >= 0 && i < len(m.inspectHits) {
			m.inspectHits[i].Add(1)
		}
	}
	if m.cfg.Static != nil {
		if s, ok := m.cfg.Static.MatchUA(sig.UserAgent); ok {
			in.StaticMatch = &s
		}
	}
	if m.cfg.Bots != nil && comp.FingerprintHash != "" {
		// L6 cache first: most unproven traffic under load is a fingerprint the DB
		// has never seen (legit readers, flash crowds), so the negative cache keeps
		// that SELECT off the read pool. A miss falls through to the real Lookup and
		// records the outcome (positive OR negative). Short TTL bounds staleness;
		// operator actions Clear the cache for instant invalidation.
		if learned, found, hit := m.sigCache.Get(comp.FingerprintHash); hit {
			if found {
				in.Learned = learned
			}
		} else if learned, ok := m.cfg.Bots.Lookup(r.Context(), comp.FingerprintHash); ok {
			in.Learned = learned
			m.sigCache.Put(comp.FingerprintHash, learned, true)
		} else {
			m.sigCache.Put(comp.FingerprintHash, nil, false)
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
	case botdb.TypeGoodBot, botdb.TypeAIAgent:
		// A recognised good bot is allowed UNLESS the verified-bot engine flagged
		// it a spoof suspect (crawler UA from an IP that is not the vendor's). In
		// that case the UA no longer earns a free pass — fall through to
		// behavioural scoring so a spoofed "Googlebot" is challenged like any other
		// unproven client. A genuine crawler is verified upstream and never reaches
		// here, so this only ever tightens screening of impostors.
		if !isSpoofSuspect(r.Context()) {
			return ActionAllow
		}
	case botdb.TypeHuman:
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

// isBypassed reports whether the path is always allowed. Matching is
// path-boundary-aware: a prefix "/os" matches "/os" and "/os/…" but NOT a
// public URL that merely starts with those characters (e.g. a blog post at
// "/oslo-guide" or "/static-site-generators"). A bare HasPrefix would silently
// disable bot protection for such posts — this must not happen.
func (m *Manager) isBypassed(r *http.Request) bool {
	p := r.URL.Path
	for _, pre := range m.cfg.BypassPrefixes {
		if pre == "" {
			continue
		}
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	// A dynamic bypass for machine endpoints whose path is not a fixed prefix —
	// today the site-root IndexNow verification file, whose name IS the operator's
	// rotatable key. A static list cannot express that, and the caller (a search
	// engine's key verifier) can no more solve a challenge than a feed reader can.
	// The predicate is supplied by the app layer and must match exactly.
	if m.cfg.BypassFn != nil && m.cfg.BypassFn(r) {
		return true
	}
	// Feeds/sitemaps/robots are machine endpoints legit clients (feed readers,
	// crawlers, unfurlers) hit WITHOUT JS — they can never solve a challenge, so
	// a surge interstitial in place of a feed silently breaks every subscriber.
	// Match them broadly by shape, not a short exact list (which missed Hugo's
	// /index.xml and WordPress /feed/). These are cheap, cacheable, and still
	// bounded by the L0 public-concurrency lane, so bypassing the shield's
	// challenge for them is safe.
	return isFeedLikePath(p)
}

// IsFeedLikePath reports whether a path is a feed/sitemap/robots endpoint by
// shape. Exported so the L0 sovereign lane (cmd layer) can grant these machine
// endpoints the same never-shed priority the shield already gives them: a 503
// on robots.txt pauses ALL crawling and a 503 on sitemap.xml drops URLs, so
// these must always reach the handler even while a flood saturates the public
// lane. The set is small, cacheable and cheap, so priority-admitting it is safe.
func IsFeedLikePath(p string) bool { return isFeedLikePath(p) }

// isFeedLikePath reports whether a path is a feed, sitemap, or robots endpoint
// by shape: any *.xml/*.atom/*.rss (covers /index.xml, /sitemap.xml, per-tag
// feeds), a /feed or /rss path segment (WordPress /feed/, /comments/feed/), or
// robots.txt / feed.json.
func isFeedLikePath(p string) bool {
	if strings.HasSuffix(p, ".xml") || strings.HasSuffix(p, ".atom") || strings.HasSuffix(p, ".rss") {
		return true
	}
	switch p {
	case "/robots.txt", "/feed", "/rss", "/feed.json":
		return true
	}
	return strings.HasSuffix(p, "/feed") || strings.HasSuffix(p, "/feed/") ||
		strings.Contains(p, "/feed/") || strings.HasSuffix(p, "/rss") || strings.HasSuffix(p, "/rss/")
}

// IsGoodBotCandidate reports whether the request's User-Agent matches a known
// search-engine or AI crawler in the static classifier (Googlebot, Bingbot,
// Applebot, DuckDuckBot, GPTBot, ClaudeBot, PerplexityBot, PageSpeed/Lighthouse,
// uptime monitors, …).
//
// It exists to LOOSEN handling for SEO safety, never to block: a recognised
// crawler must be served the real page and never met with a rate-limit 429, a
// load-shed/surge 503, or the JS "verify your browser" interstitial — none of
// which a non-JS crawler can satisfy, and each of which Google reads as a crawl
// error that de-indexes the URL.
//
// Phase 1 recognises by User-Agent ONLY. A UA is spoofable, so this is used
// exclusively on the allow/loosen path — a spoofed crawler UA can, for now,
// ride the crawler fast path but gains nothing it could not already get by
// simply behaving like a browser. Phase 2 (internal/vayushield/verifiedbot)
// replaces the trust decision with published-IP-range + forward-confirmed
// reverse-DNS verification, so only a crawler proven to come from the vendor's
// real network is fast-pathed. Deliberately does NOT recognise bad bots — those
// stay on the normal classification path.
func (m *Manager) IsGoodBotCandidate(r *http.Request) bool {
	if m == nil || m.cfg.Static == nil {
		return false
	}
	sig, ok := m.cfg.Static.MatchUA(r.UserAgent())
	if !ok {
		return false
	}
	return sig.Classification == botdb.ClassGoodBot || sig.Classification == botdb.ClassAIAgent
}

// clientBind is the per-client key a session/clearance cookie is cryptographically
// bound to: the client IP (port stripped). It never leaves the process and is
// never stored — it is folded into the token MAC only — so a clearance cookie is
// worthless when replayed from a different source (defeating "solve once, replay
// across the botnet"), with no new PII at rest. A client whose IP changes
// (roaming mobile) simply re-proves, a ~100 ms cost.
func (m *Manager) clientBind(r *http.Request) string {
	return ipOnly(m.cfg.ClientIP(r))
}

func (m *Manager) hasValidSession(r *http.Request) bool {
	if m.cfg.Signer == nil {
		return false
	}
	c, err := r.Cookie(m.cfg.SessionCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return m.cfg.Signer.VerifySession(c.Value, m.clientBind(r))
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
	ip := ipOnly(m.cfg.ClientIP(r))
	m.brain.Observe(ip, brain.SignalProof)
	// The pardon also lifts the blocklist jail, so every browser and device
	// behind that IP recovers the moment one of them proves human.
	m.blocklist.Unblock(ip)
	// And it is shared, because a false positive that heals only on the node the
	// visitor happened to land on is a false positive that keeps recurring: the
	// next request goes to a different node, gets challenged again, and the
	// reader concludes the site is broken.
	m.shareVerdict(gossip.KindPardon, m.enforcementKey(ip), 0)
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
			lc.surge || m.cfg.PressureFn != nil
		if !lc.enabled && !resilienceActive {
			next.ServeHTTP(w, r)
			return
		}
		if m.isBypassed(r) {
			next.ServeHTTP(w, r)
			return
		}

		ipKey := ipOnly(m.cfg.ClientIP(r))

		// The operator's own rules, before anything the shield infers.
		//
		// Placed ahead of the verified-session short-circuit deliberately. Every
		// other gate below yields to a signed session, because a visitor who solved
		// a challenge has proved something about themselves. An operator's DENY has
		// not been out-argued by that proof: they said not to serve this network,
		// and a rule that a client can escape by solving a puzzle is not a rule.
		//
		// The symmetric case is the reason ALLOW sits here too: it is the escape
		// hatch that gets an operator back into their own site after a
		// false-positive run, and an escape hatch that is itself subject to the
		// jail is no escape hatch.
		if pol := m.Policy(); !pol.Empty() {
			switch pol.Source(ipKey) {
			case policy.VerdictDeny:
				if !m.observing(lc, GatePolicyDeny) {
					reqclass.MarkShielded(r.Context())
					m.onEvent(ActionBlock, 1.0)
					m.serveJailed(w, r, ipKey, "operator policy")
					return
				}
			case policy.VerdictAllow:
				m.onEvent(ActionAllow, 0)
				next.ServeHTTP(w, r)
				return
			}
			if m.cfg.CountryFn != nil && pol.GeoActive() {
				if pol.Country(m.cfg.CountryFn(ipKey)) == policy.VerdictDeny &&
					!m.observing(lc, GateGeoDeny) {
					reqclass.MarkShielded(r.Context())
					m.onEvent(ActionBlock, 1.0)
					m.serveJailed(w, r, ipKey, "operator geo policy")
					return
				}
			}
		}

		// enfKey is the identity the DURABLE gates count against — the /64 for
		// IPv6, and the /24 for IPv4 only when the operator opted in. Keyed on the
		// exact address, an attacker with one routed /64 arrives as a brand-new
		// visitor 2^64 times and no bucket, sentence or reputation ever applies.
		//
		// ipKey stays the exact address in two places, on purpose:
		//   • the L2 pre-filter, which does its own (wider, /48) grouping
		//     internally and needs the address to do it;
		//   • OffloadFn, which hands the value to the kernel banlist. Those nft
		//     sets are declared without `flags interval`, so no CIDR element can
		//     be added to them at all — passing a prefix there would not widen the
		//     ban, it would be rejected. Teaching the kernel path about prefixes
		//     is a separate change (set redefinition, on-disk format, overlap
		//     collapse), not a one-line substitution.
		enfKey := m.enforcementKey(ipKey)

		// A verified visitor (signed PoW session, unspoofable) or a trusted
		// operator (valid admin login session) is exempt from EVERY gate below —
		// including the blocklist and reputation jail. This must be decided
		// BEFORE the jail gates: the operator who load-tests their own site from
		// their own IP will jail that IP, and they must still be able to browse
		// it, save settings and load assets from the very next request.
		verified := m.hasValidSession(r) ||
			(m.cfg.TrustedFn != nil && m.cfg.TrustedFn(r))

		// 0. Crawler SEO fast path (VayuShield Phase 1). A recognised search-engine
		// or AI crawler is served the real page BEFORE any availability gate. A
		// crawler carries no signed session, so without this it is treated as an
		// anonymous stranger by every gate below — the rate-limit 429, the
		// load-shed / fair-shed / Sovereign-Surge 503, and the JS "verify your
		// browser" interstitial are all things a non-JS crawler cannot satisfy, and
		// Google/Bing read a sustained non-200 (or a noindex challenge body) on a
		// content URL as a crawl error and drop the page from the index. That is the
		// exact mechanism that de-indexed the site. Recognition here is by
		// User-Agent only (see IsGoodBotCandidate) — a strictly-loosening SEO
		// safeguard, never used to block; Phase 2 hardens it with published-IP-range
		// + FCrDNS confirmation so a spoofed UA cannot ride this path. The operator
		// "block crawlers" (go-dark) switch runs in an OUTER middleware and still
		// 403s crawlers when deliberately enabled, so this never overrides it.
		if !verified {
			if fn := m.cfg.VerifiedBotFn; fn != nil {
				// Spoof-proof path: identity is confirmed by published IP range /
				// reverse DNS, not the UA string.
				ipAddr, _ := netip.ParseAddr(ipKey)
				switch fn(ipAddr, r.UserAgent()) {
				case BotVerified:
					// Identity is a fact about the network. Full fast path.
					m.onEvent(ActionAllow, 0)
					next.ServeHTTP(w, r)
					return
				case BotUnprovable:
					// A crawler UA nobody can confirm or disprove gets NO fast path:
					// it falls through to the blocklist, the jail, load shedding and
					// the rate limiter like any other stranger. Those answer 429/503
					// with Retry-After, which a real crawler backs off on and an
					// impostor gains nothing from.
					//
					// Its protection from CHALLENGES is already provided further
					// down, by Decide's TypeGoodBot branch: a recognised crawler UA
					// returns ActionAllow there unless it is a spoof suspect. That is
					// the correct place for it — the exemption belongs to "looks like
					// a crawler", which is a classification question, not to "we
					// could not verify it", which is an identity question. Verified
					// by test: a Googlebot UA is served without a challenge with no
					// special handling here.
				case BotSpoof:
					// A UA claiming a crawler from an IP that is not the vendor's —
					// strip its good-bot benefit for the rest of the pipeline.
					r = r.WithContext(withSpoofSuspect(r.Context()))
				}
			} else if m.IsGoodBotCandidate(r) {
				// No verifier wired: fall back to Phase-1 UA-only recognition.
				m.onEvent(ActionAllow, 0)
				next.ServeHTTP(w, r)
				return
			}
		}

		// 1. Blocklist — the cheapest possible gate for a known-abusive IP.
		if !verified && lc.autoBlock && m.blocklist.Blocked(enfKey) && !m.observing(lc, GateBlocklist) {
			reqclass.MarkShielded(r.Context())
			m.onEvent(ActionBlock, 1.0)
			m.serveJailed(w, r, ipKey, "blocked")
			return
		}

		// 1b. Aegis L5 — reputation jail. A source whose standing collapsed is
		// serving an automatic, escalating, self-expiring sentence: O(1) map
		// read, no classification. A verified session always passes (and a
		// solved challenge pardons the sentence — see RewardProof), so a real
		// user behind a shared/NAT IP is never locked out.
		//
		// It is gated on the operator's auto-block switch, exactly like the
		// blocklist gate above. Jailing is an availability defence, so when the
		// operator has turned auto-blocking OFF the shield must not keep serving
		// sentences behind their back: an operator who disables every anti-DDoS
		// gate and still saw real readers met with the "just a moment" throttle
		// page has no switch left to reach for, which is precisely the trap this
		// closes. Classification/challenges are unaffected — they are governed by
		// the bot-protection switch, not this one.
		if !verified && lc.autoBlock && m.brain.Jailed(enfKey) && !m.observing(lc, GateReputation) {
			reqclass.MarkShielded(r.Context())
			m.onEvent(ActionBlock, 1.0)
			m.serveJailed(w, r, ipKey, "reputation")
			return
		}

		// 2. Load shedding — protect the process from collapse under saturation.
		if lc.loadShed && !verified {
			if m.inflight.Acquire() {
				defer m.inflight.Release()
			} else if !m.observing(lc, GateLoadShed) {
				reqclass.MarkShielded(r.Context())
				m.serveThrottled(w, r, http.StatusServiceUnavailable, "load-shed", "5")
				return
			}
			// Observed only: proceed WITHOUT a slot, so there is no Release to
			// defer. Releasing one that was never acquired would inflate the cap
			// by one for every observed shed.
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
			if m.prefilter.Check(ipKey, pressure) && !m.observing(lc, GateFairShed) {
				reqclass.MarkShielded(r.Context())
				m.onEvent(ActionBlock, 1.0)
				m.serveThrottled(w, r, http.StatusTooManyRequests, "fair-shed", "5")
				return
			}
		}

		// 4. Per-IP rate limit — generous burst, so a normal reader never trips it.
		if lc.rateLimit && !verified && !m.limiter.Allow(enfKey) {
			// The in-memory consequences run even in observe mode, so the counters
			// reflect what a real rollout would look like — including escalation —
			// rather than a first-request-only sample. Only the RESPONSE and the
			// kernel offload are withheld.
			if lc.autoBlock && !m.violation.Allow(enfKey) {
				// This IP keeps breaching the limit — jail it for a while.
				m.blocklist.Block(enfKey, m.jailTTL())
			}
			m.brain.Observe(enfKey, brain.SignalRateBurst)
			// If the breaches collapsed this source's reputation, escalate to
			// the O(1) blocklist jail immediately — subsequent requests are then
			// dropped by the very first gate (the brain reached the verdict the
			// legacy 20-breach violation meter would take 4x longer to reach).
			if lc.autoBlock && m.brain.Jailed(enfKey) {
				m.blocklist.Block(enfKey, m.jailTTL())
				m.shareVerdict(gossip.KindJail, enfKey, 0)
				// L1 offload: drop this flooding source in-kernel too — never in
				// observe mode, because a kernel drop cannot be un-dropped.
				m.offload(lc, ipKey, m.jailTTL())
			} else if lc.autoBlock {
				// Not jailed yet. Share the SUSPICION so the escalation accumulates
				// across the fleet rather than restarting on each node — which is
				// the free ride a distributed swarm currently gets for nothing.
				m.shareVerdict(gossip.KindSuspect, enfKey, 0.1)
			}
			if !m.observing(lc, GateRateLimit) {
				reqclass.MarkShielded(r.Context())
				m.onEvent(ActionBlock, 1.0)
				m.serveThrottled(w, r, http.StatusTooManyRequests, "rate-limited", "10")
				return
			}
		}

		// 4c. Sovereign Surge (Aegis L3). Under genuine pressure — an operator-
		// forced surge, an active flood, or the L0 sovereignty lane nearing its cap
		// — an unproven visitor is met with the cheap, stateless PoW interstitial
		// BEFORE the expensive classification path (TLS/HTTP fingerprint + the
		// adaptive-signature SELECT). A real browser solves it once in a Web Worker
		// (<100 ms) and rides the verified lane thereafter; a client that will not
		// run JS never reaches fingerprinting, SQLite or rendering — it costs one
		// HMAC.
		//
		// The measured claim, and its limits. Surge makes the PER-REQUEST cost of
		// an unproven client one HMAC instead of a fingerprint, a SQLite read and
		// a render — genuinely the difference between absorbing an
		// application-layer flood and collapsing under it. What it does NOT do is
		// change what a source costs to REACH this code: a million sources is a
		// million TCP handshakes and a million TLS handshakes on one uplink, and
		// that is what saturates first. Any benchmark of this path measures the
		// HMAC, not the uplink, and a number from one instance type is not a
		// promise about another. Recognised search-engine/AI crawlers already took the SEO
		// fast path at gate 0, so surge never challenges them — that is what stops a
		// flood from de-indexing the site; every other unproven client is challenged
		// here. (Gate-0 recognition is UA-only in Phase 1; Phase 2's published-IP +
		// FCrDNS verification closes the UA-spoof gap so only a crawler from the
		// vendor's real network skips surge.) Verified sessions, trusted operators
		// and bypassed paths already returned above; a nil signer disables it
		// (fail-open).
		if !verified && m.underSurge(lc) && !m.observing(lc, GateSurge) {
			// Impose real cost: surge exists to defend against clients that will
			// (headless browsers) or won't (plain scrapers) run JS during a flood.
			// A difficulty-4 PoW (~65k hashes) is trivial for a headless engine, so
			// surge uses the harder tier. feedCalib=false keeps these score-blind
			// challenges out of the L4 ladder self-tuning. If the challenge cannot
			// be issued (no signer / issue error) serveChallenge reports false and
			// we fail OPEN — never an empty response.
			if m.serveChallenge(w, r, Verdict{}, challenge.HardDifficulty, false) {
				m.surgeServed.Add(1)
				reqclass.MarkShielded(r.Context())
				return
			}
			next.ServeHTTP(w, r)
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

		// Learn: a challenged-but-not-blocked "unknown" fingerprint becomes an
		// auto-learned review candidate. The action is required because the trigger
		// keys on it (a near-miss is Unknown + a challenge, not a hard block —
		// blocks already learn via jailBadActor).
		m.maybeLearn(r.Context(), v, action)

		ctx := context.WithValue(r.Context(), ctxKey{}, v)
		r = r.WithContext(ctx)

		// A non-Allow decision is bot defence (a challenge, a deliberate tarpit
		// delay, or a hard block), not a representative page load. Flag it so the
		// latency recorder keeps it out of the HTTP p95 — otherwise the tarpit's
		// own 5s sleep pins the tail for as long as bots keep probing.
		if action != ActionAllow {
			reqclass.MarkShielded(ctx)
		}

		// Gates 7 and 8. The classification ladder is the ONLY path that goes
		// through Decide, which is why "Decide already returns an Action" was never
		// a sufficient basis for observe mode — six of the eight gates above never
		// reach it. Here the mapping is direct: a challenge and a block are both
		// things the ladder would have done.
		if action != ActionAllow {
			g := GateChallenge
			if action == ActionBlock || action == ActionTarpit {
				g = GateBlock
			}
			if m.observing(lc, g) {
				// Still record the verdict on the request so downstream handlers and
				// the audit trail see what the shield thought, then serve normally.
				next.ServeHTTP(w, r)
				return
			}
		}

		switch action {
		case ActionAllow:
			// Sustained human-scored browsing slowly restores a suspect's
			// standing (no-op for untracked sources — the innocent are never
			// tracked), so a mis-scored real user heals automatically.
			if v.Result.BotScore < 0.3 {
				m.brain.Observe(enfKey, brain.SignalHuman)
			}
			next.ServeHTTP(w, r)
		case ActionChallengePoW:
			// Reputation-scaled difficulty (L5→L4): an unknown client works a
			// light, silent PoW; a source already under suspicion works harder.
			diff := challenge.DefaultDifficulty
			if m.brain.Standing(enfKey) < 0.3 {
				diff = challenge.HardDifficulty
			}
			if !m.serveChallenge(w, r, v, diff, true) {
				next.ServeHTTP(w, r) // fail open if the challenge cannot be issued
			}
		case ActionChallengeJS:
			if !m.serveChallenge(w, r, v, challenge.HardDifficulty, true) {
				next.ServeHTTP(w, r)
			}
		case ActionTarpit:
			// A confirmed bad actor (score ≥ block threshold, tarpit variant).
			if m.softenForNavigation(w, r, v, next) {
				return
			}
			m.brain.Observe(enfKey, brain.SignalBlock)
			m.jailBadActor(r.Context(), ipKey, lc.autoBlock, v)
			m.serveTarpit(w, r, v, next)
		case ActionBlock:
			// A confirmed bad actor: jail the IP so the VERY NEXT request from it
			// is dropped by the O(1) blocklist gate above — without re-running
			// classification — and fold its fingerprint into the signature
			// knowledge base so the bot database grows from real detections.
			if m.softenForNavigation(w, r, v, next) {
				return
			}
			m.brain.Observe(enfKey, brain.SignalBlock)
			m.jailBadActor(r.Context(), ipKey, lc.autoBlock, v)
			m.serveBlock(w, r, v)
		}
	})
}

// serveJailed answers a request from a jailed source. A jailed visitor is
// offered the silent PoW interstitial — solving it issues the signed session
// cookie AND pardons the sentence (RewardProof), so a false positive (including
// the operator's own IP after a load test) self-heals in one page load with no
// human interaction. But issuing a PoW + rendering the interstitial + recording
// telemetry is far more expensive than an O(1) rejection, and a jailed BOT
// keeps hammering — so the challenge is offered at most ~once per 30s per IP
// (the redeem budget); every other request, plus any request during an active
// flood or without a signer, gets the cheap flat rejection. This preserves both
// properties: real users redeem within seconds, hammering bots cost ~nothing.
func (m *Manager) serveJailed(w http.ResponseWriter, r *http.Request, ipKey, reason string) {
	// A top-level browser navigation is always offered the solvable challenge
	// while there is no active flood: solving it both issues the session cookie
	// and pardons the sentence, so a mis-jailed person is out on their FIRST page
	// load instead of bouncing on the auto-retrying throttle page until the ~30s
	// per-IP redeem budget refills. The budget still governs everything else
	// (assets, API calls, non-browser clients) and the whole carve-out collapses
	// under attack, so a flood is still answered by the cheap rejection.
	navigation := acceptsHTML(r)
	if m.cfg.Signer != nil && !m.controller.UnderAttack() && (navigation || m.redeem.Allow(m.enforcementKey(ipKey))) {
		if m.serveChallenge(w, r, Verdict{}, challenge.DefaultDifficulty, false) {
			return
		}
	}
	// Short Retry-After: a browser navigation gets the calm auto-retrying page
	// (serveThrottled) and is bounced back into the solvable challenge quickly, so
	// a jailed human recovers in seconds rather than being left waiting.
	m.serveThrottled(w, r, http.StatusTooManyRequests, reason, "5")
}

// serveThrottled writes a small, cheap rejection (429/503) with a Retry-After.
// For a real browser navigation it renders a calm "just a moment" page that
// auto-retries itself (so a jailed human is never left at a dead end and is
// bounced back into the solvable challenge as soon as the redeem budget refills),
// while every non-navigational request (API/asset/bot) still gets the flat,
// near-free text — the status code and headers are identical either way, so the
// cheap-rejection accounting the shield relies on is unchanged.
func (m *Manager) serveThrottled(w http.ResponseWriter, r *http.Request, code int, reason, retryAfter string) {
	w.Header().Set("Retry-After", retryAfter)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VayuShield", reason)
	if acceptsHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(throttledHTML(retryAfter)))
		return
	}
	w.WriteHeader(code)
	_, _ = w.Write([]byte("Request rejected by VayuShield (" + reason + "). Please retry shortly."))
}

// acceptsHTML reports whether r is a top-level browser navigation (a GET that
// asks for HTML) — the only case where rendering a friendly interstitial pays
// off. API calls, assets and bots that don't send Accept: text/html keep the
// cheap flat rejection.
func acceptsHTML(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// digitsOr returns s when it is a short all-digit string, else def — so a
// Retry-After value can be interpolated into the auto-refresh meta safely.
func digitsOr(s, def string) string {
	if s == "" || len(s) > 4 {
		return def
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return def
		}
	}
	return s
}

// throttledHTML is the calm, self-retrying page shown to a throttled/jailed
// browser navigation. It carries a meta-refresh set to the Retry-After, so the
// visitor is automatically returned — and, once the per-IP redeem budget allows,
// handed the solvable "verify you are human" challenge — with no manual reload.
func throttledHTML(retryAfter string) string {
	ra := digitsOr(retryAfter, "5")
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="refresh" content="` + ra + `">
<title>Verifying your browser…</title><meta name="robots" content="noindex">
</head><body style="margin:0;background:#0b0f14;color:#e5e7eb;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif">
<main style="max-width:30rem;margin:14vh auto;padding:0 1.25rem;text-align:center">
  <div style="background:#111827;border:1px solid #1f2937;border-radius:16px;padding:2rem 1.75rem;box-shadow:0 24px 60px -20px rgba(0,0,0,.6)">
    <div style="font-size:2rem;line-height:1" aria-hidden="true">🛡️</div>
    <h1 style="font-size:1.15rem;margin:.75rem 0 .35rem">Just a moment…</h1>
    <p style="color:#94a3b8;font-size:.9rem;margin:0">We’re verifying your connection before letting you through. This page retries itself automatically — no need to refresh.</p>
    <p style="color:#64748b;font-size:.8rem;margin:1.1rem 0 0">Not sent through shortly? <a href="" style="color:#2dd4bf;text-decoration:none">Tap here to retry</a>.</p>
  </div>
  <p style="color:#475569;font-size:.72rem;margin-top:1rem">Protected by VayuShield</p>
</main></body></html>`
}

// maybeLearn records a bot-like unknown fingerprint as an auto-learned candidate.
func (m *Manager) maybeLearn(ctx context.Context, v Verdict, action Action) {
	if m.cfg.Bots == nil || v.Composite.FingerprintHash == "" {
		return
	}
	// A genuine near-miss: an UNKNOWN client (0.4–0.75 band) that scored high
	// enough to be challenged but not blocked. The previous guard —
	// BotScore>0.75 AND ClientType==Unknown — was unsatisfiable (the scorer only
	// labels Unknown below 0.75), so this path never ran and the signature DB
	// grew only from hard blocks. We now record recurring challenged unknowns as
	// review candidates; a solved-challenge false-positive (see VerifyPoW) decays
	// any that are really shared human fingerprints, so they never auto-promote.
	nearMiss := v.Result.ClientType == botdb.TypeUnknown && v.Result.BotScore >= 0.6 &&
		(action == ActionChallengeJS || action == ActionChallengePoW)
	if nearMiss {
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
		// The sentence is served by the enforcement key (the /64 for IPv6), the
		// kernel offload below by the exact address — the nft banlist sets take
		// no CIDR element. See enforcementKey.
		m.blocklist.Block(m.enforcementKey(ipKey), m.jailTTL())
		// Tell the fleet. This is the verdict most worth sharing: it is the one
		// the shield is most confident about, and without it a swarm gets one
		// free run at every node instead of one at the fleet.
		m.shareVerdict(gossip.KindJail, m.enforcementKey(ipKey), 0)
		// L1 offload: a jailed confirmed bad actor is also dropped in-kernel, so
		// its follow-up packets never even create a connection. Routed through
		// m.offload so observe mode suppresses it — a kernel drop happens outside
		// this process and cannot be un-dropped, which would make "observe only"
		// a lie in the one layer the panel cannot see.
		m.offload(m.live(), ipKey, m.jailTTL())
	}
	if m.cfg.Bots == nil || v.Composite.FingerprintHash == "" {
		return
	}
	// NEVER auto-learn a mainstream browser's fingerprint as a bad bot.
	//
	// A fingerprint is shared by everyone running the same browser build, so
	// recording one as a bad bot indicts a whole population, not an attacker. And
	// the record compounds: confidence rises +0.10 per repeat until it crosses the
	// threshold where the scorer treats it as an identified bot — from then on every
	// real visitor using that browser is hard-blocked, and each block reinforces the
	// verdict. One burst of false positives (a mis-tuned threshold, a CDN making the
	// whole audience look like one IP) is enough to poison the database permanently
	// and 403 the operator's own readers.
	//
	// A real bad actor wearing a browser UA is still stopped — by scoring, the
	// challenge ladder and the reputation engine, none of which need this record.
	// What is given up is only the ability to auto-promote a browser fingerprint to
	// "identified bot", which was never sound.
	if isBrowserUAFamily(uaFamily(v.Signals.UserAgent)) {
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

// serveChallenge issues a signed PoW and returns the branded interstitial page,
// reporting whether it actually issued one (false = fail-open: the caller should
// let the request proceed rather than emit an empty response). The solver runs
// from a same-origin script (/__vayushield/challenge.js), so no inline script is
// needed and the strict script-src 'self' CSP is satisfied. feedCalib gates the
// L4 calibrator: the score-based ladder feeds it (true); Sovereign Surge, which
// challenges every unproven visitor regardless of score, does not (false), so it
// cannot skew the ladder's self-tuning.
func (m *Manager) serveChallenge(w http.ResponseWriter, r *http.Request, v Verdict, difficulty int, feedCalib bool) bool {
	if m.cfg.Signer == nil {
		return false // cannot challenge without a signer — fail open
	}
	if m.cfg.OnionMode {
		// A Tor Space serves plain-http .onion, so window.crypto.subtle is
		// undefined and the PoW solver cannot run — a challenge here locks EVERY
		// human out. Fail open; the non-crypto gates still enforce.
		return false
	}
	pow, err := m.cfg.Signer.IssuePoW(difficulty, m.cfg.ChallengeTTL)
	if err != nil {
		return false // issue failure — fail open
	}
	if feedCalib {
		m.calib.Served()
	}
	m.recordChallenge(r, v, actionType(difficulty), "issued", 0)
	payload, _ := json.Marshal(pow)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Retry-After tells well-behaved clients and crawlers this is temporary: a
	// 503 + Retry-After is treated as a transient outage (not deindexed), unlike
	// a bare 503, so serving the challenge to a crawler during an attack is safe.
	w.Header().Set("Retry-After", "5")
	w.Header().Set("X-VayuShield", "challenge")
	w.WriteHeader(http.StatusServiceUnavailable) // 503: retry after solving
	_, _ = w.Write([]byte(interstitialHTML(string(payload))))
	return true
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
// CrawlerIdentityIsIPVerified reports whether crawler recognition is hardened with
// published-IP-range / reverse-DNS confirmation. When it is, a crawler's identity
// CANNOT be simulated from an arbitrary source address — which matters for the
// operator-facing self-test: a synthetic Googlebot probe sent from a documentation
// IP is correctly treated as a spoof suspect, so reporting it as "blocked" would
// be a false alarm about indexing rather than a finding.
func (m *Manager) CrawlerIdentityIsIPVerified() bool {
	return m.cfg.VerifiedBotFn != nil
}

// softenForNavigation converts a score-only tarpit/block verdict into the
// solvable challenge when the request is a top-level browser navigation, and
// reports whether it handled the response.
//
// Why: the tarpit and the hard block are both reached from the SAME condition —
// score ≥ the block threshold — so a single mis-scored real browser (privacy
// browsers that strip or randomise the headers the fingerprint is built from are
// the common case) is met with a 5-second stall or a 403 on a page it is simply
// trying to read. Worse, each of those verdicts feeds SignalBlock into the
// reputation brain, so two or three page loads collapse that reader's standing
// below the jail floor and the escalating sentence then answers every subsequent
// request with the "just a moment" throttle page. That is a compounding
// false-positive spiral, and it is indistinguishable — from the reader's seat —
// from the site being broken.
//
// A navigation is instead handed the ordinary solvable challenge: a real browser
// clears it in well under a second and rides the verified lane for the whole
// session TTL, while a scraper that will not run JS is still stopped and pays
// real compute for every attempt. No reputation damage is recorded, so a
// mis-scored human can never spiral into a jail.
//
// This deliberately does NOT soften a client whose verdict came from a known-bad
// signature match (TypeBadBot / TypeHeadless): those are identified, not merely
// unproven, and keep the full tarpit/block treatment. Non-navigation traffic
// (asset and API hammering) also keeps it — that is where a tarpit actually
// wastes a scraper's time instead of a person's.
func (m *Manager) softenForNavigation(w http.ResponseWriter, r *http.Request, v Verdict, next http.Handler) bool {
	if !acceptsHTML(r) {
		return false
	}
	// Only an AUTHORITATIVE verdict — a compiled-in signature, or a learned one the
	// operator verified — forfeits the benefit of the doubt. An auto-learned guess
	// does not: those are exactly the records that can be wrong about a whole
	// population of real browsers, and a wrong one used to mean a permanent 403 with
	// no way for the visitor to prove otherwise. A challenge still stops a real bot
	// (it will not solve it) while giving a mislabelled human a way through.
	if v.Result.Authoritative {
		switch v.Result.ClientType {
		case botdb.TypeBadBot, botdb.TypeHeadless:
			return false // identified bad actor — no leniency
		}
	}
	if !m.serveChallenge(w, r, v, challenge.HardDifficulty, true) {
		next.ServeHTTP(w, r) // fail open if the challenge cannot be issued
	}
	return true
}

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
// success, returns a freshly-minted session token — cryptographically bound to
// the solving request's client (its IP), so the resulting clearance cookie is
// worthless if replayed from any other source. The caller (cmd) owns the HTTP
// surface and cookie writing. The request is required for the binding.
func (m *Manager) VerifyPoW(r *http.Request, pow challenge.PoW, nonce string) (token string, ok bool) {
	if m.cfg.Signer == nil {
		return "", false
	}
	if err := m.cfg.Signer.VerifyPoW(pow, nonce); err != nil {
		return "", false
	}
	// One-shot: a solved proof may be redeemed exactly once.
	//
	// Without this the proof is bound to NOBODY before redemption, so an attacker
	// solves once, hands the pair to N hosts, and each redeems it for a session
	// bound to its own address — pricing an entire swarm at one proof of work.
	// That inverts what the challenge is for.
	if !m.redeemer.Claim(challenge.ProofID(pow), pow.Expires) {
		return "", false
	}
	tok, err := m.cfg.Signer.IssueSession(m.cfg.SessionTTL, m.clientBind(r))
	if err != nil {
		return "", false
	}
	// A solved challenge is the L4 calibrator's ground truth: this client was a
	// real browser. But do NOT feed calibration while surge is engaged — surge
	// challenges EVERY unproven visitor regardless of score, so those outcomes
	// are not informative about the score-based ladder and would wrongly loosen
	// it for hours after an attack. Only score-driven ladder outcomes calibrate.
	if !m.underSurge(m.live()) {
		// A solve proves a real BROWSER. A headless one is also a real browser, so
		// this is the one calibrator input an attacker can manufacture — and the
		// direction it moves is "loosen". Qualify it with a signal the solve
		// cannot produce: the brain's standing, which is fed by sustained
		// low-score browsing in the Allow path and never by solving a challenge.
		//
		// The brain only tracks suspects, so an unseen source returns neutral and
		// qualifies. That is the honest limit: this excludes a source already
		// observed misbehaving, and the calibrator's own diversity floor is what
		// bounds a clean-slate farm.
		key := m.enforcementKey(ipOnly(m.cfg.ClientIP(r)))
		m.calib.Passed(m.brain.Standing(key) >= brain.Neutral, key)
	}
	// Arm the false-positive guard: a solved challenge is human proof, so if the
	// challenged fingerprint had been auto-learned as a bad-bot candidate this
	// increments its false-positive count and decays its confidence. That is what
	// stops a COARSE fingerprint (e.g. behind a reverse proxy that strips the TLS
	// sub-hashes, so many real users collapse onto one signature) from ever
	// auto-promoting to a hard block — the promotion cycle skips any signature
	// with false_positive_count>0. The stateless PoW carries no fingerprint, so
	// recompute it from the verifying request; a no-op for unknown fingerprints.
	// Batched off the request path so a flash crowd of solvers cannot serialise
	// on the single writer.
	if m.cfg.Bots != nil {
		sig, _ := m.signals(r)
		if fph := sig.Fingerprint().FingerprintHash; fph != "" {
			m.recordExec(r.Context(), `UPDATE vayushield_signatures SET false_positive_count=false_positive_count+1, confidence=MAX(0.0, confidence-0.2) WHERE fingerprint_hash=?`, fph)
			m.sigCache.Forget(fph) // verdict may have changed; drop any cached entry
		}
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
</head><body style="margin:0;background:#0b0f14;color:#e5e7eb;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif">
<main style="max-width:30rem;margin:14vh auto;padding:0 1.25rem;text-align:center">
<div style="background:#111827;border:1px solid #1f2937;border-radius:16px;padding:2rem 1.75rem;box-shadow:0 24px 60px -20px rgba(0,0,0,.6)">
<div style="font-size:2rem;line-height:1" aria-hidden="true">🛡️</div>
<h1 style="font-size:1.15rem;margin:.75rem 0 .35rem">Verify you are human</h1>
<p style="color:#94a3b8;font-size:.9rem;margin:0 0 1.25rem">Confirm you're human to continue. This keeps the site safe from bots — it only takes a moment.</p>
<label id="vayushield-box" style="display:flex;align-items:center;gap:.7rem;max-width:16rem;margin:0 auto;padding:.8rem 1rem;border:1px solid #334155;border-radius:12px;background:#0b0f14;cursor:pointer;user-select:none">
<input type="checkbox" id="vayushield-verify" style="width:20px;height:20px;accent-color:#14b8a6;cursor:pointer" aria-describedby="vayushield-status">
<span id="vayushield-label" style="font-size:.95rem">Verify you are human</span>
</label>
<p id="vayushield-status" style="color:#64748b;font-size:.8rem;margin:1rem 0 0;min-height:1.1em" role="status" aria-live="polite">&nbsp;</p>
<noscript><p style="color:#f59e0b;font-size:.85rem;margin-top:1rem">JavaScript is required to complete this check.</p></noscript>
</div>
<p style="color:#475569;font-size:.72rem;margin-top:1rem">Protected by VayuShield · Verifying your browser…</p>
</main>
<div id="vayushield-pow" data-challenge="{{.Challenge}}" style="display:none"></div>
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
var cb=document.getElementById('vayushield-verify');
var lb=document.getElementById('vayushield-label');
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
function status(t){if(st)st.textContent=t;}
function label(t){if(lb)lb.textContent=t;}
function reset(msg){running=false;if(cb){cb.checked=false;cb.disabled=false;}label('Verify you are human');status(msg);}
var running=false;
function run(){
  if(running){return;}
  running=true;
  if(cb){cb.checked=true;cb.disabled=true;}
  label('Verifying…');
  status('Checking your browser — please don’t refresh this page.');
  solve().then(function(nonce){
    if(nonce===null){reset('Verification failed — tap to try again.');return;}
    fetch('/__vayushield/pow',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({challenge:ch,nonce:nonce}),credentials:'same-origin'})
     .then(function(r){
       if(r.ok){label('Verified');status('Verified ✓ — continuing…');setTimeout(function(){location.reload();},350);}
       else{reset('Verification rejected — tap to try again.');}
     })
     .catch(function(){reset('Network error — tap to try again.');});
  }).catch(function(){reset('Verification error — tap to try again.');});
}
if(cb){
  // The box mirrors progress and lets a visitor retry if the auto-run fails.
  cb.addEventListener('change',function(){if(cb.checked&&!running){run();}});
}
// Auto-start so a false-positive jail (e.g. the operator's own IP after a load
// test) self-heals in one page load with NO interaction — the box ticks itself
// to show it is working. A real bot that never runs this script simply never
// clears. Solving pardons the jail, so the visitor is through on the reload.
run();
})();`
}

// isBrowserUAFamily reports whether a coarse UA family is a mainstream browser —
// a fingerprint shared by a whole population of real people rather than one
// actor. Used to keep browser fingerprints out of the auto-learned bad-bot
// database, where a single false positive would otherwise compound into a
// permanent block for everyone running that browser.
func isBrowserUAFamily(fam string) bool {
	switch fam {
	case "chrome", "chromium", "firefox", "safari", "edge":
		return true
	}
	return false
}
