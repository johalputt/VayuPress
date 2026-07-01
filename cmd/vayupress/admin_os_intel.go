package main

// admin_os_intel.go — VayuOS intelligence surfaces (ADR-0068, Phase 6):
// a native SEO dashboard and a privacy-preserving analytics page. Both read
// only from the local DB and on-disk cache — no third-party services, matching
// VayuPress's sovereign, zero-telemetry stance.

import (
	"context"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// handleOSSEONative renders the native os SEO dashboard: artefact freshness plus
// per-article readiness, computed live from the DB and cache.
func (a *App) handleOSSEONative(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	artefact := func(name string) (bool, string) {
		fi, err := os.Stat(filepath.Join(config.Cfg.CacheDir, name))
		if err != nil {
			return false, "not generated"
		}
		return true, fi.ModTime().UTC().Format("2006-01-02 15:04") + " UTC"
	}
	smOK, smWhen := artefact("sitemap.xml")
	feedOK, feedWhen := artefact("feed.xml")
	robotsOK, robotsWhen := artefact("robots.txt")

	// Inputs for the health checks: sitemap age, robots.txt body, head robots
	// directive and the canonical domain.
	var sitemapAge time.Duration
	if fi, err := os.Stat(filepath.Join(config.Cfg.CacheDir, "sitemap.xml")); err == nil {
		sitemapAge = time.Since(fi.ModTime())
	}
	robotsBody := ""
	if b, err := os.ReadFile(filepath.Join(config.Cfg.CacheDir, "robots.txt")); err == nil {
		robotsBody = string(b)
	}
	headRobots := ""
	if a.siteSettings != nil {
		headRobots = a.siteSettings.Get(r.Context(), settings.KeyHeadRobots)
	}
	checks := evaluateSEOHealth(smOK, sitemapAge, robotsOK, robotsBody, headRobots, config.Cfg.Domain)
	checksRows := ""
	for _, c := range checks {
		pill := `<span class="badge badge--ok">✓ Pass</span>`
		if !c.OK && c.Warn {
			pill = `<span class="badge badge--warn">! Check</span>`
		} else if !c.OK {
			pill = `<span class="badge badge--danger">✕ Issue</span>`
		}
		checksRows += `<tr><td>` + html.EscapeString(c.Label) + `</td><td>` + pill +
			`<div class="text-xs muted mt-1">` + html.EscapeString(c.Detail) + `</div></td></tr>`
	}

	// Per-article readiness. On large sites (hundreds of thousands of posts)
	// scanning every body to measure content length is expensive, so it is
	// computed in the BACKGROUND and cached — the page always renders instantly
	// and can never time out / 502 on the request path.
	stats, ready := seoStatsSnapshot()
	total, thin, noTitle, healthy := stats.total, stats.thin, stats.noTitle, stats.healthy

	num := func(n int) string {
		if !ready {
			return "…"
		}
		return strconv.Itoa(n)
	}

	badge := func(ok bool, when string) string {
		if ok {
			return `<span class="badge badge--ok">✓ Ready</span> <span class="muted text-sm">` + html.EscapeString(when) + `</span>`
		}
		return `<span class="badge badge--warn">` + html.EscapeString(when) + `</span>`
	}

	body := `<div class="page-header">
  <h1>SEO</h1>
  <button type="button" class="btn btn--primary btn--sm" data-seo-regenerate>Regenerate artefacts</button>
</div>

<div class="stat-grid mb-6">
  <div class="stat-card"><div class="stat-card__label">SEO-healthy</div><div class="stat-card__value">` + num(healthy) + `</div><div class="stat-card__bottom"><span class="muted text-xs">good title + depth</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Thin content</div><div class="stat-card__value">` + num(thin) + `</div><div class="stat-card__bottom"><span class="muted text-xs">&lt;300 words</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Missing title</div><div class="stat-card__value">` + num(noTitle) + `</div><div class="stat-card__bottom"><span class="muted text-xs">needs a title</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Total posts</div><div class="stat-card__value">` + num(total) + `</div></div>
</div>` + seoComputingNote(ready) + `

<div class="card">
  <div class="card-title">Artefacts</div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Artefact</th><th>Status</th></tr></thead>
    <tbody>
      <tr><td>Sitemap</td><td>` + badge(smOK, smWhen) + `</td></tr>
      <tr><td>RSS Feed</td><td>` + badge(feedOK, feedWhen) + `</td></tr>
      <tr><td>robots.txt</td><td>` + badge(robotsOK, robotsWhen) + `</td></tr>
    </tbody>
  </table></div>
  <div class="seo-status mt-3" data-seo-status hidden></div>
</div>

<div class="card mt-6">
  <div class="card-title">Health checks</div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Check</th><th>Result</th></tr></thead>
    <tbody>` + checksRows + `</tbody>
  </table></div>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "SEO", "seo", cfg, htmpl.HTML(body)))
}

// seoCheck is one SEO health finding. OK = pass; Warn = advisory; otherwise it
// is a hard problem (red). Detail explains the finding and the fix.
type seoCheck struct {
	Label  string
	OK     bool
	Warn   bool
	Detail string
}

// evaluateSEOHealth runs the actionable SEO checks against the current site
// state. It is a pure function (no I/O) so it is straightforward to unit-test;
// the handler gathers the inputs and renders the results.
func evaluateSEOHealth(sitemapOK bool, sitemapAge time.Duration, robotsOK bool, robotsBody, headRobots, domain string) []seoCheck {
	var out []seoCheck

	out = append(out, seoCheck{
		Label: "Sitemap generated", OK: sitemapOK,
		Detail: ternary(sitemapOK, "sitemap.xml is present and submitted to search engines via robots.txt.",
			"sitemap.xml has not been generated yet — click “Regenerate artefacts”."),
	})
	if sitemapOK {
		stale := sitemapAge > 7*24*time.Hour
		out = append(out, seoCheck{
			Label: "Sitemap fresh", OK: !stale, Warn: stale,
			Detail: ternary(stale, "sitemap.xml is over a week old; regenerate so new posts are discoverable.",
				"sitemap.xml was refreshed within the last week."),
		})
	}

	out = append(out, seoCheck{
		Label: "robots.txt present", OK: robotsOK,
		Detail: ternary(robotsOK, "robots.txt is served and points crawlers at your sitemap.",
			"robots.txt is missing — regenerate artefacts to create it."),
	})

	// A site-wide "Disallow: /" blocks all crawling — a critical, easy-to-miss
	// mistake. Detect it on any user-agent block.
	blocked := false
	for _, line := range strings.Split(robotsBody, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "disallow: /") {
			blocked = true
			break
		}
	}
	out = append(out, seoCheck{
		Label: "Crawling allowed", OK: !blocked,
		Detail: ternary(blocked, "robots.txt contains “Disallow: /”, which blocks search engines from your whole site.",
			"robots.txt does not block crawling of the site."),
	})

	noindex := strings.Contains(strings.ToLower(headRobots), "noindex")
	out = append(out, seoCheck{
		Label: "Indexing enabled", OK: !noindex,
		Detail: ternary(noindex, "The site-wide robots meta is set to noindex (Theme Studio → Head & SEO), so pages won’t be indexed.",
			"The site-wide robots directive allows indexing."),
	})

	d := strings.TrimSpace(domain)
	badDomain := d == "" || strings.HasPrefix(d, "localhost") || strings.HasPrefix(d, "127.0.0.1")
	out = append(out, seoCheck{
		Label: "Canonical domain set", OK: !badDomain, Warn: badDomain,
		Detail: ternary(badDomain, "The site domain looks unset or local; canonical URLs and share links need a real domain.",
			"Canonical URLs use "+d+"."),
	})

	return out
}

// ternary returns a when cond, else b — a tiny readability helper for the checks.
func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// ── SEO stats cache ──────────────────────────────────────────────────────────
//
// Counting "thin"/"missing title" posts requires reading every article body to
// measure its length. On a 234k-post site that is far too slow to run on the
// request path (it would time out behind nginx and return 502). So we compute
// it with a single aggregate query in a background goroutine, cache the result
// with a TTL, and refresh it lazily when the SEO page is viewed and the cache
// is stale. The page itself always renders instantly.

type seoStats struct {
	total, thin, noTitle, healthy int
	computedAt                    time.Time
	ready                         bool
}

var (
	seoStatsMu        sync.Mutex
	seoStatsCache     seoStats
	seoStatsComputing bool
	seoStatsLastTry   time.Time
)

const (
	seoStatsTTL      = 15 * time.Minute // re-use a fresh result this long
	seoStatsRetryGap = 1 * time.Minute  // throttle re-attempts after a miss/failure
)

// seoStatsSnapshot returns the cached tallies and whether a real computation has
// completed. It kicks off a background refresh when the cache is missing/stale
// and one isn't already running. It never blocks on the heavy scan.
func seoStatsSnapshot() (seoStats, bool) {
	seoStatsMu.Lock()
	defer seoStatsMu.Unlock()
	fresh := seoStatsCache.ready && time.Since(seoStatsCache.computedAt) < seoStatsTTL
	if !fresh && !seoStatsComputing && time.Since(seoStatsLastTry) > seoStatsRetryGap {
		seoStatsComputing = true
		seoStatsLastTry = time.Now()
		go computeSEOStats()
	}
	return seoStatsCache, seoStatsCache.ready
}

// computeSEOStats runs the (potentially slow) aggregate scan with a hard timeout
// and caches the result. Runs off the request path.
func computeSEOStats() {
	defer func() {
		seoStatsMu.Lock()
		seoStatsComputing = false
		seoStatsMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var total, noTitle, thin int
	// Read pool, not the writer connection: this scans content across the whole
	// catalog (which can be hundreds of thousands of posts) and must never block
	// writes/sessions. It is already cached + background-computed + time-limited.
	err := dbpkg.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(SUM(CASE WHEN TRIM(COALESCE(title,''))='' THEN 1 ELSE 0 END),0),
		        COALESCE(SUM(CASE WHEN TRIM(COALESCE(title,''))<>'' AND LENGTH(COALESCE(content,''))<1500 THEN 1 ELSE 0 END),0)
		 FROM articles`).Scan(&total, &noTitle, &thin)
	if err != nil {
		return // leave previous cache intact; retry is throttled by seoStatsRetryGap
	}
	healthy := total - thin - noTitle
	if healthy < 0 {
		healthy = 0
	}
	seoStatsMu.Lock()
	seoStatsCache = seoStats{total: total, thin: thin, noTitle: noTitle, healthy: healthy, computedAt: time.Now(), ready: true}
	seoStatsMu.Unlock()
}

