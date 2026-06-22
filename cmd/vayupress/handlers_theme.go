package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// hexColorRe matches #rgb and #rrggbb CSS hex colours (case-insensitive).
var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// verifyTokenRe matches search-engine verification tokens: letters, digits and
// the punctuation those providers use. Anything else is rejected so the value
// can only ever render inside a meta content="" attribute.
var verifyTokenRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// Page background colours the primary palette renders against. These mirror
// --pico-background-color in static/css/custom.css and are used for WCAG
// contrast checks on save.
const (
	lightModeBG  = "#f8fafc"
	darkModeBG   = "#0a0f1a"
	wcagAANormal = 4.5 // WCAG 2.x AA contrast ratio for normal-size text/links
)

// srgbToLinear linearises one 0–255 sRGB channel (WCAG relative-luminance step).
func srgbToLinear(c float64) float64 {
	c /= 255.0
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// relLuminance returns the WCAG relative luminance of a #rgb / #rrggbb colour.
func relLuminance(hexColor string) float64 {
	h := strings.TrimPrefix(hexColor, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return 0
	}
	ch := func(s string) float64 {
		n, _ := strconv.ParseInt(s, 16, 0)
		return srgbToLinear(float64(n))
	}
	return 0.2126*ch(h[0:2]) + 0.7152*ch(h[2:4]) + 0.0722*ch(h[4:6])
}

// contrastRatio returns the WCAG contrast ratio (1.0–21.0) between two colours.
func contrastRatio(a, b string) float64 {
	la, lb := relLuminance(a), relLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// contrastWarnings returns advisory (non-blocking) WCAG AA warnings for the
// primary palette colours against their page backgrounds. Primary is used as
// the link/interactive-text colour, so failing AA hurts readability — but theme
// sovereignty means we warn, not forbid.
func contrastWarnings(primaryLight, primaryDark string) []string {
	var out []string
	if primaryLight != "" {
		if cr := contrastRatio(primaryLight, lightModeBG); cr < wcagAANormal {
			out = append(out, fmt.Sprintf("Light primary %s has low contrast (%.1f:1) on the light background — WCAG AA wants ≥ %.1f:1.", primaryLight, cr, wcagAANormal))
		}
	}
	if primaryDark != "" {
		if cr := contrastRatio(primaryDark, darkModeBG); cr < wcagAANormal {
			out = append(out, fmt.Sprintf("Dark primary %s has low contrast (%.1f:1) on the dark background — WCAG AA wants ≥ %.1f:1.", primaryDark, cr, wcagAANormal))
		}
	}
	return out
}

// robotsChoices lists the <meta name="robots"> options in display order. The
// label is shown in the <select>; the value is what gets persisted and must be
// a member of settings.RobotsOptions.
var robotsChoices = []struct{ value, label string }{
	{"", "Default (no directive — fully indexable)"},
	{"index,follow", "index, follow"},
	{"index,nofollow", "index, nofollow"},
	{"noindex,follow", "noindex, follow"},
	{"noindex,nofollow", "noindex, nofollow"},
}

// robotsOptionsHTML renders the <option> elements for the robots <select>,
// marking current as selected.
func robotsOptionsHTML(current string) string {
	var sb strings.Builder
	for _, c := range robotsChoices {
		sel := ""
		if c.value == current {
			sel = " selected"
		}
		sb.WriteString(`<option value="` + template.HTMLEscapeString(c.value) + `"` + sel + `>` + template.HTMLEscapeString(c.label) + `</option>`)
	}
	return sb.String()
}

// handleThemeCSS serves the dynamic per-site theme stylesheet at /theme.css.
// Served from the same origin (text/css) so it satisfies the strict
// `style-src 'self'` CSP. An ETag over the CSS content lets browsers revalidate
// cheaply; no-cache forces revalidation so palette changes propagate even to
// disk-cached HTML pages on the next request.
func (a *App) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	etag := render.ThemeCSSETag()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("ETag", etag)
	// Palette changes are infrequent; a short max-age lets browsers serve from
	// cache without a round-trip, while the ETag still yields cheap 304s and
	// caps propagation lag (≤60 s) after a save. CachePurgeAll() already
	// regenerates the HTML pages on save.
	w.Header().Set("Cache-Control", "public, max-age=60")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	fmt.Fprint(w, render.ThemeCSS())
}

// handleThemeToggleJS serves the public sun/moon theme switcher script.
// Same-origin static asset → satisfies `script-src 'self'` without a nonce.
func (a *App) handleThemeToggleJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, render.ThemeToggleJS)
}

// handleVideoFacadeJS serves the public click-to-load video facade script.
// Same-origin static asset → satisfies `script-src 'self'` without a nonce.
func (a *App) handleVideoFacadeJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, render.VideoFacadeJS)
}

// handleThemeGet renders the admin theme-editor page.
func (a *App) handleThemeGet(w http.ResponseWriter, r *http.Request) {
	vals, err := a.siteSettings.GetAll(r.Context())
	if err != nil {
		http.Error(w, "failed to load settings", 500)
		return
	}
	// Seed the CSRF cookie the page's Save/Reset/favicon POSTs read back via the
	// X-CSRF-Token header. This GET route isn't wrapped in CSRFTokenMiddleware, so
	// without this a visitor landing directly on /admin/theme would have no token
	// and every governed write would 403 until they bounced through another page.
	if c, err := r.Cookie("vp_csrf"); err != nil || c.Value == "" {
		if token := auth.GenerateCSRFToken(); token != "" {
			http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
		}
	}
	modeStr := string(mode.Global.Current())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, themeEditorPage(vals, modeStr, render.CSPNonce(r), ""))
}

