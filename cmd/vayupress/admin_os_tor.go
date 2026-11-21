// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_tor.go — the VayuTor admin surface: a one-click switch that publishes
// every hosted domain as a Tor v3 onion service alongside its clearnet URL, the
// list of live .onion addresses, and a single count-only visitor tally (no
// identifier, time, path, or any other datum — the entire analytic). Strict CSP:
// server-rendered HTML, no inline styles; the tiny island (copy + poll + toggle)
// is a nonce'd same-origin script.

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayuos/torspace"
	vtor "github.com/johalputt/vayupress/internal/vayuos/vayutor"
)

// aptTorFallbackCmd is the one-time, run-once-as-root install of a current Tor
// from Tor Project's official repository — the guaranteed fallback for a host so
// old that even VayuTor's downloaded static build won't run on its system libc.
// After this, the system tor is current and self-updating, and VayuTor uses it
// automatically (no VayuPress restart needed).
const aptTorFallbackCmd = `sudo apt-get install -y apt-transport-https gpg wget
wget -qO- https://deb.torproject.org/torproject.org/A3C4F0F979CAA22CDBA8F512EE8CBC9E886DDD89.asc \
  | gpg --dearmor | sudo tee /usr/share/keyrings/tor.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/tor.gpg] https://deb.torproject.org/torproject.org $(lsb_release -cs) main" \
  | sudo tee /etc/apt/sources.list.d/tor.list
sudo apt-get update && sudo apt-get install -y tor`

// torOnCommand makes Tor available where VAYUOS_TOR=off took it away: the line
// goes from the env file the installer writes, and the service restarts to
// read it.
const torOnCommand = "sudo sed -i '/^VAYUOS_TOR=/d' /etc/vayupress/env && sudo systemctl restart vayupress"

// torOnionLocation is the header that tells Tor Browser a page's onion, and
// torBridgeExample the shape of a bridge line: both are code, in every
// language.
const (
	torOnionLocation = "Onion-Location"
	torBridgeExample = "obfs4 1.2.3.4:443 FINGERPRINT cert=... iat-mode=0"
)

// torCode is a translated sentence, escaped, with each htmlMark(i) in it
// drawn as code[i]: a command, a path or an address is the same in every
// language.
func torCode(sentence string, code ...string) string {
	pieces := make([]string, len(code))
	for i, c := range code {
		pieces[i] = "<code>" + esc(c) + "</code>"
	}
	return escFill(sentence, pieces...)
}

// torBold is a sentence, escaped and in bold: the lead of a paragraph.
func torBold(sentence string) string { return "<strong>" + esc(sentence) + "</strong>" }

// torSetup is the Tor page until onion services are on. Three reasons they
// are not: this is the Tor world's own instance, which the Clearnet console
// runs; VAYUOS_TOR=off; or they are simply off, and one button turns them on.
// The page used to blame VAYUOS_TOR=off for all three, and showed a strip of
// zero figures above the button in the last.
func torSetup(lang, nonce string, available bool) string {
	what := i18n.T(lang, "Every site you host, also as a .onion beside its normal address: a way in that no provider, network or observer can trace to a visitor.")
	if torspace.IsSpaceChild() {
		return string(ui.Setup(ui.SetupPage{Icon: "tor", Title: i18n.T(lang, "Onion services run from Clearnet"), What: what,
			Steps:  []ui.SetupStep{{Title: i18n.T(lang, "Clearnet's console"), Detail: i18n.T(lang, "This is the Tor world. The onion services that serve it are turned on, and looked after, from the Clearnet console.")}},
			Action: ui.HTML(`<a class="btn btn--primary" href="/os/world?target=clearnet">` + esc(i18n.T(lang, "Switch to Clearnet")) + `</a>`)}))
	}
	if !available {
		return string(ui.Setup(ui.SetupPage{Icon: "tor", Title: i18n.T(lang, "Tor is turned off"), What: what,
			Steps: []ui.SetupStep{{Title: i18n.T(lang, "Tor available to this install"),
				Detail: i18n.T(lang, "{setting} in {file} takes it away. Removing the line and restarting brings it back:", "setting", "VAYUOS_TOR=off", "file", "/etc/vayupress/env"), Command: torOnCommand}},
			Action: copyCommandButton(i18n.T(lang, "Copy the command"), torOnCommand)}))
	}
	return string(ui.Setup(ui.SetupPage{Icon: "tor", Title: i18n.T(lang, "Onion services are off"), What: what, DoneWord: i18n.T(lang, "done"),
		Steps: []ui.SetupStep{
			{Title: i18n.T(lang, "Tor available"), Done: true, Detail: i18n.T(lang, "VayuPress runs its own Tor, and fetches a current one when the server's is too old.")},
			{Title: i18n.T(lang, "Onion services on"), Detail: i18n.T(lang, "One .onion for each site you host, reachable within a few minutes. Tor Browser finds it on its own. If this server's network blocks Tor, the page says so and bridges get round it.")},
		},
		// data-tor starts the page's script, which fills the form's CSRF field
		// from the live cookie as it is sent.
		Action: ui.HTML(`<form method="post" action="/os/tor/toggle" data-tor data-tor-form><input type="hidden" name="state" value="on"><input type="hidden" name="csrf_token" value=""><button type="submit" class="btn btn--primary">` + esc(i18n.T(lang, "Turn on onion services")) + `</button></form>` +
			`<script nonce="` + nonce + `" src="/os/static/js/admin-os-tor.js?v=` + assetVer("js/admin-os-tor.js") + `"></script>`)}))
}

