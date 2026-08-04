// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_analytics.go — one hosted domain's own traffic
// (ADR-0153 Phase 6).
//
// Migration 080 put the domain into the rolled-up daily counters and left the
// EVENT LOG alone — and the event log is what the panel, Top pages, trending and
// the overview all read. So per-domain traffic was half-built: the figure on a
// client's own page was scoped, and every figure on the operator's Analytics
// page counted every site on the install together.
//
// Migration 084 gives analytics_pageviews its domain, attributed SERVER-SIDE
// from the host this install resolved. Never from the beacon: that endpoint is
// public and unauthenticated, so a domain a visitor can name is a domain a
// visitor can write traffic into.

import (
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/render"
)

// handleOSScopedAnalytics renders one domain's traffic.
func (a *App) handleOSScopedAnalytics(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	esc := html.EscapeString

	body := `<div class="page-head"><div><h1 class="page-title">Visitors</h1>` +
		`<p class="page-sub"><a href="/os/d/` + esc(d.ID) + `">← ` + esc(d.Host) + `</a></p></div></div>` +
		`<p class="page-sub">Traffic to <b>` + esc(d.Host) + `</b> over the last 30 days. ` +
		`Counted without cookies, so nobody is tracked between visits.</p>`

	if a.analytics == nil {
		body += `<div class="card"><p class="text-sm muted">Analytics are not running on this install.</p></div>`
		writeOSHTML(w, r, adminOSLayout(nonce, "Visitors · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
		return
	}

	// d.ID is the scope. It is the same value the beacon path writes, so what is
	// counted here is exactly what was recorded for this host.
	ov, _ := a.analytics.OverviewSinceScoped(r.Context(), d.ID, 30)
	top, _ := a.analytics.TopPagesScoped(r.Context(), d.ID, 30, 10)

	body += scopedAnalyticsBody(ov.TotalPageviews, ov.TotalVisits, ov.BounceRate, ov.AvgDuration, top)
	writeOSHTML(w, r, adminOSLayout(nonce, "Visitors · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}

// scopedAnalyticsBody renders the numbers and the busiest-pages table.
//
// Extracted from the handler because the markup was built inline against a
// request, a database and a live analytics service — so nothing could render
// this page without all three, and it therefore had no test at all. A page that
// cannot be rendered in a test is a page whose restyling cannot be checked.
func scopedAnalyticsBody(views, visits int, bounce, avgDur float64, top []analytics.PageStat) string {
	esc := html.EscapeString
	var b strings.Builder

	b.WriteString(`<div class="stat-grid">` +
		osStatTile("Page views", strconv.Itoa(views), "") +
		osStatTile("Visits", strconv.Itoa(visits), "") +
		osStatTile("Bounce rate", strconv.FormatFloat(bounce, 'f', 0, 64)+"%", "") +
		osStatTile("Avg. visit", strconv.FormatFloat(avgDur, 'f', 0, 64)+"s", "") +
		`</div>`)

	// ── The bands, as collapsible details (house style §11) ──────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">This site's traffic</span>` +
		`<span class="section-head__hint">This site's own pages only</span></div>`)
	b.WriteString(`<div class="mon-stack">`)

	var pages strings.Builder
	pages.WriteString(`<div class="card">`)
	if len(top) == 0 {
		pages.WriteString(`<p class="text-sm muted">No visits recorded for this site yet. Traffic recorded before ` +
			`this install was upgraded is attributed to the primary domain, because that is the only ` +
			`domain it could have been — it is not moved retroactively.</p>`)
	} else {
		pages.WriteString(`<table class="table"><thead><tr><th>Page</th><th>Views</th><th>Visits</th></tr></thead><tbody>`)
		for _, p := range top {
			pages.WriteString(`<tr><td class="mono text-sm">` + esc(p.Path) + `</td><td>` +
				strconv.Itoa(p.Pageviews) + `</td><td>` + strconv.Itoa(p.UniqueVisitors) + `</td></tr>`)
		}
		pages.WriteString(`</tbody></table>`)
	}
	pages.WriteString(`</div>`)
	pagesChip := `<span class="mon-chip mon-chip--off">nothing yet</span>`
	if len(top) > 0 {
		pagesChip = `<span class="mon-chip mon-chip--on">` + strconv.Itoa(len(top)) + ` pages</span>`
	}
	b.WriteString(monAcc("📈", "Busiest pages", "Where this site's visits landed", pagesChip, true, pages.String()))

	// What these numbers are and are not. The visitor count is deliberately the
	// distinct-session count, and saying so is cheaper than being asked why it
	// differs from the install-wide page.
	b.WriteString(monAcc("📐", "What these numbers mean", "And what they cannot be compared with",
		`<span class="mon-chip mon-chip--on">read this once</span>`, false,
		`<div class="card"><p class="text-sm muted">"Visits" counts distinct sessions on this `+
			`hostname. The install-wide Analytics page counts unique visitors from a session table that `+
			`carries no domain, so its visitor figure spans every site here — the two are different `+
			`populations and are not comparable. A 404 is not counted as a page view.</p></div>`))

	b.WriteString(`</div>`) // mon-stack
	return b.String()
}
