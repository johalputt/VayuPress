package main

// admin_v3_theme.go — Admin v3 "Theme Studio" surface (VayuOS consolidation).
//
// Folds the v1/v2 Theme Studio (preset gallery + design-token editor + live
// preview) into the single v3 admin. The heavy lifting — preset definitions,
// hex/font/dimension validation, CSS compilation, persistence — already lives in
// internal/theme and the shared handlers (handleThemePresets/Tokens/Preview/
// Apply). This file adds the v3 page shell; the JSON endpoints are reused under
// session-friendly /os/api/theme/* mirrors registered in admin_v3_ui.go.
//
// CSP posture matches the rest of admin v3: zero inline styles, the only inline
// <script> carries the per-request nonce, every dynamic string is escaped. The
// live preview never injects a <style> element — it sets --vp-* custom
// properties on the preview container through the CSSOM (scripted style writes
// are not gated by style-src), so no compiled-CSS string is ever parsed client
// side.

import (
	"html"
	"net/http"

	"github.com/johalputt/vayupress/internal/render"
)

// themeColorField is one editable colour token. Field is the canonical Tokens
// field name (matched case-insensitively by applyOverrides); Vari is the public
// --vp-* variable it maps to for the live preview (dark-mode tokens only).
type themeColorField struct {
	Field string // e.g. "AccentDark"
	Label string
	Vari  string // e.g. "accent" → --vp-accent (empty when not previewed)
}

// themeDarkColors are the dark-mode tokens, each wired to a preview variable.
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

// themeLightColors are the light-mode tokens (no live preview — preview is dark).
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

// colorRow renders one colour-token control. The colour input carries the
// canonical field name and (when set) the preview variable so the JS can both
// serialise the token and live-update the preview without a server round-trip.
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

// textRow renders a typography/layout text token control.
func textRow(field, label, placeholder string) string {
	return `<label class="theme-field theme-field--text">
  <span class="theme-field__label">` + html.EscapeString(label) + `</span>
  <input type="text" class="input" data-token="` + html.EscapeString(field) + `" placeholder="` + html.EscapeString(placeholder) + `" aria-label="` + html.EscapeString(label) + `">
</label>`
}

func (a *App) handleV3Theme(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())

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
    <div class="card mb-6">
      <div class="card-title">Presets</div>
      <div class="text-sm muted mb-3">Start from a built-in palette, then fine-tune any token below.</div>
      <div class="theme-presets" data-theme-presets aria-label="Theme presets"></div>
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
    <div class="text-xs muted mt-3">Preview reflects dark-mode tokens. Light-mode values apply on readers whose system is set to light.</div>
  </aside>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-v3-theme.js"></script>`

	writeV3HTML(w, adminV3Layout(nonce, "Theme Studio", "theme", cfg, body))
}
