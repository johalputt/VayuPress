package main

// admin_os_tor.go — the VayuTor admin surface: a one-click switch that publishes
// every hosted domain as a Tor v3 onion service alongside its clearnet URL, the
// list of live .onion addresses, and a single count-only visitor tally (no
// identifier, time, path, or any other datum — the entire analytic). Strict CSP:
// server-rendered HTML, no inline styles; the tiny island (copy + poll + toggle)
// is a nonce'd same-origin script.

import (
	"net/http"
	"strconv"
	"strings"

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
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

// handleOSTor renders the VayuTor control page.
func (a *App) handleOSTor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	var st vtor.Status
	if a.vayuTor != nil {
		st = a.vayuTor.Snapshot()
	}

	esc := htmpl.HTMLEscapeString
	body := `<div class="page-header"><h1>VayuTor</h1><span class="muted text-sm">Publish every hosted domain as a Tor onion service — a private, un-trackable way in that works alongside the normal address. No provider, network, or observer can see who visits.</span></div>`

	if a.vayuTor == nil || !st.Available {
		body += `<div class="empty-state">VayuTor is switched off at the environment level (<code>VAYUOS_TOR=off</code>). Remove that to make it available, then reload.</div>`
		writeOSHTML(w, adminOSLayout(nonce, "VayuTor", "tor", cfg, htmpl.HTML(body)))
		return
	}

	// ── Status hero + one-click toggle ──
	stateClass, stateLabel := "vt-state--off", "Inactive"
	switch {
	case st.Active && st.Connected && st.BootstrapPct >= 100:
		stateClass, stateLabel = "vt-state--on", "Active"
	case st.Active && st.Connected:
		// Control port is up and onions are registered, but tor is still joining
		// the Tor network — onions are not reachable until this hits 100%.
		stateClass, stateLabel = "vt-state--warn", "Connecting to Tor ("+strconv.Itoa(st.BootstrapPct)+"%)"
	case st.Active && !st.Connected:
		stateClass, stateLabel = "vt-state--warn", "Activating…"
	}
	btnLabel, btnKind, nextState := "Activate onion services", "btn--primary", "on"
	if st.Active {
		btnLabel, btnKind, nextState = "Deactivate", "btn--ghost", "off"
	}

	body += `<div class="card vt-hero" data-tor data-boot-pct="` + strconv.Itoa(st.BootstrapPct) + `">
  <div class="vt-hero__main">
    <div class="vt-state ` + stateClass + `"><span class="vt-dot"></span> ` + stateLabel + `</div>
    <div class="vt-hero__count"><span class="vt-count" data-tor-visits>` + strconv.FormatInt(st.Visits, 10) + `</span> <span class="muted">Tor visits</span></div>
    <div class="muted text-sm">Count only — no identity, no time, nothing else is ever recorded.</div>
  </div>
  <form class="vt-hero__action" method="post" action="/os/tor/toggle" data-tor-toggle data-tor-form>
    <input type="hidden" name="state" value="` + nextState + `">
    <input type="hidden" name="csrf_token" value="">
    <button type="submit" class="btn ` + btnKind + `">` + btnLabel + `</button>
  </form>
</div>`

	// ── Connection guidance when activated but the daemon is unreachable ──
	if st.Active && !st.Connected {
		hint := "Bringing Tor up. VayuPress runs its own Tor daemon automatically — it only needs the <code>tor</code> program installed on the server (no control-port or systemd setup required). If this persists, Tor isn't installed yet: the VayuPress deploy/update script installs it in one step (<code>apt-get install tor</code>). Re-run the updater, then reload."
		if st.LastError != "" {
			hint += `<div class="text-xs muted mt-2">last error: ` + esc(st.LastError) + `</div>`
		}
		body += `<div class="card vt-warn"><div class="card-title">⏳ Bringing onions up…</div><p class="text-sm">` + hint + `</p></div>`
	} else if st.Active && st.Connected && st.BootstrapPct < 100 {
		// Connected to our tor, but it is still joining the Tor network. Onions
		// cannot be reached until bootstrap completes — this is the usual reason a
		// freshly-activated .onion shows "Onion site not found".
		note := "Tor is joining the network — <strong>" + strconv.Itoa(st.BootstrapPct) + "%</strong>"
		if st.BootstrapEng != "" {
			note += " (" + esc(st.BootstrapEng) + ")"
		}
		if st.Transport != "" && st.Transport != "direct" {
			note += ` &middot; via <strong>` + esc(st.Transport) + `</strong>`
		}
		if st.TorManaged {
			note += ` &middot; running <strong>VayuPress-managed Tor</strong>`
			if st.TorVersion != "" {
				note += ` ` + esc(st.TorVersion)
			}
		}
		note += `. Your <code>.onion</code> addresses become reachable once this reaches 100%. The first time, this takes a couple of minutes. If it stays stuck below 100%, the server can't reach the Tor network — allow <strong>outbound</strong> connections (inbound stays closed).`
		if st.LogPath != "" {
			note += ` Diagnostic log: <code>` + esc(st.LogPath) + `</code>.`
		}
		card := `<div class="card vt-warn"><div class="card-title">⏳ Publishing to the Tor network…</div><p class="text-sm">` + note + `</p>`
		if st.LogTail != "" {
			card += `<pre class="vt-log text-xs muted mt-2">` + esc(st.LogTail) + `</pre>`
			// Targeted remediation for the most common hard failures we can
			// recognise in tor's own log.
			if tip := osTorLogRemedy(st.LogTail); tip != "" {
				card += `<p class="text-sm mt-2">` + tip + `</p>`
			}
			// When tor can't validate the consensus, the system tor is too old for
			// today's network (an EOL distro's 0.4.2.x), which no bridge can fix.
			// VayuTor auto-downloads its own current Tor; if that build can't run on
			// this host (typically an ancient system libc), fall back to a one-time
			// apt install of a current Tor.
			if strings.Contains(strings.ToLower(st.LogTail), "not signed by sufficient") {
				if st.TorDownloadErr != "" {
					card += `<p class="text-sm mt-1">VayuTor tried to run its <strong>own current Tor</strong> but this host rejected it — <code>` + esc(st.TorDownloadErr) + `</code>. Your OS is too old for a modern Tor build. One-time fix (run once as root; it then keeps itself updated):</p>`
					card += `<pre class="vt-log text-xs mt-1">` + esc(aptTorFallbackCmd) + `</pre>`
					card += `<p class="text-sm mt-1">If your OS is end-of-life (e.g. Debian 10 &ldquo;buster&rdquo;), upgrading the OS is the durable fix — everything else here already works.</p>`
				} else if st.TorVersion != "" {
					card += `<p class="text-sm mt-1">Detected Tor version: <code>` + esc(st.TorVersion) + `</code> — older than <strong>0.4.7</strong>, so it can't validate today's Tor network. VayuTor is downloading and switching to its <strong>own current Tor</strong> automatically; this takes a moment on first activation.</p>`
				}
			}
		}
		card += `</div>`
		body += card
	}

	// ── Onion address table ──
	body += `<div class="card mt-4"><div class="card-title">🧅 Your onion addresses</div>`
	if len(st.Onions) == 0 {
		if st.Active {
			body += `<div class="empty-state">No onion addresses yet — they appear here within a minute of activation, one per hosted domain.</div>`
		} else {
			body += `<div class="empty-state">Activate above to publish an onion address for every hosted domain. Both the normal URL and its <code>.onion</code> keep working at the same time, with no speed or quality trade-off.</div>`
		}
	} else {
		body += `<p class="muted text-sm mb-3">Each domain has its own <code>.onion</code>. It serves the exact same site as the clearnet URL — both work simultaneously. Share the <code>.onion</code> with privacy-focused visitors; Tor Browser also discovers it automatically (via the <code>Onion-Location</code> header).</p>`
		body += `<div class="vt-onions">`
		for _, o := range st.Onions {
			onionURL := "http://" + o.OnionHost
			body += `<div class="vt-onion">` +
				`<div class="vt-onion__host">` + esc(o.Host) + `</div>` +
				`<div class="vt-onion__row"><code class="vt-onion__addr" data-onion="` + esc(onionURL) + `">` + esc(o.OnionHost) + `</code>` +
				`<button type="button" class="btn btn--sm btn--ghost vt-copy" data-copy="` + esc(onionURL) + `">Copy</button></div>` +
				`</div>`
		}
		body += `</div>`
	}
	body += `</div>`

	body += a.osTorPageStatsCard(esc, st)
	body += a.osTorBridgesCard(r, esc, st)
	body += osTorPrivacyNote(st)
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-tor.js?v=` + assetVer("js/admin-os-tor.js") + `"></script>`
	writeOSHTML(w, adminOSLayout(nonce, "VayuTor", "tor", cfg, htmpl.HTML(body)))
}

