// SPDX-License-Identifier: Apache-2.0

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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayushield/verifiedbot"
)

// seoCrawlActivityCard renders the live "Search engine & AI crawl activity"
// panel: how many page requests each recognised search engine / AI system has
// been SERVED since the last restart. It reads VayuShield's verified-bot tally
// (server-side, so it captures crawlers that never run the JS beacon). Its whole
// purpose is assurance — visible proof that the shield is letting crawlers index
// the site, not blocking them.
func (a *App) seoCrawlActivityCard() string {
	var stats []verifiedbot.VendorStat
	if a.verifiedBots != nil {
		stats = a.verifiedBots.Stats()
	}
	if len(stats) == 0 {
		return `<p class="text-sm muted">No verified crawler visits recorded yet since the last restart. As Googlebot, Bingbot, GPTBot, ClaudeBot, PerplexityBot and others crawl your site, they appear here — live proof the shield is serving them, not blocking indexing.</p>`
	}
	fmtN := func(n int64) string {
		s := strconv.FormatInt(n, 10)
		// thousands separators
		if n < 1000 {
			return s
		}
		var out []byte
		for i, c := range []byte(s) {
			if i > 0 && (len(s)-i)%3 == 0 {
				out = append(out, ',')
			}
			out = append(out, c)
		}
		return string(out)
	}
	var searchRows, aiRows strings.Builder
	var searchTotal, aiTotal int64
	for _, s := range stats {
		row := `<tr><td class="row-title">` + html.EscapeString(s.Name) + `</td><td>` + fmtN(s.Count) + `</td></tr>`
		if s.Class == verifiedbot.ClassAIAgent {
			aiRows.WriteString(row)
			aiTotal += s.Count
		} else {
			searchRows.WriteString(row)
			searchTotal += s.Count
		}
	}
	tbl := func(title string, rows string, total int64) string {
		if rows == "" {
			return ""
		}
		return `<div class="mb-4"><div class="settings-block-title">` + title + ` <span class="muted text-sm">— ` + fmtN(total) + ` request` + plural(total) + ` served</span></div>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Crawler</th><th>Requests served</th></tr></thead>
    <tbody>` + rows + `</tbody></table></div></div>`
	}
	return `<p class="text-sm muted mb-4">Page requests VayuShield has served to verified crawlers since the last restart — proof they are reaching your content. Counted server-side (crawlers do not run the analytics beacon), so this reflects real crawl traffic even for bots that never appear in Analytics.</p>
  ` + tbl("Search engines", searchRows.String(), searchTotal) + tbl("AI systems", aiRows.String(), aiTotal)
}

