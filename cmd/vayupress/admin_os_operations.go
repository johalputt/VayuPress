package main

// admin_os_operations.go — the "Operations" hub. The advanced ops/governance
// tools (System Modes, Policy Inspector, Topology, Replay Explorer, Fault Engine,
// ADR Registry) are consolidated from their sidebar group into ONE pinned tab
// that presents them as premium cards — the same dashboard-hub pattern used for
// content and Growth — so the VayuOS sidebar stays minimal and clean. These are
// clearnet-only infrastructure controls (no Tor-world counterpart); every card
// links to its existing page, only the navigation moved.

import (
	htmpl "html/template"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
)

// handleOSOperations renders the Operations hub. Admin-only — osPathMinLevel
// gates "/os/operations" to match the admin tools it fronts.
func (a *App) handleOSOperations(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	writeOSHTML(w, adminOSLayout(nonce, "Operations", "operations", cfg, htmpl.HTML(osOperationsGrid(mode.Global.Current()))))
}

// osOperationsGrid builds the Operations hub body: a header plus a card grid for
// the advanced ops tools. The System Modes card carries a status badge whenever
// the install is NOT in normal mode (read-only / degraded / quarantined), so a
// non-normal state is impossible to miss from the hub.
func osOperationsGrid(current mode.Mode) string {
	var b strings.Builder
	b.WriteString(`<div class="page-head"><div><h1 class="page-title">Operations</h1><p class="page-sub">Advanced controls, diagnostics and governance for your install.</p></div></div>`)
	b.WriteString(`<div class="work-grid">`)

	// Surface a non-normal system mode as an attention badge on the Modes card.
	modeBadge := ""
	if current != "" && current != mode.ModeNormal {
		modeBadge = string(current)
	}
	b.WriteString(osWorkCard("/os/modes", "System Modes", "Normal · read-only · quarantine", iconModes, 0, modeBadge, true))
	b.WriteString(osWorkCard("/os/policy", "Policy Inspector", "Effective policy & guardrails", iconPolicy, 0, "", false))
	b.WriteString(osWorkCard("/os/topology", "Topology", "Services & connections map", iconTopology, 0, "", false))
	b.WriteString(osWorkCard("/os/replay", "Replay Explorer", "Inspect & replay requests", iconReplay, 0, "", false))
	b.WriteString(osWorkCard("/os/faults", "Fault Engine", "Inject & observe failures", iconFaults, 0, "", false))
	b.WriteString(osWorkCard("/os/adr", "ADR Registry", "Architecture decisions", iconADR, 0, "", false))
	b.WriteString(`</div>`)
	return b.String()
}