// osTorLogRemedy recognises the most common hard bootstrap failures in tor's
// own log and returns a specific, actionable remedy (HTML). "" if nothing known.
func osTorLogRemedy(log string) string {
	low := strings.ToLower(log)
	switch {
	case strings.Contains(low, "not signed by sufficient") ||
		strings.Contains(low, "clock") ||
		strings.Contains(low, "certificate") && strings.Contains(low, "expired"):
		// Tor rejected the network consensus. Overwhelmingly this is a wrong
		// server clock, or a tor package too old to know the current authorities.
		return `⚠ <strong>Tor can't validate the network consensus.</strong> This is almost always the <strong>server clock being off</strong> — even a few minutes of skew breaks it. Enable time sync: <code>sudo timedatectl set-ntp true</code> (check with <code>timedatectl</code>), then it recovers within a minute. If the clock is correct, your <code>tor</code> package may be too old — check <code>tor --version</code> and update it.`
	case strings.Contains(low, "obfs4proxy") ||
		strings.Contains(low, "pluggable transport") ||
		strings.Contains(low, "managed proxy"):
		return `⚠ <strong>The obfs4 bridge transport isn't installed.</strong> Run <code>sudo apt-get install -y obfs4proxy</code> (or re-run the VayuPress updater), then reload — VayuTor picks it up within a minute.`
	case strings.Contains(low, "no route to host") ||
		strings.Contains(low, "noroute"):
		return `⚠ <strong>Your network blocks Tor at the IP level</strong> (common on some VPS/mail hosts). Your firewall is fine — the provider is null-routing Tor relays. VayuTor auto-escalates to <strong>Tor bridges</strong> to route around this. If it stays stuck, get obfs4 bridges from <code>https://bridges.torproject.org</code> (or email <code>bridges@torproject.org</code>) and set <code>VAYUOS_TOR_BRIDGES</code> to those lines, then reload.`
	case strings.Contains(low, "connection refused") ||
		strings.Contains(low, "connection timed out"):
		return `⚠ <strong>The server can't reach the Tor network.</strong> Allow <strong>outbound</strong> connections in your firewall / cloud security group (inbound stays closed). VayuTor also auto-retries using only ports 80/443, then bridges, after a stall.`
	}
	return ""
}

