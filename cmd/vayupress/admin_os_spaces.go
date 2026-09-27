// SPDX-License-Identifier: Apache-2.0

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
	"net/http"

	"github.com/johalputt/vayupress/internal/anonaudit"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// anonAuditInputs snapshots the live anonymity-relevant state for the report.
func anonAuditInputs() anonaudit.Inputs {
	return anonaudit.Inputs{
		OnionMode:               config.Cfg.OnionMode,
		ClearnetEgressBlocked:   safefetch.ClearnetBlocked(),
		LoopbackBind:            config.Cfg.OnionMode, // onionSafeBindAddr binds loopback in Tor mode
		ExternalSMTPConfigured:  config.Cfg.SMTPHost != "" && !safefetch.IsLoopbackHost(config.Cfg.SMTPHost),
		ClearnetDomainSet:       config.Cfg.Domain != "" && config.Cfg.Domain != "localhost",
		BlockedClearnetAttempts: safefetch.BlockedClearnetCount(),
		EgressRoutedOverTor:     safefetch.TorEgressActive(),
	}
}

// osSpacesAnonAuditCard renders the anonymity self-audit — the operator's
// verifiable "is my IP protected?" report (honest: it never claims 100%).
func osSpacesAnonAudit(checks []anonaudit.Check) ui.HTML {
	rows := make([]ui.Row, 0, len(checks))
	for _, c := range checks {
		st := ui.State("neutral", "Note")
		switch c.Status {
		case anonaudit.Pass:
			st = ui.State("ok", "Protected")
		case anonaudit.Warn:
			st = ui.State("warn", "Review")
		case anonaudit.Fail:
			st = ui.State("danger", "At risk")
		}
		rows = append(rows, ui.Row{Label: c.Title, Hint: c.Detail, Control: st})
	}
	return ui.Section("Anonymity self-audit", "What protects your identity now", ui.Join(ui.Rows(rows...),
		ui.HTML(`<p class="muted text-sm mt-3"><a href="/docs/adr/ADR-0143-tor-space-anonymity-model" target="_blank" rel="noopener">What it checks, and what it cannot protect</a></p>`)))
}

// handleOSSpaces renders the Spaces page.
func (a *App) handleOSSpaces(w http.ResponseWriter, r *http.Request) {
	// Admin-only: the Anonymous Tor Space supervises a second server process and
	// mints a persistent onion. The route guard (osPathMinLevel: "spaces") already
	// enforces this; the explicit check is defense in depth, matching
	// handleVayuOSPGP so the handler is safe even if the route were ever remounted.
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, osHome)
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	onion := config.Cfg.OnionMode

	world := ui.State("ok", "Clearnet")
	what := "A public site, blog and mail over HTTPS on your domain."
	if onion {
		world = ui.State("accent", "Tor")
		what = "An anonymous .onion world: no clearnet domain, no CA certificate, and every clearnet callback off."
	}
	secs := []ui.HTML{ui.Section("This install", "Each install is one world", ui.Rows(
		ui.Row{Label: "World", Hint: what, Control: world},
		ui.Row{Label: "Two worlds, no mesh", Hint: "A Clearnet world and a Tor world share nothing, not even a database, so their content and logins can never be linked.",
			Control: `<a class="settings-row-go" href="/docs/adr/ADR-0141-vayuos-spaces-clearnet-tor" target="_blank" rel="noopener">How it works` + ui.Icon("ext") + `</a>`},
	))}
	if onion {
		secs = append(secs, ui.Section("Your anonymous world", "", ui.Rows(onionAddressRow(config.Cfg.Domain))),
			osSpacesAnonAudit(anonaudit.Run(anonAuditInputs())))
	} else {
		secs = append(secs, osSpacesTorSpace(a.torSpaceStatusNow()))
	}
	body := string(ui.SettingsPage("Worlds", world, "Your public world and an anonymous Tor world, side by side.", secs...))

	full := adminOSShellHead(nonce, "Worlds", "spaces", cfg) +
		body +
		adminOSShellFoot(nonce, osSpacesScript, false)
	writeOSHTML(w, r, full)
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
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyTorSpaceEnabled: state}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "write_failed", "could not save the setting", "")
		return
	}
	// Converge promptly: this mints/tears the dedicated onion and starts/stops the
	// child (a background reconcile so the request returns immediately).
	go a.reconcileTorSpace()
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "enabled": enable})
}

// onionAddressRow shows an onion address to open in Tor Browser, with Copy.
func onionAddressRow(host string) ui.Row {
	u := string(ui.Text("http://" + host))
	return ui.Row{Label: "Address", Hint: "Open it in Tor Browser.",
		Control: ui.HTML(`<code class="space-addr">` + u + `</code><button type="button" class="btn btn--sm" data-copy="` + u + `">Copy</button>`)}
}

// osSpacesTorSpace is the Anonymous Tor Space: a second, fully separate
// VayuPress as a .onion world, one button to start or stop it, its address
// once it is published, and the honest limit of what it separates.
func osSpacesTorSpace(st torSpaceStatus) ui.HTML {
	state, btn := ui.State("neutral", "Off"), `<button type="button" class="btn btn--primary btn--sm" data-space-toggle="on">Turn on</button>`
	if st.Enabled {
		btn = `<button type="button" class="btn btn--sm" data-space-toggle="off">Turn off</button>`
		state = ui.State("warn", "Starting")
		if st.Running {
			state = ui.State("ok", "Running")
		}
	}
	rows := []ui.Row{{Label: "Anonymous Tor Space", Hint: "A second, fully separate VayuPress as a .onion world: its own database, accounts and identity. While it is on, the console wears the Tor colour.",
		Control: state + ui.HTML(btn)}}
	switch {
	case st.Enabled && st.Running && st.Onion != "":
		rows = append(rows, onionAddressRow(st.Onion))
	case st.Enabled && st.Onion == "":
		rows = append(rows, ui.Row{Label: "Address", Hint: "Publishing the .onion to the Tor network; the first time takes a couple of minutes.", Control: ui.State("warn", "Publishing")})
	}
	if st.LastErr != "" {
		rows = append(rows, ui.Row{Label: "Last error", Hint: st.LastErr, Control: ui.State("danger", "Failed")})
	}
	rows = append(rows, ui.Row{Label: "What it separates", Hint: "Identity and content, not the machine: both worlds run on this server. Anonymity that survives someone seizing the server needs a separate computer."})
	return ui.Section("An anonymous world", "Beside this one, on the same server", ui.Rows(rows...))
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
    // Success reloads into the new state; a failure says why and gives the
    // button back so the operator can retry.
    if(window.vpPost){window.vpPost('/os/spaces/toggle?enable='+(enable?'1':'0'),{},function(){vpToast(enable?'Anonymous Tor Space starting…':'Tor Space stopping…','ok');setTimeout(function(){location.reload();},650);},function(d,msg){btn.disabled=false;vpToast(msg,'error');});}
    else{btn.disabled=false;}
  });
});`