// handleOSTor renders the VayuTor control page.
func (a *App) handleOSTor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	lang := i18n.Lang(r.Context())

	var st vtor.Status
	if a.vayuTor != nil {
		st = a.vayuTor.Snapshot()
	}

	if a.vayuTor == nil || !st.Available || !st.Active {
		writeOSHTML(w, r, adminOSLayout(nonce, "VayuTor", "tor", cfg, htmpl.HTML(torSetup(lang, nonce, a.vayuTor != nil && st.Available))))
		return
	}

	body := `<div class="page-header"><h1>VayuTor</h1></div>`
	body += `<p class="page-sub">` + esc(i18n.T(lang, "Publish every hosted domain as a Tor onion service: a private, untraceable way in that works beside the normal address. No provider, network or observer can see who visits.")) + `</p>`

	// ── Status hero + one-click toggle. Onion services are on from here: off,
	// the page is torSetup. ──
	stateClass, stateLabel := "vt-state--warn", i18n.T(lang, "Activating…")
	switch {
	case st.Connected && st.BootstrapPct >= 100:
		stateClass, stateLabel = "vt-state--on", i18n.T(lang, "Active")
	case st.Connected:
		// Control port is up and onions are registered, but tor is still joining
		// the Tor network — onions are not reachable until this hits 100%.
		stateClass, stateLabel = "vt-state--warn", i18n.T(lang, "Connecting to Tor ({percent}%)", "percent", st.BootstrapPct)
	}
	btnLabel, btnKind, nextState := i18n.T(lang, "Deactivate"), "btn--ghost", "off"

	// Premium overview: a stat grid (like Monetization), then a slim control card.
	shortState := i18n.T(lang, "Starting")
	switch {
	case st.Connected && st.BootstrapPct >= 100:
		shortState = i18n.T(lang, "Active")
	case st.Connected:
		shortState = strconv.Itoa(st.BootstrapPct) + "%"
	}
	netLabel := i18n.T(lang, "Direct")
	switch {
	case st.TorManaged:
		netLabel = i18n.T(lang, "Managed Tor")
	case st.Transport != "" && st.Transport != "direct":
		netLabel = st.Transport
	}
	body += `<div class="stat-grid">
  <div class="stat-card"><div class="stat-card__label">` + esc(i18n.T(lang, "Status")) + `</div><div class="stat-card__value">` + esc(shortState) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">` + esc(i18n.T(lang, "Tor visits")) + `</div><div class="stat-card__value" data-tor-visits>` + strconv.FormatInt(st.Visits, 10) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">` + esc(i18n.T(lang, "Onion addresses")) + `</div><div class="stat-card__value">` + strconv.Itoa(len(st.Onions)) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">` + esc(i18n.T(lang, "Network")) + `</div><div class="stat-card__value">` + esc(netLabel) + `</div></div>
</div>`

	body += `<div class="card vt-hero" data-tor data-boot-pct="` + strconv.Itoa(st.BootstrapPct) + `">
  <div class="vt-hero__main">
    <div class="vt-state ` + stateClass + `"><span class="vt-dot"></span> ` + esc(stateLabel) + `</div>
    <div class="muted text-sm">` + torCode(i18n.T(lang, "One click publishes every hosted domain as its own {onion}, beside the normal address: the same site, at the same speed. Count only: no identity, time or path is ever recorded.", "onion", htmlMark(0)), ".onion") + `</div>
  </div>
  <form class="vt-hero__action" method="post" action="/os/tor/toggle" data-tor-form>
    <input type="hidden" name="state" value="` + nextState + `">
    <input type="hidden" name="csrf_token" value="">
    <button type="submit" class="btn ` + btnKind + `">` + esc(btnLabel) + `</button>
  </form>
</div>`

	// ── Connection guidance when activated but the daemon is unreachable ──
	if !st.Connected {
		hint := torCode(i18n.T(lang, "Bringing Tor up. VayuPress runs its own Tor daemon by itself: it needs only the {tor} program on the server, with no control port or systemd to set up. If this lasts, Tor is not installed yet: the VayuPress deploy and update script installs it in one step ({install}). Run the updater again, then reload.",
			"tor", htmlMark(0), "install", htmlMark(1)), "tor", "apt-get install tor")
		if st.LastError != "" {
			hint += `<div class="text-xs muted mt-2">` + esc(i18n.T(lang, "Last error: {error}", "error", st.LastError)) + `</div>`
		}
		body += `<div class="card vt-warn"><div class="card-title">` + saIcon("hourglass") + ` ` + esc(i18n.T(lang, "Bringing onions up…")) + `</div><p class="text-sm">` + hint + `</p></div>`
	} else if st.BootstrapPct < 100 {
		// Connected to our tor, but it is still joining the Tor network. Onions
		// cannot be reached until bootstrap completes — this is the usual reason a
		// freshly-activated .onion shows "Onion site not found".
		var via []string
		if st.BootstrapEng != "" {
			via = append(via, st.BootstrapEng)
		}
		if st.Transport != "" && st.Transport != "direct" {
			via = append(via, i18n.T(lang, "via {transport}", "transport", st.Transport))
		}
		if st.TorManaged {
			via = append(via, strings.TrimSpace(i18n.T(lang, "running VayuPress-managed Tor {version}", "version", st.TorVersion)))
		}
		note := torBold(i18n.T(lang, "Tor is joining the network: {percent}%", "percent", st.BootstrapPct))
		if len(via) > 0 {
			note += " (" + esc(strings.Join(via, " · ")) + ")"
		}
		note += ". " + torCode(i18n.T(lang, "Your {onion} addresses can be reached once this reaches 100%. The first time, it takes a couple of minutes. If it stays below 100%, the server cannot reach the Tor network: allow outbound connections (inbound stays closed).", "onion", htmlMark(0)), ".onion")
		if st.LogPath != "" {
			note += " " + torCode(i18n.T(lang, "Diagnostic log: {path}.", "path", htmlMark(0)), st.LogPath)
		}
		card := `<div class="card vt-warn"><div class="card-title">` + saIcon("hourglass") + ` ` + esc(i18n.T(lang, "Publishing to the Tor network…")) + `</div><p class="text-sm">` + note + `</p>`
		if st.LogTail != "" {
			card += `<pre class="vt-log text-xs muted mt-2">` + esc(st.LogTail) + `</pre>`
			// Targeted remediation for the most common hard failures we can
			// recognise in tor's own log.
			if tip := osTorLogRemedy(lang, st.LogTail); tip != "" {
				card += `<p class="text-sm mt-2">` + tip + `</p>`
			}
			// When tor can't validate the consensus, the system tor is too old for
			// today's network (an EOL distro's 0.4.2.x), which no bridge can fix.
			// VayuTor auto-downloads its own current Tor; if that build can't run on
			// this host (typically an ancient system libc), fall back to a one-time
			// apt install of a current Tor.
			if strings.Contains(strings.ToLower(st.LogTail), "not signed by sufficient") {
				if st.TorDownloadErr != "" {
					card += `<p class="text-sm mt-1">` + torCode(i18n.T(lang, "VayuTor tried to run its own current Tor, and this host refused it: {error}. The OS is too old for a modern Tor build. A fix, run once as root, that then keeps itself up to date:", "error", htmlMark(0)), st.TorDownloadErr) + `</p>`
					card += `<pre class="vt-log text-xs mt-1">` + esc(aptTorFallbackCmd) + `</pre>`
					card += `<p class="text-sm mt-1">` + esc(i18n.T(lang, "If the OS is past its end of life (Debian 10 “buster”, say), upgrading it is the lasting fix; everything else here already works.")) + `</p>`
				} else if st.TorVersion != "" {
					card += `<p class="text-sm mt-1">` + torCode(i18n.T(lang, "Tor version found: {version}, older than {least}, so it cannot validate today's Tor network. VayuTor is downloading and switching to its own current Tor by itself; this takes a moment the first time.", "version", htmlMark(0), "least", htmlMark(1)), st.TorVersion, "0.4.7") + `</p>`
				}
			}
		}
		card += `</div>`
		body += card
	}

	// ── Onion address table ──
	body += `<div class="section-head"><span class="section-head__title">` + esc(i18n.T(lang, "Onion addresses")) + `</span><span class="section-head__hint">` +
		torCode(i18n.T(lang, "One {onion} for each hosted domain: the same site, a private way in", "onion", htmlMark(0)), ".onion") + `</span></div>`
	body += `<div class="card">`
	if len(st.Onions) == 0 {
		body += `<div class="empty-state">` + esc(i18n.T(lang, "No onion addresses yet. They appear here within a minute of activation, one for each hosted domain.")) + `</div>`
	} else {
		body += `<p class="muted text-sm mb-3">` + torCode(i18n.T(lang, "Each domain has its own {onion}. It serves the same site as its normal address, and both work at once. Share the {onion} with visitors who care for their privacy; Tor Browser also finds it by itself, through the {header} header.", "onion", htmlMark(0), "header", htmlMark(1)), ".onion", torOnionLocation) + `</p>`
		body += `<div class="vt-onions">`
		copyLabel := esc(i18n.T(lang, "Copy"))
		for _, o := range st.Onions {
			onionURL := "http://" + o.OnionHost
			body += `<div class="vt-onion">` +
				`<div class="vt-onion__host" translate="no">` + esc(o.Host) + `</div>` +
				`<div class="vt-onion__row"><code class="vt-onion__addr" data-onion="` + esc(onionURL) + `">` + esc(o.OnionHost) + `</code>` +
				`<button type="button" class="btn btn--sm btn--ghost vt-copy" data-copy="` + esc(onionURL) + `">` + copyLabel + `</button></div>` +
				`</div>`
		}
		body += `</div>`
	}
	body += `</div>`

	// ── Advanced, grouped into premium collapsible accordions (Monetization style) ──
	// Summary chips reflect live state so the operator sees what's set at a glance.
	bridgesConfigured := false
	if a.siteSettings != nil {
		bridgesConfigured = strings.TrimSpace(a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyTorBridges)) != ""
	}
	vanityChip := ""
	if a.vayuTor != nil {
		vs := a.vayuTor.VanityStatus()
		if vs.Active {
			vanityChip = `<span class="mon-chip">● ` + esc(i18n.T(lang, "Searching")) + `</span>`
		} else if vs.Found {
			vanityChip = `<span class="mon-chip mon-chip--on">● ` + esc(i18n.T(lang, "Custom set")) + `</span>`
		}
	}
	healthChip := ""
	switch st.Health {
	case vtor.HealthHealthy:
		healthChip = `<span class="mon-chip mon-chip--on">● ` + esc(i18n.T(lang, "Healthy")) + `</span>`
	case vtor.HealthDown:
		healthChip = `<span class="mon-chip mon-chip--off">● ` + esc(i18n.T(lang, "Down")) + `</span>`
	case vtor.HealthStarting:
		healthChip = `<span class="mon-chip">● ` + esc(i18n.T(lang, "Starting")) + `</span>`
	case vtor.HealthDegraded:
		healthChip = `<span class="mon-chip">● ` + esc(i18n.T(lang, "Degraded")) + `</span>`
	}

	body += `<div class="section-head"><span class="section-head__title">` + esc(i18n.T(lang, "Setup and network")) + `</span><span class="section-head__hint">` + esc(i18n.T(lang, "Reach Tor from anywhere, and choose your address")) + `</span></div>`
	body += `<div class="mon-stack">` +
		monAcc(saIcon("topology"), i18n.T(lang, "Bridges"), i18n.T(lang, "For networks that block Tor"), monChip(bridgesConfigured, i18n.T(lang, "Configured"), i18n.T(lang, "Not set")), false, a.osTorBridgesCard(lang, r, st)) +
		monAcc(saIcon("sparkle"), i18n.T(lang, "Custom (vanity) address"), i18n.T(lang, "A .onion that starts with letters you choose"), vanityChip, false, a.osTorVanityCard(lang, st, r.URL.Query().Get("vanity_err"))) +
		monAcc(saIcon("shield"), i18n.T(lang, "Onion-Location and hardening"), i18n.T(lang, "How Tor Browser finds your onion by itself"), "", false, a.osTorHardeningCard(lang, r)) +
		`</div>`

	body += `<div class="section-head"><span class="section-head__title">` + esc(i18n.T(lang, "Health and privacy")) + `</span><span class="section-head__hint">` + esc(i18n.T(lang, "Uptime alerts, opt-in page counts, and exactly what is never recorded")) + `</span></div>`
	body += `<div class="mon-stack">` +
		monAcc(saIcon("pulse"), i18n.T(lang, "Health and alerts"), i18n.T(lang, "Onion uptime and signed outage webhooks"), healthChip, false, osTorHealthCard(lang, st)) +
		monAcc(saIcon("doc"), i18n.T(lang, "Popular pages"), i18n.T(lang, "Private, opt-in aggregate counts"), monChip(st.PageStatsOn, i18n.T(lang, "On"), i18n.T(lang, "Off")), false, a.osTorPageStatsCard(lang, st)) +
		monAcc(saIcon("lock"), i18n.T(lang, "Privacy posture"), i18n.T(lang, "Exactly what VayuTor records, and what it does not"), `<span class="mon-chip mon-chip--on">● `+esc(i18n.T(lang, "Count only"))+`</span>`, false, osTorPrivacyNote(lang, st)) +
		`</div>`
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-tor.js?v=` + assetVer("js/admin-os-tor.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "VayuTor", "tor", cfg, htmpl.HTML(body)))
}

// osTorLogRemedy recognises the most common hard bootstrap failures in tor's
// own log and returns a specific, actionable remedy (HTML). "" if nothing known.
func osTorLogRemedy(lang, log string) string {
	low := strings.ToLower(log)
	switch {
	case strings.Contains(low, "not signed by sufficient") ||
		strings.Contains(low, "clock") ||
		strings.Contains(low, "certificate") && strings.Contains(low, "expired"):
		// Tor rejected the network consensus. Overwhelmingly this is a wrong
		// server clock, or a tor package too old to know the current authorities.
		return saIcon("warn") + ` ` + torBold(i18n.T(lang, "Tor cannot validate the network consensus.")) + ` ` +
			torCode(i18n.T(lang, "This is nearly always the server's clock being off: a few minutes of skew is enough. Turn on time sync with {sync} (check it with {check}), and it recovers within a minute. If the clock is right, the {tor} package may be too old: check {version} and update it.",
				"sync", htmlMark(0), "check", htmlMark(1), "tor", htmlMark(2), "version", htmlMark(3)), "sudo timedatectl set-ntp true", "timedatectl", "tor", "tor --version")
	case strings.Contains(low, "obfs4proxy") ||
		strings.Contains(low, "pluggable transport") ||
		strings.Contains(low, "managed proxy"):
		return saIcon("warn") + ` ` + torBold(i18n.T(lang, "The obfs4 bridge transport is not installed.")) + ` ` +
			torCode(i18n.T(lang, "Run {install} (or run the VayuPress updater again), then reload: VayuTor picks it up within a minute.", "install", htmlMark(0)), "sudo apt-get install -y obfs4proxy")
	case strings.Contains(low, "no route to host") ||
		strings.Contains(low, "noroute"):
		return saIcon("warn") + ` ` + torBold(i18n.T(lang, "This network blocks Tor at the IP level.")) + ` ` +
			torCode(i18n.T(lang, "It is common on some VPS and mail hosts: the firewall is fine, and the provider drops traffic to Tor relays. VayuTor moves to Tor bridges by itself to get round it. If it stays stuck, get obfs4 bridges from {site} (or by email to {address}), set {setting} to those lines, then reload.",
				"site", htmlMark(0), "address", htmlMark(1), "setting", htmlMark(2)), "https://bridges.torproject.org", "bridges@torproject.org", "VAYUOS_TOR_BRIDGES")
	case strings.Contains(low, "connection refused") ||
		strings.Contains(low, "connection timed out"):
		return saIcon("warn") + ` ` + torBold(i18n.T(lang, "The server cannot reach the Tor network.")) + ` ` +
			esc(i18n.T(lang, "Allow outbound connections in the firewall or cloud security group (inbound stays closed). After a stall VayuTor also tries again on ports 80 and 443 only, then through bridges."))
	}
	return ""
}

// osTorBridgesCard renders the operator's Tor bridge configuration — the entire
// "network blocks Tor" fix, done from VayuOS with no server access. Bridges are
// saved to settings (KeyTorBridges) and applied live by the engine.
func (a *App) osTorBridgesCard(lang string, r *http.Request, st vtor.Status) string {
	current := ""
	if a.siteSettings != nil {
		current = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyTorBridges)
	}
	needsObfs4 := strings.Contains(strings.ToLower(current), "obfs4")
	card := `<div class="card mt-4"><div class="card-title">` + saIcon("topology") + ` ` + esc(i18n.T(lang, "Bridges, for networks that block Tor")) + `</div>`
	// The obfs4 transport binary is required to USE obfs4 bridges. If it's missing,
	// the bridges are configured but inert (tor falls back to a direct, blocked
	// connection) — say so loudly, since it's the #1 "I pasted bridges but nothing
	// happens" cause.
	if needsObfs4 && !st.Obfs4Available {
		card += `<div class="vt-bridges__warn text-sm mb-3">` + saIcon("warn") + ` ` + esc(i18n.T(lang, "The obfs4 transport is not available on this server, so these obfs4 bridges cannot be used yet. VayuPress normally carries obfs4 itself: reload the page, and if it lasts, run the VayuPress updater again.")) + `</div>`
	}
	card += `<p class="muted text-sm mb-3">` + torCode(i18n.T(lang, "If the host or the network blocks Tor (joining stalls with “no route to host” or a consensus error), paste obfs4 bridge lines here and VayuTor gets round the block by itself, with no server access. Free bridges are at {site} (choose obfs4), or by email to {address} from a provider the Tor Project accepts, with {request} in the body. Use IPv4 bridges (addresses like {example}) unless the server has working IPv6; most do not. One bridge a line.",
		"site", htmlMark(0), "address", htmlMark(1), "request", htmlMark(2), "example", htmlMark(3)), "https://bridges.torproject.org", "bridges@torproject.org", "get transport obfs4", "1.2.3.4:443") + `</p>`
	card += `<form method="post" action="/os/tor/bridges" data-tor-form>
  <textarea class="vt-bridges__input" name="bridges" rows="4" spellcheck="false" autocomplete="off" placeholder="` + torBridgeExample + `">` + esc(current) + `</textarea>
  <input type="hidden" name="csrf_token" value="">
  <div class="vt-bridges__row"><button type="submit" class="btn btn--primary">` + esc(i18n.T(lang, "Save bridges")) + `</button>`
	if bridgesLookIPv6Only(current) {
		card += ` <span class="vt-bridges__warn text-xs">` + saIcon("warn") + ` ` + torCode(i18n.T(lang, "These are IPv6 bridges (addresses in {brackets}). They connect only if the server has working IPv6, which most VPS do not, and that shows as “connections died in state connect()ing”. Get IPv4 obfs4 bridges instead.", "brackets", htmlMark(0)), "[…]") + `</span>`
	} else if strings.TrimSpace(current) != "" {
		card += ` <span class="vt-bridges__on muted text-xs">✓ ` + esc(i18n.T(lang, "Bridges set: used by themselves when a direct connection is blocked. Empty the box and save to stop using them.")) + `</span>`
	}
	card += `</div></form></div>`
	return card
}

// bridgesLookIPv6Only reports whether every configured bridge uses an IPv6
// literal ([addr]:port) — which only works when the server itself has IPv6, a
// common footgun when the operator picks the IPv6 option on bridges.torproject.org.
func bridgesLookIPv6Only(raw string) bool {
	lines := parseTorBridges(raw)
	if len(lines) == 0 {
		return false
	}
	for _, l := range lines {
		if !strings.Contains(l, "]:") { // an IPv4 (or hostname) endpoint present → not IPv6-only
			return false
		}
	}
	return true
}

// handleOSTorBridges saves operator-supplied Tor bridge lines (from the VayuTor
// page) and kicks the engine to apply them immediately.
func (a *App) handleOSTorBridges(w http.ResponseWriter, r *http.Request) {
	if a.vayuTor == nil || a.siteSettings == nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	bridges := strings.TrimSpace(r.PostFormValue("bridges"))
	_ = a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyTorBridges: bridges})
	a.vayuTor.Kick()
	http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
}

// osTorHardeningCard renders the onion hardening controls: the Onion-Location
// advertising toggle plus a summary of the always-on onion protections.
func (a *App) osTorHardeningCard(lang string, r *http.Request) string {
	advertise := true
	if a.siteSettings != nil {
		advertise = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyTorOnionLocation) != "off"
	}
	next, label, cls := "off", i18n.T(lang, "Stop advertising the onion"), "btn--ghost"
	state := `<span class="vt-bridges__on muted text-xs">✓ ` + torCode(i18n.T(lang, "On: Tor Browser is told the onion by the {header} header, and can switch to it by itself.", "header", htmlMark(0)), torOnionLocation) + `</span>`
	if !advertise {
		next, label, cls = "on", i18n.T(lang, "Advertise the onion to Tor Browser"), "btn--primary"
		state = `<span class="muted text-xs">` + torCode(i18n.T(lang, "Off: normal responses do not announce the onion. Visitors can still use the {onion} directly.", "onion", htmlMark(0)), ".onion") + `</span>`
	}
	card := `<div class="card mt-4"><div class="card-title">` + saIcon("shield") + ` ` + esc(i18n.T(lang, "Hardening")) + `</div>`
	card += `<p class="muted text-sm mb-3"><strong>` + torOnionLocation + `.</strong> ` + torCode(i18n.T(lang, "When on, every normal page advertises its {onion} so Tor Browser can offer it or switch to it by itself. Turn it off to keep the onions live without announcing them.", "onion", htmlMark(0)), ".onion") + `</p>`
	card += `<form method="post" action="/os/tor/hardening" data-tor-form>
  <input type="hidden" name="onion_location" value="` + next + `">
  <input type="hidden" name="csrf_token" value="">
  <div class="vt-bridges__row"><button type="submit" class="btn ` + cls + `">` + esc(label) + `</button> ` + state + `</div>