// seoComputingNote shows a hint while the first background computation is in
// flight (so the "…" placeholders make sense to the operator).
func seoComputingNote(ready bool) string {
	if ready {
		return ""
	}
	return `<div class="empty-state">Computing content-quality stats in the background (large site)… reload in a few seconds.</div>`
}

// handleOSAnalytics renders the privacy-preserving analytics page from the local
// analytics_daily / analytics_referrers tables.
func (a *App) handleOSAnalytics(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	if a.analytics == nil {
		body := `<div class="page-header"><h1>Analytics</h1></div>
<div class="empty-state">Analytics are not enabled on this instance.</div>`
		writeOSHTML(w, adminOSLayout(nonce, "Analytics", "analytics", cfg, htmpl.HTML(body)))
		return
	}

	// Selected reporting period (default 30 days, up to 3 years).
	days, periodLabel := analyticsPeriod(r)

	sum, err := a.analytics.Since(r.Context(), days, 10)
	if err != nil || sum == nil {
		body := `<div class="page-header"><h1>Analytics</h1></div>` +
			osPeriodSelector(days) +
			`<div class="empty-state">No analytics data yet.</div>`
		writeOSHTML(w, adminOSLayout(nonce, "Analytics", "analytics", cfg, htmpl.HTML(body)))
		return
	}

	// Sparkline of daily views (reuse the dashboard renderer).
	vals := make([]int, 0, len(sum.Daily))
	for _, d := range sum.Daily {
		vals = append(vals, int(d.Views))
	}
	spark := ""
	if len(vals) > 0 {
		spark = `<div class="card mb-6"><div class="card-title">Views — ` + periodLabel + `</div>
<div class="sparkline-wrap">` + osSparkline(vals) + `</div></div>`
	}

	pageBars := make([]osChartBar, 0, len(sum.TopPages))
	for _, p := range sum.TopPages {
		pageBars = append(pageBars, osChartBar{Label: prettyPathText(p.Path), Value: int(p.Views), Href: p.Path})
	}
	pages := osBarList(pageBars, "No page views recorded yet. They'll appear here as visitors browse your site.")

	refBars := make([]osChartBar, 0, len(sum.Referrers))
	for _, h := range sum.Referrers {
		refBars = append(refBars, osChartBar{Label: h.Host, Value: int(h.Hits)})
	}
	refs := osBarList(refBars, "No referrers recorded yet. Links from other sites will show up here.")

	// ── VayuAnalytics extended insights (v1.8.0): audience, engagement, events ──
	ctx := r.Context()
	ov, _ := a.analytics.OverviewSince(ctx, days)
	// Previous equal-length window, for period-over-period % deltas on the
	// headline metrics. Bounds are date strings; the current window starts at
	// curFrom (inclusive) and the previous window is [prevFrom, curFrom).
	now := time.Now().UTC()
	curFrom := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	prevFrom := now.AddDate(0, 0, -(2*days - 1)).Format("2006-01-02")
	prevOv, _ := a.analytics.OverviewBetween(ctx, prevFrom, curFrom)
	devices, _ := a.analytics.Devices(ctx, days)
	browsers, _ := a.analytics.Browsers(ctx, days)
	oses, _ := a.analytics.OperatingSystems(ctx, days)
	events, _ := a.analytics.CustomEvents(ctx, days)
	utm, _ := a.analytics.UTMStats(ctx, days)
	countries, _ := a.analytics.Countries(ctx, days)
	regions, _ := a.analytics.Regions(ctx, days)
	cities, _ := a.analytics.Cities(ctx, days)

	overviewCard := ""
	if ov != nil {
		hasPrev := prevOv != nil && (prevOv.UniqueVisitors > 0 || prevOv.TotalVisits > 0 || prevOv.TotalPageviews > 0)
		pv, vis, pvw := 0, 0, 0
		var bounce float64
		if prevOv != nil {
			pv, vis, pvw, bounce = prevOv.UniqueVisitors, prevOv.TotalVisits, prevOv.TotalPageviews, prevOv.BounceRate
		}
		overviewCard = `<div class="grid grid-4 vm-metrics">` +
			osStatCardDelta("Unique visitors", strconv.Itoa(ov.UniqueVisitors), osDeltaPct(ov.UniqueVisitors, pv, hasPrev, false)) +
			osStatCardDelta("Visits", strconv.Itoa(ov.TotalVisits), osDeltaPct(ov.TotalVisits, vis, hasPrev, false)) +
			osStatCardDelta("Pageviews", strconv.Itoa(ov.TotalPageviews), osDeltaPct(ov.TotalPageviews, pvw, hasPrev, false)) +
			osStatCardDelta("Bounce rate", fmt.Sprintf("%.0f%%", ov.BounceRate), osDeltaPoints(ov.BounceRate, bounce, hasPrev)) + `</div>`
	}

	utmRows := `<div class="empty-state">No campaign traffic yet. Add <code>utm_source</code>, <code>utm_medium</code> &amp; <code>utm_campaign</code> tags to the links you share to see which campaigns bring visitors.</div>`
	if len(utm) > 0 {
		rows := ""
		for _, u := range utm {
			src := u.Source
			if src == "" {
				src = "(direct)"
			}
			rows += `<tr><td class="row-title">` + html.EscapeString(src) + `</td><td>` + html.EscapeString(u.Medium) + `</td><td>` + html.EscapeString(u.Campaign) + `</td><td>` + strconv.Itoa(u.Count) + `</td></tr>`
		}
		utmRows = `<div class="table-wrap"><table class="table"><thead><tr><th>Source</th><th>Medium</th><th>Campaign</th><th>Hits</th></tr></thead><tbody>` + rows + `</tbody></table></div>`
	}

	// Build each section once, then arrange them into tabs so the page stops
	// being one giant scroll. Tabs are switched client-side (no reload); the
	// period selector above applies to every tab.
	metricsIntro := `<p class="muted text-sm mb-3">Cookieless, no-PII (server-side daily-rotating salted hash). Populates as visitors hit your site.</p>`

	// Richer two-series traffic chart (pageviews + unique visitors) when the
	// detailed series is available; fall back to the daily-views sparkline.
	series, _ := a.analytics.PageviewSeries(ctx, days)
	chart := spark
	if len(series) > 0 {
		chart = `<div class="card mb-6"><div class="card-title">Traffic — ` + periodLabel + `</div>` +
			osTrendChart(series, "Traffic over "+periodLabel) + `</div>`
	}
	overviewPanel := metricsIntro + overviewCard + chart
	if overviewCard == "" && chart == "" {
		overviewPanel = metricsIntro + `<div class="empty-state">No visits in this period yet.</div>`
	}

	pagesPanel := `<div class="grid grid-2">
  <div class="card"><div class="card-title">Top pages</div>` + pages + `</div>
  <div class="card"><div class="card-title">Referrers</div>` + refs + `</div>
</div>`

	audiencePanel := `<div class="grid grid-3">
  <div class="card"><div class="card-title">Devices</div>` + osDonut(osSegsFromAudience(devices), "No device data yet.") + `</div>
  <div class="card"><div class="card-title">Browsers</div>` + osBarList(osBarsFromAudience(browsers), "No browser data yet.") + `</div>
  <div class="card"><div class="card-title">Operating systems</div>` + osBarList(osBarsFromAudience(oses), "No OS data yet.") + `</div>
</div>`

	utmSourceBars := make([]osChartBar, 0, len(utm))
	for _, u := range utm {
		src := u.Source
		if src == "" {
			src = "(direct)"
		}
		utmSourceBars = append(utmSourceBars, osChartBar{Label: src, Value: u.Count})
	}
	campaignsPanel := `<div class="grid grid-2">
  <div class="card"><div class="card-title">Top sources</div>` + osBarList(utmSourceBars, "No campaign traffic yet.") + `</div>
  <div class="card"><div class="card-title">Campaigns (UTM)</div>` + utmRows + `</div>
</div>`
	eventBars := make([]osChartBar, 0, len(events))
	for _, e := range events {
		eventBars = append(eventBars, osChartBar{Label: e.Name, Value: e.Count})
	}
	eventsPanel := `<div class="card"><div class="card-title">Custom events</div>` + osBarList(eventBars, "No custom events yet. Track actions with the data-vp-event attribute or window.VayuPress.track().") + `</div>`

	tabs := []struct{ id, label, icon, body string }{
		{"overview", "Overview", "📊", overviewPanel},
		{"live", "Live", "🟢", osLiveCard()},
		{"pages", "Pages", "📄", pagesPanel},
		{"audience", "Audience", "🖥️", audiencePanel},
		{"geo", "Geography", "🌍", osGeoSection(countries, regions, cities)},
		{"campaigns", "Campaigns", "📣", campaignsPanel},
		{"events", "Events", "✨", eventsPanel},
		{"goals", "Goals", "🎯", a.osGoalsSection(ctx, days)},
		{"journey", "Journey", "🧭", a.osJourneySection(ctx, days)},
		{"export", "Export", "⬇️", osExportSection(days)},
	}

	nav := `<div class="tab-list vm-analytics-tabs" role="tablist" data-analytics-tabs>`
	panels := ""
	for i, t := range tabs {
		active := ""
		hidden := " hidden"
		if i == 0 {
			active = " tab--active"
			hidden = ""
		}
		nav += `<button type="button" class="tab` + active + `" role="tab" data-atab="` + t.id + `"><span class="vm-tab-ico" aria-hidden="true">` + t.icon + `</span> ` + html.EscapeString(t.label) + `</button>`
		panels += `<section class="vm-tab-panel" role="tabpanel" data-atab-panel="` + t.id + `"` + hidden + `>` + t.body + `</section>`
	}
	nav += `</div>`

	body := `<div class="page-header"><h1>Analytics</h1>
  <span class="muted text-sm">` + strconv.FormatInt(sum.TotalViews, 10) + ` views · ` + periodLabel + ` · updated ` + now.Format("2006-01-02 15:04") + ` UTC</span>
</div>` + osPeriodSelector(days) + nav + panels + osPrivacyNote() + `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + Version + `"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Analytics", "analytics", cfg, htmpl.HTML(body)))
}

