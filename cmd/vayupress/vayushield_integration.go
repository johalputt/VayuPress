package main

// vayushield_integration.go — wires VayuShield (bot protection) and
// VayuAnalytics Enterprise (cookieless engagement analytics) into the VayuPress
// server: boot, public beacon/challenge endpoints, the GDPR privacy report, and
// the VayuOS "Bot Shield & Analytics" operator panel.
//
// The internal/vayushield and internal/vayuanalytics packages are deliberately
// free of the governance/geoip imports; this file injects those side channels
// (error-budget charging, country lookup, trusted client IP) so the engine stays
// decoupled and unit-testable while still integrating with VayuPress governance.

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/budget"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/geoip"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/severity"
	"github.com/johalputt/vayupress/internal/vayuanalytics/classifier"
	vagdpr "github.com/johalputt/vayupress/internal/vayuanalytics/gdpr"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
	vastore "github.com/johalputt/vayupress/internal/vayuanalytics/store"
	"github.com/johalputt/vayupress/internal/vayushield"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/sovereign"
)

// ── Boot ──────────────────────────────────────────────────────────────────────

// bootVayuShield constructs the bot-protection manager and the engagement
// analytics store, wires governance/geoip side channels, and starts the
// background learning + retention goroutines. Bot protection is OFF by default
// (VAYUSHIELD=on to enable); analytics ingestion is always available so the
// dashboard has data even when challenges are not being issued.
func (a *App) bootVayuShield() {
	// L0 Aegis — Admin Sovereignty Lane. Constructed first and mounted before
	// every other middleware (see registerRoutes) so that under a volumetric
	// flood it caps PUBLIC concurrency and sheds the overflow with a couple of
	// atomic ops — BEFORE any classification, render or SQLite work — leaving the
	// admin control plane and verified readers guaranteed CPU headroom. This is
	// the fix for "Save/refresh die when a big bot hit lands". The public ceiling
	// is CPU-derived by default and live-tunable via VAYUSHIELD_MAX_PUBLIC.
	a.sovereign = sovereign.New()
	if n, err := strconv.Atoi(strings.TrimSpace(config.EnvOr("VAYUSHIELD_MAX_PUBLIC", ""))); err == nil && n > 0 {
		a.sovereign.SetLimit(n)
		logging.LogInfo("vayushield", fmt.Sprintf("Aegis L0 admin-sovereignty lane active — public concurrency cap %d (VAYUSHIELD_MAX_PUBLIC)", n))
	} else {
		logging.LogInfo("vayushield", fmt.Sprintf("Aegis L0 admin-sovereignty lane active — public concurrency cap %d (auto, CPU-derived)", a.sovereign.Cap()))
	}

	a.vaSessions = vasession.NewHasher()
	a.vaEngagement = vastore.New(dbpkg.DB)
	// Dashboard reads (Overview, sources, AI traffic, top pages, realtime) run a
	// dozen heavy aggregate scans over the ever-growing vayuanalytics_sessions
	// table. Route them at the DEDICATED ADMIN read pool so opening the Bot Shield
	// / Analytics panel never serialises behind the beacon write stream on the
	// writer, and is fully isolated from public/bot read load on the public pool.
	// Writes stay on the writer.
	a.vaEngagement.UseReader(dbpkg.AdminReader())
	// Persist engagement beacons on a bounded, batched background writer instead
	// of one synchronous writer transaction per page view. This is the fix for
	// the post-restart VayuOS 502s: the beacon storm (enter + event on every
	// public view) no longer saturates the single SQLite writer, so the dynamic
	// admin panel stays responsive while caches warm.
	a.vaEngagement.StartIngest(queue.DoneCh)

	bots := botdb.New(dbpkg.DB)
	signer := challenge.NewSigner([]byte(config.Cfg.APIKey))

	a.vayuShield = vayushield.New(vayushield.Config{
		Static: botdb.NewStaticDB(),
		Bots:   bots,
		Signer: signer,
		DB:     dbpkg.DB,
		// The admin panel, API, feeds, health/metrics and the shield's own
		// endpoints are never challenged.
		BypassPrefixes:    []string{"/os", "/api", "/admin", "/debug", "/health", "/metrics", "/__vayushield", "/__vayuanalytics", "/.well-known"},
		SessionCookieName: "vayushield",
		CountryFn:         geoip.Country,
		ClientIP:          auth.ClientIP,
		CookieSecure:      auth.CSRFCookieSecure(),
		OnEvent:           a.vayuShieldOnEvent,
		// L0 -> L2 pressure link: when the public sovereignty lane passes 75%
		// occupancy the L2 pre-filter starts fair-shedding heavy hitters, easing
		// the lane back down BEFORE it saturates and starts shedding blindly.
		// This runs with zero operator configuration — the self-defense floor.
		PressureFn: func() bool {
			g := a.sovereign
			return g != nil && g.Inflight()*4 >= g.Cap()*3
		},
	})

	// Apply the operator's persisted settings (bot protection + Tier-1 resilience
	// toggles). All default OFF; the panel writes these and applies them live, so
	// nothing here throttles or challenges a real visitor until opted in.
	a.vayuShield.ApplySettings(a.shieldSettings(context.Background()))

	// Persist the shield's learning + challenge/block records on a bounded,
	// batched background writer instead of one synchronous writer transaction per
	// suspicious request. Under a bot flood — exactly when the shield is busiest —
	// this keeps the single SQLite writer free so VayuOS stays responsive.
	a.vayuShield.StartAsyncWrites(queue.DoneCh)

	if a.vayuShield.Enabled() {
		logging.LogInfo("vayushield", "bot protection ENABLED — PoW→JS→block→tarpit ladder + adaptive learning active")
	} else {
		logging.LogInfo("vayushield", "bot protection disabled — enable it in VayuOS → Bot Shield & Analytics (no restart needed)")
	}
	// The learning/purge reporter always runs (cheap 24h ticker) so the adaptive
	// database stays curated regardless of the current toggle state.
	a.vayuShield.StartReporter(queue.DoneCh, 24*time.Hour, config.Cfg.AnalyticsRetainDays, func(res vayushield.LearningResult, err error) {
		if err != nil {
			logging.LogError("vayushield", "learning cycle failed", err.Error())
			return
		}
		if res.Promoted > 0 || res.Purged > 0 {
			logging.LogInfo("vayushield", fmt.Sprintf("learning cycle: promoted %d signature(s), purged %d stale", res.Promoted, res.Purged))
		}
	})

	// VayuAnalytics data-retention sweep (daily), mirroring the legacy analytics purge.
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-queue.DoneCh:
				return
			case <-ticker.C:
				if a.vaEngagement == nil {
					continue
				}
				if n, err := a.vaEngagement.Purge(context.Background(), config.Cfg.AnalyticsRetainDays); err == nil && n > 0 {
					logging.LogInfo("vayuanalytics", fmt.Sprintf("purged %d engagement rows older than %dd", n, config.Cfg.AnalyticsRetainDays))
				}
			}
		}
	}()
}

