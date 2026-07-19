package main

// admin_os_spaces.go — VayuOS "Spaces" page (/os/spaces), ADR-0141.
//
// One place to run and control the two worlds. On a normal (clearnet) install it
// offers a ONE-CLICK toggle for an Anonymous Tor Space: a second, fully isolated
// VayuPress the parent supervises as a child — its own database, accounts and
// .onion — with no terminal. Toggling it on shifts the whole VayuOS chrome to the
// Tor (purple) palette. Every interpolated value is html-escaped; the toggle
// writes through the shared CSRF-checked vpPost helper (no inline styles — CSP).

import (
	"html"
	"net/http"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// handleOSSpaces renders the Spaces page.
func (a *App) handleOSSpaces(w http.ResponseWriter, r *http.Request) {
	// Admin-only: the Anonymous Tor Space supervises a second server process and
	// mints a persistent onion. The route guard (osPathMinLevel: "spaces") already
	// enforces this; the explicit check is defense in depth, matching
	// handleVayuOSPGP so the handler is safe even if the route were ever remounted.
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os")
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	onion := config.Cfg.OnionMode

	body := osSpacesHeader() +
		osSpacesCurrentCard(onion, config.Cfg.Domain) +
		osSpacesModelCard()
	if onion {
		// This whole install already IS the Tor world.
		body += osSpacesTorSelfCard(config.Cfg.Domain)
	} else {
		// A clearnet install can spin up a separate Anonymous Tor Space child.
		body += osSpacesTorSpaceCard(a.torSpaceStatusNow())
	}

	full := adminOSShellHead(nonce, "Spaces", "spaces", cfg) +
		body +
		adminOSShellFoot(nonce, osSpacesScript, false)
	writeOSHTML(w, full)
}

// handleOSSpaceToggle flips the Anonymous Tor Space on/off and converges the
// child immediately. CSRF-checked; the client reloads to pick up the new state
// (and the Tor colour theme).
func (a *App) handleOSSpaceToggle(w http.ResponseWriter, r *http.Request) {
	// Admin-only (defense in depth over the route guard): starting/stopping the
	// anonymous world is more privileged than Update & Backup or Storage.
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "settings unavailable", "")
		return
	}
	enable := r.URL.Query().Get("enable") == "1"
	state := "off"
	if enable {
		state = "on"
	}
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyTorSpaceEnabled: state}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "write_failed", "could not save the setting", "")
		return
	}
	// Converge promptly: this mints/tears the dedicated onion and starts/stops the
	// child (a background reconcile so the request returns immediately).
	go a.reconcileTorSpace()
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "enabled": enable})
}

func osSpacesHeader() string {
	return `<div class="page-header">
  <h1>Spaces</h1>
  <div class="page-actions">
    <a class="btn btn--sm" href="/docs/adr/ADR-0141-vayuos-spaces-clearnet-tor" target="_blank" rel="noopener">About Spaces</a>
  </div>
</div>
<p class="text-sm muted mb-4">Run your public world and an anonymous Tor world side by side.</p>`
}

