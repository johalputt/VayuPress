package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/microcosm-cc/bluemonday"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/theme"
)

// sampleArticleHTML is a representative article snippet used in theme preview
// responses. It exercises headings, paragraphs, inline code, and links so the
// caller can render a realistic preview without needing real content.
const sampleArticleHTML = `<article>
<h1>The quick brown fox</h1>
<p>VayuPress is a <strong>sovereign, observable</strong> publishing runtime.
Design tokens let you tune every colour, typeface, and spacing value without
touching a single CSS file.</p>
<h2>Getting started</h2>
<p>Pick a preset from the Theme Studio, tweak any token, and click
<em>Apply</em>. Changes propagate to <code>/theme.css</code> immediately.</p>
<blockquote><p>Simplicity is the ultimate sophistication.</p></blockquote>
<ul>
<li>Eight built-in presets</li>
<li>System fonts only — zero external requests</li>
<li>Dark and light mode via CSS custom properties</li>
</ul>
<p>Read the <a href="/docs">documentation</a> to learn more.</p>
</article>`

// handleThemePresets returns a JSON array of all built-in preset tokens.
//
//	GET /api/v1/admin/theme/presets
func (a *App) handleThemePresets(w http.ResponseWriter, r *http.Request) {
	presets := theme.AllPresets()
	writeJSON(w, r, 200, presets)
}

// handleThemeTokens returns the currently active theme tokens as JSON.
//
//	GET /api/v1/admin/theme/tokens
func (a *App) handleThemeTokens(w http.ResponseWriter, r *http.Request) {
	t, err := theme.Load(r.Context(), dbpkg.DB)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "failed to load theme tokens: "+err.Error(), "")
		return
	}
	writeJSON(w, r, 200, t)
}

// handleThemePreview accepts a partial-or-complete Tokens body and returns the
// compiled CSS plus a sanitised sample HTML snippet — enough for the Studio UI
// to render a live preview without persisting anything.
//
//	POST /api/v1/admin/theme/preview
func (a *App) handleThemePreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)

	// Start from the currently active tokens so callers can send partial overrides.
	base, err := theme.Load(r.Context(), dbpkg.DB)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "failed to load active tokens: "+err.Error(), "")
		return
	}

	// Decode the override map and apply known fields onto base.
	var overrides map[string]string
	if err := json.NewDecoder(r.Body).Decode(&overrides); err != nil {
		writeAPIError(w, r, 400, "invalid_json", "request body must be a JSON object of token overrides: "+err.Error(), "")
		return
	}
	applyOverrides(&base, overrides)

	css, err := theme.CompileCSS(base)
	if err != nil {
		writeAPIError(w, r, 400, "invalid_token", err.Error(), "")
		return
	}

	policy := bluemonday.UGCPolicy()
	safeHTML := policy.Sanitize(sampleArticleHTML)

	writeJSON(w, r, 200, map[string]string{
		"css":  css,
		"html": safeHTML,
	})
}

// handleThemeApply accepts either {"preset":"aurora"} or {"tokens":{…}},
// compiles the CSS, persists the tokens, and purges the render cache.
//
//	POST /api/v1/admin/theme/apply
func (a *App) handleThemeApply(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)

	var body struct {
		Preset string            `json:"preset"`
		Tokens *theme.Tokens     `json:"tokens"`
		Fields map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, 400, "invalid_json", "request body must be JSON: "+err.Error(), "")
		return
	}

	var t theme.Tokens

	switch {
	case body.Preset != "":
		found := false
		for _, p := range theme.AllPresets() {
			if strings.EqualFold(p.Name, body.Preset) {
				t = p
				found = true
				break
			}
		}
		if !found {
			writeAPIError(w, r, 400, "unknown_preset", "unknown preset: "+body.Preset, "")
			return
		}

	case body.Tokens != nil:
		t = *body.Tokens

	case len(body.Fields) > 0:
		var err error
		t, err = theme.Load(r.Context(), dbpkg.DB)
		if err != nil {
			writeAPIError(w, r, 500, "db_error", "failed to load active tokens: "+err.Error(), "")
			return
		}
		applyOverrides(&t, body.Fields)

	default:
		writeAPIError(w, r, 400, "bad_request", "body must include 'preset', 'tokens', or 'fields'", "")
		return
	}

	// Compile first to validate all token values before persisting.
	css, err := theme.CompileCSS(t)
	if err != nil {
		writeAPIError(w, r, 400, "invalid_token", err.Error(), "")
		return
	}

	if err := theme.Save(r.Context(), dbpkg.DB, t); err != nil {
		writeAPIError(w, r, 500, "db_error", "failed to persist tokens: "+err.Error(), "")
		return
	}

	render.SetThemeCSS(css)
	render.CachePurgeAll()

	dbpkg.AuditLog("theme.apply", dbpkg.AuditActor(r), t.Name, "")

	writeJSON(w, r, 200, map[string]string{
		"status": "ok",
		"name":   t.Name,
		"css":    css,
	})
}

// applyOverrides merges the string map onto t, matching by canonical field names
// (case-insensitive). Unknown keys are silently ignored.
func applyOverrides(t *theme.Tokens, overrides map[string]string) {
	for k, v := range overrides {
		switch strings.ToLower(k) {
		case "name":
			t.Name = v
		case "bgdark":
			t.BgDark = v
		case "surfacedark":
			t.SurfaceDark = v
		case "textdark":
			t.TextDark = v
		case "muteddark":
			t.MutedDark = v
		case "accentdark":
			t.AccentDark = v
		case "accent2dark":
			t.Accent2Dark = v
		case "hidark":
			t.HiDark = v
		case "greendark":
			t.GreenDark = v
		case "bglight":
			t.BgLight = v
		case "surfacelight":
			t.SurfaceLight = v
		case "textlight":
			t.TextLight = v
		case "mutedlight":
			t.MutedLight = v
		case "accentlight":
			t.AccentLight = v
		case "accent2light":
			t.Accent2Light = v
		case "hilight":
			t.HiLight = v
		case "fontsans":
			t.FontSans = v
		case "fontmono":
			t.FontMono = v
		case "fontsizebase":
			t.FontSizeBase = v
		case "lineheight":
			t.LineHeight = v
		case "maxwidth":
			t.MaxWidth = v
		case "radiussm":
			t.RadiusSm = v
		case "radiuslg":
			t.RadiusLg = v
		}
	}
}