// shieldChargeAt is the last unix-second the shield charged the governance
// budget, used to throttle charges to at most one per 30s under attack.
var shieldChargeAt int64

// vayuShieldOnEvent charges the bot-attack-intensity governance budget when a
// request is hard-blocked or tarpitted, throttled so a flood does not exhaust
// the budget on the first burst of requests.
func (a *App) vayuShieldOnEvent(action vayushield.Action, score float64) {
	if action != vayushield.ActionBlock && action != vayushield.ActionTarpit {
		return
	}
	now := time.Now().Unix()
	last := atomic.LoadInt64(&shieldChargeAt)
	if now-last < 30 {
		return
	}
	if atomic.CompareAndSwapInt64(&shieldChargeAt, last, now) {
		budget.Global.RecordFrom(severity.Warn, "vayushield", time.Now())
	}
}

// ── Public endpoints ──────────────────────────────────────────────────────────

// handleVayuShieldJS serves the same-origin PoW solver script referenced by the
// challenge interstitial (script-src 'self', no nonce required).
func (a *App) handleVayuShieldJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(vayushield.ChallengeJS()))
}

// handleVayuShieldPoW verifies a solved proof-of-work and, on success, issues the
// signed, httpOnly session cookie that lets the visitor through subsequent
// challenges. Security-essential cookie (no PII) — no consent banner required.
func (a *App) handleVayuShieldPoW(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Challenge challenge.PoW `json:"challenge"`
		Nonce     string        `json:"nonce"`
	}
	if err := readCappedJSON(w, r, 8*1024, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	tok, ok := a.vayuShield.VerifyPoW(r.Context(), req.Challenge, req.Nonce)
	if !ok {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// A solved challenge is the strongest human proof: raise the source's L5
	// reputation and pardon any sentence it was serving.
	a.vayuShield.RewardProof(r)
	http.SetCookie(w, a.vayuShield.SessionCookie(tok))
	w.WriteHeader(http.StatusNoContent)
}

// handleVAEnter records a page-enter engagement event. Public, per-IP rate
// limited, and never persists an IP or User-Agent (only their salted session
// hash). Classifies the traffic source and the client type (bot vs human).
func (a *App) handleVAEnter(w http.ResponseWriter, r *http.Request) {
	if a.vaEngagement == nil || a.vaSessions == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ip := loginClientIP(r)
	if !analyticsLimiter.allow(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	var req struct {
		P string `json:"p"` // location.pathname
		Q string `json:"q"` // location.search (for UTM)
		R string `json:"r"` // document.referrer
	}
	if err := readCappedJSON(w, r, 8*1024, &req); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := vaNormalizePath(req.P)
	if path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	realIP := auth.ClientIP(r)
	ua := r.UserAgent()
	lang := r.Header.Get("Accept-Language")
	now := time.Now().UTC()

	verdict := a.vayuShield.Classify(r)
	clientType := string(verdict.Result.ClientType)
	isBot := verdict.Result.ClientType == botdb.TypeBadBot || verdict.Result.ClientType == botdb.TypeHeadless ||
		verdict.Result.ClientType == botdb.TypeGoodBot || verdict.Result.ClientType == botdb.TypeAIAgent

	// A bad bot must never pollute analytics. Blocked bad bots never render (so
	// never reach this beacon); this additionally drops any bad bot that still
	// managed to fire a beacon — e.g. a headless scraper that executed the page
	// JS. The detection/block itself is recorded in the VayuShield block list
	// (vayushield_blocked), not in the engagement analytics.
	if verdict.Result.ClientType == botdb.TypeBadBot {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	utm := parseUTM(req.Q)
	class := classifier.Classify(req.R, config.Cfg.Domain, utm, isBot)
	// A human arriving via an AI assistant is AI-assisted discovery even if the
	// referrer host was not in the classifier's list but the shield recognized it.
	if !isBot && verdict.AIReferrer != "" && class.Category != classifier.AIAssisted {
		class.Category = classifier.AIAssisted
		class.Detail = verdict.AIReferrer
	}

	sess := a.vaSessions.Session(realIP, ua, lang, now)
	// Enqueue for batched, off-request-path persistence so a page view never
	// blocks on — or saturates — the single SQLite writer (see store.StartIngest).
	a.vaEngagement.QueueEnter(vastore.EnterInput{
		SessionHash: sess,
		PagePath:    path,
		Class:       class,
		Country:     geoFromHeaders(r).Country,
		ClientType:  clientType,
		BotScore:    verdict.Result.BotScore,
		Now:         now,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleVAEvent folds an engagement beacon (time-on-page, scroll depth,
// interactions) into the matching session row.
func (a *App) handleVAEvent(w http.ResponseWriter, r *http.Request) {
	if a.vaEngagement == nil || a.vaSessions == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ip := loginClientIP(r)
	if !analyticsLimiter.allow(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	var req struct {
		P string `json:"p"` // pathname
		T int    `json:"t"` // time on page (seconds)
		S int    `json:"s"` // scroll depth (percent)
		I int    `json:"i"` // interaction count
	}
	if err := readCappedJSON(w, r, 8*1024, &req); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := vaNormalizePath(req.P)
	if path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	now := time.Now().UTC()
	sess := a.vaSessions.Session(auth.ClientIP(r), r.UserAgent(), r.Header.Get("Accept-Language"), now)
	a.vaEngagement.QueueBeacon(vastore.BeaconInput{
		SessionHash:  sess,
		PagePath:     path,
		TimeOnPage:   req.T,
		ScrollDepth:  req.S,
		Interactions: req.I,
		Now:          now,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleVAEngagementJS serves the extended engagement beacon script. Operators
// (or the active theme) include it on public pages to capture time-on-page and
// scroll depth; it sets no cookies and sends no PII.
func (a *App) handleVAEngagementJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(vaEngagementJS))
}

// handlePrivacyReport serves the machine-readable GDPR disclosure.
func (a *App) handlePrivacyReport(w http.ResponseWriter, r *http.Request) {
	rep := vagdpr.NewReport(config.Cfg.AnalyticsRetainDays, config.Cfg.Domain, "")
	b, err := rep.JSON()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "report-error", err.Error(), "")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b)
}

// ── VayuOS panel ──────────────────────────────────────────────────────────────

// handleOSShield renders the "Bot Shield & Analytics" operator panel: bot
// intelligence (classification breakdown, learned signatures, review queue) plus
// engagement analytics (source breakdown and AI-vs-organic comparison).
func (a *App) handleOSShield(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 365 {
		days = v
	}

	// Styles live in the external admin-os.css (served same-origin) so they
	// satisfy the strict admin CSP (style-src 'self', ADR-0036). Every section
	// below the status hero is a native <details> disclosure (collapsed until
	// clicked); each section body carries a stable id and is refreshed in place
	// via HTMX — see the per-section body builders and handleOSShieldSection.
	var b strings.Builder
	b.WriteString(`<div class="vs-page">`)
	b.WriteString(`<div class="page-header"><h1>Bot Shield &amp; Analytics</h1><span class="muted text-sm">Sovereign bot protection · cookieless analytics · GDPR by design</span></div>`)

	// ── Live status hero — always visible. Auto-polls every 10s and on the
	// vs-refresh event (fired after a settings save), so the state + realtime
	// metrics update in place with no page reload. ────────────────────────────
	b.WriteString(`<div class="card vs-hero" id="vs-body-hero" hx-get="/os/shield/section/hero" hx-trigger="every 10s, vs-refresh from:body" hx-swap="innerHTML">`)
	b.WriteString(a.shieldHeroBody(r.Context()))
	b.WriteString(`</div>`)

	// ── Protection & settings — collapsible. The form saves via HTMX (hx-post)
	// and the server replies with an HX-Trigger that fires vs-refresh, so ONLY
	// this section's body (and the status hero) reload in place to show the
	// applied state — the whole page never refreshes. ─────────────────────────
	b.WriteString(`<details class="card"><summary class="card-title vs-summary">Protection &amp; settings</summary>`)
	b.WriteString(`<form hx-post="/os/api/shield/settings" hx-swap="none">`)
	b.WriteString(`<div id="vs-body-protection" hx-get="/os/shield/section/protection" hx-trigger="vs-refresh from:body" hx-swap="innerHTML">`)
	b.WriteString(a.shieldProtectionBody(r.Context()))
	b.WriteString(`</div></form></details>`)

	// Network hardening (Tier 2/3) — collapsible; the body is an HTMX fragment so
	// its live state updates in place and, when the privileged agent is installed,
	// the operator flips Tier 2/3 on/off right here with no terminal.
	b.WriteString(`<details class="card"><summary class="card-title vs-summary">Network hardening (Tier 2 &amp; 3) — server-level</summary><div id="vs-body-hardening" hx-get="/os/shield/section/hardening" hx-trigger="every 10s" hx-swap="innerHTML">`)
	b.WriteString(a.shieldHardeningBody())
	b.WriteString(`</div></details>`)

	// ── Bot intelligence — collapsible + individually refreshable. Both bodies
	// also listen for vs-refresh-sig (fired after a Confirm/Dismiss) so their
	// counts update in place without touching the rest of the page. ───────────
	if a.vayuShield != nil && a.vayuShield.BotStore() != nil {
		b.WriteString(`<details class="card"><summary class="card-title vs-summary">Bot signatures</summary><div id="vs-body-signatures" hx-get="/os/shield/section/signatures" hx-trigger="vs-refresh-sig from:body" hx-swap="innerHTML">`)
		b.WriteString(a.shieldSignaturesBody(r.Context()))
		b.WriteString(`</div></details>`)

		b.WriteString(`<details class="card"><summary class="card-title vs-summary">Review queue</summary><div id="vs-body-queue" hx-get="/os/shield/section/queue" hx-trigger="vs-refresh-sig from:body" hx-swap="innerHTML">`)
		b.WriteString(a.shieldQueueBody(r.Context()))
		b.WriteString(`</div></details>`)
	}

	// ── Recent blocks — collapsible + refreshable (fast indexed query on the
	// admin lane). ─────────────────────────────────────────────────────────────
	b.WriteString(`<details class="card"><summary class="card-title vs-summary">Recent blocks — bot block list</summary><div id="vs-body-blocks">`)
	b.WriteString(a.shieldBlocksBody(r.Context()))
	b.WriteString(`</div></details>`)

	// ── Engagement analytics — heavy aggregate scans, computed OFF the request
	// path and cached (admin_dashcache.go) so this panel can never block into a
	// 502. The group refreshes in place via its own ↻ Refresh button. ──────────
	if a.vaEngagement != nil {
		b.WriteString(`<details class="card"><summary class="card-title vs-summary">Engagement analytics</summary><div id="vs-body-engagement">`)
		b.WriteString(a.shieldEngagementBody(r.Context(), days))
		b.WriteString(`</div></details>`)
	}

	// Copy-to-clipboard for the Tier 2/3 commands. Same-origin, nonce-gated
	// (satisfies the strict admin CSP, ADR-0036); purely a convenience so the
	// operator can paste the exact command instead of typing it.
	b.WriteString(`<script nonce="` + nonce + `">
(function(){'use strict';
document.querySelectorAll('.vs-copy-btn').forEach(function(btn){
  btn.addEventListener('click',function(){
    var el=document.getElementById(btn.getAttribute('data-copy'));
    if(!el)return;
    var text=(el.textContent||'').trim();
    var done=function(){var o=btn.textContent;btn.textContent='Copied ✓';btn.classList.add('is-copied');setTimeout(function(){btn.textContent=o;btn.classList.remove('is-copied');},1500);};
    if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(text).then(done).catch(done);}else{done();}
  });
});
})();
</script>`)

	b.WriteString(`</div>`) // .vs-page
	writeOSHTML(w, adminOSLayout(nonce, "Bot Shield & Analytics", "shield", cfg, htmpl.HTML(b.String())))
}

// shieldDays parses the ?days analytics window (default 30, clamped 1..365).
func shieldDays(r *http.Request) int {
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 365 {
		return v
	}
	return 30
}

// vsRefresh renders a small right-aligned button that reloads ONLY the given
// section body in place via HTMX — no page reload and no custom JS.
func vsRefresh(section, bodyID, query string) string {
	return `<div class="vs-sec-tools"><button type="button" class="vs-refresh-btn" hx-get="/os/shield/section/` + section + query + `" hx-target="#` + bodyID + `" hx-swap="innerHTML" aria-label="Refresh this section" title="Refresh"><span class="vs-refresh-ico" aria-hidden="true">↻</span><span>Refresh</span></button></div>`
}

// shieldCurrentSettings returns the live shield settings, or sensible defaults
// when the engine is not initialised.
func (a *App) shieldCurrentSettings() vayushield.Settings {
	cur := vayushield.Settings{PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8, RatePerMinute: 120, Burst: 60, AutoBlockJailMinutes: 10, UnderAttackRPS: 200}
	if a.vayuShield != nil {
		cur = a.vayuShield.CurrentSettings()
	}
	return cur
}

// shieldHeroBody renders the live status hero contents (protection state +
// realtime metrics). Cheap (in-memory Status + a bounded Realtime query), so it
// is safe to poll every few seconds.
func (a *App) shieldHeroBody(ctx context.Context) string {
	cur := a.shieldCurrentSettings()
	var stt vayushield.Status
	if a.vayuShield != nil {
		stt = a.vayuShield.Status()
	}
	dot, word := "", "Off"
	if cur.Enabled {
		dot, word = " on", "Protected"
	}
	if stt.UnderAttack {
		dot, word = " attack", "Under attack"
	}
	var activeVisitors int64
	if a.vaEngagement != nil {
		if rt, err := a.vaEngagement.Realtime(ctx, 5); err == nil {
			activeVisitors = rt.ActiveVisitors
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="vs-title"><span class="vs-dot` + dot + `"></span>Bot protection — ` + word + `</div><div class="vs-metrics">`)
	b.WriteString(vsMetric(strconv.FormatInt(activeVisitors, 10), "Visitors now"))
	b.WriteString(vsMetric(strconv.FormatInt(stt.RPS, 10), "Requests/sec"))
	b.WriteString(vsMetric(strconv.FormatInt(stt.InFlight, 10), "In-flight"))
	b.WriteString(vsMetric(strconv.Itoa(stt.Blocklisted), "Blocked IPs"))
	// Aegis L0 — admin-sovereignty lane. Shows how many public requests are in
	// flight against the CPU-derived cap and how many have been shed to keep the
	// admin plane responsive. Under normal traffic these read 0 / low; a spike in
	// "Shed (L0)" is the visible proof the console stayed alive through a flood.
	if a.sovereign != nil {
		b.WriteString(vsMetric(strconv.FormatInt(a.sovereign.Inflight(), 10)+" / "+strconv.FormatInt(a.sovereign.Cap(), 10), "Public lane"))
		b.WriteString(vsMetric(strconv.FormatInt(a.sovereign.Shed(), 10), "Shed (L0)"))
	}
	b.WriteString(vsMetric(strconv.FormatInt(stt.FairShed, 10), "Fair-shed (L2)"))
	b.WriteString(vsMetric(strconv.Itoa(stt.RepJailed)+" / "+strconv.Itoa(stt.Suspects), "Rep-jailed / suspects (L5)"))
	b.WriteString(`</div>`)
	return b.String()
}

// shieldProtectionBody renders the protection/availability/analytics settings
// form fields (everything inside the <form>). Refreshed in place after a save
// so it reflects the applied (and clamped) state.
func (a *App) shieldProtectionBody(ctx context.Context) string {
	cur := a.shieldCurrentSettings()
	beaconOn := a.siteSettings == nil || a.siteSettings.Get(ctx, settings.KeyAnalyticsBeacon) != "off"
	var b strings.Builder
	b.WriteString(`<p class="muted text-sm vs-lead">Everything applies instantly — no restart. Search engines and AI assistants (ChatGPT, Claude, Perplexity) are always allowed and counted separately; verified visitors are never throttled.</p>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_enabled", "Bot protection", "Classify every visitor and challenge the suspicious ones — a silent proof-of-work, then a JS check, then a block.", cur.Enabled, true))
	b.WriteString(`<div class="vs-adv">`)
	b.WriteString(vsField("sh_pow", "Challenge at score ≥", ftoa2(cur.PoWThreshold)))
	b.WriteString(vsField("sh_js", "JS check at ≥", ftoa2(cur.JSThreshold)))
	b.WriteString(vsField("sh_block", "Block at ≥", ftoa2(cur.BlockThreshold)))
	b.WriteString(`<div class="vs-field vs-field--tog">` + vsToggle("sh_tarpit", cur.Tarpit, false) + `<label for="sh_tarpit">Tarpit the worst offenders</label></div>`)
	b.WriteString(`</div></div>`)
	b.WriteString(`<div class="card-title vs-section">Availability &amp; anti-DDoS</div>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_ratelimit", "Rate limiting", "Cap requests per IP with a generous burst. Verified visitors are exempt.", cur.RateLimit, true))
	b.WriteString(`<div class="vs-adv">` + vsField("sh_rpm", "Requests / minute", strconv.Itoa(cur.RatePerMinute)) + vsField("sh_burst", "Burst", strconv.Itoa(cur.Burst)) + `</div></div>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_loadshed", "Load shedding", "Return a cheap 503 when the server is saturated, protecting it from collapse.", cur.LoadShed, true))
	b.WriteString(`<div class="vs-adv">` + vsField("sh_maxinflight", "Max concurrent (0 = unlimited)", strconv.Itoa(cur.MaxInFlight)) + `</div></div>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_autoblock", "Auto-block abusive IPs", "Temporarily jail IPs that relentlessly breach the rate limit.", cur.AutoBlock, true))
	b.WriteString(`<div class="vs-adv">` + vsField("sh_jail", "Jail for (minutes)", strconv.Itoa(cur.AutoBlockJailMinutes)) + `</div></div>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_underattack", "Adaptive under-attack mode", "Automatically tighten challenge thresholds during a flood and relax when it passes.", cur.UnderAttack, true))
	b.WriteString(`<div class="vs-adv">` + vsField("sh_rps", "Trip at (requests/sec)", strconv.Itoa(cur.UnderAttackRPS)) + `</div></div>`)
	b.WriteString(`<div class="card-title vs-section">Analytics</div>`)
	b.WriteString(`<div class="vs-feat">`)
	b.WriteString(vsRow("sh_beacon", "Engagement analytics", "Measure time-on-page and scroll depth on public pages. Cookieless, no PII.", beaconOn, false))
	b.WriteString(`</div>`)
	b.WriteString(`<div class="vs-save"><button class="btn btn--primary" type="submit">Save &amp; apply</button><span class="muted text-sm">Applies live to every request.</span></div>`)
	return b.String()
}

// shieldSignaturesBody renders the bot-signature stats + class pills + export.
func (a *App) shieldSignaturesBody(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(vsRefresh("signatures", "vs-body-signatures", ""))
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		b.WriteString(`<p class="muted text-sm">VayuShield is not initialised.</p>`)
		return b.String()
	}
	s, err := a.vayuShield.BotStore().Stats(ctx)
	if err != nil {
		b.WriteString(`<p class="muted text-sm">Signatures are momentarily unavailable — try Refresh.</p>`)
		return b.String()
	}
	b.WriteString(`<div class="vs-stats">`)
	b.WriteString(vsStat(strconv.FormatInt(s.Total, 10), "Total signatures"))
	b.WriteString(vsStat(strconv.FormatInt(s.LearnedLast24h, 10), "Learned (24h)"))
	b.WriteString(vsStat(strconv.FormatInt(s.PendingReview, 10), "Pending review"))
	b.WriteString(`</div><div class="vs-pills">`)
	for _, k := range []string{"bad_bot", "good_bot", "ai_agent", "human", "unknown"} {
		b.WriteString(`<span class="vs-pill">` + html.EscapeString(k) + ` · ` + strconv.FormatInt(s.ByClass[k], 10) + `</span>`)
	}
	b.WriteString(`</div><p class="vs-export"><a class="btn btn--sm" href="/os/api/shield/export" download="vayushield-signatures.json">Export signatures</a></p>`)
	return b.String()
}

// shieldQueueBody renders the learned-signature review queue (Confirm/Dismiss).
func (a *App) shieldQueueBody(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(vsRefresh("queue", "vs-body-queue", ""))
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		b.WriteString(`<p class="muted text-sm">VayuShield is not initialised.</p>`)
		return b.String()
	}
	q, err := a.vayuShield.BotStore().ReviewQueue(ctx, 25)
	if err != nil {
		b.WriteString(`<p class="muted text-sm">Queue momentarily unavailable — try Refresh.</p>`)
		return b.String()
	}
	if len(q) == 0 {
		b.WriteString(`<p class="muted text-sm">No candidates awaiting review.</p>`)
		return b.String()
	}
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Fingerprint</th><th>Client</th><th>Seen</th><th>Confidence</th><th></th></tr></thead><tbody>`)
	for _, sg := range q {
		fp := sg.FingerprintHash
		if len(fp) > 14 {
			fp = fp[:14]
		}
		id := strconv.FormatInt(sg.ID, 10)
		// HTMX: on a 2xx the row is deleted; the CSRF header is added by the shell's
		// htmx:configRequest hook. The verify/dismiss handlers also fire
		// vs-refresh-sig so the signature counts and queue update in place.
		b.WriteString(`<tr><td class="font-mono text-sm">` + html.EscapeString(fp) + `…</td><td>` + html.EscapeString(sg.UserAgentPattern) + `</td><td>` + strconv.FormatInt(sg.RequestCount, 10) + `</td><td>` + ftoa2(sg.Confidence) + `</td><td class="vs-actions">` +
			`<button class="btn btn--sm btn--danger" hx-post="/os/api/shield/verify" hx-vals='{"id":"` + id + `","classification":"bad_bot"}' hx-target="closest tr" hx-swap="delete">Confirm bot</button> ` +
			`<button class="btn btn--sm" hx-post="/os/api/shield/dismiss" hx-vals='{"id":"` + id + `"}' hx-target="closest tr" hx-swap="delete">Dismiss</button>` +
			`</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// shieldEngagementBody renders the engagement-analytics cards (from the cached
// background builder) with a Refresh control. Never runs the heavy scans inline.
func (a *App) shieldEngagementBody(ctx context.Context, days int) string {
	var b strings.Builder
	b.WriteString(vsRefresh("engagement", "vs-body-engagement", "?days="+strconv.Itoa(days)))
	if a.vaEngagement == nil {
		return b.String()
	}
	if eng, ready := adminDash.get("shield-engagement:"+strconv.Itoa(days), analyticsFragmentTTL, func(c context.Context) string {
		return a.renderShieldEngagement(c, days)
	}); ready {
		b.WriteString(eng)
	} else {
		b.WriteString(`<p class="muted text-sm">Assembling analytics… computed in the background; click ↻ Refresh in a few seconds.</p>`)
	}
	return b.String()
}

// handleOSShieldSection returns a single Bot-Shield section body as an HTML
// fragment, so HTMX can refresh one section in place without a page reload.
func (a *App) handleOSShieldSection(w http.ResponseWriter, r *http.Request) {
	var out string
	switch chi.URLParam(r, "name") {
	case "hero":
		out = a.shieldHeroBody(r.Context())
	case "protection":
		out = a.shieldProtectionBody(r.Context())
	case "signatures":
		out = a.shieldSignaturesBody(r.Context())
	case "queue":
		out = a.shieldQueueBody(r.Context())
	case "blocks":
		out = a.shieldBlocksBody(r.Context())
	case "engagement":
		out = a.shieldEngagementBody(r.Context(), shieldDays(r))
	case "hardening":
		out = a.shieldHardeningBody()
	default:
		http.NotFound(w, r)
		return
	}
	writeOSFragment(w, out)
}

// writeOSFragment writes an HTML fragment (no page layout, no CSRF-cookie churn)
// with the standard admin no-cache headers, for HTMX partial swaps.
func writeOSFragment(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	_, _ = w.Write([]byte(body))
}

// renderShieldEngagement builds the engagement-analytics cards for the Bot
// Shield panel. It runs the heavy aggregate queries, so it is only ever invoked
// from the background fragment cache — never inline on a request. Best-effort:
// each card renders independently; returns "" only if nothing could be built
// (so the cache retries instead of caching an empty section).
func (a *App) renderShieldEngagement(ctx context.Context, days int) string {
	if a.vaEngagement == nil {
		return ""
	}
	var b strings.Builder
	if ov, err := a.vaEngagement.Overview(ctx, days); err == nil {
		b.WriteString(`<div class="vs-subsection"><div class="card-title vs-section">Engagement — last ` + strconv.Itoa(days) + ` days</div><div class="vs-stats">`)
		b.WriteString(vsStat(strconv.FormatInt(ov.Views, 10), "Human views"))
		b.WriteString(vsStat(strconv.FormatInt(ov.UniqueSessions, 10), "Unique sessions"))
		b.WriteString(vsStat(pct(ov.EngagementRate), "Engagement"))
		b.WriteString(vsStat(pct(ov.BounceRate), "Bounce"))
		b.WriteString(vsStat(ftoa2(ov.AvgTimeSeconds)+"s", "Avg time"))
		b.WriteString(vsStat(strconv.FormatInt(ov.BotViews, 10), "Bot views (excluded)"))
		b.WriteString(`</div></div>`)
	}
	if srcs, err := a.vaEngagement.SourceBreakdown(ctx, days); err == nil && len(srcs) > 0 {
		b.WriteString(`<div class="vs-subsection"><div class="card-title vs-section">Traffic sources</div><div class="table-wrap"><table class="table"><thead><tr><th>Source</th><th>Views</th><th>Sessions</th><th>Avg time</th><th>Avg scroll</th><th>Engagement</th></tr></thead><tbody>`)
		for _, s := range srcs {
			b.WriteString(`<tr><td>` + html.EscapeString(s.Category) + `</td><td>` + strconv.FormatInt(s.Views, 10) + `</td><td>` + strconv.FormatInt(s.Sessions, 10) + `</td><td>` + ftoa2(s.AvgTimeSeconds) + `s</td><td>` + ftoa2(s.AvgScrollPct) + `%</td><td>` + pct(s.EngagementRate) + `</td></tr>`)
		}
		b.WriteString(`</tbody></table></div></div>`)
	}
	if ai, err := a.vaEngagement.AITraffic(ctx, days); err == nil {
		b.WriteString(`<div class="vs-subsection"><div class="card-title vs-section">AI-assisted discovery vs organic search</div>`)
		b.WriteString(`<p class="muted text-sm">AI traffic is <strong>` + ftoa2(ai.AISharePercent) + `%</strong> of human views · AI engagement ` + pct(ai.AISummary.EngagementRate) + ` (avg ` + ftoa2(ai.AISummary.AvgTimeSeconds) + `s) vs organic ` + pct(ai.OrganicSummary.EngagementRate) + ` (avg ` + ftoa2(ai.OrganicSummary.AvgTimeSeconds) + `s).</p>`)
		if len(ai.BySystem) > 0 {
			b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>AI system</th><th>Views</th><th>Avg time</th><th>Engagement</th></tr></thead><tbody>`)
			for _, s := range ai.BySystem {
				b.WriteString(`<tr><td>` + html.EscapeString(s.Detail) + `</td><td>` + strconv.FormatInt(s.Views, 10) + `</td><td>` + ftoa2(s.AvgTimeSeconds) + `s</td><td>` + pct(s.EngagementRate) + `</td></tr>`)
			}
			b.WriteString(`</tbody></table></div>`)
		}
		b.WriteString(`</div>`)
	}
	if pages, err := a.vaEngagement.TopPages(ctx, days, 10); err == nil && len(pages) > 0 {
		b.WriteString(`<div class="vs-subsection"><div class="card-title vs-section">Top pages</div><div class="table-wrap"><table class="table"><thead><tr><th>Page</th><th>Views</th><th>Avg time</th><th>Engagement</th></tr></thead><tbody>`)
		for _, p := range pages {
			b.WriteString(`<tr><td>` + html.EscapeString(p.Path) + `</td><td>` + strconv.FormatInt(p.Views, 10) + `</td><td>` + ftoa2(p.AvgTimeSeconds) + `s</td><td>` + pct(p.EngagementRate) + `</td></tr>`)
		}
		b.WriteString(`</tbody></table></div></div>`)
	}
	return b.String()
}

// shieldBlocksBody renders the operator-visible bot block list body (with a
// Refresh control) — the most recent hard blocks recorded in vayushield_blocked.
// GDPR: the IP is a salted, daily-rotating hash (never stored or shown in the
// clear); the full User-Agent is not PII and is truncated for display.
// Best-effort; an empty/failed query yields a friendly empty state.
func (a *App) shieldBlocksBody(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(vsRefresh("blocks", "vs-body-blocks", ""))
	empty := `<p class="muted text-sm">No bots have been blocked yet. When VayuShield blocks a bad bot it is recorded here and its IP is jailed so the next request is dropped instantly.</p>`
	if dbpkg.DB == nil {
		b.WriteString(empty)
		return b.String()
	}
	rows, err := dbpkg.AdminReader().QueryContext(ctx,
		`SELECT created_at, COALESCE(user_agent,''), COALESCE(request_path,''), COALESCE(block_reason,''), bot_score, COALESCE(country_code,''), COALESCE(ip_hash,'')
		 FROM vayushield_blocked ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		b.WriteString(empty)
		return b.String()
	}
	defer rows.Close()
	var tbl strings.Builder
	n := 0
	for rows.Next() {
		var when time.Time
		var ua, path, reason, country, iphash string
		var score float64
		if rows.Scan(&when, &ua, &path, &reason, &score, &country, &iphash) != nil {
			continue
		}
		if len(ua) > 60 {
			ua = ua[:60]
		}
		if len(iphash) > 12 {
			iphash = iphash[:12]
		}
		tbl.WriteString(`<tr><td>` + when.UTC().Format("2006-01-02 15:04") + `</td><td class="text-sm">` + html.EscapeString(ua) + `</td><td class="text-sm">` + html.EscapeString(path) + `</td><td class="text-sm">` + html.EscapeString(reason) + `</td><td>` + ftoa2(score) + `</td><td>` + html.EscapeString(country) + `</td><td class="font-mono text-sm">` + html.EscapeString(iphash) + `…</td></tr>`)
		n++
	}
	_ = rows.Err()
	if n == 0 {
		b.WriteString(empty)
		return b.String()
	}
	b.WriteString(`<p class="muted text-sm">Every hard block is recorded here (the IP is a salted daily-rotating hash — never stored in the clear). Auto-blocked IPs are also jailed in memory, so repeat requests are dropped instantly without re-classification.</p>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>When (UTC)</th><th>User-Agent</th><th>Path</th><th>Reason</th><th>Score</th><th>Country</th><th>IP (hashed)</th></tr></thead><tbody>`)
	b.WriteString(tbl.String())
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// handleOSShieldVerify marks a learned candidate as operator-verified.
func (a *App) handleOSShieldVerify(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialized", "")
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("id")), 10, 64)
	if id <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "id required", "")
		return
	}
	class := botdb.Classification(r.PostFormValue("classification"))
	switch class {
	case botdb.ClassBadBot, botdb.ClassGoodBot, botdb.ClassAIAgent, botdb.ClassHuman:
	default:
		class = botdb.ClassBadBot
	}
	if err := a.vayuShield.BotStore().Verify(r.Context(), id, class, "confirmed via panel"); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	// HTMX hx-swap="delete" removes the row on 2xx; vs-refresh-sig also refreshes
	// the signature counts + queue body in place.
	w.Header().Set("HX-Trigger", "vs-refresh-sig")
	w.WriteHeader(http.StatusNoContent)
}

// handleOSShieldDismiss deletes a learned candidate the operator judged benign.
func (a *App) handleOSShieldDismiss(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialized", "")
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("id")), 10, 64)
	if id <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "id required", "")
		return
	}
	if err := a.vayuShield.BotStore().Dismiss(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	// HTMX hx-swap="delete" removes the row on 2xx; vs-refresh-sig also refreshes
	// the signature counts + queue body in place.
	w.Header().Set("HX-Trigger", "vs-refresh-sig")
	w.WriteHeader(http.StatusNoContent)
}

// handleOSShieldExport streams the community signature export file.
func (a *App) handleOSShieldExport(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialized", "")
		return
	}
	data, err := a.vayuShield.BotStore().Export(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "export-error", err.Error(), "")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vayushield-signatures.json"`)
	_, _ = w.Write(data)
}

// handleOSShieldSettings persists the operator's protection + resilience toggles
// and applies them to the live Manager immediately (no restart).
func (a *App) handleOSShieldSettings(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil || a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialized", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid form", "")
		return
	}
	// HTMX submits the settings form url-encoded. A checkbox appears in the form
	// body only when it is checked, so presence == on. Numeric fields submit
	// their value even while their advanced block is CSS-hidden.
	bs := func(field string) string {
		if r.PostFormValue(field) != "" {
			return "on"
		}
		return "off"
	}
	num := func(field string) string { return strings.TrimSpace(r.PostFormValue(field)) }
	// shieldSettings parses the numeric strings with safe fallbacks and
	// ApplySettings clamps thresholds to (0,1].
	kv := map[string]string{
		settings.KeyShieldEnabled:        bs("sh_enabled"),
		settings.KeyShieldPoW:            num("sh_pow"),
		settings.KeyShieldJS:             num("sh_js"),
		settings.KeyShieldBlock:          num("sh_block"),
		settings.KeyShieldTarpit:         bs("sh_tarpit"),
		settings.KeyShieldRateLimit:      bs("sh_ratelimit"),
		settings.KeyShieldRateRPM:        num("sh_rpm"),
		settings.KeyShieldBurst:          num("sh_burst"),
		settings.KeyShieldLoadShed:       bs("sh_loadshed"),
		settings.KeyShieldMaxInFlight:    num("sh_maxinflight"),
		settings.KeyShieldAutoBlock:      bs("sh_autoblock"),
		settings.KeyShieldJailMinutes:    num("sh_jail"),
		settings.KeyShieldUnderAttack:    bs("sh_underattack"),
		settings.KeyShieldUnderAttackRPS: num("sh_rps"),
		settings.KeyAnalyticsBeacon:      bs("sh_beacon"),
	}
	if err := a.siteSettings.SetMany(r.Context(), kv); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	a.vayuShield.ApplySettings(a.shieldSettings(r.Context()))
	// Fire vs-refresh so ONLY the status hero + settings body reload in place to
	// reflect the applied (and clamped) state — the whole page never refreshes.
	w.Header().Set("HX-Trigger", "vs-refresh")
	w.WriteHeader(http.StatusNoContent)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// vsToggle renders a real toggle switch. master=true marks it as the control
// that reveals its sibling .vs-adv block via the CSS :has() rule.
func vsToggle(id string, on, master bool) string {
	c := ""
	if on {
		c = " checked"
	}
	m := ""
	if master {
		m = " data-master"
	}
	return `<label class="vs-switch"><input type="checkbox" id="` + id + `" name="` + id + `"` + c + m + `><span class="vs-slider"></span></label>`
}

// vsRow renders a feature row: title + one-line description on the left, a
// toggle switch on the right.
func vsRow(id, title, desc string, on, master bool) string {
	return `<div class="vs-row"><div><div class="vs-row-title">` + html.EscapeString(title) + `</div><div class="vs-row-desc">` + html.EscapeString(desc) + `</div></div>` + vsToggle(id, on, master) + `</div>`
}

// vsField renders a compact labelled number input for a feature's advanced area.
func vsField(id, label, val string) string {
	return `<div class="vs-field"><label for="` + id + `">` + html.EscapeString(label) + `</label><input type="number" id="` + id + `" name="` + id + `" value="` + html.EscapeString(val) + `" step="any" min="0"></div>`
}

// vsStat renders a stat card (big number + label).
func vsStat(n, label string) string {
	return `<div class="vs-stat"><div class="n">` + html.EscapeString(n) + `</div><div class="l">` + html.EscapeString(label) + `</div></div>`
}

// vsMetric renders a compact hero metric (big number + label).
func vsMetric(n, label string) string {
	return `<div class="vs-metric"><div class="n">` + html.EscapeString(n) + `</div><div class="l">` + html.EscapeString(label) + `</div></div>`
}

func ftoa2(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

func pct(rate float64) string { return strconv.FormatFloat(rate*100, 'f', 1, 64) + "%" }

// vaNormalizePath trims query/fragment and caps a beacon-supplied path.
func vaNormalizePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSpace(p)
	if p == "" || p[0] != '/' {
		return ""
	}
	if len(p) > 512 {
		p = p[:512]
	}
	return p
}

// parseUTM extracts campaign parameters from a raw location.search string.
func parseUTM(search string) classifier.UTM {
	u := classifier.UTM{}
	search = strings.TrimPrefix(strings.TrimSpace(search), "?")
	if search == "" {
		return u
	}
	for _, pair := range strings.Split(search, "&") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		val := kv[1]
		switch kv[0] {
		case "utm_source":
			u.Source = val
		case "utm_medium":
			u.Medium = val
		case "utm_campaign":
			u.Campaign = val
		}
	}
	return u
}

// shieldEnvBool reads a truthy env flag (on/true/yes/1) with a default.
func shieldEnvBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(config.EnvOr(key, "")))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

// shieldSettings reads the operator's persisted VayuShield + resilience
// configuration from the settings store into a vayushield.Settings value. The
// legacy VAYUSHIELD / VAYUSHIELD_TARPIT env vars act as an OR-default so an
// operator who set them before the panel existed stays enabled.
func (a *App) shieldSettings(ctx context.Context) vayushield.Settings {
	g := func(k string) string { return a.siteSettings.Get(ctx, k) }
	on := func(k string) bool { return g(k) == "on" }
	fnum := func(k string, def float64) float64 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(g(k)), 64); err == nil {
			return v
		}
		return def
	}
	inum := func(k string, def int) int {
		if v, err := strconv.Atoi(strings.TrimSpace(g(k))); err == nil {
			return v
		}
		return def
	}
	return vayushield.Settings{
		Enabled:              on(settings.KeyShieldEnabled) || shieldEnvBool("VAYUSHIELD", false),
		PoWThreshold:         fnum(settings.KeyShieldPoW, 0.4),
		JSThreshold:          fnum(settings.KeyShieldJS, 0.6),
		BlockThreshold:       fnum(settings.KeyShieldBlock, 0.8),
		Tarpit:               on(settings.KeyShieldTarpit) || shieldEnvBool("VAYUSHIELD_TARPIT", false),
		RateLimit:            on(settings.KeyShieldRateLimit),
		RatePerMinute:        inum(settings.KeyShieldRateRPM, 120),
		Burst:                inum(settings.KeyShieldBurst, 60),
		LoadShed:             on(settings.KeyShieldLoadShed),
		MaxInFlight:          inum(settings.KeyShieldMaxInFlight, 0),
		AutoBlock:            on(settings.KeyShieldAutoBlock),
		AutoBlockJailMinutes: inum(settings.KeyShieldJailMinutes, 10),
		UnderAttack:          on(settings.KeyShieldUnderAttack),
		UnderAttackRPS:       inum(settings.KeyShieldUnderAttackRPS, 200),
	}
}