// osTorBridgesCard renders the operator's Tor bridge configuration — the entire
// "network blocks Tor" fix, done from VayuOS with no server access. Bridges are
// saved to settings (KeyTorBridges) and applied live by the engine.
func (a *App) osTorBridgesCard(r *http.Request, esc func(string) string, st vtor.Status) string {
	current := ""
	if a.siteSettings != nil {
		current = a.siteSettings.Get(r.Context(), settings.KeyTorBridges)
	}
	needsObfs4 := strings.Contains(strings.ToLower(current), "obfs4")
	card := `<div class="card mt-4 vt-bridges"><div class="card-title">🌉 Bridges — for networks that block Tor</div>`
	// The obfs4 transport binary is required to USE obfs4 bridges. If it's missing,
	// the bridges are configured but inert (tor falls back to a direct, blocked
	// connection) — say so loudly, since it's the #1 "I pasted bridges but nothing
	// happens" cause.
	if needsObfs4 && !st.Obfs4Available {
		card += `<div class="vt-bridges__warn text-sm mb-3">⚠ The obfs4 transport is unavailable on this server, so these obfs4 bridges can't be used yet. VayuPress normally provides obfs4 built-in — reload the page; if it persists, re-run the VayuPress updater.</div>`
	}
	card += `<p class="muted text-sm mb-3">If your host or ISP blocks Tor (bootstrap stalls with “no route to host” or a consensus error), paste <strong>obfs4 bridge lines</strong> here and VayuTor routes around the block automatically — no server access needed. Get free bridges at <code>https://bridges.torproject.org</code> (choose <strong>obfs4</strong>), or email <code>bridges@torproject.org</code> from Gmail/Riseup with <code>get transport obfs4</code> in the body. Use <strong>IPv4</strong> bridges (addresses like <code>1.2.3.4:443</code>) unless your server has working IPv6 — most don't. One bridge per line.</p>`
	card += `<form method="post" action="/os/tor/bridges" data-tor-form>
  <textarea class="vt-bridges__input" name="bridges" rows="4" spellcheck="false" autocomplete="off" placeholder="obfs4 1.2.3.4:443 FINGERPRINT cert=... iat-mode=0">` + esc(current) + `</textarea>
  <input type="hidden" name="csrf_token" value="">
  <div class="vt-bridges__row"><button type="submit" class="btn btn--primary">Save bridges</button>`
	if bridgesLookIPv6Only(current) {
		card += ` <span class="vt-bridges__warn text-xs">⚠ These are <strong>IPv6</strong> bridges (addresses in <code>[…]</code>). They only connect if your server has working IPv6 — most VPS don't, which shows as “connections died in state connect()ing.” Get <strong>IPv4</strong> obfs4 bridges instead.</span>`
	} else if strings.TrimSpace(current) != "" {
		card += ` <span class="vt-bridges__on muted text-xs">✓ Bridges configured — used automatically when a direct connection is blocked. Clear the box and save to stop using them.</span>`
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
	_ = a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyTorBridges: bridges})
	a.vayuTor.Kick()
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
	_ = a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyTorPageStats: state})
	http.Redirect(w, r, "/os/tor", http.StatusSeeOther)
}

