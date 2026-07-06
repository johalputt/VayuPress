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

	enabled := shieldEnvBool("VAYUSHIELD", false)
	bots := botdb.New(dbpkg.DB)
	signer := challenge.NewSigner([]byte(config.Cfg.APIKey))

	a.vayuShield = vayushield.New(vayushield.Config{
		Enabled:        enabled,
		Static:         botdb.NewStaticDB(),
		Bots:           bots,
		Signer:         signer,
		DB:             dbpkg.DB,
		PoWThreshold:   shieldEnvFloat("VAYUSHIELD_POW_THRESHOLD", 0.4),
		JSThreshold:    shieldEnvFloat("VAYUSHIELD_JS_THRESHOLD", 0.6),
		BlockThreshold: shieldEnvFloat("VAYUSHIELD_BLOCK_THRESHOLD", 0.8),
		TarpitEnabled:  shieldEnvBool("VAYUSHIELD_TARPIT", false),
		// The admin panel, API, feeds, health/metrics and the shield's own
		// endpoints are never challenged.
		BypassPrefixes:    []string{"/os", "/api", "/admin", "/debug", "/health", "/metrics", "/__vayushield", "/__vayuanalytics", "/.well-known"},
		SessionCookieName: "vayushield",
		CountryFn:         geoip.Country,
		ClientIP:          auth.ClientIP,
		CookieSecure:      auth.CSRFCookieSecure(),
		OnEvent:           a.vayuShieldOnEvent,
	})

	if enabled {
		logging.LogInfo("vayushield", "bot protection ENABLED — PoW→JS→block→tarpit ladder + adaptive learning active")
		a.vayuShield.StartReporter(queue.DoneCh, 24*time.Hour, config.Cfg.AnalyticsRetainDays, func(res vayushield.LearningResult, err error) {
			if err != nil {
				logging.LogError("vayushield", "learning cycle failed", err.Error())
				return
			}
			if res.Promoted > 0 || res.Purged > 0 {
				logging.LogInfo("vayushield", fmt.Sprintf("learning cycle: promoted %d signature(s), purged %d stale", res.Promoted, res.Purged))
			}
		})
	} else {
		logging.LogInfo("vayushield", "bot protection disabled (default) — set VAYUSHIELD=on to enable; analytics ingestion remains active")
	}

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
	// referrer host was not in the classifier's list but the shield recognised it.
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

	// Protection status.
	status := `<span class="badge badge--warn">disabled</span>`
	if a.vayuShield != nil && a.vayuShield.Enabled() {
		status = `<span class="badge badge--ok">active</span>`
	}
	pow, js, block := 0.4, 0.6, 0.8
	if a.vayuShield != nil {
		pow, js, block = a.vayuShield.EffectiveThresholds()
	}
	b.WriteString(`<div class="card"><div class="card-title">Protection</div><p class="muted">Status: ` + status +
		` · Challenge thresholds — PoW ≥ ` + ftoa2(pow) + `, JS ≥ ` + ftoa2(js) + `, block ≥ ` + ftoa2(block) + `</p>` +
		`<p class="muted text-sm">Enable with <code>VAYUSHIELD=on</code>. Good bots (search engines) and AI agents (ChatGPT, Claude, Perplexity) are always allowed and counted separately — never blocked.</p></div>`)

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
})();
</script>`)

	writeOSHTML(w, adminOSLayout(nonce, "Bot Shield & Analytics", "shield", cfg, htmpl.HTML(b.String())))
}

// handleOSShieldVerify marks a learned candidate as operator-verified.
func (a *App) handleOSShieldVerify(w http.ResponseWriter, r *http.Request) {
	if a.vayuShield == nil || a.vayuShield.BotStore() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialised", "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialised", "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "shield-disabled", "VayuShield not initialised", "")
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

// ── helpers ───────────────────────────────────────────────────────────────────

func shieldStatCard(label string, v int64) string {
	return `<div class="card"><div class="card-title">` + html.EscapeString(label) + `</div><div class="vm-stat">` + strconv.FormatInt(v, 10) + `</div></div>`
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

// shieldEnvFloat reads a float env value with a default and [0,1] clamp.
func shieldEnvFloat(key string, def float64) float64 {
	v := strings.TrimSpace(config.EnvOr(key, ""))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1 {
		return def
	}
	return f
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
try{fetch('/__vayuanalytics/enter',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({p:location.pathname,q:location.search,r:document.referrer||''}),keepalive:true});}catch(e){}
})();`
