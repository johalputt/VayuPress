// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_power.go — VayuOS "Power & Maintenance".
//
// A single, safe control room for taking the public site offline and for
// restarting the app:
//
//   - Maintenance mode (on/off): the reversible "power switch". While on, every
//     PUBLIC request gets a clean, premium "under maintenance" page (503); the
//     VayuOS console (/os), the health probes and the operator's own browsing
//     stay live, so the site can always be brought back from the web.
//   - Restart: gracefully restarts the app (systemd's Restart=always brings it
//     straight back), for a quick refresh or after an update.
//   - Shut down: turns maintenance on and then restarts, so the app comes back
//     up with the public site OFF and stays that way until maintenance is turned
//     off again — a "stay offline" that never locks the operator out of /os.
//
// The self-signal path reuses the process's existing SIGTERM graceful-shutdown
// (plugins, Tor space, HTTP server all drain) — we never hard-kill.

import (
	"html"
	htmpl "html/template"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// maintenanceExemptPrefixes are path prefixes that stay reachable while the site
// is in maintenance — the admin console and the operational/well-known surfaces,
// so the operator can always turn maintenance back off and uptime probes keep
// working. Mirrors (a subset of) the shield's bypass list.
var maintenanceExemptPrefixes = []string{
	"/os", "/__vayushield", "/__vayuanalytics", "/.well-known", "/mcp", "/oauth", "/health",
	// A phone's contacts and calendar keep syncing while the site is down.
	"/dav",
	// The typefaces the console stylesheet names, so the maintenance page and
	// the sign-in page set in Inter rather than falling back. Font files only.
	"/static/fonts",
}

// maintenancePathExempt reports whether a path stays reachable while the site is
// in maintenance. Critically this includes the WHOLE /os console — so the login
// page (/os/login), its assets (/os/static) and the Power page (/os/power) all
// keep working and the operator can always turn maintenance back off from the
// web. The prefix match is segment-aware ("/os" matches "/os" and "/os/…" but
// never a public page like "/osborne").
func maintenancePathExempt(p string) bool {
	if p == "/favicon.ico" {
		return true
	}
	for _, pre := range maintenanceExemptPrefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// maintenanceModeOn reports whether the operator has taken the public site down.
func (a *App) maintenanceModeOn(r *http.Request) bool {
	return a.siteSettings != nil && a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyMaintenanceMode) == "on"
}

// maintenanceMiddleware serves the premium maintenance page for public requests
// while maintenance mode is on. It is a near-free pass-through otherwise. The
// admin console, operational endpoints and the operator's own authenticated
// browsing are always let through so the site can be recovered from the web.
func (a *App) maintenanceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.siteSettings == nil || !a.maintenanceModeOn(r) {
			next.ServeHTTP(w, r)
			return
		}
		// The admin console (/os, incl. /os/login and /os/static), the health
		// probes and the well-known/shield/MCP surfaces are always reachable — so
		// the operator can ALWAYS sign in and lift maintenance from the web, and
		// uptime checks keep working. This is the guarantee that VayuOS never goes
		// offline with the public site.
		if maintenancePathExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// A signed-in operator keeps seeing the real site so they can verify a
		// change before lifting maintenance.
		if a.isAdminRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		a.serveMaintenance(w, r)
	})
}

// serveMaintenance writes the premium maintenance page as a 503 with a
// Retry-After, so crawlers treat it as a transient outage (not a dead site).
func (a *App) serveMaintenance(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if a.siteSettings != nil {
		msg = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyMaintenanceMessage)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "120")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(maintenancePageHTML(msg)))
}

// maintenancePageHTML is the page visitors see while the site is down for
// maintenance: a Still Air notice on the console's own stylesheet, the way the
// sign-in page is. Every asset it names stays reachable in maintenance
// (maintenancePathExempt), which TestMaintenancePageHTML checks.
// maintenanceMessageMax is how long the visitors' message may be, in
// characters. It is applied where the message is shown as well as where the
// Power page saves it: the general settings API stores the key too, and a
// limit held by one writer only is not a limit.
const maintenanceMessageMax = 280

