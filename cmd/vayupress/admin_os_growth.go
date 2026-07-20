package main

// admin_os_growth.go — the "Growth" hub. The former Audience (Members,
// Newsletter, My Profile) and Monetization (Monetization, Advertising) sidebar
// groups are consolidated into ONE pinned tab that presents them as premium cards
// with live counts — the same dashboard-style hub pattern used for content — so
// the VayuOS sidebar stays minimal and clean. Every card links to its existing
// page; only the navigation moved, nothing about those pages changed.

import (
	htmpl "html/template"
	"net/http"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
)

// handleOSGrowth renders the Growth hub. Admin-only — osPathMinLevel gates
// "/os/growth" to match the admin pages it fronts.
func (a *App) handleOSGrowth(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// Cheap on-demand counts for the card badges (best-effort — a missing table on
	// an older schema simply leaves the count at zero).
	members, subscribers := 0, 0
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(r.Context(), `SELECT COUNT(1) FROM members`).Scan(&members)
		_ = dbpkg.Reader().QueryRowContext(r.Context(), `SELECT COUNT(1) FROM members WHERE newsletter_opt_in=1`).Scan(&subscribers)
	}

	writeOSHTML(w, adminOSLayout(nonce, "Growth", "growth", cfg, htmpl.HTML(osGrowthGrid(members, subscribers))))
}

// osGrowthGrid builds the Growth hub body: a header plus the Audience and
// Monetization card rows (reusing the dashboard workspace card system), with live
// member and newsletter-subscriber counts.
func osGrowthGrid(members, subscribers int) string {
	var b strings.Builder
	b.WriteString(`<div class="page-head"><div><h1 class="page-title">Growth</h1><p class="page-sub">Your audience, subscribers and revenue — in one place.</p></div></div>`)

	b.WriteString(`<div class="section-head"><span class="section-head__title">Audience</span><span class="section-head__hint">Grow and reach your readers</span></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(osWorkCard("/os/members", "Members", "Readers, subscribers & tiers", iconMembers, members, "", true))
	b.WriteString(osWorkCard("/os/newsletter", "Newsletter", "Broadcasts & opt-ins", iconNewsletter, subscribers, "", false))
	b.WriteString(osWorkCard("/os/profile", "My Profile", "Your account & password", iconMembers, 0, "", false))
	b.WriteString(`</div>`)

	b.WriteString(`<div class="section-head"><span class="section-head__title">Monetization</span><span class="section-head__hint">Turn readers into revenue</span></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(osWorkCard("/os/monetization", "Monetization", "Plans, tiers & payments", iconMoney, 0, "", false))
	b.WriteString(osWorkCard("/os/ads", "Advertising", "Ad slots & placements", iconAds, 0, "", false))
	b.WriteString(`</div>`)
	return b.String()
}