</form>`
	card += `<p class="muted text-sm mt-3">` + torBold(i18n.T(lang, "Always on for onion visitors, with nothing to set:")) + ` ` +
		torCode(i18n.T(lang, "responses over a {onion} leave out HSTS (it means nothing over an address that proves itself), send {policy} so the onion's address never leaks, keep the same strict CSP as the normal site, and open no inbound port: onion traffic arrives through Tor's rendezvous. VayuShield handles onion abuse beside the normal site's.",
			"onion", htmlMark(0), "policy", htmlMark(1)), ".onion", "Referrer-Policy: no-referrer") + `</p>`
	card += `<p class="muted text-xs mt-2">` + esc(i18n.T(lang, "Not offered, on purpose: a switch to turn the normal site off and serve Tor only would make the site unreachable for everyone not using Tor. If that is truly wanted, it belongs at the web server or in DNS, not here.")) + `</p>`
	card += `</div>`
	return card
}

// handleOSTorVanity starts or cancels a vanity-address search for a domain.
func (a *App) handleOSTorVanity(w http.ResponseWriter, r *http.Request) {
	if a.vayuTor == nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	if r.PostFormValue("action") == "cancel" {
		a.vayuTor.CancelVanity()
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	host := r.PostFormValue("host")
	prefix := r.PostFormValue("prefix")
	if err := a.vayuTor.StartVanity(host, prefix); err != nil {
		http.Redirect(w, r, "/os/tor?vanity_err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
}

// handleOSTorHardening saves the onion hardening settings (currently the
// Onion-Location advertising toggle). Read live by the middleware — no kick.
func (a *App) handleOSTorHardening(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	val := "on"
	if r.PostFormValue("onion_location") == "off" {
		val = "off"
	}
	_ = a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyTorOnionLocation: val})
	http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
}

// handleOSTorPageStats enables/disables the opt-in per-page onion counts, or
// resets them. The engine reads the setting live, so there is nothing to kick.
func (a *App) handleOSTorPageStats(w http.ResponseWriter, r *http.Request) {
	if a.vayuTor == nil || a.siteSettings == nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	if r.PostFormValue("reset") == "1" {
		a.vayuTor.ResetPageHits()
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	state := "off"
	if r.PostFormValue("on") == "1" {
		state = "on"
	}
	_ = a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyTorPageStats: state})
	http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
}

// osTorPrivacyNote states the privacy posture explicitly. The "tracked" line is
// truthful about whether per-page counts are opted in, so the page never claims
// more privacy than is actually in effect.
func osTorPrivacyNote(lang string, st vtor.Status) string {
	tracked := `<li>` + torBold(i18n.T(lang, "Nothing is tracked.")) + ` ` + esc(i18n.T(lang, "VayuTor's one figure is a single count of visits: no IP (Tor gives none), no time, no path, no user agent, no cookie.")) + `</li>`
	if st.PageStatsOn {
		tracked = `<li>` + torBold(i18n.T(lang, "Aggregate counts only.")) + ` ` + esc(i18n.T(lang, "You chose to count pages. VayuTor keeps a running total for each page and one overall count of visits, and still no IP (Tor gives none), no time, no session, no order, no user agent, no cookie. Visits cannot be linked to each other, and a visitor cannot be identified or located.")) + `</li>`
	}
	return `<div class="card mt-4"><div class="card-title">` + esc(i18n.T(lang, "Privacy posture")) + `</div>
