// SPDX-License-Identifier: Apache-2.0

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
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/settings"
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

// shieldTierReason returns the agent-recorded short failure reason for a tier
// (from tierN.reason), so the panel can show WHY a tier errored.
func shieldTierReason(tier int) string {
	b, err := os.ReadFile(filepath.Join(shieldControlDir(), "tier"+strconv.Itoa(tier)+".reason"))
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
	writeOSFragment(w, a.shieldHardeningBody(r))
}

// shieldCDNVendors maps a request header to the proxy that sets it. Any of these
// means a CDN terminated the visitor's connection, so what reaches this server is
// the CDN's address rather than the reader's.
//
// X-Forwarded-For is deliberately NOT in this list. A local nginx reverse proxy
// sets it on every request, so treating it as a CDN signal would report a CDN in
// front of every normal install — the opposite of the honesty this is for.
var shieldCDNVendors = []struct{ header, vendor string }{
	{"CF-Ray", "Cloudflare"},
	{"CF-Connecting-IP", "Cloudflare"},
	{"X-Amz-Cf-Id", "CloudFront"},
	{"Fastly-Client-IP", "Fastly"},
	{"X-Azure-Ref", "Azure Front Door"},
	{"True-Client-IP", "a proxy"},
	{"CDN-Loop", "a proxy"}, // RFC 8586 — the standard, vendor-neutral marker
}

// shieldDetectCDN reports whether this request arrived through a CDN proxy, and
// which one. It reads the live request rather than a stored setting, because the
// setting records what the operator BELIEVES and this has to report what is
// actually happening.
func shieldDetectCDN(r *http.Request) (bool, string) {
	if r == nil {
		return false, ""
	}
	for _, v := range shieldCDNVendors {
		if strings.TrimSpace(r.Header.Get(v.header)) != "" {
			return true, v.vendor
		}
	}
	return false, ""
}

