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

	body += `<div class="stat-grid">` +
		osStatCardDelta("Page views", strconv.Itoa(ov.TotalPageviews), "") +
		osStatCardDelta("Visits", strconv.Itoa(ov.TotalVisits), "") +
		osStatCardDelta("Bounce rate", strconv.FormatFloat(ov.BounceRate, 'f', 0, 64)+"%", "") +
		osStatCardDelta("Avg. visit", strconv.FormatFloat(ov.AvgDuration, 'f', 0, 64)+"s", "") +
		`</div>`

	body += `<div class="section-head"><div class="section-head__title">Busiest pages</div>` +
		`<div class="section-head__hint">This site's own pages only</div></div><div class="card">`
	if len(top) == 0 {
		body += `<p class="text-sm muted">No visits recorded for this site yet. Traffic recorded before ` +
			`this install was upgraded is attributed to the primary domain, because that is the only ` +
			`domain it could have been — it is not moved retroactively.</p>`
	} else {
		body += `<table class="table"><thead><tr><th>Page</th><th>Views</th><th>Visits</th></tr></thead><tbody>`
		for _, p := range top {
			body += `<tr><td class="mono text-sm">` + esc(p.Path) + `</td><td>` +
				strconv.Itoa(p.Pageviews) + `</td><td>` + strconv.Itoa(p.UniqueVisitors) + `</td></tr>`
		}
		body += `</tbody></table>`
	}
	body += `</div>`

	// What these numbers are and are not. The visitor count is deliberately the
	// distinct-session count, and saying so is cheaper than being asked why it
	// differs from the install-wide page.
	body += `<div class="card"><p class="text-sm muted">"Visits" counts distinct sessions on this ` +
		`hostname. The install-wide Analytics page counts unique visitors from a session table that ` +
		`carries no domain, so its visitor figure spans every site here — the two are different ` +
		`populations and are not comparable. A 404 is not counted as a page view.</p></div>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Visitors · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}
