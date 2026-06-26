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

// themeOptionRows renders the theme-level customization controls — color scheme,
// reading width, corner style, heading case, accent fill — as <select>s bound to
// data-token-opt. The Studio JS loads/saves their values as Tokens.Options, which
// CompileCSS realises (re-tinting the accent, resizing the measure, etc.) for
// any theme. CSP-safe: plain selects, no inline handlers.
func themeOptionRows() string {
	out := ""
	for _, o := range theme.AllOptions() {
		opts := ""
		for _, c := range o.Choices {
			opts += `<option value="` + html.EscapeString(c.Value) + `">` + html.EscapeString(c.Label) + `</option>`
		}
		hint := ""
		if o.Help != "" {
			hint = `<span class="theme-field__hint">` + html.EscapeString(o.Help) + `</span>`
		}
		out += `<label class="theme-field theme-field--text">
  <span class="theme-field__label">` + html.EscapeString(o.Label) + `</span>
  <select class="input" data-token-opt="` + html.EscapeString(o.Key) + `" aria-label="` + html.EscapeString(o.Label) + `">` + opts + `</select>` + hint + `</label>`
	}
	// Per-theme extras — rendered for every theme but shown/hidden by the Studio
	// JS based on the active theme (data-opt-theme is a comma-separated list).
	for _, to := range theme.PerThemeOptions() {
		o := to.Option
		opts := ""
		for _, c := range o.Choices {
			opts += `<option value="` + html.EscapeString(c.Value) + `">` + html.EscapeString(c.Label) + `</option>`
		}
		hint := ""
		if o.Help != "" {
			hint = `<span class="theme-field__hint">` + html.EscapeString(o.Help) + `</span>`
		}
		out += `<label class="theme-field theme-field--text" data-opt-theme="` + html.EscapeString(strings.Join(to.Themes, ",")) + `" hidden>
  <span class="theme-field__label">` + html.EscapeString(o.Label) + `</span>
  <select class="input" data-token-opt="` + html.EscapeString(o.Key) + `" aria-label="` + html.EscapeString(o.Label) + `">` + opts + `</select>` + hint + `</label>`
	}
	return out
}

func (a *App) handleOSTheme(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// Current persisted custom CSS + head/SEO values (Tumblr-style code editor).
	vals, _ := a.siteSettings.GetAll(r.Context())
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
	typoRows := textRow("FontSans", "Sans-serif stack", "system-ui, sans-serif") +
		textRow("FontMono", "Monospace stack", "ui-monospace, monospace") +
		textRow("FontSizeBase", "Base font size", "1rem") +
		textRow("LineHeight", "Line height", "1.6") +
		textRow("MaxWidth", "Max content width", "72ch") +
		textRow("RadiusSm", "Small radius", "0.25rem") +
		textRow("RadiusLg", "Large radius", "0.75rem")

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

    <details class="cz-group" open>
      <summary class="cz-group__head">Presets <span class="cz-group__hint">start here</span></summary>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">Pick a starting design, then fine-tune anything below. The preview updates as you go.</p>
        <div class="theme-gallery" data-theme-presets aria-label="Theme presets">` + themePresetCards() + `</div>
      </div>
    </details>

    <details class="cz-group" open>
      <summary class="cz-group__head">Quick styles</summary>
      <div class="cz-group__body">
        <p class="text-sm muted mb-3">High-level controls that restyle the whole site — colour scheme, reading width, corners, density and more.</p>
        <div class="theme-fields theme-fields--text">` + themeOptionRows() + `</div>
      </div>
    </details>

    <details class="cz-group" open>
      <summary class="cz-group__head">Colours — dark mode</summary>
      <div class="cz-group__body">
        <div class="theme-fields">` + darkRows + `</div>
      </div>
    </details>

    <details class="cz-group">
      <summary class="cz-group__head">Colours — light mode</summary>
      <div class="cz-group__body">
        <div class="theme-fields">` + lightRows + `</div>
      </div>
    </details>

    <details class="cz-group">
      <summary class="cz-group__head">Typography &amp; layout</summary>
      <div class="cz-group__body">
        <div class="theme-fields theme-fields--text">` + typoRows + `</div>
      </div>
    </details>

    <details class="cz-group">
      <summary class="cz-group__head">Custom CSS</summary>
      <div class="cz-group__body">
        <div class="text-sm muted mb-3">Served same-origin via <code>/theme.css</code> (CSP-safe), appended after the theme styles. Max 64&nbsp;KB. Reflected live in the preview.</div>
        <textarea class="input theme-code" data-theme-css rows="10" maxlength="65536" spellcheck="false" placeholder="/* e.g. .vayu-post-title { letter-spacing: -0.02em; } */">` + html.EscapeString(val(settings.KeyThemeCustomCSS)) + `</textarea>
        <div class="vm-row">
          <button type="button" class="btn btn--primary btn--sm" data-theme-code-save>Save CSS &amp; meta</button>
          <span class="text-sm muted" data-theme-code-status></span>
        </div>
      </div>
    </details>

    <details class="cz-group">
      <summary class="cz-group__head">Head &amp; SEO (meta)</summary>
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
    </details>

    <details class="cz-group">
      <summary class="cz-group__head">Import / Export</summary>
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
    </details>
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
