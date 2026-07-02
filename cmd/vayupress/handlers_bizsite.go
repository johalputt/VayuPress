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
	"html"
	htmpl "html/template"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/bizsite"
	"github.com/johalputt/vayupress/internal/config"
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
	if mode != "business" {
		return false
	}
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
}

// bizBlogURL is where the blog lives from the business site's point of view.
func bizBlogURL(mode string) string {
	if mode == "business" && strings.TrimSpace(config.Cfg.Domain) != "" {
		return "https://blog." + config.Cfg.Domain + "/"
	}
	return "/"
}

// handleBizSite renders the business website page (also mounted at /site as an
// always-available preview).
func (a *App) handleBizSite(w http.ResponseWriter, r *http.Request) {
	mode, tpl, content := a.bizSettings(r)
	page := bizsite.Render(tpl, content, bizBlogURL(mode))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, page)
}

// handleBizSiteCSS serves the business site's stylesheet (base + template).
func (a *App) handleBizSiteCSS(w http.ResponseWriter, r *http.Request) {
	_, tpl, _ := a.bizSettings(r)
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, bizsite.CSS(tpl))
}

// ── VayuOS Website studio ────────────────────────────────────────────────────

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
	b.WriteString(`<div class="page-header"><div><h1>Website</h1>` +
		`<p class="text-sm muted">A business website at your domain — blog at blog.` + he(domain) + `, mail at mail.` + he(domain) + `. Deploy, edit and switch designs from here.</p></div>` +
		`<div class="page-actions"><a class="btn btn--ghost btn--sm" href="/site" target="_blank" rel="noopener">Preview ↗</a>` +
		`<button class="btn btn--primary btn--sm" data-biz-save>Save &amp; publish</button></div></div>`)

	// Hosting mode — explicit, never changed by updates.
	b.WriteString(`<div class="card"><div class="card-title">What does ` + he(domain) + ` show?</div>` +
		`<p class="text-sm muted">Your current choice is kept forever across updates — nothing changes unless you change it here.</p>` +
		`<label class="vb-mode"><input type="radio" name="biz-mode" value="blog"`)
	if mode != "business" {
		b.WriteString(` checked`)
	}
	b.WriteString(`> <strong>Blog at the root</strong> <span class="muted text-sm">— ` + he(domain) + ` is your blog (current default)</span></label>`)
	b.WriteString(`<label class="vb-mode"><input type="radio" name="biz-mode" value="business"`)
	if mode == "business" {
		b.WriteString(` checked`)
	}
	b.WriteString(`> <strong>Business website at the root</strong> <span class="muted text-sm">— ` + he(domain) + ` is your business site; the blog lives at blog.` + he(domain) + `</span></label>`)
	b.WriteString(`<p class="muted text-xs mt-2">Point <span class="mono">` + he(domain) + `</span>, <span class="mono">blog.` + he(domain) + `</span> and <span class="mono">mail.` + he(domain) + `</span> at this server; the installer issues and renews Let&#39;s Encrypt certificates for all three automatically.</p></div>`)

	// Template gallery.
	b.WriteString(`<div class="card"><div class="card-title">Choose a design — ` + he(activeTpl.Name) + ` is active</div><div class="biz-grid">`)
	for _, t := range bizsite.All() {
		cls := "biz-card"
		if t.Key == activeTpl.Key {
			cls += " biz-card--active"
		}
		b.WriteString(`<button type="button" class="` + cls + `" data-biz-template="` + he(t.Key) + `">` +
			`<span class="biz-card-cat">` + he(t.Category) + `</span>` +
			`<span class="biz-card-name">` + he(t.Name) + `</span>` +
			`<span class="biz-card-tag text-sm muted">` + he(t.Tagline) + `</span></button>`)
	}
	b.WriteString(`</div><p class="muted text-xs mt-2">Selecting a design keeps your content — only the look changes. Empty fields fall back to the design&#39;s sample content.</p></div>`)

	// Content editor.
	field := func(key, label, ph string) string {
		return `<label class="pm-label">` + he(label) + `</label><input class="input" data-biz-f="` + key + `" placeholder="` + he(ph) + `">`
	}
	area := func(key, label, ph string, rows string) string {
		return `<label class="pm-label">` + he(label) + `</label><textarea class="input" rows="` + rows + `" data-biz-f="` + key + `" placeholder="` + he(ph) + `"></textarea>`
	}
	b.WriteString(`<div class="card"><div class="card-title">Your content</div><div class="biz-form" data-biz-form>`)
	b.WriteString(`<div class="biz-form-col">`)
	b.WriteString(field("name", "Business name", "Maison Olive"))
	b.WriteString(field("tagline", "Tagline", "Seasonal plates, honest wine."))
	b.WriteString(area("about", "About (one paragraph per line)", "Who you are, what you do…", "4"))
	b.WriteString(field("cta", "Button label", "Book a table"))
	b.WriteString(field("ctaLink", "Button link (optional)", "#contact, tel:…, or a URL"))
	b.WriteString(field("heroImg", "Hero image URL (optional)", "/media/hero.jpg or any https image"))
	b.WriteString(`</div><div class="biz-form-col">`)
	b.WriteString(field("phone", "Phone", "+1 555 0100"))
	b.WriteString(field("email", "Email", "hello@"+domain))
	b.WriteString(field("address", "Address", "12 Main Street…"))
	b.WriteString(area("hours", "Hours (one line per range)", "Mon–Fri 09:00–18:00", "3"))
	b.WriteString(area("services", "Offerings — one per line: Title | Description | Price", "Flat white | | £3.40", "6"))
	b.WriteString(area("gallery", "Gallery image URLs (one per line)", "/media/one.jpg", "3"))
	b.WriteString(`<label class="vb-mode"><input type="checkbox" data-biz-f="showBlog"> Link the blog from the website</label>`)
	b.WriteString(`</div></div>`)
	b.WriteString(`<span class="text-sm muted" data-biz-status></span></div>`)

	// Hydration payload + external JS (CSP-safe).
	b.WriteString(`<script type="application/json" id="vp-biz-data">`)
	hydr, _ := json.Marshal(struct {
		Mode     string          `json:"mode"`
		Template string          `json:"template"`
		Content  json.RawMessage `json:"content"`
	}{mode, activeTpl.Key, contentJSON})
	b.Write(hydr)
	b.WriteString(`</script>`)
	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-website.js"></script>`)

	writeOSHTML(w, adminOSLayout(nonce, "Website", "website", cfg, htmpl.HTML(b.String())))
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
	if body.Mode != "blog" && body.Mode != "business" && body.Mode != "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "mode must be blog or business", "")
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
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "template": tpl.Key, "mode": body.Mode})
}