// themeExportVersion is the schema version of an exported theme bundle. Bump it
// only on a breaking change to the export shape so importers can refuse bundles
// they don't understand (fail-closed) rather than silently mis-applying them.
const themeExportVersion = 1

// handleThemeExport streams the persisted, allowlisted site/theme settings as a
// downloadable JSON bundle. It is a pure read over the settings allowlist — no
// secrets, no raw HTML, only the same keys the editor already round-trips — so a
// bundle is safe to share and re-import on another instance.
func (a *App) handleThemeExport(w http.ResponseWriter, r *http.Request) {
	vals, err := a.siteSettings.GetAll(r.Context())
	if err != nil {
		http.Error(w, "failed to load settings", 500)
		return
	}
	// Emit only the canonical allowlist, so an export never carries anything the
	// import path wouldn't accept back. Branding (the base64 favicon blob) is
	// deliberately excluded — it is binary, large, and not round-tripped by the
	// importer, so it would only bloat an otherwise shareable text bundle.
	out := make(map[string]string, len(settings.AllKeys))
	for key := range settings.AllKeys {
		if key == settings.KeyBrandFavicon || key == settings.KeyBrandFaviconType {
			continue
		}
		out[key] = vals[key]
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vayupress-theme.json"`)
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"vayupress_theme": themeExportVersion,
		"settings":        out,
	})
}

// handleThemeReset restores every setting to its compile-time default and
// propagates the change through the render pipeline identically to a Save.
// It is a CSRF-protected POST — idempotent on a clean install, but a
// deliberate, irreversible write on a customised one. The operator must
// explicitly confirm in the browser before the request is sent.
func (a *App) handleThemeReset(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]string{"error": "settings cannot be reset in " + string(cur) + " mode"}) //nolint:errcheck
		return
	}

	if err := a.siteSettings.SetMany(r.Context(), settings.Defaults); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": "reset failed: " + err.Error()}) //nolint:errcheck
		return
	}

	if newVals, err := a.siteSettings.GetAll(r.Context()); err == nil {
		render.SetActiveSettings(render.SiteSettings{
			Name:           newVals[settings.KeySiteName],
			Tagline:        newVals[settings.KeySiteTagline],
			Description:    newVals[settings.KeySiteDescription],
			Author:         newVals[settings.KeySiteAuthor],
			ShowMembership: newVals[settings.KeyMembershipButtons] == "true",
			PrimaryLight:   newVals[settings.KeyThemePrimaryLight],
			PrimaryDark:    newVals[settings.KeyThemePrimaryDark],
			AccentLight:    newVals[settings.KeyThemeAccentLight],
			AccentDark:     newVals[settings.KeyThemeAccentDark],
			CustomCSS:      newVals[settings.KeyThemeCustomCSS],
			Keywords:       newVals[settings.KeyHeadKeywords],
			ThemeColor:     newVals[settings.KeyHeadThemeColor],
			Robots:         newVals[settings.KeyHeadRobots],
			VerifyGoogle:   newVals[settings.KeyHeadVerifyGoogle],
			VerifyBing:     newVals[settings.KeyHeadVerifyBing],
		})
	}

	render.CachePurgeAll()
	go generateSitemap()
	go generateRSS()
	go generateRobots()

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "theme", Severity: "info",
		Msg: "site settings reset to defaults", RequestID: getRequestID(r),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
}

