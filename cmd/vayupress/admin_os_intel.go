package main

// admin_os_intel.go — VayuOS intelligence surfaces (ADR-0068, Phase 6):
// a native SEO dashboard and a privacy-preserving analytics page. Both read
// only from the local DB and on-disk cache — no third-party services, matching
// VayuPress's sovereign, zero-telemetry stance.

import (
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

	sum, err := a.analytics.Since(r.Context(), 30, 10)
	if err != nil || sum == nil {
		body := `<div class="page-header"><h1>Analytics</h1></div>
<div class="empty-state">No analytics data yet.</div>`
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
		spark = `<div class="card mb-6"><div class="card-title">Views — last 30 days</div>
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
	ov, _ := a.analytics.OverviewSince(ctx, 30)
	devices, _ := a.analytics.Devices(ctx, 30)
	browsers, _ := a.analytics.Browsers(ctx, 30)
	oses, _ := a.analytics.OperatingSystems(ctx, 30)
	events, _ := a.analytics.CustomEvents(ctx, 30)
	utm, _ := a.analytics.UTMStats(ctx, 30)

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

	extra := `<div class="card-title mt-6" style="margin-top:1.5rem">VayuAnalytics — session insights · last 30 days</div>
<p class="muted text-sm mb-3">Cookieless, no-PII (server-side daily-rotating salted hash). Populates as visitors hit your site after this update.</p>` +
		overviewCard +
		`<div class="grid grid-3">
  <div class="card"><div class="card-title">Devices</div>` + osAudienceTable(devices) + `</div>
  <div class="card"><div class="card-title">Browsers</div>` + osAudienceTable(browsers) + `</div>
  <div class="card"><div class="card-title">Operating systems</div>` + osAudienceTable(oses) + `</div>
</div>
<div class="grid grid-2">
  <div class="card"><div class="card-title">Custom events</div>` + osEventTable(events) + `</div>
  <div class="card"><div class="card-title">Campaign sources (UTM)</div>` + utmRows + `</div>
</div>`

	body := `<div class="page-header"><h1>Analytics</h1>
  <span class="muted text-sm">` + strconv.FormatInt(sum.TotalViews, 10) + ` views · 30 days</span>
</div>` + spark + `
<div class="grid grid-2">
  <div class="card"><div class="card-title">Top pages</div>` + pages + `</div>
  <div class="card"><div class="card-title">Referrers</div>` + refs + `</div>
</div>` + extra

	writeOSHTML(w, adminOSLayout(nonce, "Analytics", "analytics", cfg, htmpl.HTML(body)))
}

// osStatCard renders a single big-number stat card.
func osStatCard(label, val string) string {
	return `<div class="card"><div class="card-title">` + html.EscapeString(label) + `</div><div class="stat" style="font-size:1.6rem;font-weight:600">` + html.EscapeString(val) + `</div></div>`
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