// osSpacesCurrentCard shows which world THIS install is (from VAYUOS_MODE).
func osSpacesCurrentCard(onion bool, domain string) string {
	badge := `<span class="space-badge space-badge--clearnet">Clearnet</span>`
	desc := `A public site, blog and mail served over HTTPS on your domain.`
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
  <ul class="text-sm muted">
    <li><strong>Clearnet Space</strong> — your public site, blog and mail on your domain, over HTTPS.</li>
    <li><strong>Tor Space</strong> — an anonymous <code>.onion</code> world with its own database, no clearnet callbacks and no CA-TLS.</li>
  </ul>
  <p class="text-sm muted mt-4">They share <strong>nothing</strong> — separate databases — so their content and logins can never be linked.</p>
</div>`
}

// osSpacesTorSpaceCard is the one-click Anonymous Tor Space control + live status.
func osSpacesTorSpaceCard(st torSpaceStatus) string {
	label, kind, next := "Turn on Tor Space", "btn--primary", "on"
	statusPill := `<span class="badge badge--muted">Off</span>`
	if st.Enabled {
		label, kind, next = "Turn off Tor Space", "btn--ghost", "off"
		if st.Running {
			statusPill = `<span class="badge badge--ok">● Running</span>`
		} else {
			statusPill = `<span class="badge badge--warn">● Starting…</span>`
		}
	}

	extra := ""
	if st.Enabled && st.Running && st.Onion != "" {
		o := html.EscapeString(st.Onion)
		extra += `
  <p class="text-sm muted mt-4">Your anonymous address (open in Tor Browser):</p>
  <div class="ak-token-row">
    <input class="input font-mono ak-token-input" type="text" readonly value="http://` + o + `">
    <button type="button" class="btn btn--sm" data-copy="http://` + o + `">Copy</button>
  </div>`
	} else if st.Enabled && st.Onion == "" {
		extra += `
  <p class="text-sm muted mt-4">Publishing your <code>.onion</code> to the Tor network — the first time this takes a couple of minutes.</p>`
	}
	if st.LastErr != "" {
		extra += `
  <p class="text-sm mt-2" role="status">⚠ ` + html.EscapeString(st.LastErr) + `</p>`
	}

	return `<div class="card">
  <div class="settings-block-title">Anonymous Tor Space</div>
  <p class="text-sm muted mb-4">One click runs a <strong>second, fully separate</strong> VayuPress as an anonymous <code>.onion</code> world — its own database, accounts and identity, no terminal. While it is on, VayuOS wears the Tor colour so you always know.</p>
  <div class="ak-cred-actions">
    <button type="button" class="btn ` + kind + `" data-space-toggle="` + next + `">` + label + `</button>
    ` + statusPill + `
  </div>` + extra + `
  <p class="text-sm muted mt-4"><strong>Honest note:</strong> both worlds run on this same server, so this separates your <em>identity and content</em> — not the machine. Anonymity that survives someone seizing the server needs a physically separate computer.</p>
</div>`
}

// osSpacesTorSelfCard (whole-install Tor installs) shows this Space's own onion.
func osSpacesTorSelfCard(domain string) string {
	d := html.EscapeString(domain)
	return `<div class="card">
  <div class="settings-block-title">Your anonymous world</div>
  <p class="text-sm muted mb-4">This install is the Tor Space. Its address:</p>
  <div class="ak-token-row">
    <input class="input font-mono ak-token-input" type="text" readonly value="http://` + d + `">
    <button type="button" class="btn btn--sm" data-copy="http://` + d + `">Copy</button>
  </div>
</div>`
}

// osSpacesScript wires the copy buttons and the Tor Space toggle. It runs inside
// the shared foot IIFE, so window.vpPost (CSRF header + reload-on-success) exists.
const osSpacesScript = `
document.querySelectorAll('[data-copy]').forEach(function(btn){
  btn.addEventListener('click',function(){
    var val=btn.getAttribute('data-copy')||'';
    var prev=btn.textContent;
    var done=function(){btn.textContent='Copied';setTimeout(function(){btn.textContent=prev;},1400);};
    if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(val).then(done,done);}else{done();}
  });
});
document.querySelectorAll('[data-space-toggle]').forEach(function(btn){
  btn.addEventListener('click',function(){
    var enable=btn.getAttribute('data-space-toggle')==='on';
    btn.disabled=true;
    // vpPost reloads the page ~650ms after a successful POST, so this re-enable
    // only ever fires when the POST FAILED (403/500/network) and the page stayed
    // put — letting the operator retry instead of being stuck with a dead button.
    setTimeout(function(){btn.disabled=false;},4000);
    if(window.vpPost){window.vpPost('/os/spaces/toggle?enable='+(enable?'1':'0'),function(){return enable?'Anonymous Tor Space starting…':'Tor Space stopping…';});}
    else{btn.disabled=false;}
  });
});`
