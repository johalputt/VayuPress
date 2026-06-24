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
	"os"
	"path/filepath"
	"strconv"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
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

	// Per-article readiness (titles present, content depth). Computed in SQL so
	// we never load full bodies. Thin ≈ <1500 chars (~300 words).
	total, thin, noTitle := 0, 0, 0
	if rows, err := dbpkg.DB.QueryContext(r.Context(),
		`SELECT COALESCE(TRIM(title),''), LENGTH(COALESCE(content,'')) FROM articles`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var title string
			var clen int
			if rows.Scan(&title, &clen) != nil {
				continue
			}
			total++
			if title == "" {
				noTitle++
			} else if clen < 1500 {
				thin++
			}
		}
	}
	healthy := total - thin - noTitle
	if healthy < 0 {
		healthy = 0
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
  <div class="stat-card"><div class="stat-card__label">SEO-healthy</div><div class="stat-card__value">` + strconv.Itoa(healthy) + `</div><div class="stat-card__bottom"><span class="muted text-xs">good title + depth</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Thin content</div><div class="stat-card__value">` + strconv.Itoa(thin) + `</div><div class="stat-card__bottom"><span class="muted text-xs">&lt;300 words</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Missing title</div><div class="stat-card__value">` + strconv.Itoa(noTitle) + `</div><div class="stat-card__bottom"><span class="muted text-xs">needs a title</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Total posts</div><div class="stat-card__value">` + strconv.Itoa(total) + `</div></div>
</div>

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
<script nonce="` + nonce + `" src="/os/static/js/admin-os-intel.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "SEO", "seo", cfg, htmpl.HTML(body)))
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

	pages := `<div class="empty-state">No page views recorded yet.</div>`
	if len(sum.TopPages) > 0 {
		rows := ""
		for _, p := range sum.TopPages {
			rows += `<tr><td class="row-title">` + html.EscapeString(p.Path) + `</td><td>` + strconv.FormatInt(p.Views, 10) + `</td></tr>`
		}
		pages = `<div class="table-wrap"><table class="table"><thead><tr><th>Page</th><th>Views</th></tr></thead><tbody>` + rows + `</tbody></table></div>`
	}

	refs := `<div class="empty-state">No referrers recorded yet.</div>`
	if len(sum.Referrers) > 0 {
		rows := ""
		for _, h := range sum.Referrers {
			rows += `<tr><td class="row-title">` + html.EscapeString(h.Host) + `</td><td>` + strconv.FormatInt(h.Hits, 10) + `</td></tr>`
		}
		refs = `<div class="table-wrap"><table class="table"><thead><tr><th>Referrer</th><th>Hits</th></tr></thead><tbody>` + rows + `</tbody></table></div>`
	}

	// ── VayuAnalytics extended insights (v1.8.0): audience, engagement, events ──
	ctx := r.Context()
	ov, _ := a.analytics.OverviewSince(ctx, days)
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
		overviewCard = `<div class="grid grid-4">` +
			osStatCard("Unique visitors", strconv.Itoa(ov.UniqueVisitors)) +
			osStatCard("Visits", strconv.Itoa(ov.TotalVisits)) +
			osStatCard("Pageviews", strconv.Itoa(ov.TotalPageviews)) +
			osStatCard("Bounce rate", fmt.Sprintf("%.0f%%", ov.BounceRate)) + `</div>`
	}

	utmRows := `<div class="empty-state">No campaign traffic yet.</div>`
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

	extra := `<div class="card-title vm-section-title">VayuAnalytics — session insights · ` + periodLabel + `</div>
<p class="muted text-sm mb-3">Cookieless, no-PII (server-side daily-rotating salted hash). Populates as visitors hit your site after this update.</p>` +
		overviewCard +
		`<div class="grid grid-3">
  <div class="card"><div class="card-title">Devices</div>` + osAudienceTable(devices) + `</div>
  <div class="card"><div class="card-title">Browsers</div>` + osAudienceTable(browsers) + `</div>
  <div class="card"><div class="card-title">Operating systems</div>` + osAudienceTable(oses) + `</div>
</div>` + osGeoSection(countries, regions, cities) + `
<div class="grid grid-2">
  <div class="card"><div class="card-title">Custom events</div>` + osEventTable(events) + `</div>
  <div class="card"><div class="card-title">Campaign sources (UTM)</div>` + utmRows + `</div>
</div>` + a.osGoalsSection(ctx, days) + a.osJourneySection(ctx, days) + osExportSection(days)

	body := `<div class="page-header"><h1>Analytics</h1>
  <span class="muted text-sm">` + strconv.FormatInt(sum.TotalViews, 10) + ` views · ` + periodLabel + `</span>
</div>` + osPeriodSelector(days) + osLiveCard() + spark + `
<div class="grid grid-2">
  <div class="card"><div class="card-title">Top pages</div>` + pages + `</div>
  <div class="card"><div class="card-title">Referrers</div>` + refs + `</div>
</div>` + extra + `
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
// /os/api/analytics/realtime every few seconds and fills it in.
func osLiveCard() string {
	return `<div class="card mb-6" data-live>
  <div class="card-title"><span class="live-dot"></span> Live · active now</div>
  <div class="vm-stat" data-live-count>—</div>
  <div class="muted text-sm">visitors active in the last 5 minutes · updates every 10s</div>
  <div class="table-wrap mt-3"><table class="table"><thead><tr><th>Active page</th><th>Viewers</th></tr></thead><tbody data-live-pages><tr><td colspan="2" class="muted">Waiting for live data…</td></tr></tbody></table></div>
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
	return `<div class="grid grid-3">
  <div class="card"><div class="card-title">Countries</div>` + osAudienceTable(countries) + `</div>
  <div class="card"><div class="card-title">Regions</div>` + osAudienceTable(regions) + `</div>
  <div class="card"><div class="card-title">Cities</div>` + osAudienceTable(cities) + `</div>
</div>`
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
	body := `<div class="empty-state">No multi-page journeys recorded yet.</div>`
	if len(flows) > 0 {
		rows := ""
		for _, f := range flows {
			rows += `<tr><td class="row-title">` + html.EscapeString(f.From) + `</td><td class="muted">→</td><td class="row-title">` + html.EscapeString(f.To) + `</td><td>` + strconv.Itoa(f.Count) + `</td></tr>`
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

// osStatCard renders a single big-number stat card.
func osStatCard(label, val string) string {
	return `<div class="card"><div class="card-title">` + html.EscapeString(label) + `</div><div class="vm-stat">` + html.EscapeString(val) + `</div></div>`
}

// osAudienceTable renders a label/count breakdown (devices, browsers, OS).
func osAudienceTable(items []analytics.AudienceStat) string {
	if len(items) == 0 {
		return `<div class="empty-state">No data yet.</div>`
	}
	rows := ""
	for _, it := range items {
		label := it.Label
		if label == "" {
			label = "(unknown)"
		}
		rows += `<tr><td class="row-title">` + html.EscapeString(label) + `</td><td>` + strconv.Itoa(it.Count) + `</td></tr>`
	}
	return `<div class="table-wrap"><table class="table"><tbody>` + rows + `</tbody></table></div>`
}

// osEventTable renders custom-event counts.
func osEventTable(items []analytics.EventStat) string {
	if len(items) == 0 {
		return `<div class="empty-state">No custom events yet. Fire them with <code>VayuPress.track('name')</code> or <code>data-vp-event</code> attributes.</div>`
	}
	rows := ""
	for _, it := range items {
		rows += `<tr><td class="row-title">` + html.EscapeString(it.Name) + `</td><td>` + strconv.Itoa(it.Count) + `</td></tr>`
	}
	return `<div class="table-wrap"><table class="table"><thead><tr><th>Event</th><th>Count</th></tr></thead><tbody>` + rows + `</tbody></table></div>`
}