// analyticsPeriodOptions defines the selectable reporting windows, in days.
var analyticsPeriodOptions = []struct {
	Days  int
	Label string
}{
	{1, "24 hours"}, {7, "7 days"}, {30, "30 days"}, {90, "90 days"},
	{180, "6 months"}, {365, "1 year"}, {730, "2 years"}, {1095, "3 years"},
}

// analyticsPeriod resolves the ?days= query param to a whitelisted window,
// returning the day count and a human label. Defaults to 30 days; the maximum
// is 3 years (1095 days).
func analyticsPeriod(r *http.Request) (int, string) {
	want, _ := strconv.Atoi(r.URL.Query().Get("days"))
	for _, o := range analyticsPeriodOptions {
		if o.Days == want {
			return o.Days, "last " + o.Label
		}
	}
	return 30, "last 30 days"
}

// osPeriodSelector renders the period chooser as a row of links (GET, no JS).
func osPeriodSelector(days int) string {
	b := `<div class="vm-row mb-4" data-period>`
	for _, o := range analyticsPeriodOptions {
		cls := "btn btn--sm"
		if o.Days == days {
			cls += " btn--primary"
		}
		b += `<a class="` + cls + `" href="/os/analytics?days=` + strconv.Itoa(o.Days) + `">` + o.Label + `</a>`
	}
	return b + `</div>`
}

