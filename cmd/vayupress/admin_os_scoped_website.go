// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_website.go — what one hosted domain SERVES, and its website
// content (ADR-0154 D9).
//
// The serving side has been per-domain since ADR-0132 Stage 2b: siteSourceFor
// resolves the active domain and returns that site's own mode, template and
// content, with a deliberate rule that a secondary with no override serves its
// OWN blog rather than inheriting the primary's website — inheriting is what
// once made every client domain serve the studio's bundle.
//
// What was missing is the admin side. /os/website reads bizSettings(r), which
// resolves by REQUEST HOST, and an operator's admin request carries no secondary
// host — so it always edited the primary. A hosted domain's mode and content
// were reachable only by the CLI or by hand. Same shape as the content gap: the
// scoping existed underneath and nothing surfaced it.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// scopedSiteModes is what a hosted domain can serve at "/".
//
// "custom" is deliberately absent: a custom bundle is an upload, not a choice,
// and offering it as a radio button an operator can select with nothing
// uploaded would put a domain into a mode that serves a 404.
var scopedSiteModes = []struct{ Value, Label, Note string }{
	{"blog", "Blog", "The classic VayuPress blog at /. This is what a site serves if you choose nothing."},
	{"business", "Website", "A business website at /, with the blog moved to blog.<host>."},
	{"business_subpath", "Website + /blog", "A business website at /, with the blog at /blog on the same host."},
}

// scopedSiteMode reports the mode a domain is actually serving, resolving the
// blank override to what it MEANS rather than leaving the page to guess.
//
// A blank mode is not "unset" to a visitor — it serves the blog. Rendering the
// radio group with nothing selected would tell an operator their site serves
// nothing, which is the kind of gap between what a page shows and what the
// server does that this whole ADR exists to close.
func scopedSiteMode(d domain.Domain) string {
	if s, ok := d.Site(); ok && s.Mode != "" {
		return s.Mode
	}
	return "blog"
}

func (a *App) handleOSScopedWebsite(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	csrfTokenFor(w, r)

	site, _ := d.Site()
	content := bizsite.ParseContent(site.Content)
	tpl := bizsite.ByKey(site.Template)
	if content.Name == "" && content.Tagline == "" {
		content = tpl.Defaults
	}
	man := customsite.ReadManifest(scopedBundleDir(d))
	body := scopedWebsitePage(d, tpl.Key, content, customsite.Deployed(scopedBundleDir(d)), man) +
		scopedWebsiteScript(nonce)
	writeOSHTML(w, r, adminOSLayout(nonce, "Website · "+d.Host, "optimize", cfg, htmpl.HTML(body)))
}

