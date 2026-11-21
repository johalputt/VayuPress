// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_theme.go — VayuOS "Theme Studio" surface (VayuOS consolidation).
//
// Folds the v1/v2 Theme Studio (preset gallery + design-token editor + live
// preview) into the single os admin. The heavy lifting — preset definitions,
// hex/font/dimension validation, CSS compilation, persistence — already lives in
// internal/theme and the shared handlers (handleThemePresets/Tokens/Preview/
// Apply). This file adds the os page shell; the JSON endpoints are reused under
// session-friendly /os/api/theme/* mirrors registered in admin_os_ui.go.
//
// CSP posture matches the rest of VayuOS: zero inline styles, the only inline
// <script> carries the per-request nonce, every dynamic string is escaped. The
// live preview never injects a <style> element — it sets --vp-* custom
// properties on the preview container through the CSSOM (scripted style writes
// are not gated by style-src), so no compiled-CSS string is ever parsed client
// side.

import (
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/theme"
	"github.com/johalputt/vayupress/internal/ui"
)

// themeExport is the on-disk JSON envelope for a full VayuPress theme: design
// tokens (palette/typography/layout) plus the site-wide custom CSS and the
// validated head/SEO meta. It round-trips through the export/import endpoints.
type themeExport struct {
	Schema     int             `json:"vayupress_theme"`
	Version    string          `json:"version,omitempty"`
	ExportedAt string          `json:"exported_at,omitempty"`
	Tokens     theme.Tokens    `json:"tokens"`
	CustomCSS  string          `json:"custom_css"`
	Head       themeHeadExport `json:"head"`
}

type themeHeadExport struct {
	Keywords     string `json:"keywords"`
	ThemeColor   string `json:"theme_color"`
	Robots       string `json:"robots"`
	VerifyGoogle string `json:"verify_google"`
	VerifyBing   string `json:"verify_bing"`
}

// The pieces of CSS and HTML the Studio names inside a translated sentence:
// code, the same in every language.
const (
	themeCSSPath = "/theme.css"
	cssImport    = "@import"
	cssURL       = "url()"
	metaTag      = "<meta>"
	// typeSpecimen is the pair of letters a theme card shows its headline
	// type in: a sample of the face, not a word.
	typeSpecimen = "Aa"
)

// themeCardSVG renders a miniature REAL page for a preset — masthead, display
// headline in the preset's own palette, body lines and an accent button — as
// inline SVG. Every colour is a presentational fill/text attribute (not a style
// attribute), so strict CSP holds; every string is escaped.
func themeCardSVG(p theme.Tokens) string {
	return `<svg class="theme-card__art" viewBox="0 0 120 84" width="120" height="84" role="img" aria-hidden="true">` +
		`<rect x="0" y="0" width="120" height="84" rx="7" fill="` + esc(p.BgDark) + `"/>` +
		// Masthead: brand dot + wordmark + nav dashes.
		`<circle cx="12" cy="13" r="3" fill="` + esc(p.AccentDark) + `"/>` +
		`<rect x="19" y="10.5" width="26" height="5" rx="2.5" fill="` + esc(p.TextDark) + `" opacity="0.9"/>` +
		`<rect x="86" y="11" width="8" height="3.4" rx="1.7" fill="` + esc(p.MutedDark) + `"/>` +
		`<rect x="97" y="11" width="8" height="3.4" rx="1.7" fill="` + esc(p.MutedDark) + `"/>` +
		// Display headline — the actual type sample.
		`<text x="10" y="42" font-family="system-ui,-apple-system,'Segoe UI',sans-serif" font-size="21" font-weight="700" letter-spacing="-0.6" fill="` + esc(p.TextDark) + `">` + typeSpecimen + `</text>` +
		`<text x="44" y="41" font-family="system-ui,-apple-system,'Segoe UI',sans-serif" font-size="7.5" font-weight="600" fill="` + esc(p.AccentDark) + `">` + esc(p.Name) + `</text>` +
		// Body copy lines.
		`<rect x="10" y="50" width="100" height="4" rx="2" fill="` + esc(p.MutedDark) + `"/>` +
		`<rect x="10" y="58" width="72" height="4" rx="2" fill="` + esc(p.MutedDark) + `" opacity="0.75"/>` +
		// Accent button + secondary swatch dots (incl. light-mode hint).
		`<rect x="10" y="68" width="30" height="9" rx="4.5" fill="` + esc(p.AccentDark) + `"/>` +
		`<circle cx="98" cy="72.5" r="3" fill="` + esc(p.AccentLight) + `"/>` +
		`<circle cx="107" cy="72.5" r="3" fill="` + esc(p.BgLight) + `" stroke="` + esc(p.MutedDark) + `" stroke-width="0.8"/>` +
		`</svg>`
}

// hexLum is Rec.601-ish relative luminance on 0–255 RGB — enough to tell a
// dark-first preset from a light-first one for gallery filtering.
func hexLum(hex string) float64 {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0
	}
	v := make([]float64, 3)
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
		if err != nil {
			return 0
		}
		v[i] = float64(n)
	}
	return 0.299*v[0] + 0.587*v[1] + 0.114*v[2]
}

// themePresetCards renders the Tumblr-style theme gallery server-side: each card
// is a real miniature page (themeCardSVG) tagged with its archetype and default
// scheme so the Studio's filter chips and search can narrow 30 presets fast.
func themePresetCards(lang string) string {
	out := ""
	for _, p := range theme.AllPresets() {
		name := html.EscapeString(p.Name)
		scheme := "dark"
		if hexLum(p.BgDark) > hexLum(p.BgLight) {
			scheme = "light"
		}
		arch := theme.ArchetypeForPreset(p.Name)
		if arch == "" {
			arch = "design"
		}
		out += `<button type="button" class="theme-card" data-preset="` + name +
			`" data-archetype="` + arch + `" data-scheme="` + scheme +
			`" data-search="` + strings.ToLower(name+" "+arch+" "+scheme) + `" aria-label="` + html.EscapeString(i18n.T(lang, "Apply the {theme} theme", "theme", p.Name)) + `">` +
			themeCardSVG(p) +
			`<span class="theme-card__name" translate="no">` + name + `</span></button>`
	}
	return out
}

