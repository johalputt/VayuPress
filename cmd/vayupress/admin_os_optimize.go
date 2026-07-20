package main

// admin_os_optimize.go — the "Optimize" hub. SEO, Analytics, Bot Shield, Theme
// Studio and Theme Store, PLUS the everyday configuration surfaces (Tools &
// Plugins, Domains, Settings, VayuAPI, VayuMCP), are consolidated from the sidebar
// into ONE pinned tab of premium cards — the same dashboard-hub pattern used for
// content, Growth and Operations — so the VayuOS sidebar stays minimal and clean.
// Each card is gated to the viewer's access level, exactly like the sidebar it
// replaced, so an editor sees SEO/Analytics/Theme but never the admin-only cards
// (Bot Shield, Domains, Settings, VayuAPI, VayuMCP, Tools).

import (
	htmpl "html/template"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/render"
)

// handleOSOptimize renders the Optimize hub. Opens at editor level (osPathMinLevel
// gates "/os/optimize"); admin-only cards are hidden from editors in the grid.
func (a *App) handleOSOptimize(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	writeOSHTML(w, adminOSLayout(nonce, "Optimize", "optimize", cfg, htmpl.HTML(osOptimizeGrid(cfg.AccessLevel))))
}

// osOptimizeGrid builds the Optimize hub body as two card rows. Each card is shown
// only when the viewer's access level can actually reach it (osPathMinLevel),
// mirroring the sidebar gate — and a row's heading is emitted only when at least
// one of its cards is visible, so an editor never sees an empty "Configuration"
// heading.
func osOptimizeGrid(level int) string {
	card := func(href, title, desc, icon string, accent bool) string {
		if level < osPathMinLevel(href) {
			return ""
		}
		return osWorkCard(href, title, desc, icon, 0, "", accent)
	}
	row := func(title, hint string, cards ...string) string {
		var g strings.Builder
		any := false
		for _, c := range cards {
			if c != "" {
				g.WriteString(c)
				any = true
			}
		}
		if !any {
			return ""
		}
		return `<div class="section-head"><span class="section-head__title">` + title +
			`</span><span class="section-head__hint">` + hint + `</span></div>` +
			`<div class="work-grid">` + g.String() + `</div>`
	}

	var b strings.Builder
	b.WriteString(`<div class="page-head"><div><h1 class="page-title">Optimize</h1><p class="page-sub">Reach, protect and polish your site — plus everything to configure it.</p></div></div>`)
	b.WriteString(row("Reach, protect &amp; polish", "Grow visibility and safeguard your site",
		card("/os/seo", "SEO", "Search visibility & metadata", iconSEO, true),
		card("/os/analytics", "Analytics", "Traffic & audience insights", iconAnalytics, false),
		card("/os/shield", "Bot Shield", "Bot & abuse protection", iconSecurity, false),
		card("/os/theme", "Theme Studio", "Design & customise your theme", iconTheme, false),
		card("/os/theme/store", "Theme Store", "Browse & install themes", iconThemeStore, false),
	))
	b.WriteString(row("Configuration &amp; integrations", "Set up and extend your install",
		card("/os/tools", "Tools & Plugins", "Extensions & integrations", iconTools, false),
		card("/os/domains", "Domains", "Hostnames & routing", iconDomains, false),
		card("/os/settings", "Settings", "Site & install settings", iconSettings, false),
		card("/os/apikeys", "VayuAPI", "API keys & access", iconKey, false),
		card("/os/connector", "VayuMCP", "Model Context Protocol", iconConnector, false),
	))
	return b.String()
}