func scopedWebsitePage(d domain.Domain, tplKey string, c bizsite.Content, bundled bool, man customsite.Manifest) string {
	esc := html.EscapeString
	mode := scopedSiteMode(d)
	var b strings.Builder

	b.WriteString(`<div id="scoped-ctx" data-id="` + esc(d.ID) + `" hidden></div>`)
	b.WriteString(`<div class="page-header"><h1>Website</h1><div class="page-actions">` +
		`<a class="btn btn--ghost btn--sm" href="` + esc(seo.Origin(d.Host)) +
		`" target="_blank" rel="noopener noreferrer">View site ↗</a>` +
		`<a class="btn btn--ghost btn--sm" href="/os/d/` + esc(d.ID) + `">← ` + esc(d.Host) + `</a>` +
		`<button type="button" class="btn btn--primary btn--sm" data-site-web-save>Save &amp; publish</button>` +
		`<span id="scoped-web-status" class="text-sm muted" role="status" aria-live="polite"></span>` +
		`</div></div>`)
	b.WriteString(`<p class="page-sub">What <b>` + esc(d.Host) + `</b> serves at its root, and the content of ` +
		`that site. This domain only — the primary's website is edited from your own Website settings.</p>`)

	// ── What this domain serves ──────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">What this domain serves</span>` +
		`<span class="section-head__hint">Changes take effect within seconds</span></div>`)
	b.WriteString(`<div class="card"><div class="form-grid">`)
	for _, m := range scopedSiteModes {
		checked := ""
		if m.Value == mode {
			checked = " checked"
		}
		b.WriteString(`<label class="field field--check"><input type="radio" name="scoped-site-mode" ` +
			`value="` + esc(m.Value) + `"` + checked + `> <span class="field-label">` + esc(m.Label) + `</span>` +
			`<span class="field-hint">` + esc(m.Note) + `</span></label>`)
	}
	if bundled {
		checked := ""
		if mode == "custom" {
			checked = " checked"
		}
		b.WriteString(`<label class="field field--check"><input type="radio" name="scoped-site-mode" ` +
			`value="custom"` + checked + `> <span class="field-label">Uploaded website</span>` +
			`<span class="field-hint">The site you uploaded or had built — served exactly as authored, at /. ` +
			`` + esc(itoaSafe(man.Files)) + ` file(s), deployed ` + esc(man.DeployedAt.Format("2006-01-02 15:04")) +
			`.</span></label>`)
	}
	b.WriteString(`</div></div>`)

	// ── A whole site of your own ─────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">A whole site of your own</span>` +
		`<span class="section-head__hint">When a template is not what you want</span></div>`)
	b.WriteString(`<div class="card">
  <div class="settings-block-title">Upload a website</div>
  <p class="text-sm muted">A <code>.zip</code> of a complete static site — <code>index.html</code> at its root,
    with whatever CSS, JavaScript, images and fonts it needs beside it. It is served exactly as authored, so a
    hand-built page looks like a hand-built page. Up to 50&nbsp;MiB unpacked, 3000 files.</p>
  <p class="text-sm muted">Each deploy is atomic and keeps the one before it, so a bad publish is one click from
    being undone.</p>
  <div class="vm-row">
    <input type="file" id="scoped-bundle-file" class="input" accept=".zip,application/zip">
    <button type="button" class="btn btn--primary btn--sm" data-bundle-upload>Upload &amp; deploy</button>` +
		func() string {
			if !bundled || !man.HasPrev {
				return ""
			}
			return `<button type="button" class="btn btn--ghost btn--sm" data-bundle-rollback>Restore previous</button>`
		}() + `
    <span id="scoped-bundle-status" class="text-sm muted" role="status" aria-live="polite"></span>
  </div>
  <p class="text-sm muted">Or have one written for you: ask an assistant through <a href="/os/vayumcp">VayuMCP</a>
    to <em>build a site for ` + esc(d.Host) + `</em>. It authors the HTML and CSS itself and publishes it here —
    the same deploy path as an upload, with the same limits.</p>
</div>`)

	// ── Design ────────────────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Design</span>` +
		`<span class="section-head__hint">Used when this domain serves a website</span></div>`)
	b.WriteString(`<div class="card"><label class="field"><span class="field-label">Template</span>` +
		`<select class="input" id="scoped-web-template">`)
	for _, t := range bizsite.All() {
		sel := ""
		if t.Key == tplKey {
			sel = " selected"
		}
		b.WriteString(`<option value="` + esc(t.Key) + `"` + sel + `>` + esc(t.Name) + ` — ` + esc(t.Category) + `</option>`)
	}
	b.WriteString(`</select><span class="field-hint">Each template is a complete design. Switching one keeps ` +
		`your content.</span></label></div>`)

	// ── Content ───────────────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Content</span>` +
		`<span class="section-head__hint">What the website says</span></div>`)
	field := func(id, label, hint, val string) string {
		return `<label class="field"><span class="field-label">` + esc(label) + `</span>` +
			`<input type="text" class="input" id="` + id + `" value="` + esc(val) + `" autocomplete="off">` +
			`<span class="field-hint">` + esc(hint) + `</span></label>`
	}
	b.WriteString(`<div class="card"><div class="form-grid">`)
	b.WriteString(field("web-name", "Business name", "The name across the top of the site.", c.Name))
	b.WriteString(field("web-tagline", "Tagline", "One line under the name.", c.Tagline))
	b.WriteString(`<label class="field"><span class="field-label">About</span>` +
		`<textarea class="input" id="web-about" rows="4">` + esc(c.About) + `</textarea>` +
		`<span class="field-hint">A paragraph or two. Plain text.</span></label>`)
	b.WriteString(field("web-phone", "Phone", "Optional.", c.Phone))
	b.WriteString(field("web-email", "Email", "Optional.", c.Email))
	b.WriteString(field("web-address", "Address", "Optional.", c.Address))
	b.WriteString(field("web-hours", "Opening hours", "Optional.", c.Hours))
	b.WriteString(field("web-cta", "Button label", "The hero button, e.g. “Book a table”.", c.CTA))
	b.WriteString(field("web-ctalink", "Button link", "Where the hero button goes.", c.CTALink))
	b.WriteString(field("web-heroimg", "Hero image URL", "Optional. Left blank, the template's own art is used.", c.HeroImg))
	blogChecked := ""
	if c.ShowBlog {
		blogChecked = " checked"
	}
	b.WriteString(`<label class="field field--check"><input type="checkbox" id="web-showblog"` + blogChecked + `> ` +
		`<span class="field-label">Link the blog from this website</span>` +
		`<span class="field-hint">Adds the blog to the navigation and footer.</span></label>`)
	b.WriteString(`</div></div>`)

	// The honest note about what this page does not edit.
	b.WriteString(`<div class="card"><p class="text-sm muted">Services and gallery are not edited here yet — ` +
		`they are preserved exactly as they are when you save, so nothing you set elsewhere is lost. The ` +
		`fastest way to build a whole site is to ask an AI assistant through <a href="/os/vayumcp">VayuMCP</a>: ` +
		`it can read and write every field on this page for any site you host.</p></div>`)
	return b.String()
}