// osLiveCard renders the live-visitors panel; admin-os-intel.js polls
// /os/api/analytics/realtime every few seconds and fills it in. It shows the
// active-visitor count plus where they are (country), what they're viewing,
// and how they arrived (referrer).
func osLiveCard() string {
	return `<div class="vm-live" data-live>
  <div class="card vm-live-hero mb-4">
    <div class="card-title"><span class="live-dot"></span> Active right now</div>
    <div class="vm-live-count" data-live-count>—</div>
    <div class="muted text-sm">visitors in the last 5 minutes · auto-refreshes every 10s <span class="vm-live-pulse" data-live-updated></span></div>
  </div>
  <div class="grid grid-3 vm-live-grid">
    <div class="card"><div class="card-title">🌍 Countries</div>
      <div class="table-wrap"><table class="table"><tbody data-live-countries><tr><td class="muted">Waiting for live data…</td></tr></tbody></table></div>
    </div>
    <div class="card"><div class="card-title">📄 Active pages</div>
      <div class="table-wrap"><table class="table"><thead><tr><th>Page</th><th>Viewers</th></tr></thead><tbody data-live-pages><tr><td colspan="2" class="muted">Waiting for live data…</td></tr></tbody></table></div>
    </div>
    <div class="card"><div class="card-title">🔗 Referrers</div>
      <div class="table-wrap"><table class="table"><tbody data-live-referrers><tr><td class="muted">Waiting for live data…</td></tr></tbody></table></div>
    </div>
  </div>
</div>`
}

