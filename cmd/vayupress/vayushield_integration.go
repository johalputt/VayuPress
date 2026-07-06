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

	var b strings.Builder
	b.WriteString(`<div class="page-header"><h1>Bot Shield &amp; Analytics</h1><span class="muted text-sm">Sovereign bot protection · cookieless engagement analytics · GDPR by design</span></div>`)

	// Protection + resilience controls (all live — no restart).
	cur := vayushield.Settings{PoWThreshold: 0.4, JSThreshold: 0.6, BlockThreshold: 0.8, RatePerMinute: 120, Burst: 60, AutoBlockJailMinutes: 10, UnderAttackRPS: 200}
	var st vayushield.Status
	if a.vayuShield != nil {
		cur = a.vayuShield.CurrentSettings()
		st = a.vayuShield.Status()
	}
	beaconOn := a.siteSettings == nil || a.siteSettings.Get(r.Context(), settings.KeyAnalyticsBeacon) != "off"
	status := `<span class="badge badge--warn">disabled</span>`
	if cur.Enabled {
		status = `<span class="badge badge--ok">active</span>`
	}
	underAttack := "no"
	if st.UnderAttack {
		underAttack = `<strong style="color:#f43f5e">yes</strong>`
	}
	b.WriteString(`<div class="card"><div class="card-title">Protection &amp; resilience</div>`)
	b.WriteString(`<p class="muted text-sm">Bot protection: ` + status + ` · under attack: ` + underAttack +
		` · in-flight: ` + strconv.FormatInt(st.InFlight, 10) + ` · blocklisted IPs: ` + strconv.Itoa(st.Blocklisted) +
		` · ~` + strconv.FormatInt(st.RPS, 10) + ` req/s.</p>`)
	b.WriteString(`<p class="muted text-sm">Everything below defaults <strong>off</strong> and applies <strong>instantly, no restart</strong>. Good bots (search engines) and AI agents (ChatGPT, Claude, Perplexity) are always allowed and counted separately — never blocked. Verified visitors are never rate-limited or shed.</p>`)
	b.WriteString(`<form id="shield-settings" onsubmit="return false;">`)

	b.WriteString(`<div class="card-title" style="margin-top:.5rem">Bot protection</div>`)
	b.WriteString(shToggle("sh_enabled", "Enable bot protection", cur.Enabled, "classify every client and run the PoW → JS → block ladder for suspicious ones"))
	b.WriteString(shNum("sh_pow", "PoW threshold", ftoa2(cur.PoWThreshold), "score ≥ this (0–1) gets a silent proof-of-work"))
	b.WriteString(shNum("sh_js", "JS-challenge threshold", ftoa2(cur.JSThreshold), "score ≥ this gets a JS interstitial"))
	b.WriteString(shNum("sh_block", "Block threshold", ftoa2(cur.BlockThreshold), "score ≥ this is blocked"))
	b.WriteString(shToggle("sh_tarpit", "Tarpit worst offenders", cur.Tarpit, "delay + garble instead of a clean 403"))

	b.WriteString(`<div class="card-title" style="margin-top:.75rem">Availability / anti-DDoS (in-binary, Tier 1)</div>`)
	b.WriteString(shToggle("sh_ratelimit", "Per-IP rate limiting", cur.RateLimit, "generous token bucket; verified visitors exempt"))
	b.WriteString(shNum("sh_rpm", "Requests / minute per IP", strconv.Itoa(cur.RatePerMinute), "sustained rate"))
	b.WriteString(shNum("sh_burst", "Burst", strconv.Itoa(cur.Burst), "short spikes allowed before throttling"))
	b.WriteString(shToggle("sh_loadshed", "Load shedding", cur.LoadShed, "return 503 cheaply when the server is saturated"))
	b.WriteString(shNum("sh_maxinflight", "Max concurrent requests", strconv.Itoa(cur.MaxInFlight), "0 = unlimited"))
	b.WriteString(shToggle("sh_autoblock", "Auto-block flooding IPs", cur.AutoBlock, "jail IPs that relentlessly breach the rate limit"))
	b.WriteString(shNum("sh_jail", "Jail minutes", strconv.Itoa(cur.AutoBlockJailMinutes), "how long a jailed IP stays blocked"))
	b.WriteString(shToggle("sh_underattack", "Adaptive under-attack mode", cur.UnderAttack, "auto-tighten thresholds during a flood, relax when it passes"))
	b.WriteString(shNum("sh_rps", "Attack RPS threshold", strconv.Itoa(cur.UnderAttackRPS), "global requests/second that trips attack mode"))

	b.WriteString(`<div class="card-title" style="margin-top:.75rem">Analytics</div>`)
	b.WriteString(shToggle("sh_beacon", "Engagement beacon", beaconOn, "time-on-page + scroll depth on public pages; cookieless, no PII"))

	b.WriteString(`<div style="margin-top:1rem"><button class="btn btn--primary" id="shield-save" type="button">Save &amp; apply</button></div>`)
	b.WriteString(`</form></div>`)

	// Tier 2 / Tier 3 — network hardening that lives below the app and therefore
	// ships as sovereign scripts rather than runtime toggles (they need root /
	// the reverse proxy). Honest about the volumetric limit.
	b.WriteString(`<div class="card"><div class="card-title">Network hardening (Tier 2 &amp; 3)</div>`)
	b.WriteString(`<p class="muted text-sm">The switches above are VayuShield's in-binary (Tier 1) defenses. Floods that saturate the network are stopped <em>below</em> the application — shipped as sovereign scripts (they need root or the reverse proxy, so they are configured on the server, not toggled here):</p>`)
	b.WriteString(`<ul class="muted text-sm"><li><strong>Tier 2 · kernel/OS:</strong> <code>bash deploy/vayushield-firewall.sh apply</code> — nftables per-IP connection + rate limits and SYN-flood cookies.</li>`)
	b.WriteString(`<li><strong>Tier 3 · edge:</strong> <code>deploy/nginx-vayushield.conf</code> — per-IP request/connection shaping and slow-loris timeouts at the reverse proxy.</li></ul>`)
	b.WriteString(`<p class="muted text-sm">A true volumetric flood that saturates your uplink can only be absorbed by anycast/scrubbing capacity no single host can provide; Tiers 1–2 handle the attacks a typical publisher actually faces, entirely sovereignly.</p></div>`)

	// Bot signature intelligence.
	if a.vayuShield != nil && a.vayuShield.BotStore() != nil {
		if st, err := a.vayuShield.BotStore().Stats(r.Context()); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">Signature database</div><div class="grid grid-3">`)
			b.WriteString(shieldStatCard("Total signatures", st.Total))
			b.WriteString(shieldStatCard("Learned (24h)", st.LearnedLast24h))
			b.WriteString(shieldStatCard("Pending review", st.PendingReview))
			b.WriteString(`</div><p class="muted text-sm">By class: `)
			for _, k := range []string{"bad_bot", "good_bot", "ai_agent", "human", "unknown"} {
				b.WriteString(html.EscapeString(k) + "=" + strconv.FormatInt(st.ByClass[k], 10) + " ")
			}
			b.WriteString(`</p><p><a class="btn" href="/os/api/shield/export" download="vayushield-signatures.json">Export learned signatures</a></p></div>`)
		}

		// Operator review queue.
		if q, err := a.vayuShield.BotStore().ReviewQueue(r.Context(), 25); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">Review queue — unverified candidates</div>`)
			if len(q) == 0 {
				b.WriteString(`<p class="muted">No candidates awaiting review.</p>`)
			} else {
				b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Fingerprint</th><th>UA family</th><th>Seen</th><th>Confidence</th><th>Actions</th></tr></thead><tbody>`)
				for _, sg := range q {
					fp := sg.FingerprintHash
					if len(fp) > 16 {
						fp = fp[:16]
					}
					idAttr := strconv.FormatInt(sg.ID, 10)
					b.WriteString(`<tr><td class="mono text-sm">` + html.EscapeString(fp) + `…</td><td>` + html.EscapeString(sg.UserAgentPattern) + `</td><td>` + strconv.FormatInt(sg.RequestCount, 10) + `</td><td>` + ftoa2(sg.Confidence) + `</td>` +
						`<td><button class="btn btn--sm btn--danger" data-shield-verify="` + idAttr + `" data-class="bad_bot">Confirm bot</button> ` +
						`<button class="btn btn--sm" data-shield-dismiss="` + idAttr + `">Dismiss</button></td></tr>`)
				}
				b.WriteString(`</tbody></table></div>`)
			}
			b.WriteString(`</div>`)
		}
	}

	// Engagement analytics.
	if a.vaEngagement != nil {
		if ov, err := a.vaEngagement.Overview(r.Context(), days); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">Engagement — last ` + strconv.Itoa(days) + ` days (human traffic)</div><div class="grid grid-3">`)
			b.WriteString(shieldStatCard("Views", ov.Views))
			b.WriteString(shieldStatCard("Unique sessions", ov.UniqueSessions))
			b.WriteString(shieldStatCard("Bot views (excluded)", ov.BotViews))
			b.WriteString(`</div><p class="muted text-sm">Avg time ` + ftoa2(ov.AvgTimeSeconds) + `s · avg scroll ` + ftoa2(ov.AvgScrollPct) + `% · engagement ` + pct(ov.EngagementRate) + ` · bounce ` + pct(ov.BounceRate) + ` · new ` + strconv.FormatInt(ov.NewSessions, 10) + ` / returning ` + strconv.FormatInt(ov.ReturningSessions, 10) + `</p></div>`)
		}
		if srcs, err := a.vaEngagement.SourceBreakdown(r.Context(), days); err == nil && len(srcs) > 0 {
			b.WriteString(`<div class="card"><div class="card-title">Traffic sources</div><div class="table-wrap"><table class="table"><thead><tr><th>Source</th><th>Views</th><th>Sessions</th><th>Avg time (s)</th><th>Avg scroll</th><th>Engagement</th></tr></thead><tbody>`)
			for _, s := range srcs {
				b.WriteString(`<tr><td>` + html.EscapeString(s.Category) + `</td><td>` + strconv.FormatInt(s.Views, 10) + `</td><td>` + strconv.FormatInt(s.Sessions, 10) + `</td><td>` + ftoa2(s.AvgTimeSeconds) + `</td><td>` + ftoa2(s.AvgScrollPct) + `%</td><td>` + pct(s.EngagementRate) + `</td></tr>`)
			}
			b.WriteString(`</tbody></table></div></div>`)
		}
		if ai, err := a.vaEngagement.AITraffic(r.Context(), days); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">AI-assisted discovery vs organic search</div>`)
			b.WriteString(`<p class="muted">AI traffic share: <strong>` + ftoa2(ai.AISharePercent) + `%</strong> of human views. ` +
				`AI-referred engagement ` + pct(ai.AISummary.EngagementRate) + ` (avg ` + ftoa2(ai.AISummary.AvgTimeSeconds) + `s) vs organic ` + pct(ai.OrganicSummary.EngagementRate) + ` (avg ` + ftoa2(ai.OrganicSummary.AvgTimeSeconds) + `s).</p>`)
			if len(ai.BySystem) > 0 {
				b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>AI system</th><th>Views</th><th>Avg time (s)</th><th>Engagement</th></tr></thead><tbody>`)
				for _, s := range ai.BySystem {
					b.WriteString(`<tr><td>` + html.EscapeString(s.Detail) + `</td><td>` + strconv.FormatInt(s.Views, 10) + `</td><td>` + ftoa2(s.AvgTimeSeconds) + `</td><td>` + pct(s.EngagementRate) + `</td></tr>`)
				}
				b.WriteString(`</tbody></table></div>`)
			}
			b.WriteString(`</div>`)
		}
		if pages, err := a.vaEngagement.TopPages(r.Context(), days, 10); err == nil && len(pages) > 0 {
			b.WriteString(`<div class="card"><div class="card-title">Top pages by views</div><div class="table-wrap"><table class="table"><thead><tr><th>Page</th><th>Views</th><th>Avg time (s)</th><th>Engagement</th></tr></thead><tbody>`)
			for _, p := range pages {
				b.WriteString(`<tr><td>` + html.EscapeString(p.Path) + `</td><td>` + strconv.FormatInt(p.Views, 10) + `</td><td>` + ftoa2(p.AvgTimeSeconds) + `</td><td>` + pct(p.EngagementRate) + `</td></tr>`)
			}
			b.WriteString(`</tbody></table></div></div>`)
		}
		if rt, err := a.vaEngagement.Realtime(r.Context(), 5); err == nil {
			b.WriteString(`<div class="card"><div class="card-title">Live now</div><p class="muted">Active visitors (last 5 min): <strong>` + strconv.FormatInt(rt.ActiveVisitors, 10) + `</strong> · bots active: ` + strconv.FormatInt(rt.BotsActive, 10) + ` · active pages: ` + strconv.Itoa(len(rt.ActivePages)) + `</p></div>`)
		}
	}

	// Inline controls (CSP-safe: nonce-gated, same-origin fetch with CSRF header).
	b.WriteString(`<div id="shield-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('shield-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
function post(url,body){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(body)});}
document.querySelectorAll('[data-shield-verify]').forEach(function(btn){btn.addEventListener('click',function(){
  post('/os/api/shield/verify',{id:parseInt(btn.getAttribute('data-shield-verify'),10),classification:btn.getAttribute('data-class'),notes:'confirmed via panel'})
   .then(function(r){if(r.ok){show('Signature confirmed',false);var row=btn.closest('tr');if(row)row.remove();}else{show('Failed',true);}});
});});
document.querySelectorAll('[data-shield-dismiss]').forEach(function(btn){btn.addEventListener('click',function(){
  post('/os/api/shield/dismiss',{id:parseInt(btn.getAttribute('data-shield-dismiss'),10)})
   .then(function(r){if(r.ok){show('Dismissed',false);var row=btn.closest('tr');if(row)row.remove();}else{show('Failed',true);}});
});});
var saveBtn=document.getElementById('shield-save');
if(saveBtn){saveBtn.addEventListener('click',function(){
  function ck(id){var e=document.getElementById(id);return !!(e&&e.checked);}
  function nv(id){var e=document.getElementById(id);return e?e.value:'';}
  saveBtn.disabled=true;show('Saving…',false);
  post('/os/api/shield/settings',{enabled:ck('sh_enabled'),pow:nv('sh_pow'),js:nv('sh_js'),block:nv('sh_block'),tarpit:ck('sh_tarpit'),ratelimit:ck('sh_ratelimit'),rpm:nv('sh_rpm'),burst:nv('sh_burst'),loadshed:ck('sh_loadshed'),max_inflight:nv('sh_maxinflight'),autoblock:ck('sh_autoblock'),jail_minutes:nv('sh_jail'),underattack:ck('sh_underattack'),underattack_rps:nv('sh_rps'),beacon:ck('sh_beacon')})
   .then(function(r){saveBtn.disabled=false;if(r.ok){show('Saved & applied',false);setTimeout(function(){location.reload();},600);}else{show('Save failed',true);}})
   .catch(function(){saveBtn.disabled=false;show('Network error',true);});
});}
})();
</script>`)

	writeOSHTML(w, adminOSLayout(nonce, "Bot Shield & Analytics", "shield", cfg, htmpl.HTML(b.String())))
}

// handleOSShieldVerify marks a learned candidate as operator-verified.
func (a *App) handleOSShieldVerify(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialized", "")
		return
	}
	var req struct {
		ID             int64  `json:"id"`
		Classification string `json:"classification"`
		Notes          string `json:"notes"`
	}
	if err := readCappedJSON(w, r, 8*1024, &req); err != nil || req.ID <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "id and classification required", "")
		return
	}
	class := botdb.Classification(req.Classification)
	switch class {
	case botdb.ClassBadBot, botdb.ClassGoodBot, botdb.ClassAIAgent, botdb.ClassHuman:
	default:
		class = botdb.ClassBadBot
	}
	if err := a.vayuShield.BotStore().Verify(r.Context(), req.ID, class, req.Notes); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]bool{"verified": true})
}

// handleOSShieldDismiss deletes a learned candidate the operator judged benign.
func (a *App) handleOSShieldDismiss(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialized", "")
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readCappedJSON(w, r, 8*1024, &req); err != nil || req.ID <= 0 {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "id required", "")
		return
	}
	if err := a.vayuShield.BotStore().Dismiss(r.Context(), req.ID); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]bool{"dismissed": true})
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
	var req struct {
		Enabled        bool   `json:"enabled"`
		PoW            string `json:"pow"`
		JS             string `json:"js"`
		Block          string `json:"block"`
		Tarpit         bool   `json:"tarpit"`
		RateLimit      bool   `json:"ratelimit"`
		RPM            string `json:"rpm"`
		Burst          string `json:"burst"`
		LoadShed       bool   `json:"loadshed"`
		MaxInFlight    string `json:"max_inflight"`
		AutoBlock      bool   `json:"autoblock"`
		JailMinutes    string `json:"jail_minutes"`
		UnderAttack    bool   `json:"underattack"`
		UnderAttackRPS string `json:"underattack_rps"`
		Beacon         bool   `json:"beacon"`
	}
	if err := readCappedJSON(w, r, 8*1024, &req); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid settings payload", "")
		return
	}
	bs := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	// Numeric fields are stored as trimmed strings; shieldSettings parses them
	// with safe fallbacks and ApplySettings clamps thresholds to (0,1].
	kv := map[string]string{
		settings.KeyShieldEnabled:        bs(req.Enabled),
		settings.KeyShieldPoW:            strings.TrimSpace(req.PoW),
		settings.KeyShieldJS:             strings.TrimSpace(req.JS),
		settings.KeyShieldBlock:          strings.TrimSpace(req.Block),
		settings.KeyShieldTarpit:         bs(req.Tarpit),
		settings.KeyShieldRateLimit:      bs(req.RateLimit),
		settings.KeyShieldRateRPM:        strings.TrimSpace(req.RPM),
		settings.KeyShieldBurst:          strings.TrimSpace(req.Burst),
		settings.KeyShieldLoadShed:       bs(req.LoadShed),
		settings.KeyShieldMaxInFlight:    strings.TrimSpace(req.MaxInFlight),
		settings.KeyShieldAutoBlock:      bs(req.AutoBlock),
		settings.KeyShieldJailMinutes:    strings.TrimSpace(req.JailMinutes),
		settings.KeyShieldUnderAttack:    bs(req.UnderAttack),
		settings.KeyShieldUnderAttackRPS: strings.TrimSpace(req.UnderAttackRPS),
		settings.KeyAnalyticsBeacon:      bs(req.Beacon),
	}
	if err := a.siteSettings.SetMany(r.Context(), kv); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	a.vayuShield.ApplySettings(a.shieldSettings(r.Context()))
	writeJSON(w, r, http.StatusOK, map[string]bool{"saved": true})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func shieldStatCard(label string, v int64) string {
	return `<div class="card"><div class="card-title">` + html.EscapeString(label) + `</div><div class="vm-stat">` + strconv.FormatInt(v, 10) + `</div></div>`
}

// shToggle renders a labelled checkbox row for the settings form.
func shToggle(id, label string, checked bool, hint string) string {
	c := ""
	if checked {
		c = " checked"
	}
	return `<label style="display:block;margin:.4rem 0"><input type="checkbox" id="` + id + `"` + c + `> <strong>` + html.EscapeString(label) + `</strong> <span class="muted text-sm">— ` + html.EscapeString(hint) + `</span></label>`
}

// shNum renders a labelled numeric input row for the settings form.
func shNum(id, label, val, hint string) string {
	return `<label style="display:block;margin:.4rem 0"><span>` + html.EscapeString(label) + `:</span> <input type="number" id="` + id + `" value="` + html.EscapeString(val) + `" step="any" min="0" style="width:6.5rem"> <span class="muted text-sm">— ` + html.EscapeString(hint) + `</span></label>`
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