// shieldHardeningBody renders the Network-hardening section body: live Tier 2/3
// state with real toggles when the root agent is installed, or a clear
// explanation + copy-paste fallback when it is not yet.
//
// It takes the request so it can report the CDN situation truthfully. The copy
// here used to assert, unconditionally, that the origin served visitors directly
// with no proxy in front — on a proxied install that is simply false, and it is
// false in the most misleading direction, because it tells the operator the
// per-IP limits they just enabled are "fully effective" when those limits are in
// fact measuring a handful of CDN edge addresses.
func (a *App) shieldHardeningBody(r *http.Request) string {
	var b strings.Builder
	b.WriteString(vsRefresh("hardening", "vs-body-hardening", ""))
	b.WriteString(`<p class="muted text-sm">The toggles above are <strong>Tier 1</strong> — VayuShield's in-binary defenses. <strong>Tier 2</strong> (kernel firewall) and <strong>Tier 3</strong> (nginx edge) sit below and in front of the app; they drop abuse before it reaches VayuPress, so they <strong>improve</strong> performance under attack rather than degrade it, with no cost to legitimate visitors.</p>`)

	if shieldAgentAlive() {
		b.WriteString(`<p class="muted text-xs">✅ Privileged helper installed — you can switch these on and off right here, no terminal needed. VayuPress itself stays unprivileged; a separate root agent applies only the vetted scripts.</p>`)
		b.WriteString(shieldTierRow(2, "🛡️ Tier 2 · Kernel firewall (nftables)", "Per-IP connection/packet rate limits + SYN-flood cookies, enforced in the Linux kernel. Turning this on also activates the L1 live offload below."))
		b.WriteString(shieldTierRow(3, "🌐 Tier 3 · Edge shaping (nginx)", "Per-IP request/connection shaping + slow-loris timeouts at the reverse proxy."))
		b.WriteString(shieldOffloadRow())
		b.WriteString(a.shieldCDNAdvisory(r))
		b.WriteString(`<p class="muted text-xs">Both tiers are fully reversible from here.</p>`)
	} else {
		b.WriteString(`<div class="vs-tier"><div class="vs-tier-head">One-time setup — enable the in-panel switches</div>`)
		b.WriteString(`<p class="muted text-sm">A true in-panel toggle needs a tiny <strong>root helper</strong> installed once. The <strong>in-app one-click updater cannot install it</strong> — that updater is unprivileged by design (which is exactly what keeps VayuPress safe). Install it with <strong>one command as root</strong> from your VayuPress checkout, then this section turns into on/off switches (no terminal afterwards):</p>`)
		b.WriteString(`<div class="vs-cmd"><code id="vs-cmd-agent">cd /path/to/VayuPress &amp;&amp; git pull &amp;&amp; sudo bash deploy/vayushield-agent.sh install</code><button type="button" class="vs-copy-btn" data-copy="vs-cmd-agent">Copy</button></div>`)
		b.WriteString(`<p class="muted text-xs">Your checkout is usually <code>/tmp/VayuPress</code> (find it: <code>sudo find / -name vayushield-agent.sh -path '*/deploy/*'</code>). Running your normal shell updater (<code>scripts/update-vayupress.sh</code>) installs it too. Undo any time: <code>sudo bash deploy/vayushield-agent.sh uninstall</code>.</p>`)
		b.WriteString(`<p class="muted text-sm">Prefer to apply Tier 2/3 by hand instead? (idempotent &amp; reversible)</p>`)
		b.WriteString(`<div class="vs-cmd"><code id="vs-cmd-t2">cd /path/to/VayuPress &amp;&amp; sudo bash deploy/vayushield-firewall.sh apply</code><button type="button" class="vs-copy-btn" data-copy="vs-cmd-t2">Copy</button></div>`)
		b.WriteString(`<div class="vs-cmd"><code id="vs-cmd-t3">sudo cp deploy/nginx-vayushield.conf /etc/nginx/conf.d/ &amp;&amp; sudo nginx -t &amp;&amp; sudo systemctl reload nginx</code><button type="button" class="vs-copy-btn" data-copy="vs-cmd-t3">Copy</button></div>`)
		b.WriteString(`<p class="muted text-xs">Undo: <code>… vayushield-firewall.sh remove</code>; delete the nginx conf + reload.</p></div>`)
	}
	b.WriteString(`<p class="muted text-sm">A true volumetric flood still needs anycast/scrubbing capacity no single host provides; Tiers 1–3 handle what a typical publisher actually faces.</p>`)
	return b.String()
}

// shieldCDNAdvisory states what is actually in front of this origin and what
// that means for each tier, because the three tiers do NOT behave the same way
// behind a proxy and conflating them is what leaves an operator throttling their
// own CDN:
//
//   - Tier 1 (in-binary) reads the visitor from CF-Connecting-IP once the
//     "Behind Cloudflare / a CDN" switch is on. Only genuine edge addresses are
//     trusted, so the header cannot be spoofed.
//   - Tier 3 (nginx) needs set_real_ip_from + real_ip_header to see the visitor.
//   - Tier 2 (nftables) can NEVER see the visitor. It runs in the kernel, before
//     any HTTP header exists, so its per-IP limits key on the proxy's addresses
//     no matter what any setting says. The only fix is to allowlist the edge
//     ranges — which is why this is called out separately rather than folded in
//     with the other two.
func (a *App) shieldCDNAdvisory(r *http.Request) string {
	viaCDN, vendor := shieldDetectCDN(r)
	if !viaCDN {
		return `<p class="muted text-xs">No proxy detected in front of this origin on your own request, so Tier 2 and Tier 3 per-IP limits apply to real visitor addresses and are fully effective. If you later put a CDN in front, come back here — this notice updates on its own, and the per-IP limits would then need adjusting.</p>`
	}

	trusted := false
	if a.siteSettings != nil {
		trusted = a.siteSettings.Get(r.Context(), settings.KeyShieldBehindCDN) == "on"
	}

	var b strings.Builder
	b.WriteString(`<p class="muted text-xs"><strong>` + html.EscapeString(vendor) +
		` is proxying this origin</strong> — detected from the headers on this very request. That changes what the tiers below can see.</p>`)

	if !trusted {
		b.WriteString(`<p class="muted text-xs">⚠️ <strong>“Behind Cloudflare / a CDN” is switched off above.</strong> VayuShield is therefore treating each proxy edge address as if it were one visitor, so your whole audience looks like a handful of IPs — which trips the rate limit and can show everyone a challenge page. Turn that switch on: it reads the real visitor from the proxy's header, and only genuine edge addresses are trusted, so it cannot be spoofed.</p>`)
	} else {
		b.WriteString(`<p class="muted text-xs">✅ Tier 1 is reading the real visitor address from the proxy's header, so in-binary rate limiting applies per reader rather than per edge node.</p>`)
	}

	b.WriteString(`<p class="muted text-xs"><strong>Tier 2 is different, and no switch fixes it.</strong> The kernel firewall runs before any HTTP header exists, so its per-IP limits always see the proxy's addresses — a busy edge node easily exceeds a per-visitor connection cap and gets dropped, which reads as intermittent failures you cannot reproduce. Allowlist your proxy's ranges before enabling Tier 2, or leave Tier 2 for origins that take traffic directly (a subdomain that is not proxied, for instance).</p>`)
	b.WriteString(`<p class="muted text-xs"><strong>Tier 3</strong> needs <code>set_real_ip_from</code> for your proxy's ranges plus <code>real_ip_header</code>, or nginx's per-IP shaping keys on the edge too. The SYN-flood and slow-loris protections in both tiers are unaffected either way — those do not depend on identifying the visitor.</p>`)
	return b.String()
}

