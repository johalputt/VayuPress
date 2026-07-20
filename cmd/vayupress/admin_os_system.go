package main

// admin_os_system.go — the "System" hub. Storage & System, Settings and My
// Profile are consolidated from the sidebar into ONE pinned tab of premium cards —
// the same dashboard-hub pattern used everywhere else. It exists mainly to give
// the Tor-world console the identical minimal hub treatment the clearnet console
// uses (design parity, ADR-0141); the clearnet sidebar folds these surfaces into
// the Optimize hub instead, so it does not link here. Each card is gated to the
// viewer's access level, mirroring the sidebar it replaced.

import (
	htmpl "html/template"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/render"
)

// handleOSSystem renders the System hub. Author-gated at the route level; each
// card is gated to what the viewer can actually reach.
func (a *App) handleOSSystem(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	writeOSHTML(w, adminOSLayout(nonce, "System", "system", cfg, htmpl.HTML(osSystemGrid(cfg.AccessLevel))))
}

// osSystemGrid builds the System hub body. A card appears only when the viewer's
// access level can reach it (osPathMinLevel), so the hub never advertises a page
// the viewer cannot open.
func osSystemGrid(level int) string {
	card := func(href, title, desc, icon string, accent bool) string {
		if level < osPathMinLevel(href) {
			return ""
		}
		return osWorkCard(href, title, desc, icon, 0, "", accent)
	}
	var b strings.Builder
	b.WriteString(`<div class="page-head"><div><h1 class="page-title">System</h1><p class="page-sub">Storage, settings and your account — in one place.</p></div></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(card("/os/storage", "Storage & System", "Disk, backups & runtime", iconStorage, true))
	b.WriteString(card("/os/settings", "Settings", "Site & install settings", iconSettings, false))
	b.WriteString(card("/os/profile", "My Profile", "Your account & password", iconMembers, false))
	b.WriteString(`</div>`)
	return b.String()
}