<ul class="vt-note__list text-sm">
  ` + tracked + `
  <li>` + torBold(i18n.T(lang, "The onion keys are yours.")) + ` ` + torCode(i18n.T(lang, "Each address is pinned by a key kept only in your own database, so a restore brings the same {onion} back.", "onion", htmlMark(0)), ".onion") + `</li>
  <li>` + torBold(i18n.T(lang, "Nothing new to attack.")) + ` ` + esc(i18n.T(lang, "VayuTor opens no inbound port; onion traffic reaches the server through Tor's rendezvous, and the normal site is untouched.")) + `</li>
</ul></div>`
}

// osTorPageStatsCard renders the opt-in per-page onion analytics: a privacy-safe
// aggregate "which pages are popular" table (no time, IP, identity, or ordering),
// plus the enable/disable toggle and a reset. Visitor geolocation is impossible
// over an onion service (the server never sees the client IP), so it is stated
// as absent rather than offered.
func (a *App) osTorPageStatsCard(lang string, st vtor.Status) string {
	card := `<div class="card mt-4"><div class="card-title">` + saIcon("doc") + ` ` + esc(i18n.T(lang, "Popular pages: private, opt-in")) + `</div>`
	if !st.PageStatsOn {
		card += `<p class="muted text-sm mb-3">` + esc(i18n.T(lang, "Off unless you turn it on. On, VayuTor keeps a count for each page (a running total of views), so you can see which posts are read over Tor. It stays private: no IP (Tor gives none), no time, no session, no order, so it can never identify, locate or link a visitor. The visitor's country is not shown because it cannot be known: an onion service never sees the visitor's IP.")) + `</p>`
		card += `<form method="post" action="/os/tor/pagestats" data-tor-form>
  <input type="hidden" name="on" value="1">
  <input type="hidden" name="csrf_token" value="">
  <button type="submit" class="btn btn--primary">` + esc(i18n.T(lang, "Count each page")) + `</button>
