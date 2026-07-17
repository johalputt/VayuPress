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

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	vtor "github.com/johalputt/vayupress/internal/vayuos/vayutor"
)

// handleOSTor renders the VayuTor control page.
func (a *App) handleOSTor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	csrf := auth.GenerateCSRFToken()
	if csrf != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: csrf, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

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
	if st.Active && st.Connected {
		stateClass, stateLabel = "vt-state--on", "Active"
	} else if st.Active && !st.Connected {
		stateClass, stateLabel = "vt-state--warn", "Activating…"
	}
	btnLabel, btnKind, nextState := "Activate onion services", "btn--primary", "on"
	if st.Active {
		btnLabel, btnKind, nextState = "Deactivate", "btn--ghost", "off"
	}

	body += `<div class="card vt-hero" data-tor>
  <div class="vt-hero__main">
    <div class="vt-state ` + stateClass + `"><span class="vt-dot"></span> ` + stateLabel + `</div>
    <div class="vt-hero__count"><span class="vt-count" data-tor-visits>` + strconv.FormatInt(st.Visits, 10) + `</span> <span class="muted">Tor visits</span></div>
    <div class="muted text-sm">Count only — no identity, no time, nothing else is ever recorded.</div>
  </div>
  <form class="vt-hero__action" method="post" action="/os/tor/toggle" data-tor-toggle>
    <input type="hidden" name="state" value="` + nextState + `">
    <input type="hidden" name="csrf_token" value="` + esc(csrf) + `">
    <button type="submit" class="btn ` + btnKind + `">` + btnLabel + `</button>
  </form>
</div>`

	// ── Connection guidance when activated but the daemon is unreachable ──
	if st.Active && !st.Connected {
		hint := "Waiting for the local Tor daemon on the control port. If this persists, the server needs Tor installed with its control port enabled — the VayuPress deploy/update script does this automatically (it installs <code>tor</code>, turns on the cookie-authenticated control port, and grants access). Re-run it, then reload."
		if st.LastError != "" {
			hint += `<div class="text-xs muted mt-2">last error: ` + esc(st.LastError) + `</div>`
		}
		body += `<div class="card vt-warn"><div class="card-title">⏳ Bringing onions up…</div><p class="text-sm">` + hint + `</p></div>`
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

	body += osTorPrivacyNote()
	body += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-tor.js?v=` + assetVer("js/admin-os-tor.js") + `"></script>`
	writeOSHTML(w, adminOSLayout(nonce, "VayuTor", "tor", cfg, htmpl.HTML(body)))
}

// osTorPrivacyNote states the privacy posture explicitly.
func osTorPrivacyNote() string {
	return `<div class="card mt-4 vt-note"><div class="card-title">Privacy posture</div>
<ul class="vt-note__list text-sm">
  <li><strong>Nothing is tracked.</strong> The only VayuTor metric is a single visit count — no IP (Tor provides none), no time, no path, no user agent, no cookie.</li>
  <li><strong>Onion keys are yours.</strong> Each address is pinned by a key stored only in your own database, so a restore brings the same <code>.onion</code> back.</li>
  <li><strong>No new attack surface.</strong> VayuTor opens no inbound ports; onion traffic reaches your server through Tor's rendezvous, and the clearnet site is untouched.</li>
</ul></div>`
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
	})
}
