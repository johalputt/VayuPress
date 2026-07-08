package main

// vayushield_hardening.go — safe, in-panel activation of the Tier 2 (kernel
// nftables) and Tier 3 (nginx edge) network-hardening layers, WITHOUT giving
// the web app any privilege.
//
// Privilege separation (see ADR-0123): VayuPress runs as an unprivileged service
// and deliberately cannot touch the kernel firewall or reload nginx. So the panel
// never executes anything privileged. It only expresses INTENT by creating or
// removing an empty flag file in a control directory it owns
// (<state>/vayushield-control/tierN.want). A separate root "reconcile agent"
// (deploy/vayushield-agent.sh, installed once by the updater) polls those flags
// and runs ONLY the fixed, vetted scripts — taking no argument or content from
// the web app, so there is no injection surface. The agent writes back a status
// file (tierN.state) and a heartbeat (agent.alive) that the panel reads to show
// live state and to know whether the helper is installed.

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// shieldControlDir returns the directory the panel and the root agent use to
// exchange intent/status files. It defaults beside the state dir and is created
// (owned by the service user) so the unprivileged app can write flags there; the
// root agent only ever reads/writes into this app-owned directory.
func shieldControlDir() string {
	dir := strings.TrimSpace(os.Getenv("VAYUSHIELD_CONTROL_DIR"))
	if dir == "" {
		dir = "/var/lib/vayupress/vayushield-control"
	}
	_ = os.MkdirAll(dir, 0o750)
	return dir
}

// shieldAgentAlive reports whether the root reconcile agent is installed and
// running, by checking the freshness of its heartbeat file (updated every poll).
func shieldAgentAlive() bool {
	fi, err := os.Stat(filepath.Join(shieldControlDir(), "agent.alive"))
	if err != nil {
		return false
	}
	// The agent rewrites the heartbeat every few seconds; treat >45s as dead.
	return time.Since(fi.ModTime()) < 45*time.Second
}

// shieldTierWanted reports whether the operator has requested a tier ON (the
// intent flag exists).
func shieldTierWanted(tier int) bool {
	_, err := os.Stat(filepath.Join(shieldControlDir(), "tier"+strconv.Itoa(tier)+".want"))
	return err == nil
}

// shieldTierState returns the agent-reported state for a tier: "active",
// "inactive", "applying", "removing", "error", or "" (unknown / no agent).
func shieldTierState(tier int) string {
	b, err := os.ReadFile(filepath.Join(shieldControlDir(), "tier"+strconv.Itoa(tier)+".state"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// shieldSetTierWant creates or removes the intent flag for a tier. This is the
// ONLY privileged-ish action the panel performs — and it is not privileged at
// all: it writes/removes an empty file the service user owns. The root agent
// does the actual apply/remove.
func shieldSetTierWant(tier int, want bool) error {
	flag := filepath.Join(shieldControlDir(), "tier"+strconv.Itoa(tier)+".want")
	if want {
		f, err := os.OpenFile(flag, os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.Remove(flag); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// handleOSShieldTier is the admin/CSRF-gated endpoint behind the Tier 2/3
// toggles. It records intent (a flag file) and returns the refreshed hardening
// section so the panel updates in place; the root agent applies the change
// within its next poll and the section auto-refreshes to show the new state.
func (a *App) handleOSShieldTier(w http.ResponseWriter, r *http.Request) {
	tier, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("tier")))
	if tier != 2 && tier != 3 {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "tier must be 2 or 3", "")
		return
	}
	action := strings.TrimSpace(r.PostFormValue("action"))
	if action != "enable" && action != "disable" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "action must be enable or disable", "")
		return
	}
	if err := shieldSetTierWant(tier, action == "enable"); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "control-error", "Could not record the request: "+err.Error(), "")
		return
	}
	// Return the refreshed section so the toggle updates in place (HTMX swaps the
	// body); the section also self-polls so applying → active appears on its own.
	writeOSFragment(w, a.shieldHardeningBody())
}

