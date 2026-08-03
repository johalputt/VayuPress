// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_settings.go — one hosted domain's own site settings
// (ADR-0153 Phase 3).
//
// This is the first tool that actually writes to a domain's scope, and it is
// deliberately the smallest one: name, tagline, description and author. If the
// mechanism is wrong, it is wrong here, on four fields, rather than across a
// theme editor.
//
// Every value shown is THIS DOMAIN'S. A blank field is blank — it does not
// silently show the primary's value, because a field that displays one thing
// and stores another is how an operator comes to believe the tools are linked.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// scopedSettingKeys is the field set this page owns. Narrow on purpose: every
// key added here is a key that stops being the operator's and starts being the
// client's, and that is a decision per key rather than a bulk import.
var scopedSettingKeys = []struct {
	Key, Label, Hint string
}{
	{settings.KeySiteName, "Site name", "Shown in the browser tab, the header and search results."},
	{settings.KeySiteTagline, "Tagline", "One line under the name."},
	{settings.KeySiteDescription, "Meta description", "The sentence search engines show beneath the link."},
	{settings.KeySiteAuthor, "Author", "The by-line on posts that do not name one."},
}

// handleOSScopedSettings renders one domain's own settings.
func (a *App) handleOSScopedSettings(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	csrfTokenFor(w, r)

	sc := osScope(r)
	esc := html.EscapeString
	body := `<div id="scoped-ctx" data-id="` + esc(d.ID) + `" hidden></div>` +
		`<div class="page-head"><div><h1 class="page-title">Site settings</h1>` +
		`<p class="page-sub"><a href="/os/d/` + esc(d.ID) + `">← ` + esc(d.Host) + `</a></p></div>` +
		`<div class="page-actions"><span id="scoped-status" class="text-sm muted" role="status" aria-live="polite"></span></div></div>` +
		`<p class="page-sub">These belong to <b>` + esc(d.Host) + `</b>. A blank field is blank — this site ` +
		`falls back to the product default, never to the primary site's value.</p>` +
		`<div class="card"><div class="form-grid">`

	for _, f := range scopedSettingKeys {
		val := a.siteSettings.Get(r.Context(), sc, f.Key)
		body += `<label class="field"><span class="field-label">` + esc(f.Label) + `</span>` +
			`<input type="text" class="input" data-scoped-key="` + esc(f.Key) + `" value="` + esc(val) + `" autocomplete="off">` +
			`<span class="field-hint">` + esc(f.Hint) + `</span></label>`
	}

	body += `</div><div class="vm-row"><button type="button" class="btn btn--primary" data-scoped-save>Save</button></div></div>` +
		scopedSettingsScript(nonce)
	writeOSHTML(w, r, adminOSLayout(nonce, "Settings · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}

func scopedSettingsScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var node=document.getElementById('scoped-ctx');
var ID=node?node.getAttribute('data-id'):'';
if(!ID)return;
var st=document.getElementById('scoped-status');
var btn=document.querySelector('[data-scoped-save]');
if(!btn)return;
btn.addEventListener('click',function(){
  var payload={};
  document.querySelectorAll('[data-scoped-key]').forEach(function(el){
    payload[el.getAttribute('data-scoped-key')]=el.value;
  });
  btn.disabled=true; if(st)st.textContent='Saving…';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/settings',{method:'POST',
    headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
    body:JSON.stringify({settings:payload})})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){btn.disabled=false;
      if(st)st.textContent=res.ok?'Saved ✓':((res.j&&res.j.message)||'Could not save');})
    .catch(function(e){btn.disabled=false; if(st)st.textContent='Error: '+e;});
});
})();
</script>`
}

// handleOSScopedSettingsSave writes one domain's settings.
//
// The scope comes from the PATH, never from the body. A body field naming a
// different domain is refused rather than rescoped: silent substitution reports
// success for an attempt to edit someone else's site, which hides both the
// attempt and whatever bug produced it.
func (a *App) handleOSScopedSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	d, ok := osScopedDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return
	}
	var body struct {
		DomainID string            `json:"domain_id"`
		Settings map[string]string `json:"settings"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	if !requireScopeMatchesPath(r, body.DomainID) {
		writeAPIError(w, r, http.StatusBadRequest, "scope-mismatch",
			"this request names a different site from the one in its address; nothing was saved", "")
		return
	}

	// Only the keys this page owns. An allowlist rather than a pass-through:
	// otherwise a client-facing surface built on this endpoint later could write
	// any of the 327 keys, including the operational ones.
	allowed := map[string]bool{}
	for _, f := range scopedSettingKeys {
		allowed[f.Key] = true
	}
	kv := map[string]string{}
	for k, v := range body.Settings {
		if allowed[k] {
			kv[k] = strings.TrimSpace(v)
		}
	}
	if len(kv) == 0 {
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), osScope(r), kv); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "host": d.Host})
}

// copyableFromPrimary is the key set the "start from my house style" action
// copies. Deliberately presentational: theme, typography and head defaults.
//
// It is a COPY, not a link. ADR-0153 D2 chose isolation because inheritance is
// what produced the original complaint — a client site that silently tracked the
// operator's. Copying gives the operator their house style on a new client
// without reintroducing the link: edit the primary tomorrow and the client's
// site does not move.
//
// Identity is excluded on purpose. Copying the primary's site name, tagline,
// description or author onto a client's domain would publish the studio's
// identity on the client's site, which is worse than a wrong colour.
var copyableFromPrimary = []string{
	settings.KeyThemePrimaryLight, settings.KeyThemePrimaryDark,
	settings.KeyThemeAccentLight, settings.KeyThemeAccentDark,
	settings.KeyThemeCustomCSS,
	settings.KeyHeadThemeColor, settings.KeyHeadRobots, settings.KeyHeadKeywords,
	settings.KeyNavItems, settings.KeyFooterConfig,
	settings.KeyHomeHero,
}

// handleOSScopedCopyFromPrimary copies the operator's presentational settings
// into one hosted domain's scope, once.
func (a *App) handleOSScopedCopyFromPrimary(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	d, ok := osScopedDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return
	}
	sc := osScope(r)
	if sc.IsPrimary() || !sc.Valid() {
		// Copying the primary onto itself is a no-op that would look like it did
		// something. Refuse rather than silently succeed.
		writeAPIError(w, r, http.StatusBadRequest, "bad-scope", "this action is for a hosted site", "")
		return
	}

	primary, err := a.siteSettings.GetAll(r.Context(), settings.ForPrimary())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "read-failed", err.Error(), "")
		return
	}
	kv := map[string]string{}
	for _, k := range copyableFromPrimary {
		if v := primary[k]; v != "" {
			kv[k] = v
		}
	}
	if len(kv) == 0 {
		writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "copied": 0})
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), sc, kv); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayudomains.settings.copy_from_primary", dbpkg.AuditActor(r), d.Host,
		strconv.Itoa(len(kv))+" presentational setting(s) copied")
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "copied": len(kv), "host": d.Host})
}