// handleThemeSave processes the JSON POST from the theme editor.
// The browser sends application/json via fetch with the X-CSRF-Token header.
func (a *App) handleThemeSave(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]string{"error": "settings cannot be saved in " + string(cur) + " mode"}) //nolint:errcheck
		return
	}

	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
	}

	var body struct {
		SiteName        string `json:"site.name"`
		SiteTagline     string `json:"site.tagline"`
		SiteDescription string `json:"site.description"`
		SiteAuthor      string `json:"site.author"`
		PrimaryLight    string `json:"theme.primary_light"`
		PrimaryDark     string `json:"theme.primary_dark"`
		AccentLight     string `json:"theme.accent_light"`
		AccentDark      string `json:"theme.accent_dark"`
		CustomCSS       string `json:"theme.custom_css"`
		Keywords        string `json:"head.keywords"`
		ThemeColor      string `json:"head.theme_color"`
		Robots          string `json:"head.robots"`
		VerifyGoogle    string `json:"head.verify_google"`
		VerifyBing      string `json:"head.verify_bing"`
	}
	// Cap the request body before decoding. The largest legitimate field is the
	// 16 KB custom CSS (checked again post-decode); 64 KB leaves generous room for
	// the other small fields while refusing an oversized body up front rather than
	// streaming it into the decoder.
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(400, "invalid JSON: "+err.Error())
		return
	}

	customCSS := strings.TrimSpace(body.CustomCSS)
	if len(customCSS) > 16*1024 {
		fail(400, "Custom CSS exceeds the 16 KB limit")
		return
	}

	// Validate colour fields are #rgb / #rrggbb so they can't break the served
	// stylesheet or smuggle extra CSS declarations into the variable block.
	for label, val := range map[string]string{
		"Primary (light)": body.PrimaryLight,
		"Primary (dark)":  body.PrimaryDark,
		"Accent (light)":  body.AccentLight,
		"Accent (dark)":   body.AccentDark,
		"Theme colour":    body.ThemeColor,
	} {
		if v := strings.TrimSpace(val); v != "" && !hexColorRe.MatchString(v) {
			fail(400, label+" must be a hex colour like #0d9488")
			return
		}
	}

	// Declarative <head> capabilities: each is allowlisted/tokenised so only a
	// safe, escaped <meta> subset can ever reach the document head.
	keywords := strings.TrimSpace(body.Keywords)
	if len(keywords) > 256 {
		fail(400, "Keywords exceed the 256-character limit")
		return
	}
	robots := strings.TrimSpace(body.Robots)
	if !settings.RobotsOptions[robots] {
		fail(400, "Robots directive is not an allowed value")
		return
	}
	verifyGoogle := strings.TrimSpace(body.VerifyGoogle)
	verifyBing := strings.TrimSpace(body.VerifyBing)
	for label, tok := range map[string]string{
		"Google verification": verifyGoogle,
		"Bing verification":   verifyBing,
	} {
		if tok != "" && !verifyTokenRe.MatchString(tok) {
			fail(400, label+" token may contain only letters, digits, '-', '_', and '.'")
			return
		}
	}

	kv := map[string]string{
		settings.KeySiteName:          strings.TrimSpace(body.SiteName),
		settings.KeySiteTagline:       strings.TrimSpace(body.SiteTagline),
		settings.KeySiteDescription:   strings.TrimSpace(body.SiteDescription),
		settings.KeySiteAuthor:        strings.TrimSpace(body.SiteAuthor),
		settings.KeyThemePrimaryLight: strings.TrimSpace(body.PrimaryLight),
		settings.KeyThemePrimaryDark:  strings.TrimSpace(body.PrimaryDark),
		settings.KeyThemeAccentLight:  strings.TrimSpace(body.AccentLight),
		settings.KeyThemeAccentDark:   strings.TrimSpace(body.AccentDark),
		settings.KeyThemeCustomCSS:    customCSS,
		settings.KeyHeadKeywords:      keywords,
		settings.KeyHeadThemeColor:    strings.TrimSpace(body.ThemeColor),
		settings.KeyHeadRobots:        robots,
		settings.KeyHeadVerifyGoogle:  verifyGoogle,
		settings.KeyHeadVerifyBing:    verifyBing,
	}

	if err := a.siteSettings.SetMany(r.Context(), kv); err != nil {
		fail(500, "save failed: "+err.Error())
		return
	}

	// Push updated values into the render pipeline immediately.
	if newVals, err := a.siteSettings.GetAll(r.Context()); err == nil {
		render.SetActiveSettings(render.SiteSettings{
			Name:           newVals[settings.KeySiteName],
			Tagline:        newVals[settings.KeySiteTagline],
			Description:    newVals[settings.KeySiteDescription],
			Author:         newVals[settings.KeySiteAuthor],
			ShowMembership: newVals[settings.KeyMembershipButtons] == "true",
			PrimaryLight:   newVals[settings.KeyThemePrimaryLight],
			PrimaryDark:    newVals[settings.KeyThemePrimaryDark],
			AccentLight:    newVals[settings.KeyThemeAccentLight],
			AccentDark:     newVals[settings.KeyThemeAccentDark],
			CustomCSS:      newVals[settings.KeyThemeCustomCSS],
			Keywords:       newVals[settings.KeyHeadKeywords],
			ThemeColor:     newVals[settings.KeyHeadThemeColor],
			Robots:         newVals[settings.KeyHeadRobots],
			VerifyGoogle:   newVals[settings.KeyHeadVerifyGoogle],
			VerifyBing:     newVals[settings.KeyHeadVerifyBing],
		})
	}

	// Identity (name/tagline/description) and custom <head> are baked into the
	// cached HTML, so purge all rendered fragments and regenerate the feeds.
	// The palette propagates separately via /theme.css revalidation.
	render.CachePurgeAll()
	go generateSitemap()
	go generateRSS()
	go generateRobots()

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "theme", Severity: "info",
		Msg: "site settings updated", RequestID: getRequestID(r),
	})

	// Advisory WCAG AA contrast warnings — the save succeeds regardless; theme
	// sovereignty means we surface accessibility risks, not veto them.
	warnings := contrastWarnings(
		strings.TrimSpace(body.PrimaryLight),
		strings.TrimSpace(body.PrimaryDark),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]interface{}{ //nolint:errcheck
		"status":   "ok",
		"warnings": warnings,
	})
}

// faviconStateLabel describes whether a custom favicon is in effect, for the
// branding tab's status line.
func faviconStateLabel(stored string) string {
	if stored != "" {
		return "Custom favicon active — stored in the database."
	}
	return "Using the default VayuPress mark."
}