</form>`
		card += `</div>`
		return card
	}
	card += `<p class="muted text-sm mb-3">` + esc(i18n.T(lang, "Views for each page, added up: no IP, time, session or order, so no visit can be traced to a person. The visitor's country is left out on purpose: an onion service never sees the visitor's IP.")) + `</p>`
	if len(st.TopPages) == 0 {
		card += `<div class="empty-state">` + torCode(i18n.T(lang, "No page views counted yet. They appear here as visitors read your {onion}.", "onion", htmlMark(0)), ".onion") + `</div>`
	} else {
		card += `<div class="vt-pages__table"><table class="vt-pages__t"><thead><tr><th>` + esc(i18n.T(lang, "Domain")) + `</th><th>` + esc(i18n.T(lang, "Page")) + `</th><th class="vt-pages__num">` + esc(i18n.T(lang, "Views")) + `</th></tr></thead><tbody>`
		for _, p := range st.TopPages {
			card += `<tr><td translate="no">` + esc(p.Host) + `</td><td><code>` + esc(p.Path) + `</code></td><td class="vt-pages__num">` + strconv.FormatInt(p.Count, 10) + `</td></tr>`
		}
		card += `</tbody></table></div>`
	}
	card += `<div class="vt-pages__actions">
  <form method="post" action="/os/tor/pagestats" data-tor-form>
    <input type="hidden" name="on" value="0">
    <input type="hidden" name="csrf_token" value="">
    <button type="submit" class="btn btn--ghost btn--sm">` + esc(i18n.T(lang, "Turn off")) + `</button>
  </form>
  <form method="post" action="/os/tor/pagestats" data-tor-form>
    <input type="hidden" name="reset" value="1">
    <input type="hidden" name="csrf_token" value="">
    <button type="submit" class="btn btn--ghost btn--sm">` + esc(i18n.T(lang, "Reset counts")) + `</button>
  </form>