func scopedWebsiteScript(nonce string) string {
	return `<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var node=document.getElementById('scoped-ctx');
var ID=node?node.getAttribute('data-id'):'';
if(!ID)return;
var st=document.getElementById('scoped-web-status');
function v(id){var e=document.getElementById(id);return e?e.value.trim():'';}
var btn=document.querySelector('[data-site-web-save]');
if(!btn)return;
btn.addEventListener('click',function(){
  var m=document.querySelector('input[name="scoped-site-mode"]:checked');
  var sb=document.getElementById('web-showblog');
  var payload={mode:m?m.value:'blog',template:v('scoped-web-template'),content:{
    name:v('web-name'),tagline:v('web-tagline'),about:v('web-about'),
    phone:v('web-phone'),email:v('web-email'),address:v('web-address'),hours:v('web-hours'),
    cta:v('web-cta'),ctaLink:v('web-ctalink'),heroImg:v('web-heroimg'),
    showBlog:!!(sb&&sb.checked)}};
  btn.disabled=true; if(st)st.textContent='Saving…';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/website',{method:'POST',
    headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
    body:JSON.stringify(payload)})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){btn.disabled=false;
      if(st)st.textContent=res.ok?'Published ✓':((res.j&&res.j.message)||'Could not save');})
    .catch(function(e){btn.disabled=false; if(st)st.textContent='Error: '+e;});
});
var up=document.querySelector('[data-bundle-upload]');
if(up)up.addEventListener('click',function(){
  var f=document.getElementById('scoped-bundle-file');
  if(!f||!f.files||!f.files.length){if(st)st.textContent='Choose a .zip first.';return;}
  var fd=new FormData(); fd.append('bundle', f.files[0]);
  up.disabled=true; if(st)st.textContent='Uploading\u2026';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/website/bundle',{method:'POST',
    headers:{'X-CSRF-Token':csrf()}, body:fd})
    .then(function(r){return r.json().then(function(j){return {ok:r.ok,j:j};});})
    .then(function(res){up.disabled=false;
      if(res.ok){if(st)st.textContent='Deployed \u2713 '+(res.j.files||0)+' file(s) \u2014 reloading';window.location.reload();return;}
      if(st)st.textContent=(res.j&&res.j.message)||'Could not deploy that bundle';})
    .catch(function(e){up.disabled=false; if(st)st.textContent='Error: '+e;});
});
var rb=document.querySelector('[data-bundle-rollback]');
if(rb)rb.addEventListener('click',function(){
  if(!window.confirm('Restore the previous uploaded website?'))return;
  rb.disabled=true; if(st)st.textContent='Restoring\u2026';
  fetch('/os/d/'+encodeURIComponent(ID)+'/api/website/bundle/rollback',{method:'POST',
    headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){if(r.ok){window.location.reload();return;}
      rb.disabled=false; if(st)st.textContent='Could not restore';})
    .catch(function(e){rb.disabled=false; if(st)st.textContent='Error: '+e;});
});
})();
</script>`
}

