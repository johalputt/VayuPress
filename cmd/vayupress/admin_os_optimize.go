package main

// admin_os_optimize.go — the "Optimize" hub. SEO, Analytics, Bot Shield, Theme
// Studio and Theme Store are consolidated from their sidebar group into ONE pinned
// tab that presents them as premium cards — the same dashboard-hub pattern used
// for content, Growth and Operations — so the VayuOS sidebar stays minimal. The
// products (VayuMail, VayuTalk, VayuTor) are NOT part of this hub; they stay
// pinned in the sidebar since they are opened often. Each card is gated to the
// viewer's access level, exactly like the sidebar it replaced, so an editor never
// sees the admin-only Bot Shield card.

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

// osOptimizeGrid builds the Optimize hub body. Each card is shown only when the
// viewer's access level can actually reach it (osPathMinLevel), mirroring the
// sidebar gate — so the hub can never advertise a page the viewer can't open.
func osOptimizeGrid(level int) string {
	card := func(href, title, desc, icon string, accent bool) string {
		if level < osPathMinLevel(href) {
			return ""
		}
		return osWorkCard(href, title, desc, icon, 0, "", accent)
	}

	var b strings.Builder
	b.WriteString(`<div class="page-head"><div><h1 class="page-title">Optimize</h1><p class="page-sub">Reach, protect and polish your site.</p></div></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(card("/os/seo", "SEO", "Search visibility & metadata", iconSEO, true))
	b.WriteString(card("/os/analytics", "Analytics", "Traffic & audience insights", iconAnalytics, false))
	b.WriteString(card("/os/shield", "Bot Shield", "Bot & abuse protection", iconSecurity, false))
	b.WriteString(card("/os/theme", "Theme Studio", "Design & customise your theme", iconTheme, false))
	b.WriteString(card("/os/theme/store", "Theme Store", "Browse & install themes", iconThemeStore, false))
	b.WriteString(`</div>`)
	return b.String()
}