// themeColorField is one editable colour token. Field is the canonical Tokens
// field name (matched case-insensitively by applyOverrides); Vari is the public
// --vp-* variable it maps to for the live preview (dark-mode tokens only).
type themeColorField struct {
	Field string // e.g. "AccentDark"
	Label string
	Vari  string // e.g. "accent" → --vp-accent (empty when not previewed)
}

// brandColorFields are the accent colours surfaced prominently in the Brand
// group (the most-used controls), with live preview wiring for the dark accents.
func brandColorFields(lang string) []themeColorField {
	return []themeColorField{
		{"AccentDark", i18n.T(lang, "Accent"), "accent"},
		{"Accent2Dark", i18n.T(lang, "Accent 2"), "accent2"},
		{"AccentLight", i18n.T(lang, "Accent (light mode)"), ""},
		{"Accent2Light", i18n.T(lang, "Accent 2 (light mode)"), ""},
	}
}

// themeDarkColors are the dark-mode surface tokens (accents live in the Brand
// group), each wired to a preview variable.
func themeDarkColors(lang string) []themeColorField {
	return []themeColorField{
		{"BgDark", i18n.T(lang, "Background"), "bg"},
		{"SurfaceDark", i18n.T(lang, "Surface"), "surface"},
		{"TextDark", i18n.T(lang, "Text"), "text"},
		{"MutedDark", i18n.T(lang, "Muted"), "muted"},
		{"HiDark", i18n.T(lang, "Highlight"), "hi"},
		{"GreenDark", i18n.T(lang, "Success"), "green"},
	}
}