// osGeoSection renders country/region/city breakdowns. Geo is populated only
// when a reverse proxy (e.g. Cloudflare) supplies location headers; when empty
// it shows guidance rather than a blank card.
func osGeoSection(countries, regions, cities []analytics.AudienceStat) string {
	if len(countries) == 0 && len(regions) == 0 && len(cities) == 0 {
		return `<div class="card"><div class="card-title">Locations</div>
<div class="empty-state">No location data yet. VayuPress does no GeoIP lookups (privacy by design); to see countries/cities, front your site with a proxy that sets geo headers — e.g. Cloudflare's <code>CF-IPCountry</code> (country, all plans) and <code>CF-IPCity</code> (city, where available), or any <code>X-Geo-Country</code> / <code>X-Geo-City</code> header.</div></div>`
	}
	// Countries as a colour bar list with full country names; regions/cities keep
	// their setup-guidance empty states when no proxy location headers are present.
	countryBars := make([]osChartBar, 0, len(countries))
	for _, c := range countries {
		name := countryName(c.Label)
		if name == "" {
			name = c.Label
		}
		countryBars = append(countryBars, osChartBar{Label: name, LabelHTML: countryDisplayHTML(c.Label), Value: c.Count})
	}
	regionCard := osGeoTable(regions, "region")
	if len(regions) > 0 {
		regionCard = osBarList(osBarsFromAudience(regions), "")
	}
	cityCard := osGeoTable(cities, "city")
	if len(cities) > 0 {
		cityCard = osBarList(osBarsFromAudience(cities), "")
	}
	return `<div class="grid grid-3">
  <div class="card"><div class="card-title">Countries</div>` + osBarList(countryBars, "No country data yet.") + `</div>
  <div class="card"><div class="card-title">Regions</div>` + regionCard + `</div>
  <div class="card"><div class="card-title">Cities</div>` + cityCard + `</div>
</div>`
}