// readCappedJSON decodes a size-capped JSON request body into v.
func readCappedJSON(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(v)
}

// vaEngagementJS is the extended engagement beacon served at
// /static/js/vp-engagement.js. It sets no cookies, sends no PII, and reports
// time-on-page + scroll depth + interactions via navigator.sendBeacon so the
// exit event fires even on tab close.
const vaEngagementJS = `(function(){'use strict';
var start=Date.now(),maxScroll=0,interactions=0,sent=false;
function onScroll(){var h=document.body.scrollHeight||1;var s=(window.scrollY+window.innerHeight)/h*100;var v=Math.min(100,Math.round(s));if(v>maxScroll)maxScroll=v;}
function onInteract(){interactions++;}
function payload(){return JSON.stringify({p:location.pathname,t:Math.round((Date.now()-start)/1000),s:maxScroll,i:interactions});}
function send(){if(sent)return;sent=true;try{if(navigator.sendBeacon){navigator.sendBeacon('/__vayuanalytics/event',new Blob([payload()],{type:'application/json'}));}else{var x=new XMLHttpRequest();x.open('POST','/__vayuanalytics/event',true);x.setRequestHeader('Content-Type','application/json');x.send(payload());}}catch(e){}}
window.addEventListener('scroll',onScroll,{passive:true});
window.addEventListener('click',onInteract);
document.addEventListener('visibilitychange',function(){if(document.visibilityState==='hidden')send();});
window.addEventListener('pagehide',send);
try{fetch('/__vayuanalytics/enter',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({p:location.pathname,q:location.search,r:document.referrer||''}),keepalive:true}).catch(function(){});}catch(e){}
})();`