// shieldOffloadStatus returns the agent-reported L1 offload state ("active",
// "inactive", "error" or "") and the current in-kernel ban count ("0" when
// unknown), for the hardening row and the Aegis layer map.
func shieldOffloadStatus() (state, count string) {
	if b, err := os.ReadFile(filepath.Join(shieldControlDir(), "offload.state")); err == nil {
		state = strings.TrimSpace(string(b))
	}
	count = "0"
	if b, err := os.ReadFile(filepath.Join(shieldControlDir(), "offload.count")); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			count = v
		}
	}
	return state, count
}

// shieldOffloadRow renders the L1 dynamic-offload status line: whether the
// agent is enforcing the shield's live jail verdicts in-kernel, and how many
// IPs are currently banned there. Read-only — the offload follows Tier 2
// automatically (on when Tier 2 is on), so there is nothing to configure.
func shieldOffloadRow() string {
	state, count := shieldOffloadStatus()
	var pill string
	switch state {
	case "active":
		pill = `<span class="vs-hard-state is-on">● Enforcing — ` + html.EscapeString(count) + ` IP(s) banned in-kernel</span>`
	case "error":
		reason := ""
		if b, err := os.ReadFile(filepath.Join(shieldControlDir(), "offload.reason")); err == nil {
			reason = strings.TrimSpace(string(b))
		}
		if len(reason) > 160 {
			reason = reason[:160] + "…"
		}
		pill = `<span class="vs-hard-state is-err">✕ ` + html.EscapeString(reason) + `</span>`
	default:
		pill = `<span class="vs-hard-state is-off">○ Follows Tier 2 — turns on with it</span>`
	}
	return `<div class="vs-tier"><div class="vs-hard-row"><div><div class="vs-tier-head">⚡ L1 · Live kernel offload (Aegis)</div><p class="muted text-sm">VayuShield's own jail verdicts (confirmed bad actors, reputation sentences) are pushed into a kernel nftables timeout-set — and an XDP filter where available — so a banned attacker's packets are dropped before a connection even exists. Fully automatic; bans expire on their own.</p></div><div class="vs-hard-ctl">` + pill + `</div></div></div>`
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
		reason := shieldTierReason(tier)
		label := "Error — check the agent log"
		if reason != "" {
			if len(reason) > 160 {
				reason = reason[:160] + "…"
			}
			label = "Error: " + reason
		}
		pill = `<span class="vs-hard-state is-err">✕ ` + html.EscapeString(label) + `</span>`
		if wanted {
			btn = shieldTierBtn(tier, "disable", "Turn off", "btn--sm") + ` ` + shieldTierBtn(tier, "enable", "Retry", "btn--sm")
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
