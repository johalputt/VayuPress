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
	"errors"
	"html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
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

// shieldCDNAllowVendors is the fixed set of proxies whose published ranges the
// agent knows how to fetch. The panel may only ever name one of these, and the
// agent independently re-checks the name against its own copy of the list — the
// web app is unprivileged and its output must never be able to widen what a root
// process will run.
var shieldCDNAllowVendors = map[string]bool{"cloudflare": true}

// shieldCDNAllowState returns the agent-reported state of the last allowlist
// fetch: "applying", "active", "error" or "" when it has never been asked.
func shieldCDNAllowState() string {
	b, err := os.ReadFile(filepath.Join(shieldControlDir(), "cdnallow.state"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// shieldRequestCDNAllow asks the root agent to populate the Tier 2 proxy
// allowlist. It writes an EMPTY file whose NAME carries the vendor, so no content
// the web app produces is ever read as a command or an argument.
func shieldRequestCDNAllow(vendor string) error {
	if !shieldCDNAllowVendors[vendor] {
		return errors.New("unknown proxy vendor")
	}
	f, err := os.OpenFile(filepath.Join(shieldControlDir(), "cdnallow."+vendor+".want"),
		os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	return f.Close()
}

// handleOSShieldCDNAllow is the admin/CSRF-gated endpoint behind the "Allowlist
// the edge ranges" button.
func (a *App) handleOSShieldCDNAllow(w http.ResponseWriter, r *http.Request) {
	vendor := strings.ToLower(strings.TrimSpace(r.PostFormValue("vendor")))
	if !shieldCDNAllowVendors[vendor] {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown proxy vendor", "")
		return
	}
	if err := shieldRequestCDNAllow(vendor); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "control-error", "Could not record the request: "+err.Error(), "")
		return
	}
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

// cdnSeenUnix / cdnSeenVendor record the last time ANY request arrived through a
// proxy. This is the signal that answers the question the panel actually needs
// answered — "is this SITE proxied?" — as opposed to "did the request I am
// currently serving come through a proxy?", which is all the headers can tell us.
//
// The two differ precisely when an administrator reaches the console another way,
// and that is not an edge case: pointing a hosts entry at the origin so the panel
// stays reachable when the CDN is having a bad day is ordinary practice. The one
// person guaranteed to read this notice is therefore the one most likely to be
// bypassing the edge, which is how the first version of this came to tell a
// Cloudflare-proxied site that nothing was in front of it.
var (
	cdnSeenUnix   atomic.Int64
	cdnSeenVendor atomic.Value // string
)

// cdnObservationTTL bounds how long an observation stays meaningful. Long enough
// that a quiet site does not forget between visits to the panel, short enough
// that turning a proxy off is noticed within a day.
const cdnObservationTTL = 24 * time.Hour

// noteCDNObservation records a proxy sighting from ordinary traffic. Called from
// the request path, so it is written to be nearly free: once a sighting is fresh
// it does not look at headers again for two minutes.
//
// It deliberately does NOT verify that the header is genuine. Anyone connecting
// straight to the origin could forge one and make the panel believe a proxy is in
// front. The consequence is that the panel shows the Tier 2 kernel warning to
// someone who does not need it — a paragraph of unnecessary advice. The opposite
// error hides that warning from someone whose firewall is silently dropping their
// CDN's connections. Erring toward showing it is the right direction, so the
// cheap check stands.
func noteCDNObservation(r *http.Request) {
	if time.Since(time.Unix(cdnSeenUnix.Load(), 0)) < 2*time.Minute {
		return
	}
	if ok, vendor := shieldDetectCDN(r); ok {
		cdnSeenVendor.Store(vendor)
		cdnSeenUnix.Store(time.Now().Unix())
	}
}

// lastCDNObservation returns the vendor seen on recent ordinary traffic, or "" if
// none has been seen inside the TTL.
func lastCDNObservation() string {
	at := cdnSeenUnix.Load()
	if at == 0 || time.Since(time.Unix(at, 0)) > cdnObservationTTL {
		return ""
	}
	v, _ := cdnSeenVendor.Load().(string)
	return v
}

// shieldDetectCDN reports whether THIS request arrived through a CDN proxy, and
// which one.
//
// Read the asymmetry carefully: a header present is proof a proxy is in front; a
// header absent proves nothing at all about the site, only about this one
// connection. Callers must never turn a false return into a positive claim that
// the origin is unproxied — see shieldCDNAdvisory, which combines this with the
// operator's setting and with sightings from real traffic.
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
	// Three independent signals, deliberately combined rather than ranked by
	// convenience. `here` is the strongest evidence FOR a proxy and no evidence
	// against one; `seen` is what actually describes the site; `declared` is the
	// operator telling us directly, and outranks a silent absence of the other two.
	here, hereVendor := shieldDetectCDN(r)
	seen := lastCDNObservation()
	declared := false
	if a.siteSettings != nil {
		declared = a.siteSettings.Get(r.Context(), settings.KeyShieldBehindCDN) == "on"
	}

	vendor := hereVendor
	if vendor == "" {
		vendor = seen
	}
	if vendor == "" {
		vendor = "a proxy"
	}

	// Nothing points at a proxy. Say only that — an absence of signal is not a
	// finding. The previous version turned it into "no proxy detected … limits
	// are fully effective", which is the reassurance that stops an operator
	// allowlisting the ranges their kernel is dropping.
	if !here && seen == "" && !declared {
		return `<p class="muted text-xs">No proxy signal has reached this server — not on your own request, and not on recent visitor traffic. If nothing is in front of this origin, Tier 2 and Tier 3 per-IP limits apply to real visitor addresses and are effective. Treat that as the absence of a signal rather than proof: put a CDN in front later and this notice updates on its own.</p>`
	}

	var b strings.Builder
	switch {
	case here:
		b.WriteString(`<p class="muted text-xs"><strong>` + html.EscapeString(vendor) +
			` is proxying this origin</strong> — detected from the headers on this very request. That changes what the tiers below can see.</p>`)
	case seen != "":
		// The case that produced this whole design. The site IS proxied — real
		// visitor traffic proves it — while the administrator reads the panel over
		// a connection that skips the edge, usually a hosts entry pointing at the
		// origin so the console stays reachable when the CDN is unwell. Naming the
		// cause here saves the hours it otherwise takes to work out why the panel
		// and the DNS disagree.
		b.WriteString(`<p class="muted text-xs"><strong>` + html.EscapeString(vendor) +
			` is proxying this site</strong> — seen on recent visitor traffic. <strong>Your own connection is not going through it</strong>, which normally means a <code>hosts</code> entry (or split-horizon DNS) pointing this domain at the origin address. That is a reasonable thing to have; it just means what you see here is not what your readers get.</p>`)
	default:
		b.WriteString(`<p class="muted text-xs">You have marked this site as being <strong>behind a proxy</strong>, but no proxy header has arrived — not on this request, nor on visitor traffic in the last day. Either the proxy is no longer in front, or the site is too quiet to have shown one yet. The guidance below assumes your setting is right, since acting on it costs a paragraph and ignoring it costs dropped traffic.</p>`)
	}

	if !declared {
		b.WriteString(`<p class="muted text-xs">⚠️ <strong>“Behind Cloudflare / a CDN” is switched off above.</strong> VayuShield is therefore treating each proxy edge address as if it were one visitor, so your whole audience looks like a handful of IPs — which trips the rate limit and can show everyone a challenge page. Turn that switch on: it reads the real visitor from the proxy's header, and only genuine edge addresses are trusted, so it cannot be spoofed.</p>`)
	} else if config.IsCloudflareIP(net.ParseIP(auth.ClientIP(r))) {
		// The switch is on, but the address this request resolves to is itself an
		// edge address — so the visitor was never recovered and every reader is
		// being counted as one of a handful of edge nodes. That is the pooling
		// failure the switch exists to prevent, and it is otherwise invisible:
		// nothing errors, the limits simply apply to the wrong subject.
		//
		// It happens when a local reverse proxy sits between the edge and
		// VayuPress. VayuPress will not read CF-Connecting-IP across that hop —
		// it cannot distinguish a header the edge set from one a visitor typed,
		// and trusting it there let any client choose its own identity. nginx can
		// tell, because it sees the real peer, so nginx has to do the resolving.
		b.WriteString(`<p class="muted text-xs">⚠️ <strong>The switch is on, but this request still resolves to an edge address</strong> — so your readers are being counted as a handful of proxy nodes, which is exactly what trips the rate limit for everyone. Your reverse proxy needs to recover the visitor before VayuPress sees the request: add <code>set_real_ip_from</code> for your proxy's ranges plus <code>real_ip_header</code> to nginx (the generator command is in <code>deploy/nginx-vayupress.conf</code>), then reload. VayuPress will not read the edge header across a local proxy hop itself, because at that point it cannot tell a header your CDN set from one a visitor typed.</p>`)
	} else {
		b.WriteString(`<p class="muted text-xs">✅ Tier 1 is reading the real visitor address, so in-binary rate limiting applies per reader rather than per edge node.</p>`)
	}

	b.WriteString(`<p class="muted text-xs"><strong>Tier 2 is different, and no switch fixes it.</strong> The kernel firewall runs before any HTTP header exists, so its per-IP limits always see the proxy's addresses — a busy edge node easily exceeds a per-visitor connection cap and gets dropped, which reads as intermittent failures you cannot reproduce. The fix is to allowlist the edge ranges, which also sharpens the firewall: anything arriving from outside them skipped the proxy and still meets the full ruleset.</p>`)
	b.WriteString(a.shieldCDNAllowRow(vendor))
	b.WriteString(`<p class="muted text-xs"><strong>Tier 3</strong> needs <code>set_real_ip_from</code> for your proxy's ranges plus <code>real_ip_header</code>, or nginx's per-IP shaping keys on the edge too. The SYN-flood and slow-loris protections in both tiers are unaffected either way — those do not depend on identifying the visitor.</p>`)
	return b.String()
}

// shieldCDNAllowRow renders the one-click allowlist control. Without the root
// agent there is nothing to click, so it degrades to the exact command instead of
// showing a button that would silently do nothing.
func (a *App) shieldCDNAllowRow(vendor string) string {
	// Only vendors the agent can actually fetch get a button. Anything else —
	// including a proxy detected only by the vendor-neutral CDN-Loop header — gets
	// the manual path, because pretending to support it would be worse than saying
	// so.
	key := strings.ToLower(strings.TrimSpace(vendor))
	if !shieldCDNAllowVendors[key] {
		return `<p class="muted text-xs">Write your proxy's published ranges to <code>/etc/vayushield/cdn-allow.conf</code>, one CIDR per line, then re-apply Tier 2.</p>`
	}
	if !shieldAgentAlive() {
		return `<div class="vs-cmd"><code id="vs-cmd-cdnallow">sudo bash deploy/vayushield-firewall.sh cdn-allow ` + html.EscapeString(key) +
			` &amp;&amp; sudo bash deploy/vayushield-firewall.sh apply</code><button type="button" class="vs-copy-btn" data-copy="vs-cmd-cdnallow">Copy</button></div>`
	}
	switch shieldCDNAllowState() {
	case "applying":
		return `<p class="muted text-xs"><span class="vs-hard-state is-work">◐ Fetching the edge ranges…</span></p>`
	case "active":
		return `<p class="muted text-xs"><span class="vs-hard-state is-on">● Edge ranges allowlisted</span> — re-run after your proxy publishes new ranges. <button type="button" class="btn btn--sm" hx-post="/os/api/shield/cdn-allow" hx-vals='{"vendor":"` + html.EscapeString(key) + `"}' hx-target="#vs-body-hardening" hx-swap="innerHTML">Refresh ranges</button></p>`
	case "error":
		return `<p class="muted text-xs"><span class="vs-hard-state is-err">✕ Could not fetch the edge ranges</span> — the previous allowlist, if any, is unchanged. <button type="button" class="btn btn--sm" hx-post="/os/api/shield/cdn-allow" hx-vals='{"vendor":"` + html.EscapeString(key) + `"}' hx-target="#vs-body-hardening" hx-swap="innerHTML">Retry</button></p>`
	}
	return `<p class="muted text-xs"><button type="button" class="btn btn--primary btn--sm" hx-post="/os/api/shield/cdn-allow" hx-vals='{"vendor":"` + html.EscapeString(key) + `"}' hx-target="#vs-body-hardening" hx-swap="innerHTML">Allowlist ` + html.EscapeString(vendor) + `'s edge ranges</button> Fetches the published ranges and re-applies Tier 2. Survives reboots — the agent re-applies on boot.</p>`
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
