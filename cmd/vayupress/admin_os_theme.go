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
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/theme"
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

// themePresetCards renders the Tumblr-style theme gallery server-side. Each card
// is a miniature visual preview of the preset — a coloured "page" (background)
// with an accent bar, two body text lines, and a row of accent pills — so the
// operator can recognise a theme at a glance rather than from raw swatches.
//
// CSP-safe: every colour is carried as a data-color attribute and applied to
// the element's background via the CSSOM in JS (admin-os-theme.js #paintSwatches),
// never as an inline style attribute. Every built-in preset — including Gale and
// Zephyr — appears here, in AllPresets() display order.
func themePresetCards() string {
	out := ""
	for _, p := range theme.AllPresets() {
		name := html.EscapeString(p.Name)
		// el renders a colour-bearing element. The colour is applied via CSSOM,
		// so only the (escaped) hex string lands in the data-color attribute.
		el := func(cls, color string) string {
			return `<span class="` + cls + `" data-color="` + html.EscapeString(color) + `" aria-hidden="true"></span>`
		}
		pill := func(color string) string {
			return `<span data-color="` + html.EscapeString(color) + `" aria-hidden="true"></span>`
		}
		out += `<button type="button" class="theme-card" data-preset="` + name + `" aria-label="Apply the ` + name + ` theme">` +
			`<span class="theme-card__preview" data-color="` + html.EscapeString(p.BgDark) + `" aria-hidden="true">` +
			el("theme-card__bar", p.AccentDark) +
			`<span class="theme-card__body">` +
			el("theme-card__line", p.TextDark) +
			el("theme-card__line theme-card__line--short", p.MutedDark) +
			`</span>` +
			`<span class="theme-card__pills">` + pill(p.AccentDark) + pill(p.Accent2Dark) + pill(p.HiDark) + `</span>` +
			`</span>` +
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
  <span class="theme-field__label">Font pairing <span class="cz-group__hint">quick set</span></span>
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
		vals, _ = a.siteSettings.GetAll(r.Context())
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

	faviconBust := time.Now().Format("150405")
	navSeed := html.EscapeString(val(settings.KeyNavItems))
	membershipChecked := ""
	if val(settings.KeyMembershipButtons) == "true" {
		membershipChecked = " checked"
	}
	heroChecked := ""
	if val(settings.KeyHomeHero) == "true" {
		heroChecked = " checked"
	}

	body := `<div class="page-header">
  <div>
    <h1>Theme Studio</h1>
    <p class="text-sm muted" data-active-preset-name>Current theme</p>
  </div>
  <div class="page-actions">
    <span class="text-sm muted" data-theme-status>Loading…</span>
    <a class="btn btn--ghost btn--sm" href="/os/theme/store">Browse Theme Store</a>
    <button type="button" class="btn btn--ghost btn--sm" data-theme-revert>Revert</button>
    <button type="button" class="btn btn--primary btn--sm" data-theme-apply>Apply theme</button>
  </div>
</div>

<div class="customizer" data-theme-studio>
  <aside class="customizer__panel" aria-label="Theme controls">

    <section class="cz-group cz-group--open">
      <button type="button" class="cz-group__head" aria-expanded="true">Presets <span class="cz-group__hint">start here</span></button>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">Pick a starting design, then fine-tune anything below. The preview updates as you go.</p>
        <div class="theme-gallery" data-theme-presets aria-label="Theme presets">` + themePresetCards() + `</div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Site basics <span class="cz-group__hint">global</span></button>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">Global settings that stay fixed when you switch themes — logo, social share image and membership buttons.</p>
        <div class="cz-logo">
          <img id="brand-favicon-img" class="cz-logo__img" src="/favicon.ico?t=` + faviconBust + `" alt="Current site mark" width="44" height="44">
          <div class="cz-logo__meta">
            <div class="cz-logo__title">Logo &amp; favicon</div>
            <div class="text-xs muted" id="brand-favicon-state">Used as the favicon and nav-bar logo.</div>
          </div>
        </div>
        <div class="vm-row mt-2">
          <input type="file" id="brand-favicon-file" accept="image/png,image/x-icon,.png,.ico" class="input">
          <button type="button" class="btn btn--primary btn--sm" id="brand-favicon-upload">Upload</button>
          <button type="button" class="btn btn--sm" id="brand-favicon-remove">Default</button>
          <span id="brand-favicon-status" class="text-xs muted" role="status" aria-live="polite"></span>
        </div>
        <span class="theme-field__hint">PNG or ICO, square, &le; 256 KB. Applies to the live site immediately.</span>
        <div class="cz-logo mt-4">
          <img id="og-img" class="cz-logo__img" src="/theme-assets/og?t=` + faviconBust + `" alt="Current share image" width="64" height="34">
          <div class="cz-logo__meta">
            <div class="cz-logo__title">Social / share image</div>
            <div class="text-xs muted" id="og-img-state">Shown when your site or posts are shared (og:image).</div>
          </div>
        </div>
        <div class="vm-row mt-2">
          <input type="file" id="og-img-file" accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp" class="input">
          <button type="button" class="btn btn--primary btn--sm" id="og-img-upload">Upload</button>
          <button type="button" class="btn btn--sm" id="og-img-remove">Remove</button>
          <span id="og-img-status" class="text-xs muted" role="status" aria-live="polite"></span>
        </div>
        <span class="theme-field__hint">PNG, JPEG or WebP, &le; 1.5 MB. Used for the homepage and as a fallback for posts.</span>
        <div class="vm-row mt-4">
          <label class="cz-check"><input type="checkbox" id="site-membership"` + membershipChecked + `> Show Sign in / Sign up buttons in the nav</label>
          <span class="text-xs muted" id="site-membership-status" role="status" aria-live="polite"></span>
        </div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Brand colours <span class="cz-group__hint">theme</span></button>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">Accent colours and colour scheme for the active theme.</p>
        <div class="theme-fields">` + brandRows + `</div>
        <div class="theme-fields theme-fields--text mt-3">` + optionRowsByKeys("scheme", "accentfill") + `</div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Layout</button>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">Reading width, corners, post-feed layout, header alignment, navigation, post cards and density — applied across the whole blog.</p>
        <div class="theme-fields theme-fields--text">` + optionRowsByKeys("archetype", "width", "corners", "feedlayout", "cardimage", "headeralign", "navstyle", "cardstyle", "density") + `</div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Hero section</button>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">Style the homepage hero — layout, height and an optional background tint, gradient or uploaded image.</p>
        <div class="vm-row mb-3">
          <label class="cz-check"><input type="checkbox" id="home-hero"` + heroChecked + `> Show the homepage hero (off = clean homepage, straight to posts)</label>
          <span class="text-xs muted" id="home-hero-status" role="status" aria-live="polite"></span>
        </div>
        <div class="cz-logo">
          <img id="hero-img" class="cz-logo__img" src="/theme-assets/hero?t=` + faviconBust + `" alt="Current hero image" width="64" height="40">
          <div class="cz-logo__meta">
            <div class="cz-logo__title">Hero background image</div>
            <div class="text-xs muted" id="hero-img-state">Used when “Hero background” is set to Image.</div>
          </div>
        </div>
        <div class="vm-row mt-2">
          <input type="file" id="hero-img-file" accept="image/png,image/jpeg,image/webp,.png,.jpg,.jpeg,.webp" class="input">
          <button type="button" class="btn btn--primary btn--sm" id="hero-img-upload">Upload</button>
          <button type="button" class="btn btn--sm" id="hero-img-remove">Remove</button>
          <span id="hero-img-status" class="text-xs muted" role="status" aria-live="polite"></span>
        </div>
        <span class="theme-field__hint">PNG, JPEG or WebP, &le; 2 MB. Applies live; set “Hero background” to Image to show it.</span>
        <div class="theme-fields theme-fields--text mt-4">` + optionRowsByKeys("herostyle", "herobg", "heroheight") + `</div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Typography &amp; fonts</button>
      <div class="cz-group__body">
        ` + fontPairSelectHTML() + `
        <div class="theme-fields theme-fields--text">` + optionRowsByKeys("headingcase", "headingscale") + typoRows + `</div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Article pages</button>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">How individual posts look — header alignment, the meta line, related posts, the author box and content links.</p>
        <div class="theme-fields theme-fields--text">` + optionRowsByKeys("articlealign", "articlemeta", "relatedposts", "authorbox", "linkstyle") + `</div>
        <label class="theme-field theme-field--text mt-3">
          <span class="theme-field__label">Author bio <span class="cz-group__hint">author box</span></span>
          <input type="text" class="input" id="author-bio" maxlength="280" value="` + html.EscapeString(val(settings.KeyAuthorBio)) + `" placeholder="One line about the author">
          <span class="theme-field__hint" id="author-bio-status">Shown in the author box with your site author name. Saves on change.</span>
        </label>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Colours — dark mode</button>
      <div class="cz-group__body">
        <div class="theme-fields">` + darkRows + `</div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Colours — light mode</button>
      <div class="cz-group__body">
        <div class="theme-fields">` + lightRows + `</div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Navigation <span class="cz-group__hint">live</span></button>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">Edit the public site menu. Saved straight to your live site (the preview shows a representative menu).</p>
        <div id="cz-nav-rows" data-nav-editor></div>
        <button type="button" class="btn btn--sm mt-2" id="cz-nav-add">+ Add link</button>
        <div class="vm-row mt-3">
          <button type="button" class="btn btn--primary btn--sm" id="cz-nav-save">Save navigation</button>
          <span class="text-sm muted" id="cz-nav-status"></span>
        </div>
        <input type="hidden" id="cz-nav-seed" value="` + navSeed + `">
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Custom CSS</button>
      <div class="cz-group__body">
        <div class="text-sm muted mb-3">Served same-origin via <code>/theme.css</code> (CSP-safe), appended after the theme styles. Max 64&nbsp;KB. Reflected live in the preview.</div>
        <textarea class="input theme-code" data-theme-css rows="10" maxlength="65536" spellcheck="false" placeholder="/* e.g. .vayu-post-title { letter-spacing: -0.02em; } */">` + html.EscapeString(val(settings.KeyThemeCustomCSS)) + `</textarea>
        <div class="vm-row">
          <button type="button" class="btn btn--primary btn--sm" data-theme-code-save>Save CSS &amp; meta</button>
          <span class="text-sm muted" data-theme-code-status></span>
        </div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Head &amp; SEO (meta)</button>
      <div class="cz-group__body">
        <div class="text-sm muted mb-3">Rendered to a validated, escaped <code>&lt;meta&gt;</code> allowlist (raw &lt;head&gt; HTML is intentionally not accepted).</div>
        <div class="theme-fields theme-fields--text">
          <label class="theme-field theme-field--text"><span class="theme-field__label">Keywords</span>
            <input type="text" class="input" data-head="keywords" maxlength="256" value="` + html.EscapeString(val(settings.KeyHeadKeywords)) + `" placeholder="publishing, sovereignty"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Theme colour (hex)</span>
            <input type="text" class="input" data-head="theme_color" maxlength="7" value="` + html.EscapeString(val(settings.KeyHeadThemeColor)) + `" placeholder="#0d9488"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Robots</span>
            <select class="input" data-head="robots">` + robotsOptionsHTML(val(settings.KeyHeadRobots)) + `</select></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Google verification</span>
            <input type="text" class="input" data-head="verify_google" maxlength="128" value="` + html.EscapeString(val(settings.KeyHeadVerifyGoogle)) + `" placeholder="token"></label>
          <label class="theme-field theme-field--text"><span class="theme-field__label">Bing verification</span>
            <input type="text" class="input" data-head="verify_bing" maxlength="128" value="` + html.EscapeString(val(settings.KeyHeadVerifyBing)) + `" placeholder="token"></label>
        </div>
      </div>
    </section>

    <section class="cz-group">
      <button type="button" class="cz-group__head" aria-expanded="false">Import / Export</button>
      <div class="cz-group__body">
        <div class="text-sm muted mb-3">Download the full theme as JSON, or import one to apply it everywhere. Imported tokens are validated before they go live.</div>
        <div class="vm-row">
          <a class="btn btn--sm" href="/os/api/theme/export" download>Export theme JSON</a>
        </div>
        <div class="vm-row mt-2">
          <input type="file" accept="application/json,.json" class="input" data-theme-import-file>
          <button type="button" class="btn btn--primary btn--sm" data-theme-import>Import</button>
          <span class="text-sm muted" data-theme-import-status></span>
        </div>
      </div>
    </section>
  </aside>

  <div class="customizer__stage">
    <div class="customizer__toolbar">
      <div class="cz-devices" role="group" aria-label="Preview device">
        <button type="button" class="cz-device cz-device--active" data-theme-device="desktop" aria-pressed="true" title="Desktop">Desktop</button>
        <button type="button" class="cz-device" data-theme-device="tablet" aria-pressed="false" title="Tablet">Tablet</button>
        <button type="button" class="cz-device" data-theme-device="mobile" aria-pressed="false" title="Mobile">Mobile</button>
      </div>
      <span class="cz-toolbar-spacer"></span>
      <span class="text-xs muted" data-theme-preview-status>Live preview</span>
      <a class="btn btn--ghost btn--sm" data-theme-newtab href="#" target="_blank" rel="noopener">Open in new tab ↗</a>
    </div>
    <div class="customizer__viewport" data-theme-viewport data-device="desktop">
      <div class="customizer__frame-wrap">
        <iframe class="customizer__frame" data-theme-frame title="Live theme preview" referrerpolicy="no-referrer"></iframe>
        <div class="customizer__frame-loading" data-theme-frame-loading>Building preview…</div>
      </div>
    </div>
  </div>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-theme.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Theme Studio", "theme", cfg, htmpl.HTML(body)))
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
	if err := a.siteSettings.SetMany(r.Context(), kv); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "save failed: " + err.Error()})
		return
	}

	// Re-read the full set so we refresh the render pipeline without clobbering
	// the identity/palette values this endpoint doesn't touch.
	if nv, err := a.siteSettings.GetAll(r.Context()); err == nil {
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
	vals, _ := a.siteSettings.GetAll(r.Context())
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
	if err := a.siteSettings.SetMany(r.Context(), kv); err != nil {
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "failed to persist settings: " + err.Error()})
		return
	}
	if nv, err := a.siteSettings.GetAll(r.Context()); err == nil {
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