// themeEditorPage returns the full HTML for the theme editor admin page.
func themeEditorPage(vals map[string]string, modeStr, nonce, errMsg string) string {
	safeErr := template.HTMLEscapeString(errMsg)

	v := func(key string) string {
		if val, ok := vals[key]; ok {
			return template.HTMLEscapeString(val)
		}
		if def, ok := settings.Defaults[key]; ok {
			return template.HTMLEscapeString(def)
		}
		return ""
	}
	raw := func(key string) string {
		if val, ok := vals[key]; ok {
			return val
		}
		if def, ok := settings.Defaults[key]; ok {
			return def
		}
		return ""
	}
	// vOr returns the escaped value, falling back to fallback when unset (used to
	// give the colour swatch a sensible starting hue when no value is stored).
	vOr := func(key, fallback string) string {
		if got := raw(key); got != "" {
			return template.HTMLEscapeString(got)
		}
		return template.HTMLEscapeString(fallback)
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Theme · VayuPress Console</title>
<link rel="stylesheet" href="/static/css/admin.css">
</head><body>
<div class="app-shell">
<header class="topbar">
  <a href="/admin" class="topbar-logo">
    <span class="omega-mark">Ω</span>
    <span class="topbar-wordmark">VayuPress<span class="topbar-sep">/</span><span class="topbar-domain">Theme</span></span>
  </a>
  <div class="topbar-center"><span class="live-chip"><span class="live-dot"></span>LIVE</span></div>
  <div class="topbar-right">
    <span class="mode-badge mode-`)
	sb.WriteString(template.HTMLEscapeString(modeStr))
	sb.WriteString(`"><span class="pulse-dot"></span>`)
	sb.WriteString(template.HTMLEscapeString(modeStr))
	sb.WriteString(`</span>
  </div>
</header>
<nav class="sidebar" aria-label="Admin navigation">
  <div class="sidebar-section">
    <span class="sidebar-section-label">Core</span>
    <a href="/admin" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">◈</span>Overview</div></a>
    <a href="/api/v1/articles" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">◻</span>Articles</div></a>
    <a href="/admin/replay" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">⟲</span>Replay</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Observe</span>
    <a href="/admin/topology" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">❖</span>Topology</div></a>
    <a href="/health/dependencies" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">♥</span>Health</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Govern</span>
    <a href="/admin/modes" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">⬡</span>System Modes</div></a>
    <a href="/admin/policy" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">✦</span>Policy</div></a>
    <a href="/admin/adr" class="sidebar-item"><div class="sidebar-item-left"><span class="sidebar-icon">≡</span>ADRs</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">Site</span>
    <a href="/admin/theme" class="sidebar-item active"><div class="sidebar-item-left"><span class="sidebar-icon">◑</span>Theme</div></a>
  </div>
  <div class="sidebar-section">
    <span class="sidebar-section-label">System</span>
    <a href="/metrics" class="sidebar-item" target="_blank" rel="noopener"><div class="sidebar-item-left"><span class="sidebar-icon">∼</span>Metrics</div></a>
  </div>
  <div class="sidebar-footer">
    <span class="sidebar-version">v` + template.HTMLEscapeString(render.Version) + `</span>
    <span class="sidebar-constitution">Ω1–Ω9 compliant</span>
  </div>
</nav>
<main id="main-content">
<div class="page-header">
  <div>
    <div class="page-title">Theme &amp; Site Settings</div>
    <div class="page-sub">Customise identity, palette, and injected code · changes live immediately · governed write</div>
  </div>
  <a class="btn" href="/" target="_blank" rel="noopener">View Public Site ↗</a>
</div>
`)

	if safeErr != "" {
		sb.WriteString(`<div class="err-banner">` + safeErr + `</div>`)
	}
	sb.WriteString(`<div id="ok-banner" class="ok-banner">✓ Settings saved — public pages updated.</div>`)
	sb.WriteString(`<div id="warn-banner" class="warn-box vayu-hidden"></div>`)

	sb.WriteString(`
<div class="theme-tabs">
  <button type="button" class="theme-tab active" data-tab="identity">Identity</button>
  <button type="button" class="theme-tab" data-tab="studio">Studio</button>
  <button type="button" class="theme-tab" data-tab="branding">Branding</button>
  <button type="button" class="theme-tab" data-tab="palette">Palette</button>
  <button type="button" class="theme-tab" data-tab="code">Custom CSS</button>
  <button type="button" class="theme-tab" data-tab="head">Head &amp; SEO</button>
</div>

<!-- Identity -->
<div id="tab-identity" class="theme-panel active">
  <div class="field-row">
    <span class="field-label">Site Name</span>
    <div>
      <input type="text" id="site.name" class="field-input" value="` + v(settings.KeySiteName) + `" maxlength="80" placeholder="VayuPress">
      <div class="field-hint">Shown in the nav brand and page titles.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Tagline</span>
    <div>
      <input type="text" id="site.tagline" class="field-input" value="` + v(settings.KeySiteTagline) + `" maxlength="160" placeholder="Publishing as an adaptive runtime.">
      <div class="field-hint">Hero headline on the homepage.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Description</span>
    <div>
      <input type="text" id="site.description" class="field-input" value="` + v(settings.KeySiteDescription) + `" maxlength="300" placeholder="Durable by design, observable end to end.">
      <div class="field-hint">Used as the meta description on all public pages.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Author</span>
    <div>
      <input type="text" id="site.author" class="field-input" value="` + v(settings.KeySiteAuthor) + `" maxlength="120" placeholder="Ankush Choudhary Johal">
      <div class="field-hint">Author name in article footers and JSON-LD schema.</div>
    </div>
  </div>
</div>

<!-- Studio: curated design-token presets with live preview -->
<div id="tab-studio" class="theme-panel">
  <p class="field-hint theme-note">Pick a curated preset — the preview updates live. Click <strong>Apply Theme</strong> to publish it across every public page. Presets use system fonts only, so no external requests are ever made.</p>
  <div class="studio-layout">
    <div class="studio-presets" id="studio-presets" aria-label="Theme presets"></div>
    <div class="studio-preview-wrap">
      <div class="studio-preview" id="studio-preview">
        <h2 id="studio-preview-title">Live Preview</h2>
        <p>The quick brown fox jumps over the lazy dog. Typography, colour, and spacing update instantly as you browse presets. <a href="#" id="studio-preview-link">Read the full story →</a></p>
        <blockquote>Design is not just what it looks like and feels like. Design is how it works.</blockquote>
        <pre><code>func Greet(name string) string {
    return "Hello, " + name
}</code></pre>
        <div class="studio-tags">
          <span class="studio-tag">design</span>
          <span class="studio-tag">sovereign</span>
          <button type="button" class="studio-btn">Subscribe</button>
        </div>
      </div>
      <div class="theme-actions">
        <button type="button" id="studio-apply" class="theme-save">◑ Apply Theme</button>
        <span id="studio-status" class="save-status"></span>
      </div>
    </div>
  </div>
</div>

<!-- Branding (favicon / logo upload) -->
<div id="tab-branding" class="theme-panel">
  <div class="warn-box">Upload a custom favicon — a <strong>PNG</strong> or <strong>ICO</strong>, square, ≤ 256 KB. It is validated by magic number, stored in the database, and replaces the default mark in every browser tab and nav brand. Changes apply immediately.</div>
  <div class="field-row">
    <span class="field-label">Current favicon</span>
    <div>
      <div class="favicon-preview">
        <img id="favicon-img" src="/static/favicon-light.png" alt="Current favicon" width="48" height="48">
        <span class="field-hint" id="favicon-state">` + faviconStateLabel(raw(settings.KeyBrandFavicon)) + `</span>
      </div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Upload new</span>
    <div>
      <input type="file" id="favicon-file" accept="image/png,image/x-icon,.png,.ico" class="field-input">
      <div class="field-hint">PNG or ICO only, ≤ 256 KB. Recommended square sizes: <strong>32×32</strong> (browser tab), <strong>180×180</strong> (Apple touch icon), or <strong>512×512</strong> (PWA/install). A single square source scales down cleanly.</div>
      <div class="theme-actions favicon-actions">
        <button type="button" id="favicon-upload-btn" class="btn">⭱ Upload favicon</button>
        <button type="button" id="favicon-remove-btn" class="btn btn-danger">↺ Remove (use default)</button>
        <span id="favicon-status" class="save-status"></span>
      </div>
    </div>
  </div>
</div>

<!-- Palette -->
<div id="tab-palette" class="theme-panel">
  <p class="field-hint theme-note">These override Pico CSS variables on every public page render. Use valid hex colours (e.g. #0d9488).</p>
  <div class="section-title">Light Mode</div>
  <div class="field-row">
    <span class="field-label">Primary</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-pl" data-sync="theme.primary_light" value="` + v(settings.KeyThemePrimaryLight) + `">
        <input type="text" id="theme.primary_light" class="field-input field-hex" data-sync="swatch-pl"
               value="` + v(settings.KeyThemePrimaryLight) + `" placeholder="#0d9488" maxlength="7">
      </div>
      <div class="field-hint">Link colour, button fill, tag highlights.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Accent</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-al" data-sync="theme.accent_light" value="` + v(settings.KeyThemeAccentLight) + `">
        <input type="text" id="theme.accent_light" class="field-input field-hex" data-sync="swatch-al"
               value="` + v(settings.KeyThemeAccentLight) + `" placeholder="#f59e0b" maxlength="7">
      </div>
      <div class="field-hint">Blockquote border, mode-dot pulse, stat highlights.</div>
    </div>
  </div>
  <div class="section-title">Dark Mode</div>
  <div class="field-row">
    <span class="field-label">Primary</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-pd" data-sync="theme.primary_dark" value="` + v(settings.KeyThemePrimaryDark) + `">
        <input type="text" id="theme.primary_dark" class="field-input field-hex" data-sync="swatch-pd"
               value="` + v(settings.KeyThemePrimaryDark) + `" placeholder="#2dd4bf" maxlength="7">
      </div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Accent</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-ad" data-sync="theme.accent_dark" value="` + v(settings.KeyThemeAccentDark) + `">
        <input type="text" id="theme.accent_dark" class="field-input field-hex" data-sync="swatch-ad"
               value="` + v(settings.KeyThemeAccentDark) + `" placeholder="#fbbf24" maxlength="7">
      </div>
    </div>
  </div>
</div>

<!-- Custom Code -->
<div id="tab-code" class="theme-panel">
  <div class="warn-box">Custom CSS is served same-origin via <code>/theme.css</code> (CSP-safe) and cannot reach external origins or execute scripts. Max 16 KB.</div>
  <div class="field-row">
    <span class="field-label">Custom CSS</span>
    <div>
      <textarea id="theme.custom_css" class="field-input" rows="10" placeholder="/* e.g. body { font-family: Georgia, serif; } */" maxlength="16384">` + template.HTMLEscapeString(raw(settings.KeyThemeCustomCSS)) + `</textarea>
      <div class="field-hint">Appended after pico.min.css + custom.css in the served stylesheet.</div>
    </div>
  </div>
</div>

<!-- Head & SEO (declarative — no raw HTML) -->
<div id="tab-head" class="theme-panel">
  <div class="warn-box">Raw &lt;head&gt; HTML is intentionally not accepted — it would allow meta-refresh redirects, external beacons, and &lt;base&gt; hijacks the CSP cannot fully block. These fields render to a validated, escaped &lt;meta&gt; allowlist only.</div>
  <div class="field-row">
    <span class="field-label">Keywords</span>
    <div>
      <input type="text" id="head.keywords" class="field-input" value="` + v(settings.KeyHeadKeywords) + `" maxlength="256" placeholder="publishing, governance, sovereignty">
      <div class="field-hint">Comma-separated. Rendered as &lt;meta name="keywords"&gt;.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Theme colour</span>
    <div>
      <div class="color-pair">
        <input type="color" class="color-swatch" id="swatch-tc" data-sync="head.theme_color" value="` + vOr(settings.KeyHeadThemeColor, "#0d9488") + `">
        <input type="text" id="head.theme_color" class="field-input field-hex" data-sync="swatch-tc" value="` + v(settings.KeyHeadThemeColor) + `" placeholder="#0d9488" maxlength="7">
      </div>
      <div class="field-hint">Browser UI tint. Rendered as &lt;meta name="theme-color"&gt;.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Robots</span>
    <div>
      <select id="head.robots" class="field-input">` + robotsOptionsHTML(raw(settings.KeyHeadRobots)) + `</select>
      <div class="field-hint">Crawler directive. Leave default unless intentionally restricting indexing.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Google verify</span>
    <div>
      <input type="text" id="head.verify_google" class="field-input" value="` + v(settings.KeyHeadVerifyGoogle) + `" maxlength="128" placeholder="google-site-verification token">
      <div class="field-hint">Token only (letters/digits/-._). Rendered as the verification &lt;meta&gt;.</div>
    </div>
  </div>
  <div class="field-row">
    <span class="field-label">Bing verify</span>
    <div>
      <input type="text" id="head.verify_bing" class="field-input" value="` + v(settings.KeyHeadVerifyBing) + `" maxlength="128" placeholder="msvalidate.01 token">
      <div class="field-hint">Token only. Rendered as &lt;meta name="msvalidate.01"&gt;.</div>
    </div>
  </div>
</div>

<div class="theme-actions">
  <button id="save-btn" class="theme-save">◑ Save Settings</button>
  <a class="btn" href="/admin/theme/export" download>⭳ Export JSON</a>
  <button type="button" id="import-btn" class="btn">⭱ Import JSON</button>
  <button type="button" id="reset-btn" class="btn btn-danger">↺ Reset to Defaults</button>
  <input type="file" id="import-file" accept="application/json,.json" class="vayu-hidden">
  <span id="save-status" class="save-status"></span>
</div>

<script nonce="` + template.HTMLEscapeString(nonce) + `">
(function(){
  function getVal(id){ var el=document.getElementById(id); return el?el.value:''; }
  function csrf(){
    var m=document.cookie.split('; ').find(function(r){return r.startsWith('vp_csrf=');});
    return m?m.split('=')[1]:'';
  }
  // Tab switching (no inline handlers — CSP-clean).
  document.querySelectorAll('.theme-tab').forEach(function(btn){
    btn.addEventListener('click', function(){
      var name=btn.getAttribute('data-tab');
      document.querySelectorAll('.theme-panel').forEach(function(p){p.classList.remove('active');});
      document.querySelectorAll('.theme-tab').forEach(function(b){b.classList.remove('active');});
      var panel=document.getElementById('tab-'+name);
      if(panel) panel.classList.add('active');
      btn.classList.add('active');
    });
  });
  // Two-way colour <-> hex sync via data-sync attributes.
  document.querySelectorAll('[data-sync]').forEach(function(el){
    el.addEventListener('input', function(){
      var target=document.getElementById(el.getAttribute('data-sync'));
      if(target) target.value=el.value;
    });
  });
  // Import: read a previously-exported JSON bundle and POPULATE the form only.
  // Nothing is persisted here — the operator reviews the loaded values and then
  // clicks Save, so every imported value still passes the server's validation.
  var IMPORT_KEYS=['site.name','site.tagline','site.description','site.author',
    'theme.primary_light','theme.primary_dark','theme.accent_light','theme.accent_dark',
    'theme.custom_css','head.keywords','head.theme_color','head.robots',
    'head.verify_google','head.verify_bing'];
  var importBtn=document.getElementById('import-btn');
  var importFile=document.getElementById('import-file');
  importBtn.addEventListener('click', function(){ importFile.click(); });
  importFile.addEventListener('change', function(){
    var st=document.getElementById('save-status');
    var f=importFile.files&&importFile.files[0];
    if(!f) return;
    var reader=new FileReader();
    reader.onload=function(){
      var data;
      try { data=JSON.parse(reader.result); } catch(e){
        st.style.color='var(--error)'; st.textContent='✗ Not valid JSON'; importFile.value=''; return;
      }
      var s=data&&data.settings;
      if(!data||data.vayupress_theme!==1||typeof s!=='object'||!s){
        st.style.color='var(--error)'; st.textContent='✗ Not a VayuPress theme bundle'; importFile.value=''; return;
      }
      var n=0;
      IMPORT_KEYS.forEach(function(k){
        if(typeof s[k]!=='string') return;          // skip missing/non-string keys
        var el=document.getElementById(k);
        if(!el) return;
        el.value=s[k];
        el.dispatchEvent(new Event('input'));        // refresh linked colour swatches
        n++;
      });
      st.style.color='var(--gold)';
      st.textContent='⭱ Loaded '+n+' fields — review, then Save to apply';
      importFile.value='';                           // allow re-importing the same file
    };
    reader.readAsText(f);
  });
  // Save.
  var btn=document.getElementById('save-btn');
  var status=document.getElementById('save-status');
  btn.addEventListener('click', function(){
    btn.disabled=true;
    status.style.color='var(--muted)';
    status.textContent='Saving…';
    var payload={
      'site.name':getVal('site.name'),
      'site.tagline':getVal('site.tagline'),
      'site.description':getVal('site.description'),
      'site.author':getVal('site.author'),
      'theme.primary_light':getVal('theme.primary_light'),
      'theme.primary_dark':getVal('theme.primary_dark'),
      'theme.accent_light':getVal('theme.accent_light'),
      'theme.accent_dark':getVal('theme.accent_dark'),
      'theme.custom_css':getVal('theme.custom_css'),
      'head.keywords':getVal('head.keywords'),
      'head.theme_color':getVal('head.theme_color'),
      'head.robots':getVal('head.robots'),
      'head.verify_google':getVal('head.verify_google'),
      'head.verify_bing':getVal('head.verify_bing')
    };
    fetch('/admin/theme',{
      method:'POST',
      headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
      body:JSON.stringify(payload)
    }).then(function(r){return r.json();}).then(function(data){
      btn.disabled=false;
      if(data.error){
        status.style.color='var(--error)';
        status.textContent='✗ '+data.error;
      } else {
        status.style.color='var(--green)';
        status.textContent='✓ Saved — public pages updated';
        var ok=document.getElementById('ok-banner');
        ok.style.display='block';
        setTimeout(function(){ok.style.display='none';},4000);
        var wb=document.getElementById('warn-banner');
        if(data.warnings&&data.warnings.length){
          wb.textContent='⚠ '+data.warnings.join(' ');
          wb.style.display='block';
        } else { wb.style.display='none'; }
      }
    }).catch(function(e){
      btn.disabled=false;
      status.style.color='var(--error)';
      status.textContent='✗ Network error: '+e.message;
    });
  });
  // Reset to defaults — requires explicit confirmation; reloads the page on success
  // so the form reflects the restored values without any stale state.
  var resetBtn=document.getElementById('reset-btn');
  resetBtn.addEventListener('click', function(){
    if(!confirm('Reset ALL settings to factory defaults? This cannot be undone.')) return;
    resetBtn.disabled=true;
    status.style.color='var(--muted)';
    status.textContent='Resetting…';
    fetch('/admin/theme/reset',{
      method:'POST',
      headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
      body:'{}'
    }).then(function(r){return r.json();}).then(function(data){
      if(data.error){
        resetBtn.disabled=false;
        status.style.color='var(--error)';
        status.textContent='✗ '+data.error;
      } else {
        status.style.color='var(--green)';
        status.textContent='↺ Defaults restored — reloading…';
        setTimeout(function(){ window.location.reload(); }, 900);
      }
    }).catch(function(e){
      resetBtn.disabled=false;
      status.style.color='var(--error)';
      status.textContent='✗ Network error: '+e.message;
    });
  });
  // Branding: favicon upload (multipart) and removal. The CSRF token rides the
  // X-CSRF-Token header so it doesn't collide with the multipart body.
  var favFile=document.getElementById('favicon-file');
  var favUpload=document.getElementById('favicon-upload-btn');
  var favRemove=document.getElementById('favicon-remove-btn');
  var favStatus=document.getElementById('favicon-status');
  var favImg=document.getElementById('favicon-img');
  var favState=document.getElementById('favicon-state');
  function bust(el){ if(el) el.src='/static/favicon-light.png?t='+Date.now(); }
  if(favUpload){
    favUpload.addEventListener('click', function(){
      var f=favFile&&favFile.files&&favFile.files[0];
      if(!f){ favStatus.style.color='var(--error)'; favStatus.textContent='✗ Choose a PNG or ICO first'; return; }
      var fd=new FormData(); fd.append('favicon', f);
      favUpload.disabled=true;
      favStatus.style.color='var(--muted)'; favStatus.textContent='Uploading…';
      fetch('/admin/theme/favicon',{method:'POST',headers:{'X-CSRF-Token':csrf()},body:fd})
        .then(function(r){return r.json();}).then(function(data){
          favUpload.disabled=false;
          if(data.error){ favStatus.style.color='var(--error)'; favStatus.textContent='✗ '+data.error; return; }
          favStatus.style.color='var(--green)'; favStatus.textContent='✓ '+(data.message||'Uploaded');
          if(favState) favState.textContent='Custom favicon active — stored in the database.';
          bust(favImg); favFile.value='';
        }).catch(function(e){ favUpload.disabled=false; favStatus.style.color='var(--error)'; favStatus.textContent='✗ Network error: '+e.message; });
    });
  }
  if(favRemove){
    favRemove.addEventListener('click', function(){
      if(!confirm('Remove the custom favicon and restore the default mark?')) return;
      var fd=new FormData(); fd.append('remove','1');
      favRemove.disabled=true;
      favStatus.style.color='var(--muted)'; favStatus.textContent='Removing…';
      fetch('/admin/theme/favicon',{method:'POST',headers:{'X-CSRF-Token':csrf()},body:fd})
        .then(function(r){return r.json();}).then(function(data){
          favRemove.disabled=false;
          if(data.error){ favStatus.style.color='var(--error)'; favStatus.textContent='✗ '+data.error; return; }
          favStatus.style.color='var(--green)'; favStatus.textContent='↺ '+(data.message||'Removed');
          if(favState) favState.textContent='Using the default VayuPress mark.';
          bust(favImg);
        }).catch(function(e){ favRemove.disabled=false; favStatus.style.color='var(--error)'; favStatus.textContent='✗ Network error: '+e.message; });
    });
  }
  // ── Theme Studio: preset gallery + live preview ─────────────────────────
  // CSP-clean: the preview's colours are applied as CSS custom properties via
  // the CSSOM (el.style.setProperty), never as inline style attributes or
  // injected <style> blocks, so style-src 'self' stays intact.
  var studioPresets=document.getElementById('studio-presets');
  var studioPreview=document.getElementById('studio-preview');
  var studioApply=document.getElementById('studio-apply');
  var studioStatus=document.getElementById('studio-status');
  var studioLoaded=false;
  var studioSelected=null;   // currently highlighted preset name
  var studioCurrent=null;    // currently previewed token object
  // Map a token object's dark-mode values onto the preview container's vars.
  function studioApplyTokens(t){
    if(!studioPreview||!t) return;
    var s=studioPreview.style;
    s.setProperty('--vp-bg', t.BgDark||'#0a0f1a');
    s.setProperty('--vp-surface', t.SurfaceDark||'#111827');
    s.setProperty('--vp-text', t.TextDark||'#e5e7eb');
    s.setProperty('--vp-muted', t.MutedDark||'#6b7280');
    s.setProperty('--vp-accent', t.AccentDark||'#2dd4bf');
    s.setProperty('--vp-accent2', t.Accent2Dark||'#f59e0b');
    s.setProperty('--vp-hi', t.HiDark||'#fbbf24');
    if(t.FontSans) s.setProperty('--vp-font-sans', t.FontSans);
    if(t.RadiusLg) s.setProperty('--vp-radius-lg', t.RadiusLg);
    var title=document.getElementById('studio-preview-title');
    if(title) title.textContent=(t.Name||'Theme')+' Preview';
  }
  function studioSelectCard(name){
    studioSelected=name;
    Array.prototype.forEach.call(studioPresets.querySelectorAll('.studio-card'), function(c){
      c.classList.toggle('selected', c.getAttribute('data-name')===name);
    });
  }
  function studioBuildCard(t){
    var card=document.createElement('button');
    card.type='button';
    card.className='studio-card';
    card.setAttribute('data-name', t.Name);
    var sw=document.createElement('div');
    sw.className='studio-card-swatches';
    [t.BgDark, t.SurfaceDark, t.AccentDark, t.Accent2Dark].forEach(function(c){
      var d=document.createElement('span');
      d.className='studio-swatch';
      // setProperty on background — CSSOM, CSP-clean. Validate it's a hex first.
      if(/^#[0-9a-fA-F]{3,8}$/.test(c)) d.style.setProperty('background-color', c);
      sw.appendChild(d);
    });
    var meta=document.createElement('div');
    meta.className='studio-card-meta';
    var nm=document.createElement('span');
    nm.className='studio-card-name';
    nm.textContent=t.Name;
    var sub=document.createElement('span');
    sub.className='studio-card-sub';
    sub.textContent=(t.AccentDark||'')+' · '+(t.MaxWidth||'');
    meta.appendChild(nm); meta.appendChild(sub);
    card.appendChild(sw); card.appendChild(meta);
    card.addEventListener('click', function(){
      studioCurrent=t;
      studioApplyTokens(t);
      studioSelectCard(t.Name);
      studioStatus.textContent='';
    });
    return card;
  }
  function studioLoad(){
    if(studioLoaded) return;
    studioLoaded=true;
    studioStatus.style.color='var(--muted)';
    studioStatus.textContent='Loading presets…';
    fetch('/api/v1/admin/theme/presets',{headers:{'Accept':'application/json'}})
      .then(function(r){return r.json();})
      .then(function(list){
        studioPresets.textContent='';
        if(!Array.isArray(list)||!list.length){ studioStatus.textContent='No presets available.'; return; }
        list.forEach(function(t){ studioPresets.appendChild(studioBuildCard(t)); });
        // Preselect the first preset so the preview is populated.
        studioCurrent=list[0];
        studioApplyTokens(list[0]);
        studioSelectCard(list[0].Name);
        studioStatus.textContent='';
      }).catch(function(e){
        studioLoaded=false;
        studioStatus.style.color='var(--error)';
        studioStatus.textContent='✗ Failed to load presets: '+e.message;
      });
  }
  // Lazy-load presets the first time the Studio tab is opened.
  document.querySelectorAll('.theme-tab').forEach(function(tb){
    if(tb.getAttribute('data-tab')==='studio'){
      tb.addEventListener('click', studioLoad);
    }
  });
  // Deep-link: /admin/theme?tab=<name> opens that tab on load. Used by the
  // screenshot pipeline to capture the Studio tab, and handy for bookmarks.
  (function(){
    var want=new URLSearchParams(location.search).get('tab');
    if(!want) return;
    var tb=document.querySelector('.theme-tab[data-tab="'+want+'"]');
    if(tb) tb.click();
  })();
  if(studioApply){
    studioApply.addEventListener('click', function(){
      if(!studioCurrent){ studioStatus.style.color='var(--error)'; studioStatus.textContent='✗ Pick a preset first'; return; }
      studioApply.disabled=true;
      studioStatus.style.color='var(--muted)';
      studioStatus.textContent='Applying…';
      fetch('/api/v1/admin/theme/apply',{
        method:'POST',
        headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},
        body:JSON.stringify({preset:studioCurrent.Name})
      }).then(function(r){return r.json();}).then(function(data){
        studioApply.disabled=false;
        if(data.error){
          studioStatus.style.color='var(--error)';
          studioStatus.textContent='✗ '+data.error;
        } else {
          studioStatus.style.color='var(--green)';
          studioStatus.textContent='✓ Applied “'+(data.name||studioCurrent.Name)+'” — public pages updated';
        }
      }).catch(function(e){
        studioApply.disabled=false;
        studioStatus.style.color='var(--error)';
        studioStatus.textContent='✗ Network error: '+e.message;
      });
    });
  }
})();
</script>
</main>
</div>
</body></html>
`)
	return sb.String()
}
