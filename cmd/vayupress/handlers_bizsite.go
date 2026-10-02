// SPDX-License-Identifier: Apache-2.0

package main

// handlers_bizsite.go — the small-business website that VayuPress can serve at
// the root domain alongside the blog and VayuMail (VayuOS → Website).
//
// Topology (operator-chosen, never changed by an update):
//   - site.mode "" / "blog"  → the blog stays at the root domain (historic
//     default; existing installs are untouched).
//   - site.mode "business"   → the business site serves at the root domain
//     and the blog moves to blog.<domain> (mail stays at mail.<domain>).
//
// The site is always previewable at /site regardless of mode, so an operator
// can build and polish it before flipping the switch.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	htmpl "html/template"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/sitedoc"
	"github.com/johalputt/vayupress/internal/ui"
)

// bizSettings returns the current mode, active template and content.
func (a *App) bizSettings(r *http.Request) (mode string, tpl bizsite.Template, content bizsite.Content) {
	rawMode, rawTpl, rawContent := a.siteSourceFor(r)
	mode = strings.TrimSpace(rawMode)
	tpl = bizsite.ByKey(strings.TrimSpace(rawTpl))
	return mode, tpl, bizsite.EffectiveContent(tpl, rawContent)
}

// siteSourceFor returns the raw website settings for the request's active
// domain: mode, business template key and business content.
//
// Before this existed, all three came from install-wide settings keys and the
// custom bundle lived at one path, so ONE website served every registered
// domain. A studio hosting client sites could host exactly one of them; every
// other domain served the same bundle. That is what this splits apart.
//
// Two rules, and the second is a deliberate behaviour change:
//
//   - The PRIMARY domain reads the install-wide Website settings, unchanged. A
//     single-domain install never reaches the branch above it, so it is
//     byte-identical.
//   - A SECONDARY domain with no override of its own serves its own BLOG — it
//     does not inherit the primary's mode. Inheriting is what produced the
//     defect: with the install set to "custom", every client domain served the
//     studio's own bundle. A secondary's own scoped content is the safe answer,
//     and it is what ADR-0132 Stage 2b already gives it everywhere else.
func (a *App) siteSourceFor(r *http.Request) (mode, tpl, content string) {
	get := func(k string) string {
		if a.siteSettings == nil {
			return ""
		}
		return a.siteSettings.Get(r.Context(), settings.ForPrimary(), k)
	}
	if a.multiDomain(r) {
		if d, ok := activeDomain(r); ok && !d.IsPrimary {
			if s, ok := d.Site(); ok {
				return s.Mode, s.Template, s.Content
			}
			// No override: this domain's own blog, never the primary's website.
			return "blog", "", ""
		}
	}
	return get(settings.KeySiteMode), get(settings.KeyBizTemplate), get(settings.KeyBizContent)
}

