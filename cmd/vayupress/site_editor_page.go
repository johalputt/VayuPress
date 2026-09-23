// SPDX-License-Identifier: Apache-2.0

package main

// site_editor_page.go — the console page that edits a site document
// (ADR-0161), and the card the Website pages show in its place once a site is
// edited as a document.

import (
	"html"
	htmpl "html/template"
	"net/http"

	"github.com/johalputt/vayupress/internal/render"
)

// siteEditorShell is the editor's markup. The script builds everything inside
// from the API; the page carries only where that API is and where "back" is.
func siteEditorShell(nonce, apiBase, back, backLabel string) string {
	esc := html.EscapeString
	return `<div id="site-editor" data-base="` + esc(apiBase) + `">
<div class="card se-bar">
  <a class="btn btn--ghost btn--sm" href="` + esc(back) + `">← ` + esc(backLabel) + `</a>
  <span id="se-status" class="text-sm muted" role="status" aria-live="polite">Opening…</span>
  <button id="se-publish" type="button" class="btn btn--primary btn--sm">Publish</button>
</div>
<p id="se-error" class="se-error" role="alert" hidden></p>
<div id="se-checks" class="se-checks" aria-live="polite" hidden></div>
<div id="se-start" class="card se-start" hidden></div>
<div class="se-grid">
  <div class="se-edit">
    <div class="card"><div class="settings-block-title">Site</div><div id="se-site"></div></div>
    <nav id="se-pages" class="se-pages" aria-label="Pages"></nav>
    <div id="se-sections"></div>
    <details class="card"><summary class="settings-block-title">History</summary><div id="se-history"></div></details>
  </div>
  <div class="se-view">
    <div class="se-view-bar">
      <button type="button" class="btn btn--ghost btn--sm" data-se-device="desktop">Desktop</button>
      <button type="button" class="btn btn--ghost btn--sm" data-se-device="phone">Phone</button>
      <span class="text-sm muted">Preview of the saved draft</span>
    </div>
    <div id="se-preview-wrap" data-device="desktop"><iframe id="se-preview" title="Preview of the site" sandbox=""></iframe></div>
  </div>
</div>
<div id="se-media" class="card se-media" role="dialog" aria-label="Choose a picture" hidden></div>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-site-editor.js?v=` + assetVer("js/admin-os-site-editor.js") + `"></script>`
}

// handleOSSiteEditor is the primary site's editor.
func (a *App) handleOSSiteEditor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	csrfTokenFor(w, r)
	body := siteEditorShell(nonce, "/os/api/site-doc", "/os/website", "Website")
	writeOSHTML(w, r, adminOSLayout(nonce, "Site editor", "website", a.getOSSettings(r.Context()), htmpl.HTML(body)))
}

// handleOSScopedSiteEditor is a hosted site's editor.
func (a *App) handleOSScopedSiteEditor(w http.ResponseWriter, r *http.Request) {
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	nonce := render.CSPNonce(r)
	csrfTokenFor(w, r)
	base := "/os/d/" + d.ID
	body := siteEditorShell(nonce, base+"/api/site-doc", base+"/website", "Website · "+d.Host)
	writeOSHTML(w, r, adminOSLayout(nonce, "Site editor · "+d.Host, "optimize", a.getOSSettings(r.Context()), htmpl.HTML(body)))
}

// siteEditorCard is what a Website page shows about content. Once a site has
// a published document its old flat fields no longer reach the page, so they
// are not offered: a form whose saves change nothing visible is a control
// that does nothing. Before that, the form stays, with the editor beside it.
func siteEditorCard(editorURL string, published bool) string {
	esc := html.EscapeString
	if published {
		return `<div class="card"><div class="settings-block-title">Edited as pages and sections</div>` +
			`<p class="text-sm muted">This site is published from the site editor: its pages, sections, ` +
			`drafts and history live there, and the design chosen above dresses them.</p>` +
			`<a class="btn btn--primary btn--sm" href="` + esc(editorURL) + `">Open the site editor</a></div>`
	}
	return `<div class="card"><div class="settings-block-title">More than one page?</div>` +
		`<p class="text-sm muted">The site editor builds the same site as pages of sections — add pages, ` +
		`reorder sections, keep drafts and restore earlier versions. It opens on what you have here; ` +
		`once you publish from it, it is where this site is edited.</p>` +
		`<a class="btn btn--ghost btn--sm" href="` + esc(editorURL) + `">Open the site editor</a></div>`
}
