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

// scopedColourKeys is this site's own colour, kept SEPARATE from the identity
// set above because the Identity tile counts that set — folding colour into it
// would make "4 of 7" mean something no label explains.
//
// COLOUR LIVES HERE because the per-site Theme Studio was retired. ADR-0154 D3
// named "/os/d/{id}/settings the only editor for identity, and Theme Studio the
// only editor for colour". The second half never worked for a hosted site: that
// page's script posts to absolute /os/api/... routes, so its writes landed on
// the primary, and theme_tokens is CHECK(id=1) regardless. Retiring it would
// have left a hosted site's colours with no editor at all.
//
// These are the three the public render path reads per domain
// (siteSettingsFromValues) and the same three saveBrand writes, so the
// operator's editor and the client's own /os/mysite now agree on one store.
var scopedColourKeys = []struct {
	Key, Label, Hint string
}{
	{settings.KeyThemeAccentLight, "Accent (light)", "Hex like #2563eb. Links and buttons in the light theme."},
	{settings.KeyThemeAccentDark, "Accent (dark)", "Hex like #60a5fa. The same, for the dark theme."},
	{settings.KeyHeadThemeColor, "Browser theme colour", "Hex. Tints the browser chrome on mobile."},
}

// scopedEditableKeys is every key this page may write — identity and colour.
// The save allowlist reads from here so a field added to either group is
// editable without a second edit somewhere else.
func scopedEditableKeys() []struct{ Key, Label, Hint string } {
	out := make([]struct{ Key, Label, Hint string }, 0, len(scopedSettingKeys)+len(scopedColourKeys))
	out = append(out, scopedSettingKeys...)
	return append(out, scopedColourKeys...)
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
	values := map[string]string{}
	for _, f := range scopedEditableKeys() {
		values[f.Key] = a.siteSettings.Get(r.Context(), sc, f.Key)
	}

	// Whether this domain already carries presentation of its own, so the tile and
	// the band chip describe one state rather than each computing its own. Read
	// rather than assumed: the copy action below is one way to arrive here and the
	// Theme Studio is another, and a chip saying "product default" over a site with
	// a custom theme would be a claim defect.
	//
	// A FAILED read is its own answer. Collapsing the error into `false` would put
	// "Default" on the tile as a statement of fact about a site nobody managed to
	// look at — the error path is exactly where confident wrong answers come from.
	pres := presUnknown
	if all, err := a.siteSettings.GetAll(r.Context(), sc); err == nil {
		pres = presDefault
		for _, k := range copyableFromPrimary {
			if strings.TrimSpace(all[k]) != "" {
				pres = presCustom
				break
			}
		}
	}

	body := scopedSettingsBody(d.ID, d.Host, values, pres) + scopedSettingsScript(nonce)
	writeOSHTML(w, r, adminOSLayout(nonce, "Settings · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}

// scopedSettingsBody renders one domain's settings page.
//
// Extracted from the handler for the reason the SEO and traffic pages were:
// markup built inline against a request and a live settings store cannot be
// rendered in a test, so a restyling of it cannot be checked.
//
// The copy-from-primary control lives here because until now it lived nowhere.
// The endpoint has been routed and unit-tested since ADR-0153 and the only way
// to reach it was to craft a POST by hand — the same defect as the eval opt-in,
// which shipped in the config and in the connector with no control on any page.
// A capability an operator cannot find is a capability the product does not have.
// presentationState is tri-state on purpose. "This site is on the product
// default" and "nobody could read what this site is on" are different facts, and
// a bool has nowhere to put the second one.
type presentationState int

const (
	presUnknown presentationState = iota
	presDefault
	presCustom
)

func scopedSettingsBody(domainID, host string, values map[string]string, pres presentationState) string {
	esc := html.EscapeString
	var b strings.Builder

	b.WriteString(`<div id="scoped-ctx" data-id="` + esc(domainID) + `" hidden></div>` +
		`<div class="page-header"><h1>Site settings</h1>` +
		`<div class="page-actions"><span id="scoped-status" class="text-sm muted" role="status" aria-live="polite"></span></div></div>` +
		`<p class="page-sub"><a href="/os/d/` + esc(domainID) + `">← ` + esc(host) + `</a></p>` +
		`<p class="page-sub">These belong to <b>` + esc(host) + `</b>. A blank field is blank — this site ` +
		`falls back to the product default, never to the primary site's value.</p>`)

	// ── Four tiles ────────────────────────────────────────────────────────────
	set := 0
	for _, f := range scopedSettingKeys {
		if strings.TrimSpace(values[f.Key]) != "" {
			set++
		}
	}
	total := len(scopedSettingKeys)

	identityTone := ""
	if set == 0 {
		identityTone = "warn"
	}
	// Of the four gaps this page can have, the meta description is the one with a
	// cost attached: it is the sentence shown beneath the link, and with nothing
	// set a search engine writes its own out of whatever it finds on the page.
	descValue, descTone := "Set", ""
	if strings.TrimSpace(values[settings.KeySiteDescription]) == "" {
		descValue, descTone = "Missing", "warn"
	}
	authorValue := "Default"
	if strings.TrimSpace(values[settings.KeySiteAuthor]) != "" {
		authorValue = "Set"
	}
	// "—" rather than a guess, matching the mailbox tile's existing idiom for a
	// number this console cannot currently state.
	presentationValue := "—"
	switch pres {
	case presDefault:
		presentationValue = "Default"
	case presCustom:
		presentationValue = "Custom"
	}

	b.WriteString(`<div class="stat-grid">` +
		osStatTile("Identity", strconv.Itoa(set)+" of "+strconv.Itoa(total), identityTone) +
		osStatTile("Search description", descValue, descTone) +
		osStatTile("Author by-line", authorValue, "") +
		osStatTile("Presentation", presentationValue, "") +
		`</div>`)

	// ── The bands (house style §11) ───────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">What this site says it is</span>` +
		`<span class="section-head__hint">` + esc(host) + ` only</span></div>`)
	b.WriteString(`<div class="mon-stack">`)

	var ident strings.Builder
	ident.WriteString(`<div class="card"><div class="form-grid">`)
	for _, f := range scopedSettingKeys {
		ident.WriteString(`<label class="field"><span class="field-label">` + esc(f.Label) + `</span>` +
			`<input type="text" class="input" data-scoped-key="` + esc(f.Key) + `" value="` + esc(values[f.Key]) +
			`" autocomplete="off"><span class="field-hint">` + esc(f.Hint) + `</span></label>`)
	}
	ident.WriteString(`</div><div class="vm-row">` +
		`<button type="button" class="btn btn--primary btn--sm" data-scoped-save>Save</button></div></div>`)

	identChip := `<span class="mon-chip mon-chip--off">nothing set</span>`
	if set > 0 {
		identChip = `<span class="mon-chip mon-chip--on">` + strconv.Itoa(set) + ` of ` + strconv.Itoa(total) + `</span>`
	}
	b.WriteString(monAcc("🪪", "Identity", "Name, tagline, description and by-line for this site alone",
		identChip, true, ident.String()))

	// Colour, which used to live on the per-site Theme Studio — a page whose
	// every write landed on the operator's own install. Same store, same save
	// button, so an operator sets a client's accent where they set its name.
	var colour strings.Builder
	colour.WriteString(`<div class="card"><div class="form-grid">`)
	colourSet := 0
	for _, f := range scopedColourKeys {
		if strings.TrimSpace(values[f.Key]) != "" {
			colourSet++
		}
		colour.WriteString(`<label class="field"><span class="field-label">` + esc(f.Label) + `</span>` +
			`<input type="text" class="input" data-scoped-key="` + esc(f.Key) + `" value="` + esc(values[f.Key]) +
			`" autocomplete="off" placeholder="#2563eb"><span class="field-hint">` + esc(f.Hint) + `</span></label>`)
	}
	colour.WriteString(`</div><div class="vm-row">` +
		`<button type="button" class="btn btn--primary btn--sm" data-scoped-save>Save</button></div>` +
		`<p class="text-sm muted">Left blank, this site uses the product default — not the ` +
		`operator's colours. Presets and typography are install-wide and are not edited here.</p></div>`)

	// "no colour set", NOT "product default": the Presentation band already uses
	// that phrase for a different question, and two chips saying the same words
	// about different things is how a reader stops trusting either.
	colourChip := `<span class="mon-chip mon-chip--off">no colour set</span>`
	if colourSet > 0 {
		colourChip = `<span class="mon-chip mon-chip--on">` + strconv.Itoa(colourSet) +
			` of ` + strconv.Itoa(len(scopedColourKeys)) + `</span>`
	}
	b.WriteString(monAcc("🎨", "Colour", "This site's own accents and browser tint",
		colourChip, false, colour.String()))

	styleChip := `<span class="mon-chip mon-chip--off">not known</span>`
	switch pres {
	case presDefault:
		styleChip = `<span class="mon-chip mon-chip--off">product default</span>`
	case presCustom:
		styleChip = `<span class="mon-chip mon-chip--on">custom</span>`
	}
	b.WriteString(monAcc("🎨", "Start from your house style",
		"Copies theme, navigation and footer across — once", styleChip, false,
		`<div class="card"><div class="settings-block-title">Copy presentation from your own site</div>`+
			`<p class="text-sm muted">Theme colours, custom CSS, head defaults, navigation, footer and the home `+
			`hero are copied from your primary site onto <b>`+esc(host)+`</b>. It is a <b>copy, not a link</b>: `+
			`editing your own site tomorrow leaves this one exactly where it is.</p>`+
			`<p class="text-sm muted">Name, tagline, description and by-line are <b>not</b> copied. Publishing `+
			`your own identity on somebody else's domain is a worse outcome than a wrong colour.</p>`+
			`<div class="vm-row"><button type="button" class="btn btn--primary btn--sm" data-scoped-copy-style>`+
			`Copy presentation</button><span id="scoped-style-status" class="text-sm muted" role="status" `+
			`aria-live="polite"></span></div></div>`))

	b.WriteString(`</div>`) // mon-stack
	return b.String()
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
if(btn)btn.addEventListener('click',function(){
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
// The copy action is wired independently of the save button. Chaining it behind
// an early return on [data-scoped-save] is how one missing control silently
// takes the other one with it.
var cpy=document.querySelector('[data-scoped-copy-style]');
var cst=document.getElementById('scoped-style-status');
if(cpy)cpy.addEventListener('click',function(){
  cpy.disabled=true; if(cst)cst.textContent='Copying…';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/copy-from-primary',{method:'POST',
    headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){cpy.disabled=false;
      if(!cst)return;
      if(!res.ok){cst.textContent=(res.j&&res.j.message)||'Could not copy';return;}
      var n=(res.j&&typeof res.j.copied==='number')?res.j.copied:0;
      cst.textContent=n?(n+' setting(s) copied — reload to see them'):'Nothing to copy: your own site is still on the defaults';})
    .catch(function(e){cpy.disabled=false; if(cst)cst.textContent='Error: '+e;});
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
	for _, f := range scopedEditableKeys() {
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
