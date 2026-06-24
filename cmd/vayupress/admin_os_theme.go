package main

import (
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"

	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/theme"
)

type themeColorField struct {
	Field string
	Label string
	Vari  string
}

func themeDarkColors() []themeColorField {
	return []themeColorField{
		{"BgDark", "Background", "bg"},
		{"SurfaceDark", "Surface", "surface"},
		{"TextDark", "Text", "text"},
		{"MutedDark", "Muted", "muted"},
		{"AccentDark", "Accent", "accent"},
		{"Accent2Dark", "Accent 2", "accent2"},
		{"HiDark", "Highlight", "hi"},
		{"GreenDark", "Success", "green"},
	}
}

func themeLightColors() []themeColorField {
	return []themeColorField{
		{"BgLight", "Background", ""},
		{"SurfaceLight", "Surface", ""},
		{"TextLight", "Text", ""},
		{"MutedLight", "Muted", ""},
		{"AccentLight", "Accent", ""},
		{"Accent2Light", "Accent 2", ""},
		{"HiLight", "Highlight", ""},
	}
}

func colorRow(f themeColorField) string {
	vari := ""
	if f.Vari != "" {
		vari = ` data-token-var="` + html.EscapeString(f.Vari) + `"`
	}
	return `<label class="theme-field">
  <span class="theme-field__label">` + html.EscapeString(f.Label) + `</span>
  <input type="color" class="theme-field__color" data-token="` + html.EscapeString(f.Field) + `"` + vari + ` aria-label="` + html.EscapeString(f.Label) + `">
</label>`
}

func textRow(field, label, placeholder string) string {
	return `<label class="theme-field theme-field--text">
  <span class="theme-field__label">` + html.EscapeString(label) + `</span>
  <input type="text" class="input" data-token="` + html.EscapeString(field) + `" placeholder="` + html.EscapeString(placeholder) + `" aria-label="` + html.EscapeString(label) + `">
</label>`
}

func themeCardHTML(t theme.Tokens) string {
	borderColor := t.AccentDark
	if borderColor == "" {
		borderColor = t.BgDark
	}
	accentColor := t.AccentDark
	if accentColor == "" {
		accentColor = "#6c5ce7"
	}

	return `<button class="theme-card" data-preset="` + html.EscapeString(t.Name) + `"
  title="Apply ` + html.EscapeString(t.Name) + ` theme"
  aria-label="Apply ` + html.EscapeString(t.Name) + ` preset">
  <div class="theme-card__preview" style="background:` + html.EscapeString(t.BgDark) + `">
    <div class="theme-card__bar" style="background:` + html.EscapeString(t.SurfaceDark) + `"></div>
    <div class="theme-card__body">
      <div class="theme-card__line" style="background:` + html.EscapeString(t.MutedDark) + `"></div>
      <div class="theme-card__line theme-card__line--short" style="background:` + html.EscapeString(t.MutedDark) + `"></div>
      <div class="theme-card__pills">
        <span style="background:` + html.EscapeString(accentColor) + `"></span>
        <span style="background:` + html.EscapeString(t.Accent2Dark) + `"></span>
      </div>
    </div>
  </div>
  <span class="theme-card__name">` + html.EscapeString(t.Name) + `</span>
</button>`
}

func (a *App) handleOSTheme(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	darkRows := ""
	for _, f := range themeDarkColors() {
		darkRows += colorRow(f)
	}
	lightRows := ""
	for _, f := range themeLightColors() {
		lightRows += colorRow(f)
	}
	typoRows := textRow("FontSans", "Sans-serif stack", "system-ui, sans-serif") +
		textRow("FontMono", "Monospace stack", "ui-monospace, monospace") +
		textRow("FontSizeBase", "Base font size", "1rem") +
		textRow("LineHeight", "Line height", "1.6") +
		textRow("MaxWidth", "Max content width", "72ch") +
		textRow("RadiusSm", "Small radius", "0.25rem") +
		textRow("RadiusLg", "Large radius", "0.75rem")

	// Build theme gallery cards
	presets := theme.AllPresets()
	galleryCards := ""
	for _, t := range presets {
		galleryCards += themeCardHTML(t)
	}

	body := `<div class="page-header">
  <h1>Theme Studio</h1>
  <div class="page-actions">
    <span class="text-sm muted" data-theme-status>Loading…</span>
    <button type="button" class="btn btn--ghost btn--sm" data-theme-revert>Revert</button>
    <button type="button" class="btn btn--primary btn--sm" data-theme-apply>Apply theme</button>
  </div>
</div>

<div class="theme-studio" data-theme-studio>
  <div class="theme-studio__main">

    <!-- ── Gallery ── -->
    <div class="card mb-6">
      <div class="card-title">Gallery</div>
      <div class="text-sm muted mb-3">` + strconv.Itoa(len(presets)) + ` built-in themes. Click to preview, apply to set your site's theme.</div>
      <div class="theme-gallery" data-theme-gallery aria-label="Theme gallery">
        ` + galleryCards + `
      </div>
    </div>

    <!-- ── Active preset indicator ── -->
    <div class="card mb-6">
      <div class="card-title" data-active-preset-name>Current theme</div>
      <div class="text-sm muted">Fine-tune any token below to customise the active theme.</div>
    </div>

    <div class="card mb-6">
      <div class="card-title">Dark mode colours</div>
      <div class="theme-fields">` + darkRows + `</div>
    </div>

    <div class="card mb-6">
      <div class="card-title">Light mode colours</div>
      <div class="theme-fields">` + lightRows + `</div>
    </div>

    <div class="card">
      <div class="card-title">Typography &amp; layout</div>
      <div class="theme-fields theme-fields--text">` + typoRows + `</div>
    </div>
  </div>

  <aside class="theme-studio__preview" aria-label="Live preview">
    <div class="card-title">Live preview</div>
    <div class="theme-prev" data-theme-preview>
      <article class="theme-prev__article">
        <h1 class="theme-prev__h1">The quick brown fox</h1>
        <p class="theme-prev__p">VayuPress is a <strong>sovereign, observable</strong> publishing runtime. Every colour and typeface is a tunable token.</p>
        <h2 class="theme-prev__h2">Getting started</h2>
        <p class="theme-prev__p">Pick a preset, tweak a token, and click <em>Apply</em>. Changes reach <code class="theme-prev__code">/theme.css</code> immediately.</p>
        <blockquote class="theme-prev__quote">Simplicity is the ultimate sophistication.</blockquote>
        <p class="theme-prev__p"><a class="theme-prev__link" href="#">Read the documentation →</a></p>
        <div class="theme-prev__chips">
          <span class="theme-prev__chip theme-prev__chip--accent">accent</span>
          <span class="theme-prev__chip theme-prev__chip--hi">highlight</span>
          <span class="theme-prev__chip theme-prev__chip--green">success</span>
        </div>
      </article>
    </div>
  </aside>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-theme.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Theme Studio", "theme", cfg, htmpl.HTML(body)))
}