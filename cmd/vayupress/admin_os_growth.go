// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_growth.go — the "Growth" hub: a clean launcher for the Audience
// (Members, Newsletter, Profile) and Monetization surfaces. All revenue controls,
// KPIs, the premium mail-ID marketplace and the orders audit ledger live inside
// the Monetization console (/os/monetization) — the single premium command
// centre — so this hub stays uncluttered.

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
	ctx := r.Context()

	memberCount, subscribers, paid := 0, 0, 0
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM members`).Scan(&memberCount)
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM members WHERE newsletter_opt_in=1`).Scan(&subscribers)
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM members WHERE tier NOT IN ('free','')`).Scan(&paid)
	}

	writeOSHTML(w, r, adminOSLayout(nonce, "Growth", "growth", cfg, htmpl.HTML(osGrowthGrid(memberCount, subscribers, paid))))
}

// osGrowthGrid builds the clean Growth hub: the Audience cards and a Monetization
// launcher card grid — each links to its detail page.
func osGrowthGrid(memberCount, subscribers, paid int) string {
	var b strings.Builder
	b.WriteString(`<div class="page-header"><h1>Growth</h1></div><p class="page-sub">Your audience and subscribers. Revenue, products and payouts live in Monetization.</p>`)

	b.WriteString(`<div class="section-head"><span class="section-head__title">Audience</span><span class="section-head__hint">Grow and reach your readers</span></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(osWorkCard("/os/members", "Members", "Readers, subscribers & tiers", iconMembers, memberCount, "", true))
	b.WriteString(osWorkCard("/os/newsletter", "Newsletter", "Broadcasts & opt-ins", iconNewsletter, subscribers, "", false))
	b.WriteString(osWorkCard("/os/profile", "My Profile", "Your account & password", iconMembers, 0, "", false))
	b.WriteString(`</div>`)

	b.WriteString(`<div class="section-head"><span class="section-head__title">Monetization</span><span class="section-head__hint">Turn readers into revenue — one control centre</span></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(osWorkCard("/os/monetization", "Monetization", "Payments, plans, mail-IDs & paid posts", iconMoney, paid, "", true))
	b.WriteString(osWorkCard("/os/ads", "Advertising", "Ad slots & placements", iconAds, 0, "", false))
	b.WriteString(`</div>`)
	return b.String()
}
