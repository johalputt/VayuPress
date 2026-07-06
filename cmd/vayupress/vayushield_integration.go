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
)

// ── Boot ──────────────────────────────────────────────────────────────────────

// bootVayuShield constructs the bot-protection manager and the engagement
// analytics store, wires governance/geoip side channels, and starts the
// background learning + retention goroutines. Bot protection is OFF by default
// (VAYUSHIELD=on to enable); analytics ingestion is always available so the
// dashboard has data even when challenges are not being issued.
func (a *App) bootVayuShield() {
	a.vaSessions = vasession.NewHasher()
	a.vaEngagement = vastore.New(dbpkg.DB)

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
	})

	// Apply the operator's persisted settings (bot protection + Tier-1 resilience
	// toggles). All default OFF; the panel writes these and applies them live, so
	// nothing here throttles or challenges a real visitor until opted in.
	a.vayuShield.ApplySettings(a.shieldSettings(context.Background()))

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

	utm := parseUTM(req.Q)
	class := classifier.Classify(req.R, config.Cfg.Domain, utm, isBot)
	// A human arriving via an AI assistant is AI-assisted discovery even if the
	// referrer host was not in the classifier's list but the shield recognized it.
	if !isBot && verdict.AIReferrer != "" && class.Category != classifier.AIAssisted {
		class.Category = classifier.AIAssisted
		class.Detail = verdict.AIReferrer
	}

	sess := a.vaSessions.Session(realIP, ua, lang, now)
	_ = a.vaEngagement.RecordEnter(r.Context(), vastore.EnterInput{
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
	_ = a.vaEngagement.RecordBeacon(r.Context(), vastore.BeaconInput{
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

	cur := vayushield.Settings{PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8, RatePerMinute: 120, Burst: 60, AutoBlockJailMinutes: 10, UnderAttackRPS: 200}
	var stt vayushield.Status
	if a.vayuShield != nil {
		cur = a.vayuShield.CurrentSettings()
		stt = a.vayuShield.Status()
	}
	beaconOn := a.siteSettings == nil || a.siteSettings.Get(r.Context(), settings.KeyAnalyticsBeacon) != "off"

	// Styles live in the external admin-os.css (served same-origin) so they
	// satisfy the strict admin CSP (style-src 'self', ADR-0036) — inline <style>
	// blocks and style="" attributes are blocked by policy, so every rule here is
	// a class defined in the stylesheet. .vs-page gives the cards vertical rhythm.
	var b strings.Builder
	b.WriteString(`<div class="vs-page">`)
	b.WriteString(`<div class="page-header"><h1>Bot Shield &amp; Analytics</h1><span class="muted text-sm">Sovereign bot protection · cookieless analytics · GDPR by design</span></div>`)

	// ── Status hero ──────────────────────────────────────────────────────────
	dot, word := "", "Off"
	if cur.Enabled {
		dot, word = " on", "Protected"
	}
	if stt.UnderAttack {
		dot, word = " attack", "Under attack"
	}
	activeVisitors := int64(0)
	if a.vaEngagement != nil {
		if rt, err := a.vaEngagement.Realtime(r.Context(), 5); err == nil {
			activeVisitors = rt.ActiveVisitors
		}
	}
	b.WriteString(`<div class="card vs-hero"><div class="vs-title"><span class="vs-dot` + dot + `"></span>Bot protection — ` + word + `</div><div class="vs-metrics">`)
	b.WriteString(vsMetric(strconv.FormatInt(activeVisitors, 10), "Visitors now"))
	b.WriteString(vsMetric(strconv.FormatInt(stt.RPS, 10), "Requests/sec"))
	b.WriteString(vsMetric(strconv.FormatInt(stt.InFlight, 10), "In-flight"))
	b.WriteString(vsMetric(strconv.Itoa(stt.Blocklisted), "Blocked IPs"))
	b.WriteString(`</div></div>`)

	// ── Settings (HTMX: posts the form, server replies HX-Refresh to reflect the
	// applied state; no custom JS). Advanced fields reveal via CSS when a feature
	// is switched on. ─────────────────────────────────────────────────────────
	b.WriteString(`<form class="card" hx-post="/os/api/shield/settings" hx-swap="none">`)
	b.WriteString(`<div class="card-title">Protection</div>`)
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
	b.WriteString(`</form>`)

	// Network hardening (Tier 2/3) — server-level, collapsed by default.
	b.WriteString(`<details class="card"><summary class="card-title vs-summary">Network hardening (Tier 2 &amp; 3) — server-level</summary>`)
	b.WriteString(`<p class="muted text-sm">The switches above are the in-binary (Tier 1) defenses. Floods that saturate the network are stopped <em>below</em> the app, as sovereign scripts (they need root or the reverse proxy):</p>`)
	b.WriteString(`<ul class="muted text-sm"><li><strong>Tier 2 · kernel:</strong> <code>bash deploy/vayushield-firewall.sh apply</code> — nftables per-IP limits + SYN-flood cookies.</li>`)
	b.WriteString(`<li><strong>Tier 3 · edge:</strong> <code>deploy/nginx-vayushield.conf</code> — per-IP shaping + slow-loris timeouts at the reverse proxy.</li></ul>`)
	b.WriteString(`<p class="muted text-sm">A true volumetric flood can only be absorbed by anycast/scrubbing capacity no single host provides; Tiers 1–2 handle what a typical publisher actually faces.</p></details>`)

	// ── Bot intelligence ──────────────────────────────────────────────────────
	if a.vayuShield != nil && a.vayuShield.BotStore() != nil {
		if s, err := a.vayuShield.BotStore().Stats(r.Context()); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">Bot signatures</div><div class="vs-stats">`)
			b.WriteString(vsStat(strconv.FormatInt(s.Total, 10), "Total signatures"))
			b.WriteString(vsStat(strconv.FormatInt(s.LearnedLast24h, 10), "Learned (24h)"))
			b.WriteString(vsStat(strconv.FormatInt(s.PendingReview, 10), "Pending review"))
			b.WriteString(`</div><div class="vs-pills">`)
			for _, k := range []string{"bad_bot", "good_bot", "ai_agent", "human", "unknown"} {
				b.WriteString(`<span class="vs-pill">` + html.EscapeString(k) + ` · ` + strconv.FormatInt(s.ByClass[k], 10) + `</span>`)
			}
			b.WriteString(`</div><p class="vs-export"><a class="btn btn--sm" href="/os/api/shield/export" download="vayushield-signatures.json">Export signatures</a></p></div>`)
		}

		if q, err := a.vayuShield.BotStore().ReviewQueue(r.Context(), 25); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">Review queue</div>`)
			if len(q) == 0 {
				b.WriteString(`<p class="muted text-sm">No candidates awaiting review.</p>`)
			} else {
				b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Fingerprint</th><th>Client</th><th>Seen</th><th>Confidence</th><th></th></tr></thead><tbody>`)
				for _, sg := range q {
					fp := sg.FingerprintHash
					if len(fp) > 14 {
						fp = fp[:14]
					}
					id := strconv.FormatInt(sg.ID, 10)
					// HTMX: on a 2xx the row is deleted; CSRF header is added by the
					// shell's htmx:configRequest hook.
					b.WriteString(`<tr><td class="font-mono text-sm">` + html.EscapeString(fp) + `…</td><td>` + html.EscapeString(sg.UserAgentPattern) + `</td><td>` + strconv.FormatInt(sg.RequestCount, 10) + `</td><td>` + ftoa2(sg.Confidence) + `</td><td class="vs-actions">` +
						`<button class="btn btn--sm btn--danger" hx-post="/os/api/shield/verify" hx-vals='{"id":"` + id + `","classification":"bad_bot"}' hx-target="closest tr" hx-swap="delete">Confirm bot</button> ` +
						`<button class="btn btn--sm" hx-post="/os/api/shield/dismiss" hx-vals='{"id":"` + id + `"}' hx-target="closest tr" hx-swap="delete">Dismiss</button>` +
						`</td></tr>`)
				}
				b.WriteString(`</tbody></table></div>`)
			}
			b.WriteString(`</div>`)
		}
	}

	// ── Engagement analytics ──────────────────────────────────────────────────
	if a.vaEngagement != nil {
		if ov, err := a.vaEngagement.Overview(r.Context(), days); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">Engagement — last ` + strconv.Itoa(days) + ` days</div><div class="vs-stats">`)
			b.WriteString(vsStat(strconv.FormatInt(ov.Views, 10), "Human views"))
			b.WriteString(vsStat(strconv.FormatInt(ov.UniqueSessions, 10), "Unique sessions"))
			b.WriteString(vsStat(pct(ov.EngagementRate), "Engagement"))
			b.WriteString(vsStat(pct(ov.BounceRate), "Bounce"))
			b.WriteString(vsStat(ftoa2(ov.AvgTimeSeconds)+"s", "Avg time"))
			b.WriteString(vsStat(strconv.FormatInt(ov.BotViews, 10), "Bot views (excluded)"))
			b.WriteString(`</div></div>`)
		}
		if srcs, err := a.vaEngagement.SourceBreakdown(r.Context(), days); err == nil && len(srcs) > 0 {
			b.WriteString(`<div class="card"><div class="card-title">Traffic sources</div><div class="table-wrap"><table class="table"><thead><tr><th>Source</th><th>Views</th><th>Sessions</th><th>Avg time</th><th>Avg scroll</th><th>Engagement</th></tr></thead><tbody>`)
			for _, s := range srcs {
				b.WriteString(`<tr><td>` + html.EscapeString(s.Category) + `</td><td>` + strconv.FormatInt(s.Views, 10) + `</td><td>` + strconv.FormatInt(s.Sessions, 10) + `</td><td>` + ftoa2(s.AvgTimeSeconds) + `s</td><td>` + ftoa2(s.AvgScrollPct) + `%</td><td>` + pct(s.EngagementRate) + `</td></tr>`)
			}
			b.WriteString(`</tbody></table></div></div>`)
		}
		if ai, err := a.vaEngagement.AITraffic(r.Context(), days); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">AI-assisted discovery vs organic search</div>`)
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
		if pages, err := a.vaEngagement.TopPages(r.Context(), days, 10); err == nil && len(pages) > 0 {
			b.WriteString(`<div class="card"><div class="card-title">Top pages</div><div class="table-wrap"><table class="table"><thead><tr><th>Page</th><th>Views</th><th>Avg time</th><th>Engagement</th></tr></thead><tbody>`)
			for _, p := range pages {
				b.WriteString(`<tr><td>` + html.EscapeString(p.Path) + `</td><td>` + strconv.FormatInt(p.Views, 10) + `</td><td>` + ftoa2(p.AvgTimeSeconds) + `s</td><td>` + pct(p.EngagementRate) + `</td></tr>`)
			}
			b.WriteString(`</tbody></table></div></div>`)
		}
	}

	b.WriteString(`</div>`) // .vs-page
	writeOSHTML(w, adminOSLayout(nonce, "Bot Shield & Analytics", "shield", cfg, htmpl.HTML(b.String())))
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
	w.WriteHeader(http.StatusNoContent) // HTMX hx-swap="delete" removes the row on 2xx
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
	w.WriteHeader(http.StatusNoContent) // HTMX hx-swap="delete" removes the row on 2xx
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
	// Ask HTMX to reload so the panel reflects the freshly-applied state.
	w.Header().Set("HX-Refresh", "true")
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