// osTorPrivacyNote states the privacy posture explicitly. The "tracked" line is
// truthful about whether per-page counts are opted in, so the page never claims
// more privacy than is actually in effect.
func osTorPrivacyNote(st vtor.Status) string {
	tracked := `<li><strong>Nothing is tracked.</strong> The only VayuTor metric is a single visit count — no IP (Tor provides none), no time, no path, no user agent, no cookie.</li>`
	if st.PageStatsOn {
		tracked = `<li><strong>Aggregate counts only.</strong> You've opted into per-page counts. VayuTor keeps a running total per page and a single overall visit count — and still <strong>no IP</strong> (Tor provides none), <strong>no time</strong>, no session, no ordering, no user agent, no cookie. Individual visits can't be correlated, and a visitor can't be identified or located.</li>`
	}
	return `<div class="card mt-4 vt-note"><div class="card-title">Privacy posture</div>
<ul class="vt-note__list text-sm">
  ` + tracked + `
  <li><strong>Onion keys are yours.</strong> Each address is pinned by a key stored only in your own database, so a restore brings the same <code>.onion</code> back.</li>
  <li><strong>No new attack surface.</strong> VayuTor opens no inbound ports; onion traffic reaches your server through Tor's rendezvous, and the clearnet site is untouched.</li>
</ul></div>`
}

// osTorPageStatsCard renders the opt-in per-page onion analytics: a privacy-safe
// aggregate "which pages are popular" table (no time, IP, identity, or ordering),
// plus the enable/disable toggle and a reset. Visitor geolocation is impossible
// over an onion service (the server never sees the client IP), so it is stated
// as absent rather than offered.
func (a *App) osTorPageStatsCard(esc func(string) string, st vtor.Status) string {
	card := `<div class="card mt-4 vt-pages"><div class="card-title">📄 Popular pages — private, opt-in</div>`
	if !st.PageStatsOn {
		card += `<p class="muted text-sm mb-3">Off by default. When enabled, VayuTor keeps an <strong>aggregate count per page</strong> (a running total of views) so you can see which posts are popular over Tor. It stays privacy-safe: <strong>no IP</strong> (Tor provides none), no time, no session, no ordering — so it can never identify, locate, or correlate a visitor. <strong>Visitor country isn't shown because it's impossible</strong>: an onion service never sees the visitor's IP.</p>`
		card += `<form method="post" action="/os/tor/pagestats" data-tor-form>
  <input type="hidden" name="on" value="1">
  <input type="hidden" name="csrf_token" value="">
  <button type="submit" class="btn btn--primary">Enable per-page counts</button>
</form>`
		card += `</div>`
		return card
	}
	card += `<p class="muted text-sm mb-3">Aggregate views per page — no IP, time, session, or ordering, so no visit can be traced to a person. Visitor country is deliberately absent (an onion service never sees the client IP).</p>`
	if len(st.TopPages) == 0 {
		card += `<div class="empty-state">No page views counted yet. They appear here as visitors browse your <code>.onion</code>.</div>`
	} else {
		card += `<div class="vt-pages__table"><table class="vt-pages__t"><thead><tr><th>Domain</th><th>Page</th><th class="vt-pages__num">Views</th></tr></thead><tbody>`
		for _, p := range st.TopPages {
			card += `<tr><td>` + esc(p.Host) + `</td><td><code>` + esc(p.Path) + `</code></td><td class="vt-pages__num">` + strconv.FormatInt(p.Count, 10) + `</td></tr>`
		}
		card += `</tbody></table></div>`
	}
	card += `<div class="vt-pages__actions">
  <form method="post" action="/os/tor/pagestats" data-tor-form>
    <input type="hidden" name="on" value="0">
    <input type="hidden" name="csrf_token" value="">
    <button type="submit" class="btn btn--ghost btn--sm">Turn off</button>
  </form>
  <form method="post" action="/os/tor/pagestats" data-tor-form>
    <input type="hidden" name="reset" value="1">
    <input type="hidden" name="csrf_token" value="">
    <button type="submit" class="btn btn--ghost btn--sm">Reset counts</button>
  </form>
</div></div>`
	return card
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
	_ = a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyTorEnabled: state})
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
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"visits":    st.Visits,
		"active":    st.Active,
		"connected": st.Connected,
		"onions":    len(st.Onions),
		"bootstrap": st.BootstrapPct,
	})
}
