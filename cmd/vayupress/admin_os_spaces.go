package main

// admin_os_spaces.go — VayuOS "Spaces" page (/os/spaces), ADR-0141.
//
// Surfaces the two independent worlds a VayuOS operator can run — a Clearnet
// Space (public HTTPS site) and an anonymous Tor Space (.onion) — inside the
// admin. It shows which world THIS install is, explains the model, and (on a
// clearnet install) gives the one command that stands up a separate Tor Space
// alongside it, with best-effort live status. It is a read-only info page: the
// privileged provisioning itself is run by the operator on the server, never by
// the web tier (which is unprivileged), exactly like the setup-*-subdomain
// scripts. Every interpolated value is html-escaped; no inline styles (CSP).

import (
	"html"
	"net/http"
	"os"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// torSpaceSetupCmd is the single operator command that provisions a sibling Tor
// Space (see scripts/setup-tor-space.sh). Shown verbatim in a copy box.
const torSpaceSetupCmd = "sudo bash scripts/setup-tor-space.sh"

// Paths a clearnet install can read to detect a sibling Tor Space. The unit file
// is world-readable; onion.txt is written by setup-tor-space.sh into the Tor
// Space's data dir (same www-data owner, so this install can read it).
const (
	torSpaceUnitPath  = "/etc/systemd/system/vayupress-tor.service"
	torSpaceOnionPath = "/var/lib/vayupress-tor/onion.txt"
)

// iconSpaces is the sidebar glyph (two stacked layers = two worlds).
var iconSpaces = svgIcon("M10 2.5l7.5 4.2-7.5 4.2-7.5-4.2 7.5-4.2zM2.5 11l7.5 4.2 7.5-4.2")

// handleOSSpaces renders the Spaces page. GET-only, read-only.
func (a *App) handleOSSpaces(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	onion := config.Cfg.OnionMode

	body := osSpacesHeader() +
		osSpacesCurrentCard(onion, config.Cfg.Domain) +
		osSpacesModelCard()
	if onion {
		body += osSpacesTorSelfCard(config.Cfg.Domain)
	} else {
		provisioned, sibOnion := torSpaceState()
		body += osSpacesAddTorCard(provisioned, sibOnion)
	}

	full := adminOSShellHead(nonce, "Spaces", "spaces", cfg) +
		body +
		adminOSShellFoot(nonce, osSpacesScript, false)
	writeOSHTML(w, full)
}

// torSpaceState is a best-effort probe of a sibling Tor Space: whether its
// systemd unit exists, and its .onion if the provisioner recorded one. Never
// errors — an absent/unreadable path just yields (false, "").
func torSpaceState() (provisioned bool, onion string) {
	if _, err := os.Stat(torSpaceUnitPath); err == nil {
		provisioned = true
	}
	if b, err := os.ReadFile(torSpaceOnionPath); err == nil { //nosec G304 -- fixed constant path, public onion address
		v := strings.TrimSpace(string(b))
		// Only surface a plausible v3 onion — never render arbitrary file content
		// (and bound what a malformed marker file can put on the page).
		if len(v) <= 128 && strings.HasSuffix(v, ".onion") {
			onion = v
		}
	}
	return provisioned, onion
}

func osSpacesHeader() string {
	return `<div class="page-header">
  <h1>Spaces</h1>
  <div class="page-actions">
    <a class="btn btn--sm" href="/docs/adr/ADR-0141-vayuos-spaces-clearnet-tor" target="_blank" rel="noopener">About Spaces</a>
  </div>
</div>
<p class="text-sm muted mb-4">Run your public world and an anonymous Tor world side by side — two separate installs that share nothing.</p>`
}

// osSpacesCurrentCard shows which world THIS install is (from VAYUOS_MODE).
func osSpacesCurrentCard(onion bool, domain string) string {
	badge := `<span class="space-badge space-badge--clearnet">Clearnet</span>`
	desc := `A public site, blog and mail served over HTTPS on your domain. To make an install a Tor Space instead, set <code>VAYUOS_MODE=tor</code> and give it its own database.`
	if onion {
		badge = `<span class="space-badge space-badge--tor">Tor</span>`
		d := html.EscapeString(domain)
		desc = `An anonymous <code>.onion</code> world (<code>` + d + `</code>): no clearnet domain, no CA-TLS, and every clearnet callback is disabled.`
	}
	return `<div class="card">
  <div class="settings-block-title">This install</div>
  <p class="text-sm muted mb-4">Every VayuOS install runs in exactly one world. This one is a:</p>
  <p>` + badge + `</p>
  <p class="text-sm muted mt-4">` + desc + `</p>
</div>`
}

// osSpacesModelCard explains the two-world "no mesh" model.
func osSpacesModelCard() string {
	return `<div class="card">
  <div class="settings-block-title">Two worlds, no mesh</div>
  <p class="text-sm muted mb-4">VayuOS Spaces are two independent worlds you can run side by side:</p>
  <ul class="text-sm muted">
    <li><strong>Clearnet Space</strong> — your public site, blog and mail on your domain, over HTTPS.</li>
    <li><strong>Tor Space</strong> — an anonymous <code>.onion</code> world with its own database, no clearnet callbacks and no CA-TLS.</li>
  </ul>
  <p class="text-sm muted mt-4">They share <strong>nothing</strong> — separate databases — so the two can never be correlated. Run one, the other, or both at once; each is a separate install selected by <code>VAYUOS_MODE</code>.</p>
</div>`
}

// osSpacesAddTorCard (clearnet installs) surfaces the one-command provisioner and
// best-effort live status of a sibling Tor Space.
func osSpacesAddTorCard(provisioned bool, onion string) string {
	cmd := html.EscapeString(torSpaceSetupCmd)
	status := `<span class="badge badge--muted">Not provisioned yet</span>
  <p class="text-sm muted mt-4">Run the command above once on your server to create it.</p>`
	if provisioned {
		status = `<span class="badge badge--ok">● Tor Space provisioned</span>`
		if onion != "" {
			o := html.EscapeString(onion)
			status += `
  <p class="text-sm muted mt-4">Its address (open in Tor Browser):</p>
  <div class="ak-token-row">
    <input class="input font-mono ak-token-input" type="text" readonly value="http://` + o + `">
    <button type="button" class="btn btn--sm" data-copy="http://` + o + `">Copy</button>
  </div>`
		} else {
			status += `
  <p class="text-sm muted mt-4">Retrieve its address with <code>journalctl -u vayupress-tor</code> on the server.</p>`
		}
	}
	return `<div class="card">
  <div class="settings-block-title">Add a Tor Space</div>
  <p class="text-sm muted mb-4">Stand up a second, fully separate Tor Space alongside this Clearnet one — both run at the same time. Run this once on your server, as root:</p>
  <div class="ak-token-row">
    <input class="input font-mono ak-token-input" type="text" readonly value="` + cmd + `">
    <button type="button" class="btn btn--sm" data-copy="` + cmd + `">Copy</button>
  </div>
  <p class="text-sm muted mt-4">It reuses this install's binary and Tor daemon, provisions a persistent <code>.onion</code>, and starts a separate <code>vayupress-tor</code> service with its own database. Nothing is shared with this Clearnet Space.</p>
  <div class="settings-block-title text-sm mt-4">Status</div>
  ` + status + `
  <p class="text-sm muted mt-4">Remove just the Tor Space service (its identity and data are kept): <code>` + cmd + ` --remove</code></p>
</div>`
}

// osSpacesTorSelfCard (Tor installs) shows this Space's own onion address.
func osSpacesTorSelfCard(domain string) string {
	d := html.EscapeString(domain)
	return `<div class="card">
  <div class="settings-block-title">Your anonymous world</div>
  <p class="text-sm muted mb-4">This install is the Tor Space. Its address:</p>
  <div class="ak-token-row">
    <input class="input font-mono ak-token-input" type="text" readonly value="http://` + d + `">
    <button type="button" class="btn btn--sm" data-copy="http://` + d + `">Copy</button>
  </div>
  <p class="text-sm muted mt-4">Your Clearnet Space, if you run one, is a completely separate install with its own database. Move content between worlds with the offline bundle: <code>vayupress migrate export --file bundle.vaybundle</code></p>
</div>`
}

// osSpacesScript is the nonce-gated page controller (runs inside the shared foot
// IIFE). It only wires the copy buttons — no writes, no network beyond clipboard.
const osSpacesScript = `
document.querySelectorAll('[data-copy]').forEach(function(btn){
  btn.addEventListener('click',function(){
    var val=btn.getAttribute('data-copy')||'';
    var prev=btn.textContent;
    var done=function(){btn.textContent='Copied';setTimeout(function(){btn.textContent=prev;},1400);};
    if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(val).then(done,done);}else{done();}
  });
});`
