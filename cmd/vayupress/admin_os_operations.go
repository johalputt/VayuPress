package main

// admin_os_operations.go — the "Operations" hub. The advanced ops/governance
// tools are consolidated from their sidebar groups into ONE pinned tab that
// presents them as premium cards — the same dashboard-hub pattern used for
// content, Growth and Optimize — so the VayuOS sidebar stays minimal and clean.
// Two rows:
//   - Controls & diagnostics: System Modes, Policy Inspector, Topology, Replay
//     Explorer, Fault Engine, ADR Registry.
//   - Health & governance: Monitoring, Governance, Storage & System, Security.
// These are clearnet-only infrastructure controls (no Tor-world counterpart);
// every card links to its existing page, only the navigation moved.

import (
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
)

// handleOSOperations renders the Operations hub. Admin-only — osPathMinLevel
// gates "/os/operations" to match the admin tools it fronts.
func (a *App) handleOSOperations(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	snap := a.getAdminSnapshot()
	writeOSHTML(w, adminOSLayout(nonce, "Operations", "operations", cfg,
		htmpl.HTML(osOperationsGrid(mode.Global.Current(), int(snap.StoragePct)))))
}

// osOperationsGrid builds the Operations hub body. The System Modes card carries a
// status badge whenever the install is not in normal mode, and Storage & System
// carries a usage badge when disk is running high (>= 75%), so a state that needs
// attention is impossible to miss from the hub.
func osOperationsGrid(current mode.Mode, storagePct int) string {
	var b strings.Builder
	b.WriteString(`<div class="page-head"><div><h1 class="page-title">Operations</h1><p class="page-sub">Advanced controls, diagnostics and governance for your install.</p></div></div>`)

	// Controls & diagnostics.
	b.WriteString(`<div class="section-head"><span class="section-head__title">Controls &amp; diagnostics</span><span class="section-head__hint">Run, inspect &amp; recover</span></div>`)
	b.WriteString(`<div class="work-grid">`)
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

	// Health & governance.
	b.WriteString(`<div class="section-head"><span class="section-head__title">Health &amp; governance</span><span class="section-head__hint">Watch, protect &amp; maintain</span></div>`)
	b.WriteString(`<div class="work-grid">`)
	b.WriteString(osWorkCard("/os/monitoring", "Monitoring", "Live metrics & health", iconMonitoring, 0, "", false))
	b.WriteString(osWorkCard("/os/governance", "Governance", "Policies & audit trail", iconGovernance, 0, "", false))
	storBadge := ""
	if storagePct >= 75 {
		storBadge = strconv.Itoa(storagePct) + "%"
	}
	b.WriteString(osWorkCard("/os/storage", "Storage & System", "Disk, backups & runtime", iconStorage, 0, storBadge, false))
	b.WriteString(osWorkCard("/os/security", "Security", "Sessions, 2FA & hardening", iconSecurity, 0, "", false))
	b.WriteString(`</div>`)
	return b.String()
}