// seoCrawlChip is the status pill for the crawl-activity accordion: total
// verified-crawler requests served, or a neutral "waiting" state.
func (a *App) seoCrawlChip() string {
	var total int64
	if a.verifiedBots != nil {
		for _, s := range a.verifiedBots.Stats() {
			total += s.Count
		}
	}
	if total == 0 {
		return `<span class="mon-chip mon-chip--off">○ Waiting for crawls</span>`
	}
	return `<span class="mon-chip mon-chip--on">● ` + strconv.FormatInt(total, 10) + ` served</span>`
}

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
		return true, config.FormatSiteStamp(fi.ModTime())
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
		headRobots = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyHeadRobots)
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

	// Instant-indexing (IndexNow) status: is a usable key configured, and where is
	// its public verification file? Shown alongside a one-click live self-test.
	inKey := a.indexNowKey()
	var indexNowStatus string
	switch {
	case config.Cfg.OnionMode:
		indexNowStatus = `<span class="badge badge--warn">Off</span> IndexNow is disabled in Tor/anonymous mode.`
	case inKey == "":
		indexNowStatus = `<span class="badge badge--warn">Not connected</span> Click <strong>Connect &amp; verify IndexNow</strong> below — a key is created, hosted and verified automatically. Nothing to set up by hand.`
	case !validIndexNowKey(inKey):
		indexNowStatus = `<span class="badge badge--danger">Invalid key</span> The key must be 8–128 characters of a–z, A–Z, 0–9 or hyphen — IndexNow will reject the current value.`
	default:
		link := "/" + inKey + ".txt"
		indexNowStatus = `<span class="badge badge--ok">Connected</span> Verification file: <a href="` + link + `" target="_blank" rel="noopener" class="mono">https://` + html.EscapeString(config.Cfg.Domain) + link + `</a>`
	}

	// Accordion status pills (Monetization-console style).
	inChip := `<span class="mon-chip mon-chip--off">○ Not connected</span>`
	if inKey != "" && validIndexNowKey(inKey) && !config.Cfg.OnionMode {
		inChip = `<span class="mon-chip mon-chip--on">● Connected</span>`
	}
	artChip := `<span class="mon-chip mon-chip--off">○ Incomplete</span>`
	if smOK && feedOK && robotsOK {
		artChip = `<span class="mon-chip mon-chip--on">● Ready</span>`
	}

	body := `<div class="page-header">
  <h1>SEO</h1>
  <div class="page-actions"><button type="button" class="btn btn--primary btn--sm" data-seo-regenerate>Regenerate artefacts</button></div>
</div>
<p class="page-sub">Search visibility, instant indexing and content health — plus live proof search engines and AI systems are crawling your content.</p>

<div class="stat-grid mb-6">
  <div class="stat-card"><div class="stat-card__label">SEO-healthy</div><div class="stat-card__value">` + num(healthy) + `</div><div class="stat-card__bottom"><span class="muted text-xs">good title + depth</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Thin content</div><div class="stat-card__value">` + num(thin) + `</div><div class="stat-card__bottom"><span class="muted text-xs">&lt;300 words</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Missing title</div><div class="stat-card__value">` + num(noTitle) + `</div><div class="stat-card__bottom"><span class="muted text-xs">needs a title</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Total posts</div><div class="stat-card__value">` + num(total) + `</div></div>
</div>` + seoComputingNote(ready) + `

<div class="section-head"><span class="section-head__title">Indexing</span><span class="section-head__hint">Get crawled fast &amp; see who is crawling</span></div>
<div class="mon-stack">` +
		monAcc(saIcon("bot"), "Search engine & AI crawl activity", "Live per-crawler counts — proof indexing works", a.seoCrawlChip(), true, a.seoCrawlActivityCard()) +
		monAcc(saIcon("bolt"), "Instant indexing (IndexNow)", "One-click auto-connect to Bing, Yandex & more", inChip, false,
			`<p class="text-sm muted">IndexNow tells Bing, Yandex and other participating engines the moment you publish or update a post, so changes get crawled in minutes instead of days. It is fully automatic — one click creates your key, hosts the verification file at your domain root and verifies it with IndexNow. After that, every post you publish is submitted for you.</p>
  <p class="text-sm mt-2">`+indexNowStatus+`</p>
  <div class="mt-3"><button type="button" class="btn btn--primary btn--sm" data-indexnow-test>Connect &amp; verify IndexNow</button></div>
  <div class="seo-status mt-3" data-indexnow-result hidden></div>`) + `
</div>

<div class="section-head"><span class="section-head__title">Site health</span><span class="section-head__hint">Artefacts &amp; on-page SEO checks</span></div>
<div class="mon-stack">` +
		monAcc(saIcon("doc"), "Artefacts", "Sitemap, RSS & robots.txt freshness", artChip, false,
			`<div class="table-wrap"><table class="table">
    <thead><tr><th>Artefact</th><th>Status</th></tr></thead>
    <tbody>
      <tr><td>Sitemap</td><td>`+badge(smOK, smWhen)+`</td></tr>
      <tr><td>RSS Feed</td><td>`+badge(feedOK, feedWhen)+`</td></tr>
      <tr><td>robots.txt</td><td>`+badge(robotsOK, robotsWhen)+`</td></tr>
    </tbody>
  </table></div>
  <div class="seo-status mt-3" data-seo-status hidden></div>`) +
		monAcc(saIcon("check-c"), "Health checks", "On-page SEO & crawlability", "", false,
			`<div class="table-wrap"><table class="table">
    <thead><tr><th>Check</th><th>Result</th></tr></thead>
    <tbody>`+checksRows+`</tbody>
  </table></div>`) + `
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + assetVer("js/admin-os-intel.js") + `"></script>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
// Delegated, and looked up per click: Regenerate re-renders this page in place
// (vpRefresh), which replaces the button a direct listener would be bound to.
document.addEventListener('click',function(e){
  var btn=e.target&&e.target.closest?e.target.closest('[data-indexnow-test]'):null;
  var out=document.querySelector('[data-indexnow-result]');
  if(!btn||!out)return;
  btn.disabled=true;out.hidden=false;out.className='seo-status mt-3';out.textContent='Testing IndexNow…';
  fetch('/os/api/seo/indexnow-test',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:'{}'})
   .then(function(r){return r.json();})
   .then(function(j){
     btn.disabled=false;
     var ok=j&&j.ok;
     out.className='seo-status mt-3 '+(ok?'editor-status--ok':'editor-status--warn');
     out.textContent=(ok?'✓ ':'✕ ')+((j&&j.detail)||(j&&j.error&&j.error.message)||'Test failed.');
   })
   .catch(function(){btn.disabled=false;out.className='seo-status mt-3 editor-status--danger';out.textContent='✕ Network error running the test.';});
});
})();
</script>`

	writeOSHTML(w, r, adminOSLayout(nonce, "SEO", "seo", cfg, htmpl.HTML(body)))
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
		writeOSHTML(w, r, adminOSLayout(nonce, "Analytics", "analytics", cfg, ui.Overview(ui.OverviewPage{Title: "Analytics"},
			ui.Band{Title: "Visitors", Aside: ui.Empty("trend", "Analytics are off", "This install was started without the analytics store.", "")})))
		return
	}

	// Selected reporting period (default 30 days, up to 3 years).
	days, periodLabel := analyticsPeriod(r)

	// The Analytics report runs ~a dozen aggregate scans over large tables. It is
	// computed OFF the request path and cached (admin_dashcache.go) so this tab
	// can never block a request into a 502; a startup warmer keeps the default
	// window hot. The nonce'd tabs script is appended per-request (never cached).
	frag, ready := adminDash.get("analytics:"+strconv.Itoa(days), analyticsFragmentTTL, func(ctx context.Context) string {
		return a.renderAnalyticsBody(ctx, days, periodLabel)
	})
	if !ready {
		frag = string(ui.Overview(ui.OverviewPage{Title: "Analytics", State: ui.Text("Counting the last " + periodLabel), Actions: osPeriodSelector(days)},
			ui.Band{Title: "Visitors", Hint: "Last " + periodLabel, Chart: `<p class="table-empty">This is counted in the background; reload in a few seconds.</p>`}))
	}
	body := frag + "\n" + `<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js?v=` + assetVer("js/admin-os-intel.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Analytics", "analytics", cfg, htmpl.HTML(body)))
}

// renderAnalyticsBody builds Analytics as the Overview kind (fidelity plan):
// visitors in the chosen range with the range as a segmented control, one
// chart beside who is on the site now, then the sections laid open, none a
// disclosure. It runs the heavy aggregate queries, so it is only ever called
// from the background fragment cache, never inline on a request; "" on a hard
// query error lets the cache retry rather than keep an empty report.
func (a *App) renderAnalyticsBody(ctx context.Context, days int, periodLabel string) string {
	sum, err := a.analytics.Since(ctx, days, 10)
	if err != nil {
		return ""
	}
	page := ui.OverviewPage{Title: "Analytics", Actions: osPeriodSelector(days)}
	sites, scope := a.analyticsScopeNote(ctx)
	if sum == nil {
		page.State = ui.Text("No visits recorded yet")
		return string(ui.Overview(page, ui.Band{Title: "Visitors", Hint: "Last " + periodLabel,
			Chart: `<p class="table-empty">Visits appear here as people read your site.</p>` + scope}))
	}

	ov, _ := a.analytics.OverviewSince(ctx, days)
	// The previous window of the same length, for each figure's change. Bounds
	// are date strings; the current window starts at curFrom (inclusive) and the
	// previous one is [prevFrom, curFrom).
	now := time.Now().UTC()
	curFrom := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	prevFrom := now.AddDate(0, 0, -(2*days - 1)).Format("2006-01-02")
	prevOv, _ := a.analytics.OverviewBetween(ctx, prevFrom, curFrom)
	devices, _ := a.analytics.Devices(ctx, days)
	browsers, _ := a.analytics.Browsers(ctx, days)
	oses, _ := a.analytics.OperatingSystems(ctx, days)
	channels, _ := a.analytics.Channels(ctx, days)
	events, _ := a.analytics.CustomEvents(ctx, days)
	utm, _ := a.analytics.UTMStats(ctx, days)
	countries, _ := a.analytics.Countries(ctx, days)
	regions, _ := a.analytics.Regions(ctx, days)
	cities, _ := a.analytics.Cities(ctx, days)
	series, _ := a.analytics.PageviewSeries(ctx, days)

	// The state is the one number the page is opened for, with whose traffic it
	// is: on an install hosting several sites every figure here adds them up,
	// and the page says so rather than let a client's number pass for the
	// operator's.
	state := "No visits in the last " + periodLabel
	if ov != nil && ov.UniqueVisitors > 0 {
		state = countOf(ov.UniqueVisitors, "visitor") + " in the last " + periodLabel
	}
	figs := analyticsFigures(ov, prevOv, sum.TotalViews)
	if sites > 1 {
		state += ", across all " + strconv.Itoa(sites) + " sites"
	}
	page.State = ui.Text(state)

	chart := ui.HTML(`<p class="table-empty">No visits in this period yet.</p>`)
	switch {
	case len(series) > 0:
		chart = ui.HTML(osTrendChart(series, "Traffic over "+periodLabel))
	case len(sum.Daily) > 0:
		vals := make([]int, 0, len(sum.Daily))
		for _, d := range sum.Daily {
			vals = append(vals, int(d.Views))
		}
		chart = ui.HTML(`<div class="sparkline-wrap">` + osSparkline(vals) + `</div>`)
	}

	bars := func(items []osChartBar, denom osBarDenom, empty string) string { return osBarList(items, denom, empty) }
	pageBars := make([]osChartBar, 0, len(sum.TopPages))
	for _, p := range sum.TopPages {
		pageBars = append(pageBars, osChartBar{Label: prettyPathText(p.Path), Value: int(p.Views), Href: p.Path})
	}
	refBars := make([]osChartBar, 0, len(sum.Referrers))
	for _, h := range sum.Referrers {
		refBars = append(refBars, osChartBar{Label: h.Host, Value: int(h.Hits)})
	}
	utmSourceBars := make([]osChartBar, 0, len(utm))
	utmRows := ""
	for _, u := range utm {
		src := u.Source
		if src == "" {
			src = "(direct)"
		}
		utmSourceBars = append(utmSourceBars, osChartBar{Label: src, Value: u.Count})
		utmRows += `<tr><td class="post-row__name">` + html.EscapeString(src) + `</td><td>` + html.EscapeString(u.Medium) + `</td><td>` + html.EscapeString(u.Campaign) + `</td><td>` + strconv.Itoa(u.Count) + `</td></tr>`
	}
	campaigns := `<p class="table-empty">No campaign traffic yet. Tag the links you share with <code>utm_source</code>, <code>utm_medium</code> and <code>utm_campaign</code> to see which bring visitors.</p>`
	if utmRows != "" {
		campaigns = analyticsCols(
			analyticsPanel("Sources", bars(utmSourceBars, osShareHidden(), "")),
			analyticsPanel("Campaigns", `<div class="table-wrap"><table class="table"><thead><tr><th>Source</th><th>Medium</th><th>Campaign</th><th>Hits</th></tr></thead><tbody>`+utmRows+`</tbody></table></div>`))
	}
	eventBars := make([]osChartBar, 0, len(events))
	for _, e := range events {
		eventBars = append(eventBars, osChartBar{Label: e.Name, Value: e.Count})
	}

	sections := []ui.HTML{
		ui.HTML(analyticsCols(
			string(ui.Section("Top pages", "", ui.HTML(bars(pageBars, osShareOf(int(sum.TotalViews)), "No page views recorded yet."))))+"",
			string(ui.Section("Referrers", "", ui.HTML(bars(refBars, osShareOf(int(sum.TotalReferrals)), "No links from other sites yet.")))))),
		ui.Section("How they found you", "Direct, search, social and other sites, from the referrer alone",
			ui.HTML(bars(osBarsFromAudience(channels), osShareOfListed(), "Visitors are grouped here once they arrive."))),
		ui.Section("What they read on", "", ui.HTML(analyticsCols(
			analyticsPanel("Devices", osDonut(osSegsFromAudience(devices), "No device data yet.")),
			analyticsPanel("Browsers", bars(osBarsFromAudience(browsers), osShareOfListed(), "No browser data yet.")),
			analyticsPanel("Systems", bars(osBarsFromAudience(oses), osShareOfListed(), "No system data yet."))))),
		ui.Section("Where they are", "Coarse location only", ui.HTML(osGeoSection(countries, regions, cities))),
		ui.Section("Campaigns", "", ui.HTML(campaigns)),
		ui.Section("Events", "What you track with data-vp-event or VayuPress.track()",
			ui.HTML(bars(eventBars, osShareHidden(), "No custom events yet."))),
		ui.HTML(a.osGoalsSection(ctx, days)),
		ui.HTML(a.osJourneySection(ctx, days)),
		ui.HTML(osExportSection(days)),
		ui.Explain(ui.HTML(`<p>Everything here is counted and kept on this server: no cookies, no personal data, and no request to anyone else. A visitor is recognised within a day by a salted hash that changes daily, so nobody can be followed from one day to the next.</p>`)),
	}
	return string(ui.Overview(page, ui.Band{Title: "Visitors", Hint: "Last " + periodLabel, Figures: figs, Chart: chart + scope,
		Aside: ui.Section("Right now", "", ui.HTML(osLiveCard()))}, sections...))
}

// analyticsFigures is the band's figure group: visitors and pageviews, which
// the browser beacon measures, each with its change on the window before, and
// the server's own count of page requests, which includes crawlers and
// visitors without JavaScript. The two counts are of different populations,
// so they are named differently and the request count says why it is higher:
// labelled alike, "31643 views" over "1327 pageviews" read as a
// contradiction. A figure that would read 0 is left out.
func analyticsFigures(ov, prev *analytics.Overview, requests int64) []ui.Figure {
	var figs []ui.Figure
	if ov != nil && ov.UniqueVisitors > 0 {
		var pv, pvw int
		if prev != nil {
			pv, pvw = prev.UniqueVisitors, prev.TotalPageviews
		}
		figs = append(figs, ui.Figure{Value: strconv.Itoa(ov.UniqueVisitors), Label: "Visitors", Note: analyticsChange(ov.UniqueVisitors, pv)},
			ui.Figure{Value: strconv.Itoa(ov.TotalPageviews), Label: "Pageviews", Note: analyticsChange(ov.TotalPageviews, pvw)})
	}
	if requests > 0 {
		figs = append(figs, ui.Figure{Value: strconv.FormatInt(requests, 10), Label: "Page requests", Note: "Crawlers and visitors without JavaScript included"})
	}
	return figs
}

// analyticsScopeNote says whose traffic these figures are. Every reader here
// is unscoped, so on an install serving several sites each figure adds them
// all up; the note says so and points at where one site's own figures are. On
// a single-site install it says nothing: a caveat that can never apply there
// is one operators learn to skip, the ones that matter included.
func (a *App) analyticsScopeNote(ctx context.Context) (int, ui.HTML) {
	if a.domains == nil {
		return 0, ""
	}
	list, err := a.domains.List(ctx)
	if err != nil || len(list) < 2 {
		return len(list), ""
	}
	return len(list), ui.HTML(`<p class="settings-row-hint">These figures add up all ` + strconv.Itoa(len(list)) +
		` sites this install serves. For one site on its own, open it from <a href="/os/domains">Sites</a> and use its own Visitors page, which counts that hostname alone.</p>`)
}

// analyticsChange says how a figure moved against the window before it, in
// words: a coloured arrow on a filled chip is the badge the page grammar
// replaced, and the words say which way is better on their own. A window
// before with none of the thing counted has nothing to compare with.
func analyticsChange(cur, prev int) string {
	switch {
	case prev == 0:
		return ""
	case cur == prev:
		return "The same as the period before"
	}
	pct := float64(cur-prev) / float64(prev) * 100
	if pct > 0 {
		return fmt.Sprintf("Up %.0f%% on the period before", pct)
	}
	return fmt.Sprintf("Down %.0f%% on the period before", -pct)
}

// analyticsCols sets panels side by side, as many as fit.
func analyticsCols(panels ...string) string {
	return `<div class="sa-cols">` + strings.Join(panels, "") + `</div>`
}

// analyticsPanel is one titled part of a section, without a box: a section
// already frames it, and a box inside it is the nesting the grammar forbids.
func analyticsPanel(title, body string) string {
	return `<div><h3 class="sa-panel__title">` + html.EscapeString(title) + `</h3>` + body + `</div>`
}

// analyticsPeriodOptions defines the selectable reporting windows, in days.
var analyticsPeriodOptions = []struct {
	Days  int
	Label string
}{
	{1, "24 hours"}, {7, "7 days"}, {30, "30 days"}, {90, "90 days"},
	{180, "6 months"}, {365, "1 year"}, {730, "2 years"}, {1095, "3 years"},
}

// analyticsLabelForDays returns the human label for a whitelisted window, used
// by the background dashboard warmer (which computes the default window).
func analyticsLabelForDays(days int) string {
	for _, o := range analyticsPeriodOptions {
		if o.Days == days {
			return o.Label
		}
	}
	return strconv.Itoa(days) + " days"
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
func osPeriodSelector(days int) ui.HTML {
	segs := make([]ui.Segment, 0, len(analyticsPeriodOptions))
	for _, o := range analyticsPeriodOptions {
		segs = append(segs, ui.Segment{Label: o.Label, Href: "/os/analytics?days=" + strconv.Itoa(o.Days), Count: -1, On: o.Days == days})
	}
	// data-period: the analytics script marks the chooser as loading while the
	// chosen range is fetched.
	return `<div data-period>` + ui.Segments("Period", segs...) + `</div>`
}

// osLiveCard renders the live-visitors panel; admin-os-intel.js polls
// /os/api/analytics/realtime every few seconds and fills it in. It shows the
// active-visitor count plus where they are (country), what they're viewing,
// and how they arrived (referrer).
func osLiveCard() string {
	return `<div class="vm-liveview" data-live>
  <div class="vm-live-hero mb-4">
    <div class="vm-live-hero__main">
      <div class="vm-live-badge"><span class="live-dot"></span> LIVE</div>
      <div class="vm-live-count" data-live-count>—</div>
      <div class="vm-live-sub muted text-sm">visitors active in the last <span data-live-window>5</span> minutes · auto-refreshes every 10s <span class="vm-live-updated text-xs" data-live-updated></span></div>
    </div>
    <div class="vm-live-rings" aria-hidden="true"><span></span><span></span><span></span></div>
  </div>
  <div class="sa-cols">
    <div><h3 class="sa-panel__title">Countries</h3>
      <div class="vp-bars vm-live-list" data-live-countries><p class="table-empty">Waiting for live data…</p></div></div>
    <div><h3 class="sa-panel__title">Pages</h3>
      <div class="vp-bars vm-live-list" data-live-pages><p class="table-empty">Waiting for live data…</p></div></div>
    <div><h3 class="sa-panel__title">Referrers</h3>
      <div class="vp-bars vm-live-list" data-live-referrers><p class="table-empty">Waiting for live data…</p></div></div>
  </div>