// clipMaintenanceMessage trims the message and keeps at most
// maintenanceMessageMax characters. It counts runes: the byte slice it
// replaced cut a character in half in any script beyond ASCII.
func clipMaintenanceMessage(m string) string {
	m = strings.TrimSpace(m)
	if r := []rune(m); len(r) > maintenanceMessageMax {
		m = string(r[:maintenanceMessageMax])
	}
	return m
}

func maintenancePageHTML(message string) string {
	brand := html.EscapeString(config.Cfg.Domain)
	title, foot := "Under maintenance", "Powered by VayuPress"
	if brand != "" {
		title, foot = brand+" — under maintenance", brand+" · powered by VayuPress"
	}
	msg := "We’re making things better behind the scenes and will be back online shortly. Thanks for your patience."
	if m := clipMaintenanceMessage(message); m != "" {
		msg = html.EscapeString(m)
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>` + title + `</title>
<meta name="robots" content="noindex">
<link rel="stylesheet" href="/os/static/css/vayuos.css?v=` + assetVer("css/vayuos.css") + `">
</head><body class="vp-os auth-page" data-ui="still-air" data-theme="auto">
<main class="auth-col"><div class="login-card">
<h1 class="login-title">We’ll be right back</h1>
<p class="login-sub">` + msg + `</p>
<p class="maint-status"><span class="sa-dot sa-dot--warn" aria-hidden="true"></span>Down for maintenance. This page checks again every 30 seconds.</p>
</div>
<p class="maint-foot">` + foot + `</p>
</main>
</body></html>`
}

// ── Admin page + actions ─────────────────────────────────────────────────────

// handleOSPower renders the Power & Maintenance control page.
func (a *App) handleOSPower(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	on := a.maintenanceModeOn(r)
	msg := ""
	if a.siteSettings != nil {
		msg = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyMaintenanceMessage)
	}
	crawlersOff := a.crawlersBlocked(r.Context())
	feedbackAddr := a.feedbackEmail(r.Context())
	writeOSHTML(w, r, settingsLayout(nonce, "Power", "operations", cfg,
		htmpl.HTML(osPowerBody(nonce, on, msg, crawlersOff, feedbackAddr))))
}

// handleOSPowerPreview renders the public maintenance page inside the console so
// the operator can see exactly what visitors get, without taking the site down.
func (a *App) handleOSPowerPreview(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if a.siteSettings != nil {
		msg = a.siteSettings.Get(r.Context(), settings.ForPrimary(), settings.KeyMaintenanceMessage)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(maintenancePageHTML(msg)))
}

// osPowerBody builds the control page. The inline script (nonce'd) drives the
// toggle and the restart/shutdown buttons; all three POST to the CSRF-protected
// /os/api/power/* endpoints.
func osPowerBody(nonce string, on bool, message string, crawlersOff bool, feedbackAddr string) string {
	state := ui.State("ok", "Live")
	siteHint := "Visitors reach the public site normally. Offline, they see a maintenance page and the console stays open."
	siteBtn := `<button type="button" class="btn btn--sm" data-power-toggle data-on="0">Take the site offline</button>`
	if on {
		state = ui.State("warn", "Down for maintenance")
		siteHint = "Visitors see the maintenance page. The console stays open."
		siteBtn = `<button type="button" class="btn btn--primary btn--sm" data-power-toggle data-on="1">Bring the site back</button>`
	}
	crawl, crawlBtn := ui.State("ok", "Allowed"), `<button type="button" class="btn btn--sm" data-crawlers-toggle data-on="0">Block</button>`
	crawlHint := "Google, Bing, GPTBot, ClaudeBot, PerplexityBot and other search and AI crawlers may index the public site."
	if crawlersOff {
		crawl, crawlBtn = ui.State("warn", "Blocked"), `<button type="button" class="btn btn--sm" data-crawlers-toggle data-on="1">Allow</button>`
		crawlHint = "robots.txt disallows everything, known crawlers get a 403, and every public page says noindex."
	}
	field := func(id, key, kind, label, value string) ui.HTML {
		return settingControl(settingField{ID: id, Key: key, Kind: kind, Label: label}, value)
	}
	msg := string(field("mnt-msg", settings.KeyMaintenanceMessage, "textarea", "Message while offline", message))
	msg = strings.Replace(msg, `<textarea `, `<textarea maxlength="`+strconv.Itoa(maintenanceMessageMax)+`" placeholder="We’re upgrading the system and will be back shortly." `, 1)

	page := ui.SettingsPage("Power", state, "",
		ui.Section("The public site", "The console is never taken offline", ui.Rows(
			ui.Row{Label: "Maintenance", Hint: siteHint, Control: ui.HTML(siteBtn)},
			ui.Row{Label: "Message while offline", Hint: "At most " + strconv.Itoa(maintenanceMessageMax) + " characters. Empty shows the default.", ID: "mnt-msg", Control: ui.HTML(msg)},
			ui.Row{Label: "The maintenance page", Hint: "What visitors see, without taking the site down.",
				Control: `<a class="settings-row-go" href="/os/power/preview" target="_blank" rel="noopener">Preview` + ui.Icon("ext") + `</a>`},
			ui.Row{Label: "Search engines and AI crawlers", Hint: crawlHint + " Visitors, the console and VayuMCP are never affected.", Control: crawl + ui.HTML(crawlBtn)},
		)),
		ui.Section("Feedback", "", ui.Rows(
			ui.Row{Label: "Feedback inbox", Hint: "Send feedback, in the account menu, writes a PGP-encrypted report here. Empty sends it to the VayuPress team at feedback@vayupress.com.", ID: "fb-addr",
				Control: field("fb-addr", settings.KeyFeedbackEmail, "email", "Feedback inbox", feedbackAddr)},
		)),
		ui.Section("Restart", "The service restarts itself after each", ui.Rows(
			ui.Row{Label: "Restart the app", Hint: "In-flight requests finish first. Back in a few seconds, live.", Control: `<button type="button" class="btn btn--sm" data-power-restart>Restart</button>`},
			ui.Row{Label: "Shut the site down", Hint: "Maintenance goes on and the app restarts, so it comes back offline and stays so until you bring it back.", Control: `<button type="button" class="btn btn--danger btn--sm" data-power-shutdown>Shut down</button>`},
		)),
		ui.SaveBar(),
	)
	return string(page) + `
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
function toast(msg,kind){if(window.vpToast){window.vpToast(msg,kind);}}
function post(url,body){return fetch(url,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:body?JSON.stringify(body):'{}'});}
var tgl=document.querySelector('[data-power-toggle]');
if(tgl){tgl.addEventListener('click',function(){
  var turnOn=tgl.getAttribute('data-on')!=='1';
  var apply=function(){
  tgl.disabled=true;
  post('/os/api/power/maintenance',{on:turnOn}).then(function(r){return r.json();}).then(function(j){
    if(j&&j.ok){toast(turnOn?'Maintenance mode is ON — the public site is offline.':'Maintenance mode is OFF — your site is live.','ok');setTimeout(function(){location.reload();},700);}
    else{tgl.disabled=false;toast((j&&j.error&&j.error.message)||'Could not change maintenance mode','error');}
  }).catch(function(){tgl.disabled=false;toast('Network error','error');});
  };
  if(turnOn){vpConfirm({title:'Go offline?',message:'Take the public site offline now? Visitors will see the maintenance page. Your admin console stays open.',confirm:'Go offline'},apply);return;}
  apply();
});}
var crawlBtn=document.querySelector('[data-crawlers-toggle]');
if(crawlBtn){crawlBtn.addEventListener('click',function(){
  var block=crawlBtn.getAttribute('data-on')!=='1';
  var apply=function(){
  crawlBtn.disabled=true;
  post('/os/api/power/crawlers',{block:block}).then(function(r){return r.json();}).then(function(j){
    if(j&&j.ok){toast(block?'Search engines & AI crawlers are now blocked.':'Crawlers are allowed again — your site can be indexed.','ok');setTimeout(function(){location.reload();},700);}
    else{crawlBtn.disabled=false;toast((j&&j.error&&j.error.message)||'Could not change crawler access','error');}
  }).catch(function(){crawlBtn.disabled=false;toast('Network error','error');});
  };
  if(block){vpConfirm({title:'Block crawlers?',message:'Block all search engines and AI crawlers now? Your site stops being indexed until you allow crawlers again.',confirm:'Block'},apply);return;}
  apply();
});}
var reBtn=document.querySelector('[data-power-restart]');
if(reBtn){reBtn.addEventListener('click',function(){
  vpConfirm({title:'Restart the app?',message:'Restart the app now? The site is unavailable for a few seconds and comes back automatically.',confirm:'Restart'},function(){
  reBtn.disabled=true;
  post('/os/api/power/restart').then(function(r){return r.json();}).then(function(j){
    toast('Restarting… this page will reconnect in a few seconds.','ok');
    setTimeout(function(){location.reload();},9000);
  }).catch(function(){toast('Restart signal sent — reconnecting shortly.','ok');setTimeout(function(){location.reload();},9000);});
  });
});}
var offBtn=document.querySelector('[data-power-shutdown]');
if(offBtn){offBtn.addEventListener('click',function(){
  vpConfirm({title:'Shut the site down?',message:'Maintenance mode turns ON and the app restarts, coming back with the public site OFF. You can bring it back any time from this page.',confirm:'Shut down'},function(){
  offBtn.disabled=true;
  post('/os/api/power/shutdown').then(function(r){return r.json();}).then(function(j){
    toast('Shutting down to maintenance… reconnecting shortly.','ok');
    setTimeout(function(){location.reload();},9000);
  }).catch(function(){toast('Shutdown signal sent.','ok');setTimeout(function(){location.reload();},9000);});
  });
});}
})();
</script>`
}

// handleOSPowerMaintenance toggles maintenance mode and/or saves the message.
func (a *App) handleOSPowerMaintenance(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "settings unavailable", "")
		return
	}
	var body struct {
		On      *bool   `json:"on"`
		Message *string `json:"message"`
	}
	if err := readCappedJSON(w, r, 4*1024, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "invalid request body", "")
		return
	}
	kv := map[string]string{}
	if body.On != nil {
		if *body.On {
			kv[settings.KeyMaintenanceMode] = "on"
		} else {
			kv[settings.KeyMaintenanceMode] = "off"
		}
	}
	if body.Message != nil {
		kv[settings.KeyMaintenanceMessage] = clipMaintenanceMessage(*body.Message)
	}
	if len(kv) == 0 {
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), kv); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "write_failed", "could not save the setting", "")
		return
	}
	if body.On != nil {
		logging.LogWarn("power", "maintenance mode set to "+kv[settings.KeyMaintenanceMode]+" by admin")
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "maintenance": a.maintenanceModeOn(r)})
}

// handleOSPowerRestart gracefully restarts the app (SIGTERM → the process's own
// graceful shutdown → the service manager restarts it live).
func (a *App) handleOSPowerRestart(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	logging.LogWarn("power", "app restart requested by admin")
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "action": "restart"})
	scheduleSelfShutdown()
}

// handleOSPowerShutdown turns maintenance ON, then restarts — so the app returns
// with the public site offline and stays that way until maintenance is lifted.
func (a *App) handleOSPowerShutdown(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrator access required", "")
		return
	}
	if a.siteSettings != nil {
		_ = a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyMaintenanceMode: "on"})
	}
	logging.LogWarn("power", "app shutdown-to-maintenance requested by admin")
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "action": "shutdown"})
	scheduleSelfShutdown()
}

// scheduleSelfShutdown sends this process SIGTERM after a short grace period, so
// the HTTP response is flushed first. SIGTERM triggers the existing graceful
// shutdown in main() (plugins, Tor space and the HTTP server all drain); the
// service manager (systemd Restart=always) then brings the app back.
func scheduleSelfShutdown() {
	go func() {
		time.Sleep(700 * time.Millisecond)
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
	}()
}