// osGeoTable renders a region/city breakdown. Unlike a plain audience table, its
// empty state explains how to populate it (these fields need proxy location
// headers that countries don't), so an operator who sees flags but blank
// regions/cities knows exactly what to enable.
func osGeoTable(items []analytics.AudienceStat, kind string) string {
	if len(items) == 0 {
		return `<div class="empty-state">No ` + html.EscapeString(kind) + ` data yet. ` +
			`Countries come from <code>CF-IPCountry</code> (sent by Cloudflare on every plan), but ` +
			html.EscapeString(kind) + `s need extra location headers: in Cloudflare enable ` +
			`<strong>Rules → Settings → Add visitor location headers</strong> (adds <code>cf-region</code> &amp; <code>cf-ipcity</code>). ` +
			`CloudFront, Vercel and Fastly geo headers, and generic <code>X-Geo-Region</code> / <code>X-Geo-City</code>, are also recognised.</div>`
	}
	rows := ""
	for _, it := range items {
		label := html.EscapeString(it.Label)
		if it.Label == "" {
			label = "(unknown)"
		}
		rows += `<tr><td class="row-title">` + label + `</td><td>` + strconv.Itoa(it.Count) + `</td></tr>`
	}
	return `<div class="table-wrap"><table class="table"><tbody>` + rows + `</tbody></table></div>`
}