</div>`
}

// osGeoSection renders the Geography tab: countries + an offline continent
// breakdown (both work with no proxy, since countries resolve offline), then
// regions + cities (which need proxy location headers). When region/city data is
// absent it shows a precise, premium setup card rather than a blank panel.
func osGeoSection(countries, regions, cities []analytics.AudienceStat) string {
	if len(countries) == 0 && len(regions) == 0 && len(cities) == 0 {
		return osGeoSetupNote(true)
	}
	// Countries as a colour bar list with full country names + flags.
	countryBars := make([]osChartBar, 0, len(countries))
	for _, c := range countries {
		name := countryName(c.Label)
		if name == "" {
			name = c.Label
		}
		countryBars = append(countryBars, osChartBar{Label: name, LabelHTML: countryDisplayHTML(c.Label), Value: c.Count})
	}
	// Continents — aggregated offline from the country codes (no proxy needed),
	// so the Geography tab always shows a real world-region view. Sorted desc.
	contAgg := map[string]int{}
	for _, c := range countries {
		if name := continentName(c.Label); name != "" {
			contAgg[name] += c.Count
		}
	}
	continentBars := make([]osChartBar, 0, len(contAgg))
	for name, n := range contAgg {
		continentBars = append(continentBars, osChartBar{Label: name, Value: n})
	}
	sort.SliceStable(continentBars, func(i, j int) bool { return continentBars[i].Value > continentBars[j].Value })

	top := analyticsCols(
		analyticsPanel("Countries", `<div class="vp-geo-scroll">`+osBarList(countryBars, osShareOfListed(), "No country data yet.")+`</div>`),
		analyticsPanel("Continents", osBarList(continentBars, osShareOfListed(), "No continent data yet.")))

	// Regions & cities need proxy headers. When both are absent, show one setup
	// card spanning the row; otherwise show whichever populated bar lists exist.
	var detail string
	if len(regions) == 0 && len(cities) == 0 {
		detail = analyticsPanel("Regions and cities", osGeoSetupNote(false))
	} else {
		regionCard := osGeoSetupNote(false)
		if len(regions) > 0 {
			regionCard = `<div class="vp-geo-scroll">` + osBarList(osBarsFromAudience(regions), osShareOfListed(), "") + `</div>`
		}
		cityCard := osGeoSetupNote(false)
		if len(cities) > 0 {
			cityCard = `<div class="vp-geo-scroll">` + osBarList(osBarsFromAudience(cities), osShareOfListed(), "") + `</div>`
		}
		detail = analyticsCols(analyticsPanel("Regions", regionCard), analyticsPanel("Cities", cityCard))
	}
	return top + detail
}

// osGeoSetupNote renders a precise, premium setup card explaining how to light up
// region/city data. VayuPress does no IP geolocation itself (privacy by design);
// regions and cities require a CDN location header. The steps are Cloudflare-first
// (the common case) with other providers noted. CSP-safe: no inline styles.
func osGeoSetupNote(full bool) string {
	lead := "Regions and cities need one extra signal from your reverse proxy — VayuPress never geolocates IPs itself (privacy by design). Countries already resolve automatically."
	if full {
		lead = "No location data yet. VayuPress does no GeoIP lookups (privacy by design). Countries resolve automatically; regions and cities need one extra signal from your reverse proxy."
	}
	return `<div class="vp-geo-setup">
  <p class="vp-geo-setup__lead">` + lead + `</p>
  <ol class="vp-geo-setup__steps">
    <li><span class="vp-geo-setup__n">1</span> Cloudflare dashboard → your site → <strong>Rules → Settings</strong></li>
    <li><span class="vp-geo-setup__n">2</span> Turn on <strong>Add visitor location headers</strong></li>
    <li><span class="vp-geo-setup__n">3</span> Done — regions &amp; cities appear within minutes (adds <code>cf-region</code> &amp; <code>cf-ipcity</code>)</li>
  </ol>
  <p class="vp-geo-setup__alt">Also recognised automatically: AWS CloudFront, Vercel &amp; Fastly geo headers, and generic <code>X-Geo-Region</code> / <code>X-Geo-City</code>.</p>
