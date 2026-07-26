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
	"encoding/json"
	"fmt"
	"html"
	htmpl "html/template"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// bizSettings returns the current mode, active template and content.
func (a *App) bizSettings(r *http.Request) (mode string, tpl bizsite.Template, content bizsite.Content) {
	get := func(k string) string {
		if a.siteSettings == nil {
			return ""
		}
		return a.siteSettings.Get(r.Context(), k)
	}
	mode = strings.TrimSpace(get(settings.KeySiteMode))
	tpl = bizsite.ByKey(strings.TrimSpace(get(settings.KeyBizTemplate)))
	content = bizsite.ParseContent(get(settings.KeyBizContent))
	if content.Name == "" && content.Tagline == "" {
		content = tpl.Defaults
	}
	return mode, tpl, content
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

// handleBizSite renders the business website page (also mounted at /site as an
// always-available preview).
func (a *App) handleBizSite(w http.ResponseWriter, r *http.Request) {
	mode, tpl, content := a.bizSettings(r)
	// Live preview: the VayuOS "Preview" button passes ?preview=<design> so an
	// operator can see a design they have SELECTED but not yet saved. Unknown
	// keys are ignored (fall back to the saved/active design).
	if pv, ok := previewTemplate(r); ok {
		tpl = pv
	}
	page := bizsite.Render(tpl, content, bizBlogURL(mode))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, page)
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
	_, tpl, _ := a.bizSettings(r)
	// Serve the previewed design's stylesheet when one is requested (the preview
	// page links /site.css?v=<design>) so the preview's markup and CSS always
	// match; otherwise the saved/active design.
	if pv, ok := previewTemplate(r); ok {
		tpl = pv
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, bizsite.CSS(tpl))
}

// ── VayuOS Website studio ────────────────────────────────────────────────────

// bizModeLabel renders the hosting mode as a short human label for the page's
// stat header and the accordion's status chip, so what the domain currently
// serves is readable without expanding anything.
func bizModeLabel(mode string) string {
	switch mode {
	case "business":
		return "Business site"
	case "business_subpath":
		return "Site + /blog"
	case "custom":
		return "Custom upload"
	default:
		return "Blog"
	}
}

