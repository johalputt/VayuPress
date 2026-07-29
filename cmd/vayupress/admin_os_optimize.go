// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_optimize.go — the "Optimize" hub. SEO, Analytics, VayuShield, Theme
// Studio and Theme Store, PLUS the everyday configuration surfaces (Tools &
// Plugins, Domains, Settings, VayuAPI, VayuMCP), are consolidated from the sidebar
// into ONE pinned tab of premium cards — the same dashboard-hub pattern used for
// content, Growth and Operations — so the VayuOS sidebar stays minimal and clean.
// Each card is gated to the viewer's access level, exactly like the sidebar it
// replaced, so an editor sees SEO/Analytics/Theme but never the admin-only cards
// (VayuShield, Domains, Settings, VayuAPI, VayuMCP, Tools).

import (
	"html"
	htmpl "html/template"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/render"
)

// optimizeSite is the lightweight per-domain descriptor the Optimize hub needs to
// render a "Your websites" card (one per secondary domain) linking to that site's
// manager. It carries only what the card shows — id, host and what it serves.
type optimizeSite struct {
	ID    string
	Host  string
	Label string
}

// handleOSOptimize renders the Optimize hub. Opens at editor level (osPathMinLevel
// gates "/os/optimize"); admin-only cards are hidden from editors in the grid.
func (a *App) handleOSOptimize(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// Each secondary domain becomes a "Your websites" card linking to its per-site
	// manager, so the operator controls every registered site from the Optimize hub.
	var sites []optimizeSite
	if a.domains != nil {
		if list, err := a.domains.List(r.Context()); err == nil {
			for _, d := range list {
				if d.IsPrimary || isPendingTorSite(d.Host) {
					continue
				}
				sites = append(sites, optimizeSite{ID: d.ID, Host: d.Host, Label: siteTypeLabel(d.EffectiveSiteType())})
			}
		}
	}

	writeOSHTML(w, r, adminOSLayout(nonce, "Optimize", "optimize", cfg, htmpl.HTML(osOptimizeGrid(cfg.AccessLevel, sites))))
}

// osOptimizeGrid builds the Optimize hub body as two card rows. Each card is shown
// only when the viewer's access level can actually reach it (osPathMinLevel),
// mirroring the sidebar gate — and a row's heading is emitted only when at least
// one of its cards is visible, so an editor never sees an empty "Configuration"
// heading.
func osOptimizeGrid(level int, sites []optimizeSite) string {
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

	// Your websites — one card per secondary domain, each opening that site's own
	// manager where its branding, content and lifecycle are controlled. Shown only
	// when a secondary site exists and the viewer can reach the domain registry.
	if len(sites) > 0 && level >= osPathMinLevel("/os/domains") {
		var cards strings.Builder
		for _, s := range sites {
			cards.WriteString(osWorkCard("/os/domains/"+html.EscapeString(s.ID), s.Host, s.Label, iconDomains, 0, "", false))
		}
		b.WriteString(`<div class="section-head"><span class="section-head__title">Your websites</span>` +
			`<span class="section-head__hint">Edit branding, content &amp; theme per site</span></div>` +
			`<div class="work-grid">` + cards.String() + `</div>`)
	}

	b.WriteString(row("Reach, protect &amp; polish", "Grow visibility and safeguard your site",
		card("/os/seo", "SEO", "Search visibility & metadata", iconSEO, true),
		card("/os/analytics", "Analytics", "Traffic & audience insights", iconAnalytics, false),
		card("/os/shield", "VayuShield", "Bot & abuse protection", iconSecurity, false),
		card("/os/theme", "Theme Studio", "Design & customise your theme", iconTheme, false),
		card("/os/theme/store", "Theme Store", "Browse & install themes", iconThemeStore, false),
	))
	b.WriteString(row("Configuration &amp; integrations", "Set up and extend your install",
		card("/os/tools", "Tools & Plugins", "Extensions & integrations", iconTools, false),
		card("/os/domains", "Domains", "Hostnames & routing", iconDomains, false),
		card("/os/settings", "Settings", "Site & install settings", iconSettings, false),
		card("/os/apikeys", "VayuAPI", "API keys & access", iconKey, false),
		card("/os/connector", "VayuMCP", "Model Context Protocol", iconConnector, false),
		card("/os/claudecode", "Claude Code", "Connect Claude & Claude Code", iconClaudeCode, false),
		card("/os/buzz", "Buzz", "Connect Buzz agents", iconBuzz, false),
	))
	return b.String()
}