// osGoalsSection renders the conversion-goals card: a create form, plus a table
// of each goal's completions and conversion rate over the selected window.
func (a *App) osGoalsSection(ctx context.Context, days int) string {
	results, _ := a.analytics.GoalResults(ctx, days)
	rows := `<tr><td colspan="5" class="muted">No goals yet. Add one above (e.g. a "/thank-you" path view or a "signup" custom event).</td></tr>`
	if len(results) > 0 {
		rows = ""
		for _, g := range results {
			rows += `<tr><td class="row-title">` + html.EscapeString(g.Name) + `</td>` +
				`<td><span class="badge">` + html.EscapeString(g.Kind) + `</span></td>` +
				`<td class="muted">` + html.EscapeString(g.Target) + `</td>` +
				`<td>` + strconv.Itoa(g.Completions) + ` <span class="muted text-xs">(` + strconv.Itoa(g.UniqueVisitors) + ` visitors)</span></td>` +
				`<td>` + fmt.Sprintf("%.1f%%", g.ConversionRate) + `</td>` +
				`<td><button class="btn btn--danger btn--sm" data-goal-delete="` + html.EscapeString(g.ID) + `">Delete</button></td></tr>`
		}
	}
	return `<div class="card mt-6" data-goals>
  <div class="card-title">Conversion goals</div>
  <p class="muted text-sm mb-3">Track how many visitors reach a page or fire a custom event. Conversion rate is the share of all unique visitors in the window.</p>
  <form class="vm-row mb-3" data-goal-form>
    <input class="input" type="text" data-goal-name placeholder="Goal name (e.g. Newsletter signup)" required>
    <select class="input" data-goal-kind>
      <option value="path">Page view</option>
      <option value="event">Custom event</option>
    </select>
    <input class="input" type="text" data-goal-target placeholder="/thank-you  or  signup" required>
    <button class="btn btn--primary" type="submit">Add goal</button>
  </form>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Goal</th><th>Type</th><th>Target</th><th>Completions</th><th>Conv. rate</th><th></th></tr></thead>
    <tbody>` + rows + `</tbody>
  </table></div>
</div>`
}

// osJourneySection renders the top page-to-page transitions (visitor journey).
func (a *App) osJourneySection(ctx context.Context, days int) string {
	flows, _ := a.analytics.PathFlows(ctx, days, 25)
	body := `<div class="empty-state">No multi-page journeys recorded yet. Once visitors browse more than one page in a session, their most common paths will show here.</div>`
	if len(flows) > 0 {
		rows := ""
		for _, f := range flows {
			rows += `<tr><td class="row-title">` + osPrettyPath(f.From) + `</td><td class="muted">→</td><td class="row-title">` + osPrettyPath(f.To) + `</td><td>` + strconv.Itoa(f.Count) + `</td></tr>`
		}
		body = `<div class="table-wrap"><table class="table"><thead><tr><th>From</th><th></th><th>To</th><th>Transitions</th></tr></thead><tbody>` + rows + `</tbody></table></div>`
	}
	return `<div class="card mt-6">
  <div class="card-title">Visitor journey</div>
  <p class="muted text-sm mb-3">Most common page-to-page transitions. <code>(entry)</code> marks where sessions begin and <code>(exit)</code> where they end.</p>` + body + `</div>`
}

// osExportSection renders download links for every report in CSV and JSON over
// the selected window.
func osExportSection(days int) string {
	labels := map[string]string{
		"overview": "Overview", "pages": "Top pages", "referrers": "Referrers",
		"browsers": "Browsers", "devices": "Devices", "os": "Operating systems",
		"countries": "Countries", "regions": "Regions", "cities": "Cities",
		"utm": "Campaigns (UTM)", "events": "Custom events", "sessions": "Sessions",
		"goals": "Goals", "journey": "Visitor journey",
	}
	d := strconv.Itoa(days)
	rows := ""
	for _, rep := range analyticsExportReports {
		base := "/os/api/analytics/export?days=" + d + "&report=" + rep
		rows += `<tr><td class="row-title">` + html.EscapeString(labels[rep]) + `</td>` +
			`<td><a class="btn btn--sm" href="` + base + `&format=csv" download>CSV</a> ` +
			`<a class="btn btn--sm" href="` + base + `&format=json" download>JSON</a></td></tr>`
	}
	return `<div class="card mt-6">
  <div class="card-title">Export reports</div>
  <p class="muted text-sm mb-3">Download any report as CSV or JSON for the selected period. Exports are computed locally and contain no PII.</p>
  <div class="table-wrap"><table class="table"><thead><tr><th>Report</th><th>Download</th></tr></thead><tbody>` + rows + `</tbody></table></div>
</div>`
}