// handleOSScopedWebsiteSave writes one domain's website configuration.
//
// The domain comes from the PATH. Services and gallery are READ BACK from the
// stored content and carried forward, because this page does not edit them: a
// save that silently dropped every field the form happens not to render is how
// an operator loses work they never touched.
func (a *App) handleOSScopedWebsiteSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.domains == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "domain registry not initialised", "")
		return
	}
	d, ok := osScopedDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return
	}
	var body struct {
		Mode     string           `json:"mode"`
		Template string           `json:"template"`
		Content  bizsite.Content  `json:"content"`
		DomainID string           `json:"domain_id"`
		Raw      *json.RawMessage `json:"-"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid JSON", "")
		return
	}
	if !requireScopeMatchesPath(r, body.DomainID) {
		writeAPIError(w, r, http.StatusBadRequest, "scope-mismatch",
			"this request names a different site from the one in its address; nothing was saved", "")
		return
	}

	cfg, err := scopedWebsiteConfig(d, body.Mode, body.Template, body.Content)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", err.Error(), "")
		return
	}
	if err := a.domains.SetSite(r.Context(), d.ID, cfg); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	render.CachePurgeAll()
	dbpkg.AuditLog("vayudomains.website.save", dbpkg.AuditActor(r), d.Host, "mode="+cfg.Mode+" template="+cfg.Template)
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "host": d.Host, "mode": cfg.Mode})
}

// scopedWebsiteConfig validates a submitted website configuration and merges it
// with what is already stored, so fields this surface does not edit survive.
//
// Shared by the console and the MCP tools rather than duplicated: two validators
// for one shape is how one of them ends up accepting a mode the renderer does
// not know, and a domain then serves nothing.
func scopedWebsiteConfig(d domain.Domain, mode, template string, c bizsite.Content) (domain.SiteConfig, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	valid := false
	for _, m := range scopedSiteModes {
		if m.Value == mode {
			valid = true
			break
		}
	}
	// "custom" is accepted only when a bundle is actually deployed for this site.
	//
	// Same rule the primary's Website settings enforce, and for the same reason:
	// selecting a mode with nothing behind it publishes a domain that serves a
	// 404 at its root, which looks like a broken site rather than an unfinished
	// choice. It is absent from scopedSiteModes on purpose — a bundle is an
	// upload, not a radio button — and appears there only once one exists.
	if mode == "custom" && customsite.Deployed(scopedBundleDir(d)) {
		valid = true
	}
	if !valid {
		if mode == "custom" {
			return domain.SiteConfig{}, errNoBundleDeployed
		}
		return domain.SiteConfig{}, errUnknownSiteMode(mode)
	}
	// An unknown template key resolves to the default rather than being stored:
	// a stored key nothing matches renders an unstyled page later, far from the
	// save that caused it.
	template = bizsite.ByKey(strings.TrimSpace(template)).Key

	// Carry forward what this surface does not edit.
	if prev, ok := d.Site(); ok {
		old := bizsite.ParseContent(prev.Content)
		if len(c.Services) == 0 {
			c.Services = old.Services
		}
		if len(c.Gallery) == 0 {
			c.Gallery = old.Gallery
		}
		if c.SectionA == "" {
			c.SectionA = old.SectionA
		}
	}

	raw, err := json.Marshal(c)
	if err != nil {
		return domain.SiteConfig{}, err
	}
	return domain.SiteConfig{Mode: mode, Template: template, Content: string(raw)}, nil
}

type siteModeError string

func (e siteModeError) Error() string {
	return "unknown site mode " + string(e) + " — use blog, business or business_subpath"
}

func errUnknownSiteMode(m string) error { return siteModeError(m) }

// errNoBundleDeployed distinguishes "that mode does not exist" from "that mode
// exists and you have not uploaded anything to serve in it". Collapsing the two
// would tell an operator their upload feature is unsupported.
var errNoBundleDeployed = bundleError(
	"no uploaded website exists for this domain yet — upload one first, or ask an assistant to build one")
