package main

// admin_v3_intel.go — Admin v3 intelligence surfaces (ADR-0068, Phase 6):
// a native SEO dashboard and a privacy-preserving analytics page. Both read
// only from the local DB and on-disk cache — no third-party services, matching
// VayuPress's sovereign, zero-telemetry stance.

import (
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
)

// handleV3SEONative renders the native v3 SEO dashboard: artefact freshness plus
// per-article readiness, computed live from the DB and cache.
func (a *App) handleV3SEONative(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())

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
<script nonce="` + nonce + `" src="/admin/v3/static/js/admin-v3-intel.js"></script>`

	writeV3HTML(w, adminV3Layout(nonce, "SEO", "seo", cfg, body))
}

// handleV3Analytics renders the privacy-preserving analytics page from the local
// analytics_daily / analytics_referrers tables.
func (a *App) handleV3Analytics(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())

	if a.analytics == nil {
		body := `<div class="page-header"><h1>Analytics</h1></div>
<div class="empty-state">Analytics are not enabled on this instance.</div>`
		writeV3HTML(w, adminV3Layout(nonce, "Analytics", "analytics", cfg, body))
		return
	}

	sum, err := a.analytics.Since(r.Context(), 30, 10)
	if err != nil || sum == nil {
		body := `<div class="page-header"><h1>Analytics</h1></div>
<div class="empty-state">No analytics data yet.</div>`
		writeV3HTML(w, adminV3Layout(nonce, "Analytics", "analytics", cfg, body))
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
<div class="sparkline-wrap">` + v3Sparkline(vals) + `</div></div>`
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

	body := `<div class="page-header"><h1>Analytics</h1>
  <span class="muted text-sm">` + strconv.FormatInt(sum.TotalViews, 10) + ` views · 30 days</span>
</div>` + spark + `
<div class="grid grid-2">
  <div class="card"><div class="card-title">Top pages</div>` + pages + `</div>
  <div class="card"><div class="card-title">Referrers</div>` + refs + `</div>
</div>`

	writeV3HTML(w, adminV3Layout(nonce, "Analytics", "analytics", cfg, body))
}
