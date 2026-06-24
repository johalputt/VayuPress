package main

// admin_os_intel.go — VayuOS intelligence surfaces (ADR-0068, Phase 6):
// a native SEO dashboard and a privacy-preserving analytics page. Both read
// only from the local DB and on-disk cache — no third-party services, matching
// VayuPress's sovereign, zero-telemetry stance.

import (
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

	// Load overview data for the default tab
	sum, err := a.analytics.Since(r.Context(), 30, 10)
	if err != nil || sum == nil {
		sum = &analytics.Summary{Days: 30, TopPages: []analytics.PathCount{}, Referrers: []analytics.HostCount{}, Daily: []analytics.DayCount{}}
	}

	// Sparkline of daily views
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

	// Tab navigation
	tabs := `<div class="tab-bar">
<a href="/os/analytics?tab=overview" class="tab active" data-tab="overview">Overview</a>
<a href="/os/analytics?tab=realtime" class="tab" data-tab="realtime">Realtime</a>
<a href="/os/analytics?tab=pages" class="tab" data-tab="pages">Pages</a>
<a href="/os/analytics?tab=referrers" class="tab" data-tab="referrers">Referrers</a>
<a href="/os/analytics?tab=audience" class="tab" data-tab="audience">Audience</a>
<a href="/os/analytics?tab=events" class="tab" data-tab="events">Events</a>
<a href="/os/analytics?tab=funnels" class="tab" data-tab="funnels">Funnels</a>
<a href="/os/analytics?tab=retention" class="tab" data-tab="retention">Retention</a>
<a href="/os/analytics?tab=revenue" class="tab" data-tab="revenue">Revenue</a>
<a href="/os/analytics?tab=replays" class="tab" data-tab="replays">Replays</a>
</div>`

	// Tab content containers
	body := `<div class="page-header"><h1>Analytics</h1>
  <span class="muted text-sm">` + strconv.FormatInt(sum.TotalViews, 10) + ` views · 30 days</span>
</div>` + tabs + `
<div id="tab-overview" class="tab-content active">` + spark + `
<div class="grid grid-2">
  <div class="card"><div class="card-title">Top pages</div>` + pages + `</div>
  <div class="card"><div class="card-title">Referrers</div>` + refs + `</div>
</div></div>
<div id="tab-realtime" class="tab-content"><div class="card" id="realtime-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-pages" class="tab-content"><div class="card" id="pages-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-referrers" class="tab-content"><div class="card" id="referrers-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-audience" class="tab-content"><div class="card" id="audience-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-events" class="tab-content"><div class="card" id="events-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-funnels" class="tab-content"><div class="card" id="funnels-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-retention" class="tab-content"><div class="card" id="retention-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-revenue" class="tab-content"><div class="card" id="revenue-card"><div class="card-title">Loading...</div></div></div>
<div id="tab-replays" class="tab-content"><div class="card" id="replays-card"><div class="card-title">Loading...</div></div></div>
<script nonce="` + nonce + `">
(function(){
var tab=new URLSearchParams(window.location.search).get('tab')||'overview';
document.querySelectorAll('.tab').forEach(function(t){if(t.dataset.tab===tab)t.classList.add('active');else t.classList.remove('active')});
document.querySelectorAll('.tab-content').forEach(function(c){c.style.display='none'});
var el=document.getElementById('tab-'+tab);if(el)el.style.display='block';
})();
</script>`

	writeOSHTML(w, adminOSLayout(nonce, "Analytics", "analytics", cfg, htmpl.HTML(body)))
}