// osStatCardDelta renders a big-number stat card with an optional period-over-
// period change badge (deltaHTML may be empty).
func osStatCardDelta(label, val, deltaHTML string) string {
	return `<div class="card"><div class="card-title">` + html.EscapeString(label) + `</div>` +
		`<div class="vm-stat-row"><span class="vm-stat">` + html.EscapeString(val) + `</span>` + deltaHTML + `</div></div>`
}

// osDeltaPct renders a relative percentage-change badge comparing the current
// value to the previous equal-length window. When lowerIsBetter is true (e.g.
// bounce rate) the colour semantics are inverted. Returns "" when there is no
// comparable previous data.
func osDeltaPct(cur, prev int, hasPrev, lowerIsBetter bool) string {
	if !hasPrev {
		if cur > 0 {
			return `<span class="vm-delta vm-delta--new" title="No data in the previous period">new</span>`
		}
		return ""
	}
	if prev == 0 {
		if cur == 0 {
			return ""
		}
		return `<span class="vm-delta vm-delta--good" title="Up from 0 in the previous period">▲ new</span>`
	}
	pct := float64(cur-prev) / float64(prev) * 100
	return osDeltaBadge(pct, cur >= prev, lowerIsBetter, fmt.Sprintf("%.0f%%", absFloat(pct)))
}

// osDeltaPoints renders a percentage-point change badge for rate metrics such
// as bounce rate (where a decrease is an improvement).
func osDeltaPoints(cur, prev float64, hasPrev bool) string {
	if !hasPrev {
		return ""
	}
	diff := cur - prev
	if absFloat(diff) < 0.05 {
		return `<span class="vm-delta vm-delta--flat" title="No change vs previous period">±0 pts</span>`
	}
	return osDeltaBadge(diff, cur >= prev, true, fmt.Sprintf("%.1f pts", absFloat(diff)))
}

// osDeltaBadge builds the arrow + text badge with good/bad/flat colouring.
func osDeltaBadge(delta float64, up, lowerIsBetter bool, text string) string {
	if absFloat(delta) < 0.5 {
		return `<span class="vm-delta vm-delta--flat" title="No meaningful change vs previous period">±0%</span>`
	}
	arrow := "▲"
	if !up {
		arrow = "▼"
	}
	good := up != lowerIsBetter // up & higher-is-better, or down & lower-is-better
	cls := "vm-delta--bad"
	if good {
		cls = "vm-delta--good"
	}
	return `<span class="vm-delta ` + cls + `" title="vs previous ` + "period" + `">` + arrow + ` ` + html.EscapeString(text) + `</span>`
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// osPrettyPath renders a page path for display: URL-decoded, query-string
// stripped, and truncated with an ellipsis (full value preserved in a tooltip).
// The literal journey markers "(entry)" / "(exit)" are passed through verbatim.
func osPrettyPath(p string) string {
	full := p
	disp := p
	// Drop any query/fragment that may have slipped through.
	if i := strings.IndexAny(disp, "?#"); i >= 0 {
		disp = disp[:i]
	}
	if dec, err := url.QueryUnescape(disp); err == nil && dec != "" {
		disp = dec
	}
	if disp == "" {
		disp = "/"
	}
	const max = 48
	if len([]rune(disp)) > max {
		r := []rune(disp)
		disp = string(r[:max-1]) + "…"
	}
	return `<span title="` + html.EscapeString(full) + `">` + html.EscapeString(disp) + `</span>`
}

// osPrivacyNote renders the trust footer shown at the bottom of the analytics
// page, reassuring operators that nothing leaves their server.
func osPrivacyNote() string {
	return `<p class="vm-privacy-note muted text-sm">🔒 All analytics are computed and stored locally on your own server. No cookies, no PII, no third-party requests — your data never leaves this instance.</p>`
}