</div>`
}

// osGoalsSection renders the conversion-goals card: a create form, plus a table
// of each goal's completions and conversion rate over the selected window.
func (a *App) osGoalsSection(ctx context.Context, days int) string {
	results, _ := a.analytics.GoalResults(ctx, days)
	rows := `<tr><td colspan="6" class="muted">` + noGoalsYet + `</td></tr>`
	if len(results) > 0 {
		rows = ""
		for _, g := range results {
			rows += `<tr><td class="post-row__name">` + html.EscapeString(g.Name) + `</td>` +
				`<td>` + html.EscapeString(goalKindLabel(g.Kind)) + `</td>` +
				`<td class="muted">` + html.EscapeString(g.Target) + `</td>` +
				`<td>` + strconv.Itoa(g.Completions) + ` <span class="muted text-xs">(` + strconv.Itoa(g.UniqueVisitors) + ` visitors)</span></td>` +
				`<td>` + fmt.Sprintf("%.1f%%", g.ConversionRate) + `</td>` +
				`<td><button class="btn btn--ghost btn--sm" data-goal-delete="` + html.EscapeString(g.ID) + `">Delete</button></td></tr>`
		}
	}
	// Adding a goal is a form, so it rises in a sheet.
	sheet := ui.Sheet("goal-new", "New goal", ui.HTML(`<form data-goal-form>
  <div class="field"><label class="field-label" for="goal-name">Name</label>
    <input id="goal-name" class="input" type="text" data-goal-name placeholder="Newsletter signup" required></div>
  <div class="field"><label class="field-label" for="goal-kind">Reached by</label>
    <select id="goal-kind" class="select" data-goal-kind><option value="path">Viewing a page</option><option value="event">A custom event</option></select></div>
  <div class="field"><label class="field-label" for="goal-target">Page or event</label>
    <input id="goal-target" class="input" type="text" data-goal-target placeholder="/thank-you  or  signup" required></div>
  <div class="mt-3 sa-list__sheet-actions"><button class="btn btn--primary btn--sm" type="submit">Add goal</button></div>