// themeLightColors are the light-mode surface tokens (no live preview — preview
// is dark; accents live in the Brand group).
func themeLightColors(lang string) []themeColorField {
	return []themeColorField{
		{"BgLight", i18n.T(lang, "Background"), ""},
		{"SurfaceLight", i18n.T(lang, "Surface"), ""},
		{"TextLight", i18n.T(lang, "Text"), ""},
		{"MutedLight", i18n.T(lang, "Muted"), ""},
		{"HiLight", i18n.T(lang, "Highlight"), ""},
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

// optionSelectRow renders one customization option as a labelled <select> bound
// to data-token-opt. When themesCSV is non-empty the row carries data-opt-theme
// and starts hidden, so the Studio JS shows it only for matching themes.
func optionSelectRow(lang string, o theme.Option, themesCSV string) string {
	opts := ""
	for _, c := range o.Choices {
		// The default is marked: it is not always the first choice (text size
		// runs 17 to 23 and defaults to 20), and the Studio falls back to it.
		def := ""
		if c.Value == o.Default {
			def = ` data-default`
		}
		opts += `<option value="` + html.EscapeString(c.Value) + `"` + def + `>` + html.EscapeString(themeWord(lang, c.Label)) + `</option>`
	}
	hint := ""
	if o.Help != "" {
		hint = `<span class="theme-field__hint">` + html.EscapeString(themeWord(lang, o.Help)) + `</span>`
	}
	// A shared option is marked so the Studio can hide it for a theme that
	// brings its own layout, which the shared options would not change.
	attr := ` data-opt-shared`
	if themesCSV != "" {
		attr = ` data-opt-theme="` + html.EscapeString(themesCSV) + `" hidden`
	}
	return `<label class="theme-field theme-field--text"` + attr + `>
  <span class="theme-field__label">` + html.EscapeString(themeWord(lang, o.Label)) + `</span>
  <select class="input" data-token-opt="` + html.EscapeString(o.Key) + `" aria-label="` + html.EscapeString(themeWord(lang, o.Label)) + `">` + opts + `</select>` + hint + `</label>`
}

// optionRowsByKeys renders the named options (shared or per-theme) in order, so
// the Studio can compose them into Ghost-style groups (Brand, Layout, …).
func optionRowsByKeys(lang string, keys ...string) string {
	out := ""
	for _, k := range keys {
		for _, o := range theme.AllOptions() {
			if o.Key == k {
				out += optionSelectRow(lang, o, "")
				goto next
			}
		}
		for _, to := range theme.PerThemeOptions() {
			if to.Option.Key == k {
				out += optionSelectRow(lang, to.Option, strings.Join(to.Themes, ","))
				goto next
			}
		}
	next:
	}
	return out
}

// fontPairSelectHTML renders a friendly "Font pairing" quick-set: each option
// carries a sans + mono font stack (system/web-safe only — zero external
// requests) that the Studio JS applies to the FontSans/FontMono tokens at once.
// "Keep current" is the default so loading a preset doesn't force a pairing.
func fontPairSelectHTML(lang string) string {
	type pair struct{ Label, Sans, Mono string }
	pairs := []pair{
		{i18n.T(lang, "Keep current"), "", ""},
		{i18n.T(lang, "System UI"), `system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif`, `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`},
		{i18n.T(lang, "Modern (Inter-style)"), `"Inter", system-ui, -apple-system, "Segoe UI", sans-serif`, `ui-monospace, SFMono-Regular, Menlo, monospace`},
		{i18n.T(lang, "Classic serif"), `Georgia, Cambria, "Times New Roman", Times, serif`, `"Courier New", ui-monospace, monospace`},
		{i18n.T(lang, "Editorial serif"), `"Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif`, `ui-monospace, Menlo, monospace`},
		{i18n.T(lang, "Humanist"), `"Optima", Candara, "Segoe UI", "Helvetica Neue", sans-serif`, `ui-monospace, Menlo, monospace`},
		{i18n.T(lang, "Geometric"), `"Avenir Next", "Century Gothic", Futura, system-ui, sans-serif`, `ui-monospace, Menlo, monospace`},
		{i18n.T(lang, "Monospace"), `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`, `ui-monospace, SFMono-Regular, Menlo, monospace`},
	}
	opts := ""
	for _, p := range pairs {
		opts += `<option value="` + html.EscapeString(p.Label) + `" data-sans="` + html.EscapeString(p.Sans) + `" data-mono="` + html.EscapeString(p.Mono) + `">` + html.EscapeString(p.Label) + `</option>`
	}
	return `<label class="theme-field theme-field--text mb-4">
  <span class="theme-field__label">` + esc(i18n.T(lang, "Font pairing")) + `</span>
  <select class="input" data-font-pair aria-label="` + esc(i18n.T(lang, "Font pairing")) + `">` + opts + `</select>
  <span class="theme-field__hint">` + esc(i18n.T(lang, "Sets the body and mono fonts in one click; fine-tune the exact stacks below. All are system or web-safe fonts: no font is loaded from elsewhere.")) + `</span>
</label>`
}

func (a *App) handleOSTheme(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	lang := i18n.Lang(r.Context())
	t := func(s string) string { return esc(s) }

	// Current persisted custom CSS + head/SEO values (Tumblr-style code editor).
	// Guard the settings store like every other settings-dependent handler does:
	// if it isn't ready yet (startup race / settings-store init failure) we still
	// render the full Studio — including the theme gallery — from Defaults rather
	// than dereferencing a nil store and panicking (which would 500 the page and
	// make the gallery appear to "not show" at all). A nil map reads safely.
	var vals map[string]string
	if a.siteSettings != nil {
		vals, _ = a.siteSettings.GetAll(r.Context(), osScope(r))
	}
	val := func(k string) string {
		if v, ok := vals[k]; ok {
			return v
		}
		return settings.Defaults[k]
	}

	darkRows := ""
	for _, f := range themeDarkColors(lang) {
		darkRows += colorRow(f)
	}
	lightRows := ""
	for _, f := range themeLightColors(lang) {
		lightRows += colorRow(f)
	}
	brandRows := ""
	for _, f := range brandColorFields(lang) {
		brandRows += colorRow(f)
	}
	typoRows := textRow("FontSans", i18n.T(lang, "Sans-serif stack"), "system-ui, sans-serif") +
		textRow("FontMono", i18n.T(lang, "Monospace stack"), "ui-monospace, monospace") +
		textRow("FontSizeBase", i18n.T(lang, "Base font size"), "1rem") +
		textRow("LineHeight", i18n.T(lang, "Line height"), "1.6") +
		textRow("MaxWidth", i18n.T(lang, "Max content width"), "72ch") +
		textRow("RadiusSm", i18n.T(lang, "Small radius"), "0.25rem") +
		textRow("RadiusLg", i18n.T(lang, "Large radius"), "0.75rem")

	// Accessibility readout for the saved palette (see admin_os_theme_a11y.go).
	a11y := themeA11yChecks(
		val(settings.KeyThemeAccentDark), "",
		val(settings.KeyThemeAccentLight), "")

	faviconBust := time.Now().Format("150405")

	// The mark this page is EDITING, not the install's.
	//
	// It was the bare /favicon.ico at every mount, so a hosted domain's Theme
	// Studio showed the operator's own logo above a control labelled "Logo &
	// favicon" for that domain. The upload beneath it wrote install-wide too, so
	// the picture was at least consistent with the behaviour — both were wrong
	// together. Now the upload is scoped, and this must follow it or the page
	// would show one site's mark while saving another's.
	brandMarkURL := "/favicon.ico?t=" + faviconBust
	if d, ok := osScopedDomain(r); ok {
		brandMarkURL = "/os/d/" + html.EscapeString(d.ID) + "/branding/mark?t=" + faviconBust
	}
	navSeed := html.EscapeString(val(settings.KeyNavItems))
	membershipChecked := ""
	if val(settings.KeyMembershipButtons) == "true" {
		membershipChecked = " checked"
	}
	heroChecked := ""
	if val(settings.KeyHomeHero) == "true" {
		heroChecked = " checked"
	}

	// The page is a document (page grammar: "the live preview is the
	// document, with the Studio as the inspector, tabs replacing the chips"):
	// the preview fills the page and the Studio's four groups are tabs of the
	// inspector beside it, each a run of sections, so every control is two
	// clicks away at most and none is folded inside another.
	tab := func(key, label string, on bool) string {
		sel := "false"
		if on {
			sel = "true"
		}
		return `<button type="button" class="sa-itabs__tab" role="tab" id="tt-` + key + `" aria-controls="tp-` + key + `" aria-selected="` + sel + `" data-theme-tab="` + key + `">` + t(label) + `</button>`
	}
	panel := func(key string, on bool, body string) string {
		hidden := " hidden"
		if on {
			hidden = ""
		}
		return `<div class="sa-itabs__panel" role="tabpanel" id="tp-` + key + `" aria-labelledby="tt-` + key + `" data-theme-panel="` + key + `"` + hidden + `>` + body + `</div>`
	}
	sec := func(title, body string) string { return string(ui.InspectorSection(title, ui.HTML(body))) }

	chip := func(filter, label string, on bool) string {
		pressed, cls := "false", "seg-btn"
		if on {
			pressed, cls = "true", "seg-btn is-active"
		}
		return `<button type="button" class="` + cls + `" aria-pressed="` + pressed + `" data-ffilter="` + filter + `">` + t(label) + `</button>`
	}
	design := sec(i18n.T(lang, "Presets"), `<div class="theme-filter" data-theme-filter>
          <input type="search" class="input theme-filter__search" data-theme-search placeholder="`+t(i18n.T(lang, "Search 30 themes"))+`" aria-label="`+t(i18n.T(lang, "Search themes"))+`">
          <div class="theme-filter__chips" role="group" aria-label="`+t(i18n.T(lang, "Filter themes"))+`">`+
		chip("all", i18n.T(lang, "All"), true)+chip("scheme:dark", i18n.T(lang, "Dark"), false)+chip("scheme:light", i18n.T(lang, "Light"), false)+
		chip("arch:minimal", themeWord(lang, "Minimal"), false)+chip("arch:classic", themeWord(lang, "Classic"), false)+chip("arch:magazine", themeWord(lang, "Magazine"), false)+
		chip("arch:editorial", themeWord(lang, "Editorial"), false)+chip("arch:bold", themeWord(lang, "Bold"), false)+chip("arch:design", i18n.T(lang, "Design"), false)+`
          </div>
          <span class="sa-insp__hint" data-theme-filter-count role="status" aria-live="polite"></span>
        </div>
        <div class="theme-gallery" data-theme-presets aria-label="`+t(i18n.T(lang, "Theme presets"))+`">`+themePresetCards(lang)+`</div>`) +
		sec(i18n.T(lang, "Logo and share image"), `<p class="sa-insp__text">`+t(i18n.T(lang, "These stay when the theme changes."))+`</p>
        <div class="cz-logo">
          <img id="brand-favicon-img" class="cz-logo__img" src="`+brandMarkURL+`" alt="`+t(i18n.T(lang, "Current site mark"))+`" width="44" height="44">
          <div class="cz-logo__meta"><div class="cz-logo__title">`+t(i18n.T(lang, "Logo and favicon"))+`</div><div class="sa-insp__hint" id="brand-favicon-state">`+t(i18n.T(lang, "The favicon and the logo in the menu."))+`</div></div>
        </div>
        <div class="cz-upload">
          <input type="file" id="brand-favicon-file" accept="image/png,image/jpeg,image/webp,image/gif,image/x-icon,.png,.jpg,.jpeg,.webp,.gif,.ico" class="input" aria-label="`+t(i18n.T(lang, "Logo file"))+`">
          <button type="button" class="btn btn--sm" id="brand-favicon-upload">`+t(i18n.T(lang, "Upload"))+`</button>
          <button type="button" class="btn btn--ghost btn--sm" id="brand-favicon-remove">`+t(i18n.T(lang, "Default"))+`</button>
          <span id="brand-favicon-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>
        <span class="sa-insp__hint">`+t(i18n.T(lang, "PNG or ICO, square, up to 256 KB. Live at once."))+`</span>
        <div class="cz-logo">
          <img id="og-img" class="cz-logo__img" src="/theme-assets/og?t=`+faviconBust+`" alt="`+t(i18n.T(lang, "Current share image"))+`" width="64" height="34">
          <div class="cz-logo__meta"><div class="cz-logo__title">`+t(i18n.T(lang, "Share image"))+`</div><div class="sa-insp__hint" id="og-img-state">`+t(i18n.T(lang, "Shown when the site or a post is shared."))+`</div></div>
        </div>
        <div class="cz-upload">
          <input type="file" id="og-img-file" accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp" class="input" aria-label="`+t(i18n.T(lang, "Share image file"))+`">
          <button type="button" class="btn btn--sm" id="og-img-upload">`+t(i18n.T(lang, "Upload"))+`</button>
          <button type="button" class="btn btn--ghost btn--sm" id="og-img-remove">`+t(i18n.T(lang, "Remove"))+`</button>
          <span id="og-img-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>
        <span class="sa-insp__hint">`+t(i18n.T(lang, "PNG, JPEG or WebP, up to 1.5 MB."))+`</span>
        <label class="sa-insp__check"><input type="checkbox" class="toggle" role="switch" id="site-membership"`+membershipChecked+`> `+t(i18n.T(lang, "Sign in and Sign up in the menu"))+`</label>
        <span class="sa-insp__hint" id="site-membership-status" role="status" aria-live="polite"></span>`)

	colour := sec(i18n.T(lang, "Palette from one colour"), `<div class="cz-gen" data-studio-gen>
          <div class="cz-upload">
            <input type="color" class="theme-field__color" value="#e0562f" data-gen-accent aria-label="`+t(i18n.T(lang, "Seed accent"))+`">
            <select class="input" data-gen-mood aria-label="`+t(i18n.T(lang, "Mood"))+`"><option value="calm">`+t(i18n.T(lang, "Calm"))+`</option><option value="vivid">`+t(i18n.T(lang, "Vivid"))+`</option><option value="muted">`+t(i18n.T(lang, "Muted"))+`</option></select>
            <button type="button" class="btn btn--sm" data-gen-apply>`+t(i18n.T(lang, "Recolour"))+`</button>
            <button type="button" class="btn btn--ghost btn--sm" data-gen-surprise>`+t(i18n.T(lang, "Surprise me"))+`</button>
          </div>
          <span class="sa-insp__hint">`+t(i18n.T(lang, "One accent becomes a whole dark and light palette."))+`</span>
          <span id="gen-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>`) +
		sec(i18n.T(lang, "Brand colours"), a11ySummary(lang, a11y)+`<div class="theme-fields">`+brandRows+`</div>
        <div class="theme-fields theme-fields--text">`+optionRowsByKeys(lang, "scheme", "accentfill", "paper")+`</div>`+themeA11yPanel(lang, a11y)) +
		`<details class="sa-insp sa-insp--more"><summary class="sa-insp__head">` + t(i18n.T(lang, "Every colour, dark")) + `</summary><div class="theme-fields">` + darkRows + `</div></details>` +
		`<details class="sa-insp sa-insp--more"><summary class="sa-insp__head">` + t(i18n.T(lang, "Every colour, light")) + `</summary><div class="theme-fields">` + lightRows + `</div></details>`

	var halcyonRows strings.Builder
	for _, o := range theme.HalcyonOptions() {
		halcyonRows.WriteString(optionSelectRow(lang, o, theme.Halcyon().Name))
	}
	layout := sec("Halcyon", `<div class="theme-fields theme-fields--text">`+halcyonRows.String()+`</div>`) +
		sec(i18n.T(lang, "Layout"), `<div class="theme-fields theme-fields--text">`+optionRowsByKeys(lang, "archetype", "width", "corners", "feedlayout", "cardimage", "headeralign", "navstyle", "cardstyle", "density", "columnrules", "pagefade")+`</div>`) +
		sec(i18n.T(lang, "Hero"), `<label class="sa-insp__check"><input type="checkbox" class="toggle" role="switch" id="home-hero"`+heroChecked+`> `+t(i18n.T(lang, "A hero on the home page"))+`</label>
        <span class="sa-insp__hint" id="home-hero-status" role="status" aria-live="polite"></span>
        <div class="cz-logo">
          <img id="hero-img" class="cz-logo__img" src="/theme-assets/hero?t=`+faviconBust+`" alt="`+t(i18n.T(lang, "Current hero image"))+`" width="64" height="40">
          <div class="cz-logo__meta"><div class="cz-logo__title">`+t(i18n.T(lang, "Hero image"))+`</div><div class="sa-insp__hint" id="hero-img-state">`+t(i18n.T(lang, "Shown when the hero background is an uploaded image."))+`</div></div>
        </div>
        <div class="cz-upload">
          <input type="file" id="hero-img-file" accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp" class="input" aria-label="`+t(i18n.T(lang, "Hero image file"))+`">
          <button type="button" class="btn btn--sm" id="hero-img-upload">`+t(i18n.T(lang, "Upload"))+`</button>
          <button type="button" class="btn btn--ghost btn--sm" id="hero-img-remove">`+t(i18n.T(lang, "Remove"))+`</button>
          <span id="hero-img-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>
        <div class="theme-fields theme-fields--text">`+optionRowsByKeys(lang, "herostyle", "herobg", "heroheight")+`</div>`) +
		sec(i18n.T(lang, "Type"), fontPairSelectHTML(lang)+`<div class="theme-fields theme-fields--text">`+optionRowsByKeys(lang, "headingcase", "headingscale", "dropcap")+typoRows+`</div>`) +
		sec(i18n.T(lang, "Posts"), `<div class="theme-fields theme-fields--text">`+optionRowsByKeys(lang, "articlealign", "articlemeta", "relatedposts", "trendingposts", "authorbox", "linkstyle", "readingprogress")+`</div>
        <label class="theme-field theme-field--text"><span class="theme-field__label">`+t(i18n.T(lang, "Author bio"))+`</span>
          <textarea class="input" id="author-bio" rows="4" maxlength="`+strconv.Itoa(settings.MaxAuthorBioLen)+`" placeholder="`+t(i18n.T(lang, "A few lines about the author"))+`">`+html.EscapeString(val(settings.KeyAuthorBio))+`</textarea>
          <span class="theme-field__hint" id="author-bio-status">`+t(i18n.T(lang, "In the author box, beside the author's name; up to {n} characters. Saved as you leave the field.", "n", settings.MaxAuthorBioLen))+`</span></label>`)

	advanced := sec(i18n.T(lang, "Menu"), `<p class="sa-insp__text">`+t(i18n.T(lang, "Saved straight to the live site; the preview shows a sample menu."))+`</p>
        <div id="cz-nav-rows" data-nav-editor></div>
        <div class="cz-upload">
          <button type="button" class="btn btn--ghost btn--sm" id="cz-nav-add">`+t(i18n.T(lang, "Add a link"))+`</button>
          <button type="button" class="btn btn--sm" id="cz-nav-save">`+t(i18n.T(lang, "Save the menu"))+`</button>
          <span class="sa-insp__hint" id="cz-nav-status"></span>
        </div>
        <input type="hidden" id="cz-nav-seed" value="`+navSeed+`">`) +
		sec(i18n.T(lang, "Custom CSS"), `<textarea class="input theme-code" data-theme-css rows="10" maxlength="65536" spellcheck="false" aria-label="`+t(i18n.T(lang, "Custom CSS"))+`" placeholder="/* .vayu-post-title { letter-spacing: -0.02em; } */">`+html.EscapeString(val(settings.KeyThemeCustomCSS))+`</textarea>`+
			string(ui.ExplainTitled(i18n.T(lang, "How this works"), ui.HTML(`<p>`+escFill(i18n.T(lang, "Served from this site as {file}, after the theme's own styles. {import} and outside {url} are removed, so it loads nothing from elsewhere. Up to 64 KB.",
				"file", htmlMark(0), "import", htmlMark(1), "url", htmlMark(2)), "<code>"+themeCSSPath+"</code>", "<code>"+cssImport+"</code>", "<code>"+cssURL+"</code>")+`</p>`)))) +
		sec(i18n.T(lang, "Head tags"), `<div class="theme-fields theme-fields--text">
          <label class="theme-field theme-field--text"><span class="theme-field__label">`+t(i18n.T(lang, "Keywords"))+`</span>
            <input type="text" class="input" data-head="keywords" maxlength="256" value="`+html.EscapeString(val(settings.KeyHeadKeywords))+`" placeholder="`+t(i18n.T(lang, "publishing, sovereignty"))+`"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">`+t(i18n.T(lang, "Theme colour"))+`</span>
            <input type="text" class="input" data-head="theme_color" maxlength="7" value="`+html.EscapeString(val(settings.KeyHeadThemeColor))+`" placeholder="#0d9488"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">`+t(i18n.T(lang, "Robots"))+`</span>
            <select class="input" data-head="robots">`+robotsOptionsHTML(lang, val(settings.KeyHeadRobots))+`</select></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">`+t(i18n.T(lang, "Google verification"))+`</span>
            <input type="text" class="input" data-head="verify_google" maxlength="128" value="`+html.EscapeString(val(settings.KeyHeadVerifyGoogle))+`" placeholder="`+t(i18n.T(lang, "token"))+`"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">`+t(i18n.T(lang, "Bing verification"))+`</span>
            <input type="text" class="input" data-head="verify_bing" maxlength="128" value="`+html.EscapeString(val(settings.KeyHeadVerifyBing))+`" placeholder="`+t(i18n.T(lang, "token"))+`"></label>
        </div>
        <div class="cz-upload"><button type="button" class="btn btn--sm" data-theme-code-save>`+t(i18n.T(lang, "Save CSS and head tags"))+`</button><span class="sa-insp__hint" data-theme-code-status></span></div>`+
			string(ui.ExplainTitled(i18n.T(lang, "How this works"), ui.HTML(`<p>`+escFill(i18n.T(lang, "Written as escaped {meta} tags from this list only; raw head HTML is not accepted.", "meta", htmlMark(0)), "<code>"+esc(metaTag)+"</code>")+`</p>`)))) +
		sec(i18n.T(lang, "Move a theme"), `<p class="sa-insp__text">`+t(i18n.T(lang, "The whole theme as JSON, to keep or to bring to another install. An imported theme is checked before it goes live."))+`</p>
        <div class="cz-upload"><a class="btn btn--ghost btn--sm" href="/os/api/theme/export" download>`+t(i18n.T(lang, "Export"))+`</a></div>
        <div class="cz-upload">
          <input type="file" accept="application/json,.json" class="input" data-theme-import-file aria-label="`+t(i18n.T(lang, "Theme file"))+`">
          <button type="button" class="btn btn--sm" data-theme-import>`+t(i18n.T(lang, "Import"))+`</button>
          <span class="sa-insp__hint" data-theme-import-status></span>
        </div>`)

	inspector := `<div class="sa-itabs" role="tablist" aria-label="` + t(i18n.T(lang, "Theme settings")) + `">` + tab("design", i18n.T(lang, "Design"), true) + tab("colour", i18n.T(lang, "Colour"), false) + tab("layout", i18n.T(lang, "Layout"), false) + tab("advanced", i18n.T(lang, "Advanced"), false) + `</div>` +
		panel("design", true, design) + panel("colour", false, colour) + panel("layout", false, layout) + panel("advanced", false, advanced)

	opt := func(attr, value, label string, on bool) string {
		pressed := "false"
		if on {
			pressed = "true"
		}
		return `<button type="button" class="sa-seg__opt" ` + attr + `="` + value + `" aria-pressed="` + pressed + `">` + t(label) + `</button>`
	}
	preview := `<div class="theme-draft-banner" data-theme-draft hidden data-has-draft="0">
  <span>` + t(i18n.T(lang, "Resume the theme you were changing?")) + `</span>
  <button type="button" class="btn btn--sm" data-draft-resume>` + t(i18n.T(lang, "Restore")) + `</button>
  <button type="button" class="btn btn--ghost btn--sm" data-draft-discard>` + t(i18n.T(lang, "Discard")) + `</button>
</div>
<div class="customizer__toolbar">
  <div class="sa-seg" role="group" aria-label="` + t(i18n.T(lang, "Preview device")) + `">` +
		opt("data-theme-device", "desktop", i18n.T(lang, "Desktop"), true) + opt("data-theme-device", "tablet", i18n.T(lang, "Tablet"), false) + opt("data-theme-device", "mobile", i18n.T(lang, "Phone"), false) + `
  </div>
  <div class="sa-seg" role="group" aria-label="` + t(i18n.T(lang, "Preview page")) + `" data-theme-pages hidden>` +
		opt("data-theme-page", "home", i18n.T(lang, "Home"), true) + opt("data-theme-page", "topic", i18n.T(lang, "Topic"), false) + opt("data-theme-page", "article", i18n.T(lang, "Article"), false) +
		opt("data-theme-page", "search", i18n.T(lang, "Search"), false) + opt("data-theme-page", "signin", i18n.T(lang, "Sign in"), false) + `
  </div>
  <div class="sa-seg" role="group" aria-label="` + t(i18n.T(lang, "Preview colour scheme")) + `">` +
		opt("data-theme-scheme", "dark", i18n.T(lang, "Dark"), true) + opt("data-theme-scheme", "light", i18n.T(lang, "Light"), false) + `
  </div>
  <span class="sa-doc__fill"></span>
  <span class="sa-insp__hint" data-theme-preview-status>` + t(i18n.T(lang, "Live preview")) + `</span>
  <a class="btn btn--ghost btn--sm" data-theme-newtab href="#" target="_blank" rel="noopener">` + t(i18n.T(lang, "Open in a tab")) + ` ↗</a>
</div>
<div class="customizer__viewport" data-theme-viewport data-device="desktop">
  <div class="customizer__frame-wrap">
    <iframe class="customizer__frame" data-theme-frame title="` + t(i18n.T(lang, "Live theme preview")) + `" referrerpolicy="no-referrer"></iframe>
    <div class="customizer__frame-loading" data-theme-frame-loading>` + t(i18n.T(lang, "Building the preview")) + `</div>
  </div>
</div>`

	body := string(ui.Document(ui.DocumentPage{
		Title: i18n.T(lang, "Theme"),
		Hook:  "data-theme-studio",
		State: ui.HTML(`<span data-active-preset-name>` + t(i18n.T(lang, "Current theme")) + `</span> <span class="sa-doc__msg" data-theme-status>` + t(i18n.T(lang, "Loading")) + `</span>`),
		Actions: ui.HTML(`<span class="sa-doc__msg" data-theme-changes hidden aria-live="polite"></span>` +
			`<a class="btn btn--ghost btn--sm" href="/os/theme/store">` + t(i18n.T(lang, "Theme store")) + `</a>` +
			`<button type="button" class="btn btn--ghost btn--sm" data-theme-revert>` + t(i18n.T(lang, "Revert")) + `</button>` +
			`<button type="button" class="btn btn--ghost btn--sm btn--icon" data-studio-focus aria-pressed="true" aria-label="` + t(i18n.T(lang, "Theme settings beside the preview")) + `" title="` + t(i18n.T(lang, "Hide or show the settings")) + `">` + saIcon("settings") + `</button>` +
			`<button type="button" class="btn btn--primary btn--sm" data-theme-apply>` + t(i18n.T(lang, "Apply theme")) + `</button>`),
		Inspector: ui.HTML(inspector),
		Label:     i18n.T(lang, "Theme settings"),
	}, ui.HTML(preview))) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-theme.js?v=` + assetVer("js/admin-os-theme.js") + `"></script>`

	// Resumable draft (Wave C): surface the autosaved snapshot, if any, so the
	// client banner can offer Restore/Discard. Payload is escaped; the banner
	// stays hidden unless a draft exists.
	if d := themeDraftFor(vals); d != "" {
		body = strings.Replace(body, `data-has-draft="0"`,
			`data-has-draft="1" data-draft-payload="`+html.EscapeString(d)+`"`, 1)
	}
	writeOSHTML(w, r, adminOSLayout(nonce, i18n.T(lang, "Theme Studio"), "theme", cfg, htmpl.HTML(body)))
}

// handleOSThemeCode persists the Theme Studio "Custom CSS" + head/SEO meta
// fields. It writes ONLY these keys (never the identity/palette ones) so a
// partial POST can't wipe unrelated settings, then refreshes the render
// pipeline and purges cached HTML. Custom CSS reaches the public site via the
// same-origin /theme.css stylesheet (CSP-safe). Raw <head> HTML is not
// accepted — head fields are validated to an escaped <meta> allowlist.
func (a *App) handleOSThemeCode(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "Settings cannot be saved while the install is in {mode} mode.", "mode", strings.ToLower(saModeLabelIn(i18n.Lang(r.Context()), cur)))})
		return
	}
	var body struct {
		CustomCSS    string `json:"custom_css"`
		Keywords     string `json:"keywords"`
		ThemeColor   string `json:"theme_color"`
		Robots       string `json:"robots"`
		VerifyGoogle string `json:"verify_google"`
		VerifyBing   string `json:"verify_bing"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The request could not be read: {error}", "error", err.Error())})
		return
	}

	css := strings.TrimSpace(body.CustomCSS)
	if len(css) > 64*1024 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "Custom CSS is over its 64 KB limit.")})
		return
	}
	keywords := strings.TrimSpace(body.Keywords)
	if len(keywords) > 256 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "Keywords are over their 256-character limit.")})
		return
	}
	themeColor := strings.TrimSpace(body.ThemeColor)
	if themeColor != "" && !hexColorRe.MatchString(themeColor) {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The theme colour must be a hex colour, like {example}.", "example", "#0d9488")})
		return
	}
	robots := strings.TrimSpace(body.Robots)
	if robots == "" {
		robots = settings.Defaults[settings.KeyHeadRobots]
	}
	if !settings.RobotsOptions[robots] {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "That is not a robots directive this page offers.")})
		return
	}
	verifyGoogle := strings.TrimSpace(body.VerifyGoogle)
	verifyBing := strings.TrimSpace(body.VerifyBing)
	for _, tok := range []string{verifyGoogle, verifyBing} {
		if tok != "" && !verifyTokenRe.MatchString(tok) {
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "A verification token holds only letters, digits and {chars}.", "chars", "- _ .")})
			return
		}
	}

	kv := map[string]string{
		settings.KeyThemeCustomCSS:   css,
		settings.KeyHeadKeywords:     keywords,
		settings.KeyHeadThemeColor:   themeColor,
		settings.KeyHeadRobots:       robots,
		settings.KeyHeadVerifyGoogle: verifyGoogle,
		settings.KeyHeadVerifyBing:   verifyBing,
	}
	if err := a.siteSettings.SetMany(r.Context(), osScope(r), kv); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "Saving failed: {error}", "error", err.Error())})
		return
	}

	// Re-read the full set so we refresh the render pipeline without clobbering
	// the identity/palette values this endpoint doesn't touch.
	// The render package's active settings are a PROCESS-WIDE singleton: they are
	// the primary site's live configuration. Pushing a hosted domain's values into
	// it would repaint the operator's own site with a client's theme until the next
	// restart — a cross-tenant write with no database change behind it.
	//
	// A hosted domain needs no refresh here at all: its pages are rendered from its
	// own scope on each request (brandForRequest), so the next request already sees
	// the save.
	if nv, err := a.siteSettings.GetAll(r.Context(), osScope(r)); err == nil && osScope(r).IsPrimary() {
		render.SetActiveSettings(render.SiteSettings{
			Name:            nv[settings.KeySiteName],
			Tagline:         nv[settings.KeySiteTagline],
			Description:     nv[settings.KeySiteDescription],
			Author:          nv[settings.KeySiteAuthor],
			AuthorBio:       nv[settings.KeyAuthorBio],
			ShowMembership:  nv[settings.KeyMembershipButtons] == "true",
			PrimaryLight:    nv[settings.KeyThemePrimaryLight],
			PrimaryDark:     nv[settings.KeyThemePrimaryDark],
			AccentLight:     nv[settings.KeyThemeAccentLight],
			AccentDark:      nv[settings.KeyThemeAccentDark],
			CustomCSS:       nv[settings.KeyThemeCustomCSS],
			Keywords:        nv[settings.KeyHeadKeywords],
			ThemeColor:      nv[settings.KeyHeadThemeColor],
			Robots:          nv[settings.KeyHeadRobots],
			VerifyGoogle:    nv[settings.KeyHeadVerifyGoogle],
			VerifyBing:      nv[settings.KeyHeadVerifyBing],
			NavJSON:         nv[settings.KeyNavItems],
			FooterJSON:      nv[settings.KeyFooterConfig],
			OGImage:         render.OGImagePath(nv[settings.KeyThemeOGImage]),
			ShowHero:        nv[settings.KeyHomeHero] == "true",
			CommentsEnabled: nv[settings.KeyFeatureComments] != "off",
		})
	}
	render.CachePurgeAll()

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "theme", Severity: "info",
		Msg: "theme custom CSS / head settings updated", RequestID: getRequestID(r),
	})
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSThemeExport streams the full active theme (tokens + custom CSS +
// head/SEO meta) as a downloadable JSON file.
func (a *App) handleOSThemeExport(w http.ResponseWriter, r *http.Request) {
	t, err := theme.Load(r.Context(), dbpkg.DB)
	if err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The theme could not be read: {error}", "error", err.Error())})
		return
	}
	vals, _ := a.siteSettings.GetAll(r.Context(), osScope(r))
	get := func(k string) string {
		if v, ok := vals[k]; ok {
			return v
		}
		return settings.Defaults[k]
	}
	env := themeExport{
		Schema:     1,
		Version:    Version,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Tokens:     t,
		CustomCSS:  get(settings.KeyThemeCustomCSS),
		Head: themeHeadExport{
			Keywords:     get(settings.KeyHeadKeywords),
			ThemeColor:   get(settings.KeyHeadThemeColor),
			Robots:       get(settings.KeyHeadRobots),
			VerifyGoogle: get(settings.KeyHeadVerifyGoogle),
			VerifyBing:   get(settings.KeyHeadVerifyBing),
		},
	}
	name := "vayupress-theme-" + time.Now().UTC().Format("20060102") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(env)
}

// handleOSThemeImport applies a theme JSON envelope produced by export: it
// validates the tokens (must compile), the custom CSS (<=16 KB) and the head
// meta (escaped allowlist), then persists tokens + settings, refreshes the
// render pipeline, and purges cached HTML.
func (a *App) handleOSThemeImport(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "A theme cannot be imported while the install is in {mode} mode.", "mode", strings.ToLower(saModeLabelIn(i18n.Lang(r.Context()), cur)))})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	var env themeExport
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "That is not a theme file: {error}", "error", err.Error())})
		return
	}
	if env.Schema != 1 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "That theme file names no version this install reads ({expected}).", "expected", "vayupress_theme: 1")})
		return
	}

	// Validate tokens by compiling them (rejects malformed colours/values).
	css, err := theme.CompileCSS(env.Tokens)
	if err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The theme's values are not valid: {error}", "error", err.Error())})
		return
	}

	ccss := strings.TrimSpace(env.CustomCSS)
	if len(ccss) > 64*1024 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The file's custom CSS is over its 64 KB limit.")})
		return
	}
	keywords := strings.TrimSpace(env.Head.Keywords)
	if len(keywords) > 256 {
		keywords = keywords[:256]
	}
	themeColor := strings.TrimSpace(env.Head.ThemeColor)
	if themeColor != "" && !hexColorRe.MatchString(themeColor) {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The file's theme colour is not a hex colour.")})
		return
	}
	robots := strings.TrimSpace(env.Head.Robots)
	if robots == "" || !settings.RobotsOptions[robots] {
		robots = settings.Defaults[settings.KeyHeadRobots]
	}
	verifyGoogle := strings.TrimSpace(env.Head.VerifyGoogle)
	verifyBing := strings.TrimSpace(env.Head.VerifyBing)
	for _, tok := range []string{verifyGoogle, verifyBing} {
		if tok != "" && !verifyTokenRe.MatchString(tok) {
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "A verification token in the file holds characters it may not.")})
			return
		}
	}

	if err := theme.Save(r.Context(), dbpkg.DB, env.Tokens); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The theme could not be saved: {error}", "error", err.Error())})
		return
	}
	render.ActivateTheme(env.Tokens, css)

	kv := map[string]string{
		settings.KeyThemeCustomCSS:   ccss,
		settings.KeyHeadKeywords:     keywords,
		settings.KeyHeadThemeColor:   themeColor,
		settings.KeyHeadRobots:       robots,
		settings.KeyHeadVerifyGoogle: verifyGoogle,
		settings.KeyHeadVerifyBing:   verifyBing,
	}
	if err := a.siteSettings.SetMany(r.Context(), osScope(r), kv); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": i18n.T(i18n.Lang(r.Context()), "The settings could not be saved: {error}", "error", err.Error())})
		return
	}
	// The render package's active settings are a PROCESS-WIDE singleton: they are
	// the primary site's live configuration. Pushing a hosted domain's values into
	// it would repaint the operator's own site with a client's theme until the next
	// restart — a cross-tenant write with no database change behind it.
	//
	// A hosted domain needs no refresh here at all: its pages are rendered from its
	// own scope on each request (brandForRequest), so the next request already sees
	// the save.
	if nv, err := a.siteSettings.GetAll(r.Context(), osScope(r)); err == nil && osScope(r).IsPrimary() {
		render.SetActiveSettings(render.SiteSettings{
			Name:            nv[settings.KeySiteName],
			Tagline:         nv[settings.KeySiteTagline],
			Description:     nv[settings.KeySiteDescription],
			Author:          nv[settings.KeySiteAuthor],
			AuthorBio:       nv[settings.KeyAuthorBio],
			ShowMembership:  nv[settings.KeyMembershipButtons] == "true",
			PrimaryLight:    nv[settings.KeyThemePrimaryLight],
			PrimaryDark:     nv[settings.KeyThemePrimaryDark],
			AccentLight:     nv[settings.KeyThemeAccentLight],
			AccentDark:      nv[settings.KeyThemeAccentDark],
			CustomCSS:       nv[settings.KeyThemeCustomCSS],
			Keywords:        nv[settings.KeyHeadKeywords],
			ThemeColor:      nv[settings.KeyHeadThemeColor],
			Robots:          nv[settings.KeyHeadRobots],
			VerifyGoogle:    nv[settings.KeyHeadVerifyGoogle],
			VerifyBing:      nv[settings.KeyHeadVerifyBing],
			NavJSON:         nv[settings.KeyNavItems],
			FooterJSON:      nv[settings.KeyFooterConfig],
			OGImage:         render.OGImagePath(nv[settings.KeyThemeOGImage]),
			ShowHero:        nv[settings.KeyHomeHero] == "true",
			CommentsEnabled: nv[settings.KeyFeatureComments] != "off",
		})
	}
	render.CachePurgeAll()
	dbpkg.AuditLog("theme.import", dbpkg.AuditActor(r), env.Tokens.Name, "")

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "theme", Severity: "info",
		Msg: "theme imported: " + env.Tokens.Name, RequestID: getRequestID(r),
	})
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "name": env.Tokens.Name})
}