// shieldHardeningBody renders the Network-hardening section body: live Tier 2/3
// state with real toggles when the root agent is installed, or a clear
// explanation + copy-paste fallback when it is not yet.
func (a *App) shieldHardeningBody() string {
	var b strings.Builder
	b.WriteString(vsRefresh("hardening", "vs-body-hardening", ""))
	b.WriteString(`<p class="muted text-sm">The toggles above are <strong>Tier 1</strong> — VayuShield's in-binary defenses. <strong>Tier 2</strong> (kernel firewall) and <strong>Tier 3</strong> (nginx edge) sit below and in front of the app; they drop abuse before it reaches VayuPress, so they <strong>improve</strong> performance under attack rather than degrade it, with no cost to legitimate visitors.</p>`)

	if shieldAgentAlive() {
		b.WriteString(`<p class="muted text-xs">✅ Privileged helper installed — you can switch these on and off right here, no terminal needed. VayuPress itself stays unprivileged; a separate root agent applies only the vetted scripts.</p>`)
		b.WriteString(shieldTierRow(2, "🛡️ Tier 2 · Kernel firewall (nftables)", "Per-IP connection/packet rate limits + SYN-flood cookies, enforced in the Linux kernel."))
		b.WriteString(shieldTierRow(3, "🌐 Tier 3 · Edge shaping (nginx)", "Per-IP request/connection shaping + slow-loris timeouts at the reverse proxy."))
		b.WriteString(`<p class="muted text-xs">Your origin serves visitors directly (no CDN proxy in front), so Tier 2's per-IP limits apply to real client IPs and are fully effective. (If you ever put a CDN/proxy in front, per-IP limits would then see the CDN's IPs — the SYN/edge hardening stays safe either way.) Both tiers are fully reversible from here.</p>`)
	} else {
		b.WriteString(`<div class="vs-tier"><div class="vs-tier-head">One-time setup pending</div>`)
		b.WriteString(`<p class="muted text-sm">A true in-panel switch needs a tiny <strong>root helper</strong> installed once — it is installed <strong>automatically the next time you update</strong> VayuPress (the updater runs with the privileges required; the web app never gains them). After that, Tier 2/3 become on/off switches right here, with no terminal.</p>`)
		b.WriteString(`<p class="muted text-sm">Until then you can apply them manually (idempotent &amp; reversible):</p>`)
		b.WriteString(`<div class="vs-cmd"><code id="vs-cmd-t2">cd /path/to/VayuPress &amp;&amp; sudo bash deploy/vayushield-firewall.sh apply</code><button type="button" class="vs-copy-btn" data-copy="vs-cmd-t2">Copy</button></div>`)
		b.WriteString(`<div class="vs-cmd"><code id="vs-cmd-t3">sudo cp deploy/nginx-vayushield.conf /etc/nginx/conf.d/ &amp;&amp; sudo nginx -t &amp;&amp; sudo systemctl reload nginx</code><button type="button" class="vs-copy-btn" data-copy="vs-cmd-t3">Copy</button></div>`)
		b.WriteString(`<p class="muted text-xs">Undo: <code>… vayushield-firewall.sh remove</code>; delete the nginx conf + reload.</p></div>`)
	}
	b.WriteString(`<p class="muted text-sm">A true volumetric flood still needs anycast/scrubbing capacity no single host provides; Tiers 1–3 handle what a typical publisher actually faces.</p>`)
	return b.String()
}

// shieldTierRow renders one tier's status pill + enable/disable toggle button.
func shieldTierRow(tier int, title, desc string) string {
	state := shieldTierState(tier)
	wanted := shieldTierWanted(tier)
	var pill, btn string
	switch state {
	case "active":
		pill = `<span class="vs-hard-state is-on">● Active</span>`
		btn = shieldTierBtn(tier, "disable", "Turn off", "btn--sm")
	case "applying":
		pill = `<span class="vs-hard-state is-work">◐ Applying…</span>`
		btn = shieldTierBtn(tier, "disable", "Cancel", "btn--sm")
	case "removing":
		pill = `<span class="vs-hard-state is-work">◐ Turning off…</span>`
		btn = ""
	case "error":
		pill = `<span class="vs-hard-state is-err">✕ Error — check the agent log</span>`
		if wanted {
			btn = shieldTierBtn(tier, "disable", "Turn off", "btn--sm")
		} else {
			btn = shieldTierBtn(tier, "enable", "Retry", "btn--sm")
		}
	default: // inactive / unknown
		if wanted {
			pill = `<span class="vs-hard-state is-work">◐ Requested…</span>`
			btn = shieldTierBtn(tier, "disable", "Cancel", "btn--sm")
		} else {
			pill = `<span class="vs-hard-state is-off">○ Inactive</span>`
			btn = shieldTierBtn(tier, "enable", "Turn on", "btn--sm btn--primary")
		}
	}
	return `<div class="vs-tier"><div class="vs-hard-row"><div><div class="vs-tier-head">` + title + `</div><p class="muted text-sm">` + desc + `</p></div><div class="vs-hard-ctl">` + pill + btn + `</div></div></div>`
}

// shieldTierBtn builds an HTMX toggle button that posts the intent and swaps the
// refreshed hardening section in place.
func shieldTierBtn(tier int, action, label, cls string) string {
	return `<button type="button" class="btn ` + cls + `" hx-post="/os/api/shield/tier" hx-vals='{"tier":"` + strconv.Itoa(tier) + `","action":"` + action + `"}' hx-target="#vs-body-hardening" hx-swap="innerHTML">` + label + `</button>`
}