// bizRootActive reports whether this request should serve the business site at
// "/": mode is "business" AND the request host is the root domain (never the
// blog subdomain, so blog.<domain> keeps serving the blog feed).
func (a *App) bizRootActive(r *http.Request) bool {
	mode, _, _ := a.bizSettings(r)
	switch mode {
	case "business_subpath":
		// Website owns "/" on every host; the blog lives at /blog on the SAME
		// domain (no subdomain), with posts still at /slug. Always active.
		return true
	case "business":
		// Website owns the root domain; the blog moves to blog.<domain>, so the
		// business site must NOT take over the blog subdomain.
		domain := strings.TrimSpace(config.Cfg.Domain)
		if domain == "" {
			return true // no domain configured: single-host install, honour the mode
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		return !strings.HasPrefix(host, "blog.")
	default:
		return false
	}
}

// bizBlogURL is where the blog lives from the business site's point of view.
func bizBlogURL(mode string) string {
	switch mode {
	case "business_subpath":
		return "/blog" // same domain, blog under /blog
	case "business":
		if strings.TrimSpace(config.Cfg.Domain) != "" {
			return "https://blog." + config.Cfg.Domain + "/"
		}
	}
	return "/"
}

// handleBizSite renders the business website's home page (also mounted at
// /site as an always-available preview). See site_documents.go.
func (a *App) handleBizSite(w http.ResponseWriter, r *http.Request) {
	a.renderSitePage(w, r, "")
}

// previewTemplate returns a known design named by the request's ?preview= (or
// the ?v= cache-bust the preview page carries) so a live preview shows the
// selected-but-unsaved design. It returns ok=false when neither names a real
// design, so the caller keeps the saved/active design.
func previewTemplate(r *http.Request) (bizsite.Template, bool) {
	for _, key := range []string{
		strings.TrimSpace(r.URL.Query().Get("preview")),
		strings.TrimSpace(r.URL.Query().Get("v")),
	} {
		// The site's stylesheet link carries "<design>.<brand hash>", so a
		// brand change busts caches too; the design is the part before the dot.
		key, _, _ = strings.Cut(key, ".")
		if key == "" {
			continue
		}
		if t := bizsite.ByKey(key); t.Key == key { // ByKey falls back to the first design for unknown keys
			return t, true
		}
	}
	return bizsite.Template{}, false
}

// handleBizSiteCSS serves the business site's stylesheet (base + template).
func (a *App) handleBizSiteCSS(w http.ResponseWriter, r *http.Request) {
	_, tpl, doc := a.siteDocument(r)
	// Serve the previewed design's stylesheet when one is requested (the preview
	// page links /site.css?v=<design>) so the preview's markup and CSS always
	// match; otherwise the saved/active design.
	if pv, ok := previewTemplate(r); ok {
		tpl = pv
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, siteCSS(tpl, doc))
}

// siteCSS is a site's whole stylesheet: its design, then its brand.
func siteCSS(tpl bizsite.Template, doc sitedoc.Document) string {
	return bizsite.CSS(tpl) + sitedoc.StyleCSS(doc.Style, tpl.Dark)
}

// siteCSSVersion names one stylesheet's content, for its cache-busting URL.
func siteCSSVersion(tpl bizsite.Template, doc sitedoc.Document) string {
	sum := sha256.Sum256([]byte(sitedoc.StyleCSS(doc.Style, tpl.Dark)))
	return tpl.Key + "." + hex.EncodeToString(sum[:4])
}

// ── VayuOS Website studio ────────────────────────────────────────────────────

// websiteServes says, as the page's state, what the domain shows today.
func websiteServes(domain, mode, design string) string {
	switch mode {
	case "business":
		return domain + " shows the website, in " + design
	case "business_subpath":
		return domain + " shows the website, the blog at /blog"
	case "custom":
		return domain + " shows the site you uploaded"
	default:
		return domain + " shows the blog"
	}
}

// handleOSWebsite renders the website as a document (page grammar, render
// 02's kind): the site itself, previewed in the design chosen, with what the
// domain serves, the design, the content, an uploaded build and whether the
// site installs as an app in the inspector beside it.
func (a *App) handleOSWebsite(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	mode, activeTpl, content := a.bizSettings(r)
	domain := strings.TrimSpace(config.Cfg.Domain)
	if domain == "" {
		domain = "yourdomain.com"
	}
	contentJSON, _ := json.Marshal(content)
	dir := a.customSiteDir(r)
	man := customsite.ReadManifest(dir)
	deployed := customsite.Deployed(dir)
	_, published := publishedSiteDoc(r.Context(), "")

	// ── The document: the site, or what stands in its place ────────────────
	var doc string
	if mode == "custom" {
		doc = `<div class="web-doc__note"><p>` + esc(domain) + ` serves the site you uploaded`
		if deployed {
			doc += `: ` + strconv.Itoa(man.Files) + ` file` + plural(man.Files) + `, deployed ` + esc(config.FormatSiteStamp(man.DeployedAt))
		}
		doc += `.</p><a class="btn btn--ghost btn--sm" href="/" target="_blank" rel="noopener">View it ↗</a></div>`
	} else {
		if mode == "blog" {
			doc = `<p class="web-doc__caption">The website as it will look. ` + esc(domain) + ` shows the blog until Hosting says otherwise.</p>`
		}
		// The site editor's preview, framable by the console alone and
		// sandboxed: it renders the saved site in the design named here,
		// so choosing a design shows it before it is saved.
		doc += `<iframe class="web-doc__frame" data-biz-frame title="The website" sandbox="" src="/os/api/site-doc/preview?page=&amp;preview=` + esc(activeTpl.Key) + `"></iframe>`
	}

	// ── Hosting ─────────────────────────────────────────────────────────────
	var host strings.Builder
	for _, m := range []struct{ value, label, note string }{
		{"blog", "The blog", domain + " is the blog"},
		{"business", "The website", "the blog moves to blog." + domain},
		{"business_subpath", "The website, the blog at /blog", "posts keep their addresses"},
		{"custom", "The site you uploaded", "served as it was built"},
	} {
		checked := ""
		if m.value == mode || (m.value == "blog" && mode == "") {
			checked = " checked"
		}
		host.WriteString(`<label class="sa-insp__option"><input type="radio" name="biz-mode" value="` + m.value + `"` + checked + `>` +
			`<span><span class="sa-insp__option-label">` + esc(m.label) + `</span><span class="sa-insp__hint">` + esc(m.note) + `</span></span></label>`)
	}
	host.WriteString(string(ui.Explain(ui.HTML(`<p>Updates never change this. The website option points ` + esc(domain) + `, blog.` + esc(domain) + ` and mail.` + esc(domain) +
		` at this server, and certificates for all three are issued and renewed for you; the /blog and uploaded options need only ` + esc(domain) + `.</p>`))))

	// ── Design ──────────────────────────────────────────────────────────────
	design := string(ui.InspectorRow("In use", ui.HTML(`<span class="sa-insp__pair"><span data-biz-design-name>`+esc(activeTpl.Name)+`</span>`+
		`<button type="button" class="btn btn--ghost btn--xs" data-sheet="web-designs">Change</button></span>`)))
	var gal strings.Builder
	gal.WriteString(`<div class="biz-grid">`)
	for _, t := range bizsite.All() {
		cls := "biz-card"
		if t.Key == activeTpl.Key {
			cls += " biz-card--active"
		}
		gal.WriteString(`<button type="button" class="` + cls + `" data-biz-template="` + esc(t.Key) + `" data-biz-template-name="` + esc(t.Name) + `">` +
			`<span class="biz-card-cat">` + esc(t.Category) + `</span><span class="biz-card-name">` + esc(t.Name) + `</span>` +
			`<span class="biz-card-tag">` + esc(t.Tagline) + `</span></button>`)
	}
	gal.WriteString(`</div><p class="text-sm muted">A design changes the look and keeps the content. A field left empty shows the design's sample.</p>`)

	// ── Content ─────────────────────────────────────────────────────────────
	var con strings.Builder
	if published {
		con.WriteString(`<p class="sa-insp__text">This site is published from the site editor, as pages of sections with drafts and history.</p>` +
			`<a class="btn btn--sm" href="/os/website/editor">Open the site editor</a>`)
	} else {
		field := func(key, label, ph string) {
			con.WriteString(`<label class="sa-insp__field"><span class="sa-insp__label">` + esc(label) + `</span><input class="input" data-biz-f="` + key + `" placeholder="` + esc(ph) + `"></label>`)
		}
		// Multi-line content is edited only in a textarea: a single-line
		// input strips line breaks from its value, and a save then stores
		// the hours or the address run together (admin_os_scoped_website.go).
		area := func(key, label, ph, rows string) {
			con.WriteString(`<label class="sa-insp__field"><span class="sa-insp__label">` + esc(label) + `</span><textarea class="textarea" rows="` + rows + `" data-biz-f="` + key + `" placeholder="` + esc(ph) + `"></textarea></label>`)
		}
		field("name", "Name", "Maison Olive")
		field("tagline", "Tagline", "Seasonal plates, honest wine.")
		area("about", "About, a paragraph a line", "Who you are, what you do", "4")
		field("cta", "Button", "Book a table")
		field("ctaLink", "Button link", "#contact, tel:, or an address")
		field("heroImg", "Hero image", "/media/hero.jpg")
		field("phone", "Phone", "+1 555 0100")
		field("email", "Email", "hello@"+domain)
		area("address", "Address, a line a row", "12 Main Street", "2")
		area("hours", "Hours, a range a line", "Mon–Fri 09:00–18:00", "3")
		area("services", "Offerings: title | description | price, one a line", "Flat white | | £3.40", "5")
		area("gallery", "Gallery images, one a line", "/media/one.jpg", "3")
		con.WriteString(`<label class="sa-insp__check"><input type="checkbox" data-biz-f="showBlog"> Link the blog from the website</label>` +
			`<p class="sa-insp__text">More than one page? The site editor builds the same site as pages of sections, with drafts and history. It opens on what is here.</p>` +
			`<a class="btn btn--sm" href="/os/website/editor">Open the site editor</a>`)
	}

	// ── An uploaded build ───────────────────────────────────────────────────
	built := "None"
	if deployed {
		built = strconv.Itoa(man.Files) + " file" + plural(man.Files) + ", " + config.FormatSiteStamp(man.DeployedAt)
	}
	upload := string(ui.InspectorRow("Uploaded", ui.Text(built))) + `<div class="sa-insp__actions"><button type="button" class="btn btn--sm" data-sheet="web-bundle">Upload a .zip</button>`
	if deployed {
		upload += `<a class="btn btn--ghost btn--sm" href="/os/api/website/custom-bundle/download" download>Download</a>`
	}
	if man.HasPrev {
		upload += `<button type="button" class="btn btn--ghost btn--sm" data-biz-rollback>Roll back</button>`
	}
	upload += `</div>`
	bundle := `<p class="text-sm muted">A complete static site as a .zip, with index.html at its root and its assets by relative path. It is live once Hosting says the site you uploaded. <a href="/os/api/website/custom-guide">The build guide for an assistant ↓</a></p>` +
		bundleRoomLine() +
		`<div class="biz-deploy"><input type="file" accept=".zip,application/zip" data-biz-zip class="input" aria-label="The .zip to deploy">` +
		`<button type="button" class="btn btn--primary btn--sm" data-biz-deploy>Deploy</button></div>` +
		bundleHistoryHTML(dir, "/os/api/website/custom-bundle")

	inspector := ui.Join(
		ui.InspectorSection("Hosting", ui.HTML(host.String())),
		ui.InspectorSection("Design", ui.HTML(design)),
		ui.InspectorSection("Content", ui.HTML(con.String())),
		ui.InspectorSection("Your own build", ui.HTML(upload)),
		ui.HTML(a.pwaHealthSection(r, nonce)),
	)
	page := ui.Document(ui.DocumentPage{
		Title: "Website",
		State: ui.State("ok", websiteServes(domain, mode, activeTpl.Name)),
		Actions: ui.HTML(`<span class="sa-doc__msg" data-biz-status role="status" aria-live="polite"></span><span class="sa-doc__msg" data-biz-deploy-status role="status" aria-live="polite"></span>` +
			`<a class="btn btn--ghost btn--sm" data-biz-preview href="/site?preview=` + esc(activeTpl.Key) + `" target="_blank" rel="noopener">Preview ↗</a>` +
			`<button type="button" class="btn btn--primary btn--sm" data-biz-save>Save &amp; publish</button>`),
		Inspector: inspector,
		Label:     "Website settings",
	}, ui.HTML(doc))

	var b strings.Builder
	b.WriteString(string(page))
	b.WriteString(string(ui.Sheet("web-designs", "Choose a design", ui.HTML(gal.String()))))
	b.WriteString(string(ui.Sheet("web-bundle", "Upload a site", ui.HTML(bundle))))

	// Hydration payload + external JS (CSP-safe).
	b.WriteString(`<script type="application/json" id="vp-biz-data">`)
	hydr, _ := json.Marshal(struct {
		Mode     string          `json:"mode"`
		Template string          `json:"template"`
		Content  json.RawMessage `json:"content"`
	}{mode, activeTpl.Key, contentJSON})
	b.Write(hydr)
	b.WriteString(`</script>`)
	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-bundle.js?v=` + assetVer("js/admin-os-bundle.js") + `"></script>`)
	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-website.js?v=` + assetVer("js/admin-os-website.js") + `"></script>`)

	writeOSHTML(w, r, adminOSLayout(nonce, "Website", "website", cfg, htmpl.HTML(b.String())))
}

// handleOSWebsiteSave persists mode/template/content.
//
//	POST /os/api/website/save  {mode, template, content}
func (a *App) handleOSWebsiteSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	var body struct {
		Mode     string          `json:"mode"`
		Template string          `json:"template"`
		Content  bizsite.Content `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	switch body.Mode {
	case "", "blog", "business", "business_subpath", "custom":
		// ok
	default:
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "mode must be blog, business, business_subpath or custom", "")
		return
	}
	if body.Mode == "custom" && !customsite.Deployed(a.customSiteDir(r)) {
		writeAPIError(w, r, http.StatusBadRequest, "no_custom_bundle", "Deploy a custom website .zip before switching to custom mode.", "")
		return
	}
	tpl := bizsite.ByKey(body.Template) // unknown keys fall back to the first template
	raw, err := json.Marshal(body.Content)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid content", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-unavailable", "settings store not ready", "")
		return
	}
	values := map[string]string{
		settings.KeySiteMode:    body.Mode,
		settings.KeyBizTemplate: tpl.Key,
		settings.KeyBizContent:  string(raw),
	}
	// Published as a document: the page no longer offers the flat fields, so
	// what arrives for them is empty, and the stored content (the fallback)
	// is kept rather than overwritten with nothing.
	if _, published := publishedSiteDoc(r.Context(), ""); published {
		delete(values, settings.KeyBizContent)
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), values); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	render.CachePurgeAll()
	render.SetBlogBase(blogBaseForMode(body.Mode))
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "template": tpl.Key, "mode": body.Mode})
}

// blogBaseForMode maps a site mode to the blog's URL base path: "/blog" for the
// business_subpath mode (website at "/", blog under /blog), "/" otherwise.
func blogBaseForMode(mode string) string {
	// Both the /blog subpath mode and the custom-website mode keep the website at
	// "/" and move the blog to /blog (posts stay at /slug).
	if mode == "business_subpath" || mode == "custom" {
		return "/blog"
	}
	return "/"
}