</div></div>`
	return card
}

// osTorVanityCard renders the custom-prefix ("vanity") onion address generator:
// pick a domain and a short prefix, and VayuTor brute-forces a matching key in
// the background, then republishes that domain's onion under the new address.
func (a *App) osTorVanityCard(lang string, st vtor.Status, startErr string) string {
	if a.vayuTor == nil {
		return ""
	}
	vs := a.vayuTor.VanityStatus()
	card := `<div class="card mt-4" data-vanity` + boolAttr(" data-vanity-active", vs.Active) + `><div class="card-title">` + saIcon("sparkle") + ` ` + esc(i18n.T(lang, "Custom (vanity) address")) + `</div>`
	card += `<p class="muted text-sm mb-3">` + torCode(i18n.T(lang, "Give a domain a {onion} that starts with letters you choose, instead of a random one. VayuTor looks for a matching key in the background on this server, and nothing leaves it. Each letter more is about 32 times the work: 1 to 4 take seconds, 5 a few minutes, 6 or 7 can take hours. The letters allowed are {letters} and {digits}.",
		"onion", htmlMark(0), "letters", htmlMark(1), "digits", htmlMark(2)), ".onion", "a–z", "2–7") + `</p>`

	if vs.Active {
		card += `<div class="vt-vanity__run"><div class="vt-health__badge vt-health__badge--warn">` + saIcon("hourglass") + ` ` + esc(i18n.T(lang, "Searching")) + `</div> `
		card += `<span class="text-sm">` + escFill(i18n.T(lang, "prefix {prefix} for {host}", "prefix", htmlMark(0), "host", htmlMark(1)), `<code>`+esc(vs.Prefix)+`</code>`, `<strong translate="no">`+esc(vs.Host)+`</strong>`) + `</span></div>`
		card += `<p class="text-sm muted mt-2">` + escFill(i18n.T(lang, "Attempts: {tries} · elapsed {elapsed}. You can leave this page; the search goes on.", "tries", htmlMark(0), "elapsed", htmlMark(1)),
			`<strong data-vanity-tries>`+strconv.FormatInt(vs.Tries, 10)+`</strong>`, `<span data-vanity-elapsed>`+esc(fmtDur(lang, vs.Elapsed))+`</span>`) + `</p>`
		if vs.Warn {
			card += `<p class="vt-bridges__warn text-xs mt-1">` + saIcon("warn") + ` ` + esc(i18n.T(lang, "{n, plural, one {A prefix of # letter can take a long time (about {attempts} attempts on average).} other {A prefix of # letters can take a long time (about {attempts} attempts on average).}}", "n", len(vs.Prefix), "attempts", pow32(lang, len(vs.Prefix)))) + `</p>`
		}
		card += `<form method="post" action="/os/tor/vanity" data-tor-form class="mt-2">
  <input type="hidden" name="action" value="cancel">
  <input type="hidden" name="csrf_token" value="">
  <button type="submit" class="btn btn--ghost btn--sm">` + esc(i18n.T(lang, "Cancel the search")) + `</button>