</form>`))
	return string(ui.Section("Goals", "The share of visitors who reach a page or fire an event", ui.HTML(`<div data-goals>
  <button type="button" class="btn btn--ghost btn--sm mb-3" data-sheet="goal-new">`+saIcon("plus")+` New goal</button>
  <div class="table-wrap"><table class="table">
    <thead><tr><th>Goal</th><th>Reached by</th><th>Target</th><th>Completions</th><th>Conversion</th><th></th></tr></thead>
    <tbody>`+rows+`</tbody>
  </table></div></div>`))) + string(sheet)
}

// noGoalsYet is the goals table's empty row; the script writes the same words
// when the last goal is deleted.
const noGoalsYet = `No goals yet. Add one with New goal: a page such as /thank-you, or a custom event such as signup.`

// goalKindLabel names how a goal is reached, in words.
func goalKindLabel(kind string) string {
	if kind == "event" {
		return "A custom event"
	}
	return "Viewing a page"
}

// osJourneySection renders the top page-to-page transitions (visitor journey).
func (a *App) osJourneySection(ctx context.Context, days int) string {
	flows, _ := a.analytics.PathFlows(ctx, days, 25)
	body := `<p class="table-empty">Once visitors read more than one page in a visit, their most common paths show here.</p>`
	if len(flows) > 0 {
		rows := ""
		for _, f := range flows {
			rows += `<tr><td class="post-row__name">` + osPrettyPath(f.From) + `</td><td class="muted">→</td><td class="post-row__name">` + osPrettyPath(f.To) + `</td><td>` + strconv.Itoa(f.Count) + `</td></tr>`
		}
		body = `<div class="table-wrap"><table class="table"><thead><tr><th>From</th><th></th><th>To</th><th>Times</th></tr></thead><tbody>` + rows + `</tbody></table></div>`
	}
	return string(ui.Section("Journeys", "(entry) is where a visit begins, (exit) where it ends", ui.HTML(body)))
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
		rows += `<tr><td class="post-row__name">` + html.EscapeString(labels[rep]) + `</td>` +
			`<td><a class="btn btn--sm" href="` + base + `&format=csv" download>CSV</a> ` +
			`<a class="btn btn--sm" href="` + base + `&format=json" download>JSON</a></td></tr>`
	}
	return string(ui.Section("Export", "Any report for this period, as CSV or JSON",
		ui.HTML(`<div class="table-wrap"><table class="table"><thead><tr><th>Report</th><th>Download</th></tr></thead><tbody>`+rows+`</tbody></table></div>`)))
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
