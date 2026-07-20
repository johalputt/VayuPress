package main

// admin_os_growth.go — the "Growth" hub: the operator's monetization command
// centre. It opens with live KPI cards (revenue, paid members, pending orders,
// premium addresses sold), then a full PREMIUM MAIL-ID MARKETPLACE control unit
// (per-status counts, the current price, and the live list of every vanity
// address a member has bought), and finally the Audience + Monetization section
// cards that drill into each detail page. Everything the monetization stack does
// is visible and reachable from here.

import (
	"context"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/render"
)

// handleOSGrowth renders the Growth hub. Admin-only — osPathMinLevel gates
// "/os/growth" to match the admin pages it fronts.
func (a *App) handleOSGrowth(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()

	// Cheap on-demand counts for the KPI cards (best-effort — a missing table on
	// an older schema simply leaves the count at zero).
	memberCount, subscribers, paid := 0, 0, 0
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM members`).Scan(&memberCount)
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM members WHERE newsletter_opt_in=1`).Scan(&subscribers)
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM members WHERE tier NOT IN ('free','')`).Scan(&paid)
	}

	writeOSHTML(w, adminOSLayout(nonce, "Growth", "growth", cfg, htmpl.HTML(a.osGrowthBody(ctx, memberCount, subscribers, paid))))
}

// osGrowthBody builds the Growth command centre: KPI row, the premium mail-ID
// marketplace control unit, then the Audience + Monetization card grids.
func (a *App) osGrowthBody(ctx context.Context, memberCount, subscribers, paid int) string {
	var stats payments.Stats
	if a.payments != nil {
		stats, _ = a.payments.Stats(ctx)
	}
	var gPending, gPaid, gClaimed int
	var grants []members.PremiumGrant
	if a.members != nil {
		gPending, gPaid, gClaimed = a.members.PremiumGrantCounts(ctx)
		grants, _ = a.members.AllPremiumGrants(ctx, 100)
	}
	sold := gPaid + gClaimed
	currency := a.payCurrency(ctx)
	revCurrency := stats.Currency
	if revCurrency == "" {
		revCurrency = currency
	}
	premiumPrice := priceLabel(currency, a.premiumMailIDPriceCents(ctx))

	var b strings.Builder
	b.WriteString(`<div class="page-head"><div><h1 class="page-title">Growth</h1><p class="page-sub">Your audience, subscribers and revenue — controlled from one place.</p></div></div>`)

	// Headline KPIs.
	b.WriteString(`<div class="stat-grid">`)
	b.WriteString(growthStat("Revenue collected", html.EscapeString(priceLabel(revCurrency, stats.RevenueCents))))
	b.WriteString(growthStat("Paid members", strconv.Itoa(paid)))
	b.WriteString(growthStat("Pending orders", strconv.Itoa(stats.Pending)))
	b.WriteString(growthStat("Premium addresses sold", strconv.Itoa(sold)))
	b.WriteString(`</div>`)

	// Premium mail-ID marketplace control unit.
	b.WriteString(`<div class="section-head"><span class="section-head__title">Premium mail-ID marketplace</span><span class="section-head__hint">Your vanity addresses &amp; their sales</span></div>`)
	b.WriteString(`<div class="stat-grid">`)
	b.WriteString(growthStat("Active addresses", strconv.Itoa(gClaimed)))
	b.WriteString(growthStat("Awaiting activation", strconv.Itoa(gPaid)))
	b.WriteString(growthStat("Reserved (unpaid)", strconv.Itoa(gPending)))
	b.WriteString(growthStat("Price per address", html.EscapeString(premiumPrice)))
	b.WriteString(`</div>`)
	b.WriteString(`<div class="card"><div class="settings-block-title">Premium addresses</div>`)
	b.WriteString(`<p class="text-sm muted mb-4">Every premium (vanity) address a member has bought, live. Members buy these from their account; you set the price and the terms they must accept in Monetization.</p>`)
	b.WriteString(premiumGrantsTable(grants))
	b.WriteString(`<div class="mt-2"><a class="btn btn--primary btn--sm" href="/os/monetization">Set price &amp; terms →</a></div></div>`)

	// Audience section cards.
	b.WriteString(`<div class="section-head"><span class="section-head__title">Audience</span><span class="section-head__hint">Grow and reach your readers</span></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(osWorkCard("/os/members", "Members", "Readers, subscribers & tiers", iconMembers, memberCount, "", true))
	b.WriteString(osWorkCard("/os/newsletter", "Newsletter", "Broadcasts & opt-ins", iconNewsletter, subscribers, "", false))
	b.WriteString(osWorkCard("/os/profile", "My Profile", "Your account & password", iconMembers, 0, "", false))
	b.WriteString(`</div>`)

	// Monetization section cards.
	b.WriteString(`<div class="section-head"><span class="section-head__title">Monetization</span><span class="section-head__hint">Turn readers into revenue</span></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(osWorkCard("/os/monetization", "Monetization", "Plans, tiers, payments & mail-ID pricing", iconMoney, 0, "", false))
	b.WriteString(osWorkCard("/os/ads", "Advertising", "Ad slots & placements", iconAds, 0, "", false))
	b.WriteString(`</div>`)
	return b.String()
}

// growthStat renders one KPI card.
func growthStat(label, value string) string {
	return `<div class="stat-card"><div class="stat-card__label">` + label + `</div><div class="stat-card__value">` + value + `</div></div>`
}

// premiumGrantPill maps a grant's lifecycle status to a coloured pill.
func premiumGrantPill(status string) string {
	switch status {
	case members.GrantClaimed:
		return `<span class="status-pill status-pill--live">● active</span>`
	case members.GrantPaid:
		return `<span class="status-pill status-pill--draft">● paid · awaiting activation</span>`
	case members.GrantPending:
		return `<span class="status-pill">● awaiting payment</span>`
	default:
		return `<span class="status-pill">● ` + html.EscapeString(status) + `</span>`
	}
}

// premiumGrantsTable renders the live list of premium-address purchases.
func premiumGrantsTable(grants []members.PremiumGrant) string {
	if len(grants) == 0 {
		return `<div class="table-empty">No premium addresses sold yet. They appear here as members buy vanity IDs from their account.</div>`
	}
	rows := ""
	for i := range grants {
		g := grants[i]
		rows += `<tr>` +
			`<td class="row-title"><code>` + html.EscapeString(g.Address()) + `</code></td>` +
			`<td class="muted text-sm">` + html.EscapeString(g.Email) + `</td>` +
			`<td>` + premiumGrantPill(g.Status) + `</td>` +
			`<td class="muted text-sm">` + g.CreatedAt.UTC().Format("2 Jan 2006") + `</td>` +
			`</tr>`
	}
	return `<div class="table-wrap"><table class="table">` +
		`<thead><tr><th>Address</th><th>Buyer</th><th>Status</th><th>Purchased</th></tr></thead>` +
		`<tbody>` + rows + `</tbody></table></div>`
}