</form>`
		card += `</div>`
		return card
	}

	if vs.Found && vs.Address != "" {
		host := `<strong translate="no">` + esc(vs.Host) + `</strong>`
		card += `<div class="vt-vanity__run"><div class="vt-health__badge vt-health__badge--ok">✓ ` + esc(i18n.T(lang, "Found")) + `</div> <span class="text-sm">` + escFill(i18n.T(lang, "for {host}", "host", htmlMark(0)), host) + `</span></div>`
		card += `<div class="vt-onion__row mt-2"><code class="vt-onion__addr">` + esc(vs.Address) + `</code></div>`
		card += `<p class="text-sm muted mt-1">` + escFill(i18n.T(lang, "Publishing {host} again under this address: it takes the old one's place in the list above within a minute.", "host", htmlMark(0)), host) + `</p>`
	} else if vs.Error != "" && vs.Error != "cancelled" {
		// The engine's own account of the search, as it recorded it.
		card += `<p class="vt-bridges__warn text-sm mb-2">` + escFill(i18n.T(lang, "Last search: {error}", "error", htmlMark(0)), `<span translate="no">`+esc(vs.Error)+`</span>`) + `</p>`
	}

	if startErr != "" {
		card += `<p class="vt-bridges__warn text-sm mb-2" translate="no">` + esc(startErr) + `</p>`
	}
	// New-search form (also shown after a completed/cancelled search).
	if len(st.Onions) == 0 {
		card += `<div class="empty-state">` + esc(i18n.T(lang, "Turn on VayuTor and wait for its onions to appear; then a domain can be given a custom address.")) + `</div></div>`
		return card
	}
	card += `<form method="post" action="/os/tor/vanity" data-tor-form class="vt-vanity__form">
  <input type="hidden" name="action" value="start">
  <input type="hidden" name="csrf_token" value="">
  <label class="field-label" for="vanity-host">` + esc(i18n.T(lang, "Domain")) + `</label>
  <select id="vanity-host" name="host" class="vt-vanity__select">`
	for _, o := range st.Onions {
		card += `<option value="` + esc(o.Host) + `" translate="no">` + esc(o.Host) + `</option>`
	}
	card += `</select>
  <label class="field-label" for="vanity-prefix">` + esc(i18n.T(lang, "Starts with")) + `</label>
  <input id="vanity-prefix" name="prefix" class="vt-vanity__input" type="text" inputmode="latin" autocomplete="off" spellcheck="false" maxlength="7" pattern="[a-z2-7]{1,7}" placeholder="vayu" required>
  <button type="submit" class="btn btn--primary">` + esc(i18n.T(lang, "Generate")) + `</button>
