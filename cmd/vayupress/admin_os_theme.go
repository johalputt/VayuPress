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
		`<text x="10" y="42" font-family="system-ui,-apple-system,'Segoe UI',sans-serif" font-size="21" font-weight="700" letter-spacing="-0.6" fill="` + esc(p.TextDark) + `">Aa</text>` +
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
func themePresetCards() string {
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
			`" data-search="` + strings.ToLower(name+" "+arch+" "+scheme) + `" aria-label="Apply the ` + name + ` theme">` +
			themeCardSVG(p) +
			`<span class="theme-card__name">` + name + `</span></button>`
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
func brandColorFields() []themeColorField {
	return []themeColorField{
		{"AccentDark", "Accent", "accent"},
		{"Accent2Dark", "Accent 2", "accent2"},
		{"AccentLight", "Accent (light mode)", ""},
		{"Accent2Light", "Accent 2 (light mode)", ""},
	}
}

// themeDarkColors are the dark-mode surface tokens (accents live in the Brand
// group), each wired to a preview variable.
func themeDarkColors() []themeColorField {
	return []themeColorField{
		{"BgDark", "Background", "bg"},
		{"SurfaceDark", "Surface", "surface"},
		{"TextDark", "Text", "text"},
		{"MutedDark", "Muted", "muted"},
		{"HiDark", "Highlight", "hi"},
		{"GreenDark", "Success", "green"},
	}
}

// themeLightColors are the light-mode surface tokens (no live preview — preview
// is dark; accents live in the Brand group).
func themeLightColors() []themeColorField {
	return []themeColorField{
		{"BgLight", "Background", ""},
		{"SurfaceLight", "Surface", ""},
		{"TextLight", "Text", ""},
		{"MutedLight", "Muted", ""},
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

// optionSelectRow renders one customization option as a labelled <select> bound
// to data-token-opt. When themesCSV is non-empty the row carries data-opt-theme
// and starts hidden, so the Studio JS shows it only for matching themes.
func optionSelectRow(o theme.Option, themesCSV string) string {
	opts := ""
	for _, c := range o.Choices {
		opts += `<option value="` + html.EscapeString(c.Value) + `">` + html.EscapeString(c.Label) + `</option>`
	}
	hint := ""
	if o.Help != "" {
		hint = `<span class="theme-field__hint">` + html.EscapeString(o.Help) + `</span>`
	}
	attr := ""
	if themesCSV != "" {
		attr = ` data-opt-theme="` + html.EscapeString(themesCSV) + `" hidden`
	}
	return `<label class="theme-field theme-field--text"` + attr + `>
  <span class="theme-field__label">` + html.EscapeString(o.Label) + `</span>
  <select class="input" data-token-opt="` + html.EscapeString(o.Key) + `" aria-label="` + html.EscapeString(o.Label) + `">` + opts + `</select>` + hint + `</label>`
}

// optionRowsByKeys renders the named options (shared or per-theme) in order, so
// the Studio can compose them into Ghost-style groups (Brand, Layout, …).
func optionRowsByKeys(keys ...string) string {
	out := ""
	for _, k := range keys {
		for _, o := range theme.AllOptions() {
			if o.Key == k {
				out += optionSelectRow(o, "")
				goto next
			}
		}
		for _, to := range theme.PerThemeOptions() {
			if to.Option.Key == k {
				out += optionSelectRow(to.Option, strings.Join(to.Themes, ","))
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
func fontPairSelectHTML() string {
	type pair struct{ Label, Sans, Mono string }
	pairs := []pair{
		{"Keep current", "", ""},
		{"System UI", `system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif`, `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`},
		{"Modern (Inter-style)", `"Inter", system-ui, -apple-system, "Segoe UI", sans-serif`, `ui-monospace, SFMono-Regular, Menlo, monospace`},
		{"Classic serif", `Georgia, Cambria, "Times New Roman", Times, serif`, `"Courier New", ui-monospace, monospace`},
		{"Editorial serif", `"Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif`, `ui-monospace, Menlo, monospace`},
		{"Humanist", `"Optima", Candara, "Segoe UI", "Helvetica Neue", sans-serif`, `ui-monospace, Menlo, monospace`},
		{"Geometric", `"Avenir Next", "Century Gothic", Futura, system-ui, sans-serif`, `ui-monospace, Menlo, monospace`},
		{"Monospace", `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`, `ui-monospace, SFMono-Regular, Menlo, monospace`},
	}
	opts := ""
	for _, p := range pairs {
		opts += `<option value="` + html.EscapeString(p.Label) + `" data-sans="` + html.EscapeString(p.Sans) + `" data-mono="` + html.EscapeString(p.Mono) + `">` + html.EscapeString(p.Label) + `</option>`
	}
	return `<label class="theme-field theme-field--text mb-4">
  <span class="theme-field__label">Font pairing</span>
  <select class="input" data-font-pair aria-label="Font pairing">` + opts + `</select>
  <span class="theme-field__hint">Sets the body &amp; mono fonts in one click. Fine-tune the exact stacks below. All system/web-safe — no external fonts loaded.</span>
</label>`
}

func (a *App) handleOSTheme(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

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
	for _, f := range themeDarkColors() {
		darkRows += colorRow(f)
	}
	lightRows := ""
	for _, f := range themeLightColors() {
		lightRows += colorRow(f)
	}
	brandRows := ""
	for _, f := range brandColorFields() {
		brandRows += colorRow(f)
	}
	typoRows := textRow("FontSans", "Sans-serif stack", "system-ui, sans-serif") +
		textRow("FontMono", "Monospace stack", "ui-monospace, monospace") +
		textRow("FontSizeBase", "Base font size", "1rem") +
		textRow("LineHeight", "Line height", "1.6") +
		textRow("MaxWidth", "Max content width", "72ch") +
		textRow("RadiusSm", "Small radius", "0.25rem") +
		textRow("RadiusLg", "Large radius", "0.75rem")

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

	// The page is a document (page grammar; plan §3: "the live preview is the
	// document, with the Studio as the inspector, tabs replacing the chips"):
	// the preview fills the page and the Studio's four groups are tabs of the
	// inspector beside it, each a run of sections, so every control is two
	// clicks away at most and none is folded inside another.
	tab := func(key, label string, on bool) string {
		sel := "false"
		if on {
			sel = "true"
		}
		return `<button type="button" class="sa-itabs__tab" role="tab" id="tt-` + key + `" aria-controls="tp-` + key + `" aria-selected="` + sel + `" data-theme-tab="` + key + `">` + label + `</button>`
	}
	panel := func(key string, on bool, body string) string {
		hidden := " hidden"
		if on {
			hidden = ""
		}
		return `<div class="sa-itabs__panel" role="tabpanel" id="tp-` + key + `" aria-labelledby="tt-` + key + `" data-theme-panel="` + key + `"` + hidden + `>` + body + `</div>`
	}
	sec := func(title, body string) string { return string(ui.InspectorSection(title, ui.HTML(body))) }

	design := sec("Presets", `<div class="theme-filter" data-theme-filter>
          <input type="search" class="input theme-filter__search" data-theme-search placeholder="Search 30 themes" aria-label="Search themes">
          <div class="theme-filter__chips" role="group" aria-label="Filter themes">
            <button type="button" class="seg-btn is-active" aria-pressed="true" data-ffilter="all">All</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="scheme:dark">Dark</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="scheme:light">Light</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="arch:minimal">Minimal</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="arch:classic">Classic</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="arch:magazine">Magazine</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="arch:editorial">Editorial</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="arch:bold">Bold</button>
            <button type="button" class="seg-btn" aria-pressed="false" data-ffilter="arch:design">Design</button>
          </div>
          <span class="sa-insp__hint" data-theme-filter-count role="status" aria-live="polite"></span>
        </div>
        <div class="theme-gallery" data-theme-presets aria-label="Theme presets">`+themePresetCards()+`</div>`) +
		sec("Logo and share image", `<p class="sa-insp__text">These stay when the theme changes.</p>
        <div class="cz-logo">
          <img id="brand-favicon-img" class="cz-logo__img" src="`+brandMarkURL+`" alt="Current site mark" width="44" height="44">
          <div class="cz-logo__meta"><div class="cz-logo__title">Logo and favicon</div><div class="sa-insp__hint" id="brand-favicon-state">The favicon and the logo in the menu.</div></div>
        </div>
        <div class="cz-upload">
          <input type="file" id="brand-favicon-file" accept="image/png,image/jpeg,image/webp,image/gif,image/x-icon,.png,.jpg,.jpeg,.webp,.gif,.ico" class="input" aria-label="Logo file">
          <button type="button" class="btn btn--sm" id="brand-favicon-upload">Upload</button>
          <button type="button" class="btn btn--ghost btn--sm" id="brand-favicon-remove">Default</button>
          <span id="brand-favicon-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>
        <span class="sa-insp__hint">PNG or ICO, square, up to 256 KB. Live at once.</span>
        <div class="cz-logo">
          <img id="og-img" class="cz-logo__img" src="/theme-assets/og?t=`+faviconBust+`" alt="Current share image" width="64" height="34">
          <div class="cz-logo__meta"><div class="cz-logo__title">Share image</div><div class="sa-insp__hint" id="og-img-state">Shown when the site or a post is shared.</div></div>
        </div>
        <div class="cz-upload">
          <input type="file" id="og-img-file" accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp" class="input" aria-label="Share image file">
          <button type="button" class="btn btn--sm" id="og-img-upload">Upload</button>
          <button type="button" class="btn btn--ghost btn--sm" id="og-img-remove">Remove</button>
          <span id="og-img-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>
        <span class="sa-insp__hint">PNG, JPEG or WebP, up to 1.5 MB.</span>
        <label class="sa-insp__check"><input type="checkbox" class="toggle" role="switch" id="site-membership"`+membershipChecked+`> Sign in and Sign up in the menu</label>
        <span class="sa-insp__hint" id="site-membership-status" role="status" aria-live="polite"></span>`)

	colour := sec("Palette from one colour", `<div class="cz-gen" data-studio-gen>
          <div class="cz-upload">
            <input type="color" class="theme-field__color" value="#e0562f" data-gen-accent aria-label="Seed accent">
            <select class="input" data-gen-mood aria-label="Mood"><option value="calm">Calm</option><option value="vivid">Vivid</option><option value="muted">Muted</option></select>
            <button type="button" class="btn btn--sm" data-gen-apply>Recolour</button>
            <button type="button" class="btn btn--ghost btn--sm" data-gen-surprise>Surprise me</button>
          </div>
          <span class="sa-insp__hint">One accent becomes a whole dark and light palette.</span>
          <span id="gen-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>`) +
		sec("Brand colours", a11ySummary(a11y)+`<div class="theme-fields">`+brandRows+`</div>
        <div class="theme-fields theme-fields--text">`+optionRowsByKeys("scheme", "accentfill", "paper")+`</div>`+themeA11yPanel(a11y)) +
		`<details class="sa-insp sa-insp--more"><summary class="sa-insp__head">Every colour, dark</summary><div class="theme-fields">` + darkRows + `</div></details>` +
		`<details class="sa-insp sa-insp--more"><summary class="sa-insp__head">Every colour, light</summary><div class="theme-fields">` + lightRows + `</div></details>`

	layout := sec("Layout", `<div class="theme-fields theme-fields--text">`+optionRowsByKeys("archetype", "width", "corners", "feedlayout", "cardimage", "headeralign", "navstyle", "cardstyle", "density", "columnrules", "pagefade")+`</div>`) +
		sec("Hero", `<label class="sa-insp__check"><input type="checkbox" class="toggle" role="switch" id="home-hero"`+heroChecked+`> A hero on the home page</label>
        <span class="sa-insp__hint" id="home-hero-status" role="status" aria-live="polite"></span>
        <div class="cz-logo">
          <img id="hero-img" class="cz-logo__img" src="/theme-assets/hero?t=`+faviconBust+`" alt="Current hero image" width="64" height="40">
          <div class="cz-logo__meta"><div class="cz-logo__title">Hero image</div><div class="sa-insp__hint" id="hero-img-state">Shown when the hero background is Image.</div></div>
        </div>
        <div class="cz-upload">
          <input type="file" id="hero-img-file" accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp" class="input" aria-label="Hero image file">
          <button type="button" class="btn btn--sm" id="hero-img-upload">Upload</button>
          <button type="button" class="btn btn--ghost btn--sm" id="hero-img-remove">Remove</button>
          <span id="hero-img-status" class="sa-insp__hint" role="status" aria-live="polite"></span>
        </div>
        <div class="theme-fields theme-fields--text">`+optionRowsByKeys("herostyle", "herobg", "heroheight")+`</div>`) +
		sec("Type", fontPairSelectHTML()+`<div class="theme-fields theme-fields--text">`+optionRowsByKeys("headingcase", "headingscale", "dropcap")+typoRows+`</div>`) +
		sec("Posts", `<div class="theme-fields theme-fields--text">`+optionRowsByKeys("articlealign", "articlemeta", "relatedposts", "trendingposts", "authorbox", "linkstyle", "readingprogress")+`</div>
        <label class="theme-field theme-field--text"><span class="theme-field__label">Author bio</span>
          <input type="text" class="input" id="author-bio" maxlength="280" value="`+html.EscapeString(val(settings.KeyAuthorBio))+`" placeholder="One line about the author">
          <span class="theme-field__hint" id="author-bio-status">In the author box, beside the author's name. Saved as you leave the field.</span></label>`)

	advanced := sec("Menu", `<p class="sa-insp__text">Saved straight to the live site; the preview shows a sample menu.</p>
        <div id="cz-nav-rows" data-nav-editor></div>
        <div class="cz-upload">
          <button type="button" class="btn btn--ghost btn--sm" id="cz-nav-add">Add a link</button>
          <button type="button" class="btn btn--sm" id="cz-nav-save">Save the menu</button>
          <span class="sa-insp__hint" id="cz-nav-status"></span>
        </div>
        <input type="hidden" id="cz-nav-seed" value="`+navSeed+`">`) +
		sec("Custom CSS", `<textarea class="input theme-code" data-theme-css rows="10" maxlength="65536" spellcheck="false" aria-label="Custom CSS" placeholder="/* .vayu-post-title { letter-spacing: -0.02em; } */">`+html.EscapeString(val(settings.KeyThemeCustomCSS))+`</textarea>`+
			string(ui.Explain(ui.HTML(`<p>Served from this site as <code>/theme.css</code>, after the theme's own styles. <code>@import</code> and outside <code>url()</code> are removed, so it loads nothing from elsewhere. Up to 64 KB.</p>`)))) +
		sec("Head tags", `<div class="theme-fields theme-fields--text">
          <label class="theme-field theme-field--text"><span class="theme-field__label">Keywords</span>
            <input type="text" class="input" data-head="keywords" maxlength="256" value="`+html.EscapeString(val(settings.KeyHeadKeywords))+`" placeholder="publishing, sovereignty"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Theme colour</span>
            <input type="text" class="input" data-head="theme_color" maxlength="7" value="`+html.EscapeString(val(settings.KeyHeadThemeColor))+`" placeholder="#0d9488"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Robots</span>
            <select class="input" data-head="robots">`+robotsOptionsHTML(val(settings.KeyHeadRobots))+`</select></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Google verification</span>
            <input type="text" class="input" data-head="verify_google" maxlength="128" value="`+html.EscapeString(val(settings.KeyHeadVerifyGoogle))+`" placeholder="token"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Bing verification</span>
            <input type="text" class="input" data-head="verify_bing" maxlength="128" value="`+html.EscapeString(val(settings.KeyHeadVerifyBing))+`" placeholder="token"></label>
        </div>
        <div class="cz-upload"><button type="button" class="btn btn--sm" data-theme-code-save>Save CSS and head tags</button><span class="sa-insp__hint" data-theme-code-status></span></div>`+
			string(ui.Explain(ui.HTML(`<p>Written as escaped <code>&lt;meta&gt;</code> tags from this list only; raw head HTML is not accepted.</p>`)))) +
		sec("Move a theme", `<p class="sa-insp__text">The whole theme as JSON, to keep or to bring to another install. An imported theme is checked before it goes live.</p>
        <div class="cz-upload"><a class="btn btn--ghost btn--sm" href="/os/api/theme/export" download>Export</a></div>
        <div class="cz-upload">
          <input type="file" accept="application/json,.json" class="input" data-theme-import-file aria-label="Theme file">
          <button type="button" class="btn btn--sm" data-theme-import>Import</button>
          <span class="sa-insp__hint" data-theme-import-status></span>
        </div>`)

	inspector := `<div class="sa-itabs" role="tablist" aria-label="Theme settings">` + tab("design", "Design", true) + tab("colour", "Colour", false) + tab("layout", "Layout", false) + tab("advanced", "Advanced", false) + `</div>` +
		panel("design", true, design) + panel("colour", false, colour) + panel("layout", false, layout) + panel("advanced", false, advanced)

	preview := `<div class="theme-draft-banner" data-theme-draft hidden data-has-draft="0">
  <span>Resume the theme you were changing?</span>
  <button type="button" class="btn btn--sm" data-draft-resume>Restore</button>
  <button type="button" class="btn btn--ghost btn--sm" data-draft-discard>Discard</button>
</div>
<div class="customizer__toolbar">
  <div class="sa-seg" role="group" aria-label="Preview device">
    <button type="button" class="sa-seg__opt" data-theme-device="desktop" aria-pressed="true">Desktop</button>
    <button type="button" class="sa-seg__opt" data-theme-device="tablet" aria-pressed="false">Tablet</button>
    <button type="button" class="sa-seg__opt" data-theme-device="mobile" aria-pressed="false">Phone</button>
  </div>
  <div class="sa-seg" role="group" aria-label="Preview colour scheme">
    <button type="button" class="sa-seg__opt" data-theme-scheme="dark" aria-pressed="true">Dark</button>
    <button type="button" class="sa-seg__opt" data-theme-scheme="light" aria-pressed="false">Light</button>
  </div>
  <span class="sa-doc__fill"></span>
  <span class="sa-insp__hint" data-theme-preview-status>Live preview</span>
  <a class="btn btn--ghost btn--sm" data-theme-newtab href="#" target="_blank" rel="noopener">Open in a tab ↗</a>
</div>
<div class="customizer__viewport" data-theme-viewport data-device="desktop">
  <div class="customizer__frame-wrap">
    <iframe class="customizer__frame" data-theme-frame title="Live theme preview" referrerpolicy="no-referrer"></iframe>
    <div class="customizer__frame-loading" data-theme-frame-loading>Building the preview</div>
  </div>
</div>`

	body := string(ui.Document(ui.DocumentPage{
		Title: "Theme",
		Hook:  "data-theme-studio",
		State: ui.HTML(`<span data-active-preset-name>Current theme</span> <span class="sa-doc__msg" data-theme-status>Loading</span>`),
		Actions: ui.HTML(`<span class="sa-doc__msg" data-theme-changes hidden aria-live="polite"></span>` +
			`<a class="btn btn--ghost btn--sm" href="/os/theme/store">Theme store</a>` +
			`<button type="button" class="btn btn--ghost btn--sm" data-theme-revert>Revert</button>` +
			`<button type="button" class="btn btn--ghost btn--sm btn--icon" data-studio-focus aria-pressed="true" aria-label="Theme settings beside the preview" title="Hide or show the settings">` + saIcon("settings") + `</button>` +
			`<button type="button" class="btn btn--primary btn--sm" data-theme-apply>Apply theme</button>`),
		Inspector: ui.HTML(inspector),
		Label:     "Theme settings",
	}, ui.HTML(preview))) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-theme.js?v=` + assetVer("js/admin-os-theme.js") + `"></script>`

	// Resumable draft (Wave C): surface the autosaved snapshot, if any, so the
	// client banner can offer Restore/Discard. Payload is escaped; the banner
	// stays hidden unless a draft exists.
	if d := themeDraftFor(vals); d != "" {
		body = strings.Replace(body, `data-has-draft="0"`,
			`data-has-draft="1" data-draft-payload="`+html.EscapeString(d)+`"`, 1)
	}
	writeOSHTML(w, r, adminOSLayout(nonce, "Theme Studio", "theme", cfg, htmpl.HTML(body)))
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
		writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "settings cannot be saved in " + string(cur) + " mode"})
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
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	css := strings.TrimSpace(body.CustomCSS)
	if len(css) > 64*1024 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "Custom CSS exceeds the 64 KB limit"})
		return
	}
	keywords := strings.TrimSpace(body.Keywords)
	if len(keywords) > 256 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "Keywords exceed the 256-character limit"})
		return
	}
	themeColor := strings.TrimSpace(body.ThemeColor)
	if themeColor != "" && !hexColorRe.MatchString(themeColor) {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "Theme colour must be a hex colour like #0d9488"})
		return
	}
	robots := strings.TrimSpace(body.Robots)
	if robots == "" {
		robots = settings.Defaults[settings.KeyHeadRobots]
	}
	if !settings.RobotsOptions[robots] {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "Robots directive is not an allowed value"})
		return
	}
	verifyGoogle := strings.TrimSpace(body.VerifyGoogle)
	verifyBing := strings.TrimSpace(body.VerifyBing)
	for _, tok := range []string{verifyGoogle, verifyBing} {
		if tok != "" && !verifyTokenRe.MatchString(tok) {
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "Verification token may contain only letters, digits, '-', '_', and '.'"})
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
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
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
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "failed to load theme: " + err.Error()})
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
		writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "theme cannot be imported in " + string(cur) + " mode"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128*1024)
	var env themeExport
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "not a valid theme file: " + err.Error()})
		return
	}
	if env.Schema != 1 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "unsupported or missing theme schema (expected vayupress_theme: 1)"})
		return
	}

	// Validate tokens by compiling them (rejects malformed colours/values).
	css, err := theme.CompileCSS(env.Tokens)
	if err != nil {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invalid theme tokens: " + err.Error()})
		return
	}

	ccss := strings.TrimSpace(env.CustomCSS)
	if len(ccss) > 64*1024 {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "custom CSS in file exceeds the 64 KB limit"})
		return
	}
	keywords := strings.TrimSpace(env.Head.Keywords)
	if len(keywords) > 256 {
		keywords = keywords[:256]
	}
	themeColor := strings.TrimSpace(env.Head.ThemeColor)
	if themeColor != "" && !hexColorRe.MatchString(themeColor) {
		writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "head theme_color is not a valid hex colour"})
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
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": "head verification token contains invalid characters"})
			return
		}
	}

	if err := theme.Save(r.Context(), dbpkg.DB, env.Tokens); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "failed to persist tokens: " + err.Error()})
		return
	}
	render.SetThemeCSS(css)

	kv := map[string]string{
		settings.KeyThemeCustomCSS:   ccss,
		settings.KeyHeadKeywords:     keywords,
		settings.KeyHeadThemeColor:   themeColor,
		settings.KeyHeadRobots:       robots,
		settings.KeyHeadVerifyGoogle: verifyGoogle,
		settings.KeyHeadVerifyBing:   verifyBing,
	}
	if err := a.siteSettings.SetMany(r.Context(), osScope(r), kv); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "failed to persist settings: " + err.Error()})
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