// handleOSWebsite renders the Website studio: hosting-mode chooser, template
// gallery, and the content editor.
func (a *App) handleOSWebsite(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	mode, activeTpl, content := a.bizSettings(r)
	domain := strings.TrimSpace(config.Cfg.Domain)
	if domain == "" {
		domain = "yourdomain.com"
	}
	he := html.EscapeString

	contentJSON, _ := json.Marshal(content)

	var b strings.Builder
	b.WriteString(`<div class="page-header"><div><h1>Website</h1></div>` +
		`<div class="page-actions"><a class="btn btn--ghost btn--sm" data-biz-preview href="/site?preview=` + he(activeTpl.Key) + `" target="_blank" rel="noopener">Preview ↗</a>` +
		`<button class="btn btn--primary btn--sm" data-biz-save>Save &amp; publish</button></div></div>` +
		`<p class="page-sub">A business website at your domain — blog at blog.` + he(domain) + `, mail at mail.` + he(domain) + `. Deploy, edit and switch designs from here.</p>`)

	// Premium stat header (Monetization-console style): the four facts an
	// operator wants confirmed at a glance before touching anything.
	cmHead := customsite.ReadManifest(customSiteDir())
	buildLabel := "None"
	if customsite.Deployed(customSiteDir()) {
		buildLabel = fmt.Sprintf("%d files", cmHead.Files)
	}
	b.WriteString(`<div class="stat-grid mb-6">` +
		`<div class="stat-card"><div class="stat-card__label">Root serves</div><div class="stat-card__value stat-card__value--sm">` + he(bizModeLabel(mode)) + `</div><div class="stat-card__bottom"><span class="muted text-xs">` + he(domain) + `</span></div></div>` +
		`<div class="stat-card"><div class="stat-card__label">Active design</div><div class="stat-card__value stat-card__value--sm">` + he(activeTpl.Name) + `</div><div class="stat-card__bottom"><span class="muted text-xs">` + he(activeTpl.Category) + `</span></div></div>` +
		`<div class="stat-card"><div class="stat-card__label">Designs</div><div class="stat-card__value">` + fmt.Sprintf("%d", len(bizsite.All())) + `</div><div class="stat-card__bottom"><span class="muted text-xs">ready to switch to</span></div></div>` +
		`<div class="stat-card"><div class="stat-card__label">Custom build</div><div class="stat-card__value stat-card__value--sm">` + he(buildLabel) + `</div><div class="stat-card__bottom"><span class="muted text-xs">uploaded static site</span></div></div>` +
		`</div>`)

	b.WriteString(`<div class="section-head"><span class="section-head__title">Hosting</span>` +
		`<span class="section-head__hint">What your domain serves — kept across updates</span></div><div class="mon-stack">`)

	// Hosting mode — explicit, never changed by updates.
	var hostBody strings.Builder
	b2 := &hostBody
	b2.WriteString(`<p class="text-sm muted">Your current choice is kept forever across updates — nothing changes unless you change it here.</p>` +
		`<label class="vb-mode"><input type="radio" name="biz-mode" value="blog"`)
	if mode != "business" {
		b2.WriteString(` checked`)
	}
	b2.WriteString(`> <strong>Blog at the root</strong> <span class="muted text-sm">— ` + he(domain) + ` is your blog (current default)</span></label>`)
	b2.WriteString(`<label class="vb-mode"><input type="radio" name="biz-mode" value="business"`)
	if mode == "business" {
		b2.WriteString(` checked`)
	}
	b2.WriteString(`> <strong>Business website at the root</strong> <span class="muted text-sm">— ` + he(domain) + ` is your business site; the blog lives at blog.` + he(domain) + `</span></label>`)
	b2.WriteString(`<label class="vb-mode"><input type="radio" name="biz-mode" value="business_subpath"`)
	if mode == "business_subpath" {
		b2.WriteString(` checked`)
	}
	b2.WriteString(`> <strong>Website at the root, blog at /blog</strong> <span class="muted text-sm">— ` + he(domain) + ` is your business site, the blog homepage is ` + he(domain) + `/blog, and every existing post keeps its ` + he(domain) + `/slug URL. One domain, no subdomain or extra certificate needed.</span></label>`)
	b2.WriteString(`<label class="vb-mode"><input type="radio" name="biz-mode" value="custom"`)
	if mode == "custom" {
		b2.WriteString(` checked`)
	}
	b2.WriteString(`> <strong>Custom uploaded website</strong> <span class="muted text-sm">— serve your own static site (built by hand or with AI, uploaded below) at ` + he(domain) + `; the blog stays at ` + he(domain) + `/blog and posts keep their ` + he(domain) + `/slug URLs</span></label>`)
	b2.WriteString(`<p class="muted text-xs mt-2">The subdomain option points <span class="mono">` + he(domain) + `</span>, <span class="mono">blog.` + he(domain) + `</span> and <span class="mono">mail.` + he(domain) + `</span> at this server; the installer issues and renews Let&#39;s Encrypt certificates for all three automatically. The <span class="mono">/blog</span> and custom options need only <span class="mono">` + he(domain) + `</span>.</p>`)
	b.WriteString(monAcc("🌐", "What does "+he(domain)+" show?", "Blog, business site, /blog or your own upload",
		`<span class="mon-chip mon-chip--on">● `+he(bizModeLabel(mode))+`</span>`, true, hostBody.String()))
	b.WriteString(`</div>`)

	// ── Design & content ──────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Design &amp; content</span>` +
		`<span class="section-head__hint">Pick a look, then fill in the details — switching designs keeps your content</span></div><div class="mon-stack">`)

	// Template gallery.
	var galBody strings.Builder
	galBody.WriteString(`<div class="biz-grid">`)
	for _, t := range bizsite.All() {
		cls := "biz-card"
		if t.Key == activeTpl.Key {
			cls += " biz-card--active"
		}
		galBody.WriteString(`<button type="button" class="` + cls + `" data-biz-template="` + he(t.Key) + `">` +
			`<span class="biz-card-cat">` + he(t.Category) + `</span>` +
			`<span class="biz-card-name">` + he(t.Name) + `</span>` +
			`<span class="biz-card-tag text-sm muted">` + he(t.Tagline) + `</span></button>`)
	}
	galBody.WriteString(`</div><p class="muted text-xs mt-2">Selecting a design keeps your content — only the look changes. Empty fields fall back to the design&#39;s sample content.</p>`)
	b.WriteString(monAcc("🎨", "Choose a design", fmt.Sprintf("%d ready-made looks — %s is active", len(bizsite.All()), he(activeTpl.Name)),
		`<span class="mon-chip mon-chip--on">● `+he(activeTpl.Name)+`</span>`, false, galBody.String()))

	// Content editor.
	field := func(key, label, ph string) string {
		return `<label class="pm-label">` + he(label) + `</label><input class="input" data-biz-f="` + key + `" placeholder="` + he(ph) + `">`
	}
	area := func(key, label, ph string, rows string) string {
		return `<label class="pm-label">` + he(label) + `</label><textarea class="input" rows="` + rows + `" data-biz-f="` + key + `" placeholder="` + he(ph) + `"></textarea>`
	}
	var formBody strings.Builder
	formBody.WriteString(`<div class="biz-form" data-biz-form>`)
	formBody.WriteString(`<div class="biz-form-col">`)
	formBody.WriteString(field("name", "Business name", "Maison Olive"))
	formBody.WriteString(field("tagline", "Tagline", "Seasonal plates, honest wine."))
	formBody.WriteString(area("about", "About (one paragraph per line)", "Who you are, what you do…", "4"))
	formBody.WriteString(field("cta", "Button label", "Book a table"))
	formBody.WriteString(field("ctaLink", "Button link (optional)", "#contact, tel:…, or a URL"))
	formBody.WriteString(field("heroImg", "Hero image URL (optional)", "/media/hero.jpg or any https image"))
	formBody.WriteString(`</div><div class="biz-form-col">`)
	formBody.WriteString(field("phone", "Phone", "+1 555 0100"))
	formBody.WriteString(field("email", "Email", "hello@"+domain))
	formBody.WriteString(field("address", "Address", "12 Main Street…"))
	formBody.WriteString(area("hours", "Hours (one line per range)", "Mon–Fri 09:00–18:00", "3"))
	formBody.WriteString(area("services", "Offerings — one per line: Title | Description | Price", "Flat white | | £3.40", "6"))
	formBody.WriteString(area("gallery", "Gallery image URLs (one per line)", "/media/one.jpg", "3"))
	formBody.WriteString(`<label class="vb-mode"><input type="checkbox" data-biz-f="showBlog"> Link the blog from the website</label>`)
	formBody.WriteString(`</div></div>`)
	formBody.WriteString(`<span class="text-sm muted" data-biz-status></span>`)
	b.WriteString(monAcc("✍️", "Your content", "Name, tagline, contact details, hours, offerings &amp; gallery", "", false, formBody.String()))
	// Whether this site installs as a REAL app. It lives here because it is a
	// property of the public site, and it opens itself when something is failing.
	b.WriteString(a.pwaHealthCardHTML(r, nonce))
	b.WriteString(`</div>`)

	// ── Custom build ──────────────────────────────────────────────────────────
	b.WriteString(`<div class="section-head"><span class="section-head__title">Custom build</span>` +
		`<span class="section-head__hint">Bring your own static site instead of a ready-made design</span></div><div class="mon-stack">`)
	cm := customsite.ReadManifest(customSiteDir())
	customDeployed := customsite.Deployed(customSiteDir())
	var zipBody strings.Builder
	zipBody.WriteString(`<p class="text-sm muted">Upload a complete static website as a <span class="mono">.zip</span> — it must contain <span class="mono">index.html</span> at its root and reference assets with relative paths. It goes live at <span class="mono">` + he(domain) + `</span> once you choose <strong>Custom uploaded website</strong> above and Save &amp; publish. Building with an AI assistant? <a href="/os/api/website/custom-guide">Download the build guide ↓</a></p>`)
	if customDeployed {
		zipBody.WriteString(`<p class="text-sm">Current build: <strong>` + fmt.Sprintf("%d", cm.Files) + `</strong> files, ` + fmt.Sprintf("%.1f", float64(cm.Bytes)/(1024*1024)) + ` MiB` +
			`, deployed <span class="mono">` + he(config.FormatSiteStamp(cm.DeployedAt)) + `</span>.</p>`)
	}
	zipBody.WriteString(`<div class="biz-deploy"><input type="file" accept=".zip,application/zip" data-biz-zip class="input">` +
		`<button type="button" class="btn btn--primary btn--sm" data-biz-deploy>Deploy .zip</button>`)
	if cm.HasPrev {
		zipBody.WriteString(`<button type="button" class="btn btn--ghost btn--sm" data-biz-rollback>Roll back</button>`)
	}
	zipBody.WriteString(`<span class="text-sm muted" data-biz-deploy-status></span></div>`)
	b.WriteString(monAcc("📦", "Deploy a custom build", "Upload a .zip static site — with one-click rollback",
		monChip(customDeployed, buildLabel+" deployed", "None uploaded"), false, zipBody.String()))
	b.WriteString(`</div>`)

	// Hydration payload + external JS (CSP-safe).
	b.WriteString(`<script type="application/json" id="vp-biz-data">`)
	hydr, _ := json.Marshal(struct {
		Mode     string          `json:"mode"`
		Template string          `json:"template"`
		Content  json.RawMessage `json:"content"`
	}{mode, activeTpl.Key, contentJSON})
	b.Write(hydr)
	b.WriteString(`</script>`)
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
	if body.Mode == "custom" && !customsite.Deployed(customSiteDir()) {
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
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{
		settings.KeySiteMode:    body.Mode,
		settings.KeyBizTemplate: tpl.Key,
		settings.KeyBizContent:  string(raw),
	}); err != nil {
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