</form>
<p class="muted text-xs mt-2">` + esc(i18n.T(lang, "Keep it to 4 or 5 letters. The rest of the address stays random.")) + `</p>`
	card += `</div>`
	return card
}

// boolAttr returns attr when cond is true, else "" — for optional HTML attributes.
func boolAttr(attr string, cond bool) string {
	if cond {
		return attr + `="1"`
	}
	return ""
}

// fmtDur renders a coarse elapsed duration like "3s", "2m 10s", "1h 4m",
// with the units in lang.
func fmtDur(lang string, d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return i18n.T(lang, "{s}s", "s", int(d.Seconds()))
	case d < time.Hour:
		return i18n.T(lang, "{m}m {s}s", "m", int(d.Minutes()), "s", int(d.Seconds())%60)
	default:
		return i18n.T(lang, "{h}h {m}m", "h", int(d.Hours()), "m", int(d.Minutes())%60)
	}
}

// pow32 renders a friendly "32^n" attempt estimate.
func pow32(lang string, n int) string {
	v := 1
	for i := 0; i < n; i++ {
		v *= 32
	}
	switch {
	case v >= 1_000_000_000:
		return i18n.T(lang, "{n} billion", "n", v/1_000_000_000)
	case v >= 1_000_000:
		return i18n.T(lang, "{n} million", "n", v/1_000_000)
	default:
		return strconv.Itoa(v)
	}
}

// osTorHealthCard renders the onion health state, a short transition history,
// and how outage alerts are delivered (via subscribed webhooks).
func osTorHealthCard(lang string, st vtor.Status) string {
	cls, label, icon := torHealthBadge(lang, st.Health)
	card := `<div class="card mt-4"><div class="card-title">` + saIcon("pulse") + ` ` + esc(i18n.T(lang, "Health and alerts")) + `</div>`
	card += `<div class="vt-health__now"><span class="vt-health__badge ` + cls + `">` + saIcon(icon) + " " + esc(label) + `</span>`
	if st.Health != vtor.HealthOff {
		if d := torRelDur(lang, st.HealthSince); d != "" {
			card += ` <span class="muted text-sm">` + esc(i18n.T(lang, "for {duration}", "duration", d)) + `</span>`
		}
	}
	card += `</div>`
	if st.HealthReason != "" && st.Health != vtor.HealthHealthy && st.Health != vtor.HealthOff {
		// The engine's reason, as it recorded it.
		card += `<p class="text-sm muted mt-1" translate="no">` + esc(st.HealthReason) + `</p>`
	}
	card += `<p class="text-sm muted mt-2">` + torCode(i18n.T(lang, "VayuTor watches every onion and, when they go down or recover, posts a signed alert to each webhook subscribed to {down} or {recovered}. Alerts wait a moment, so a brief blip or the normal climb at start never pages you, and they carry only how the service is (its state, the reason, and the counts of onions and domains), never anything about a visitor.",
		"down", htmlMark(0), "recovered", htmlMark(1)), "tor.onion_down", "tor.onion_recovered") + `</p>`
	if len(st.HealthLog) > 0 {
		card += `<div class="vt-health__log">`
		for i := len(st.HealthLog) - 1; i >= 0; i-- { // newest first
			ev := st.HealthLog[i]
			c, l, ic := torHealthBadge(lang, ev.State)
			card += `<div class="vt-health__row"><span class="vt-health__badge vt-health__badge--sm ` + c + `">` + saIcon(ic) + " " + esc(l) + `</span>`
			if ev.Reason != "" {
				card += ` <span class="muted text-xs" translate="no">` + esc(ev.Reason) + `</span>`
			}
			if d := torRelDur(lang, ev.At); d != "" {
				card += ` <span class="muted text-xs vt-health__ago">` + esc(i18n.T(lang, "{duration} ago", "duration", d)) + `</span>`
			}
			card += `</div>`
		}
		card += `</div>`
	}
	card += `</div>`
	return card
}

// torHealthBadge maps a health state to its CSS class, label, and icon (a
// name from the Still Air set).
func torHealthBadge(lang, state string) (cls, label, icon string) {
	switch state {
	case vtor.HealthHealthy:
		return "vt-health__badge--ok", i18n.T(lang, "Healthy"), "check-c"
	case vtor.HealthStarting:
		return "vt-health__badge--warn", i18n.T(lang, "Starting"), "hourglass"
	case vtor.HealthDegraded:
		return "vt-health__badge--warn", i18n.T(lang, "Degraded"), "warn"
	case vtor.HealthDown:
		return "vt-health__badge--err", i18n.T(lang, "Down"), "error"
	default:
		return "vt-health__badge--off", i18n.T(lang, "Off"), "power"
	}
}

// torRelDur renders a coarse "how long ago / for how long" duration, "" if the
// time is zero, with the units in lang.
func torRelDur(lang string, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return i18n.T(lang, "<1m")
	case d < time.Hour:
		return i18n.T(lang, "{m}m", "m", int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.T(lang, "{h}h {m}m", "h", int(d.Hours()), "m", int(d.Minutes())%60)
	default:
		return i18n.T(lang, "{d}d {h}h", "d", int(d.Hours())/24, "h", int(d.Hours())%24)
	}
}

// handleOSTorToggle flips the one-click activation setting and kicks the engine
// to reconcile immediately.
func (a *App) handleOSTorToggle(w http.ResponseWriter, r *http.Request) {
	if a.vayuTor == nil || a.siteSettings == nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
		return
	}
	state := "off"
	if r.PostFormValue("state") == "on" {
		state = "on"
	}
	_ = a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyTorEnabled: state})
	a.vayuTor.Kick()
	http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
}

// handleOSTorStats returns the live count + connection state as JSON for the
// page's lightweight poller. Count only — never any per-visitor datum.
func (a *App) handleOSTorStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	st := vtor.Status{}
	if a.vayuTor != nil {
		st = a.vayuTor.Snapshot()
	}
	resp := map[string]interface{}{
		"visits":    st.Visits,
		"active":    st.Active,
		"connected": st.Connected,
		"onions":    len(st.Onions),
		"bootstrap": st.BootstrapPct,
	}
	if a.vayuTor != nil {
		vs := a.vayuTor.VanityStatus()
		if vs.Active || vs.Found {
			resp["vanity"] = map[string]interface{}{
				"active":  vs.Active,
				"host":    vs.Host,
				"prefix":  vs.Prefix,
				"tries":   vs.Tries,
				"found":   vs.Found,
				"address": vs.Address,
				"elapsed": fmtDur(i18n.Lang(r.Context()), vs.Elapsed),
			}
		}
	}
	writeJSON(w, r, http.StatusOK, resp)
}
