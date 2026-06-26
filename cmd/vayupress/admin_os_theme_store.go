package main

// admin_os_theme_store.go — VayuOS "Theme Store" surface.
//
// A dedicated showcase/gallery, distinct from the Theme Studio token editor:
// every built-in theme is presented as a rich card (visual preview + name +
// tagline + description + tags + category) with a one-click "Deploy" button
// that activates the theme site-wide. The active theme is badged. Category
// chips and a search box (client-side) make a growing catalogue navigable.
//
// "Deploying" reuses the existing, validated apply pipeline: the store JS POSTs
// {"preset": "<Name>"} to /os/api/theme/apply, which compiles + persists the
// full token set (including any per-preset CustomCSS such as Gale/Zephyr) and
// purges the render cache. No new write path is introduced here.
//
// CSP posture matches the rest of VayuOS: no inline styles, the only inline
// <script> carries the per-request nonce, every dynamic string is escaped, and
// preview colours are carried as data-color attributes applied via the CSSOM.

import (
	"html"
	htmpl "html/template"
	"io"
	"net/http"
	"net/url"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/theme"
)

// handleOSThemeStore renders the Theme Store page: a filterable gallery of
// deployable themes. The currently active theme (theme.Load) is highlighted and
// its Deploy button is shown as the active state.
func (a *App) handleOSThemeStore(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// Best-effort active-theme detection; on error we simply badge nothing.
	active := ""
	if t, err := theme.Load(r.Context(), dbpkg.DB); err == nil {
		active = t.Name
	}

	store := theme.Store()

	body := `<div class="page-header">
  <div>
    <h1>Theme Store</h1>
    <p class="text-sm muted">Browse the full catalogue and deploy any theme to your live site in one click.</p>
  </div>
  <div class="page-actions">
    <span class="text-sm muted" data-store-status></span>
    <a class="btn btn--ghost btn--sm" href="/os/theme">Open Theme Studio</a>
  </div>
</div>

<div class="store" data-theme-store data-active-theme="` + html.EscapeString(active) + `">
  <div class="store-toolbar">
    <div class="store-filters" role="group" aria-label="Filter themes by category">` + themeStoreFilterChips() + `</div>
    <label class="store-search">
      <input type="search" class="input" data-store-search placeholder="Search themes…" aria-label="Search themes" autocomplete="off">
    </label>
  </div>
  <div class="store-meta text-sm muted"><span data-store-count>` + intToStr(len(store)) + `</span> themes · deploy instantly · fully sovereign, no external assets</div>
  <div class="store-grid" data-store-grid>` + themeStoreCards(active) + `</div>
  <div class="store-empty" data-store-empty hidden>No themes match your filters.</div>
</div>

<div class="store-preview" data-store-overlay hidden>
  <div class="store-preview__bar">
    <span class="store-preview__title" data-store-preview-title>Preview</span>
    <span class="store-preview__spacer"></span>
    <button type="button" class="btn btn--primary btn--sm" data-store-preview-deploy>Deploy this theme</button>
    <a class="btn btn--ghost btn--sm" data-store-preview-customize href="/os/theme">Customize</a>
    <button type="button" class="store-preview__close" data-store-preview-close aria-label="Close preview">&times;</button>
  </div>
  <iframe class="store-preview__frame" data-store-preview-frame title="Live theme preview" loading="lazy" referrerpolicy="no-referrer"></iframe>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-theme-store.js"></script>`

	writeOSHTML(w, adminOSLayout(nonce, "Theme Store", "theme-store", cfg, htmpl.HTML(body)))
}

// themeStoreFilterChips renders the category filter buttons. "All" is the
// default-active chip; the rest come from the curated theme.Categories() list.
func themeStoreFilterChips() string {
	out := `<button type="button" class="store-chip store-chip--active" data-store-filter="all" aria-pressed="true">All</button>`
	for _, c := range theme.Categories() {
		ec := html.EscapeString(c)
		out += `<button type="button" class="store-chip" data-store-filter="` + ec + `" aria-pressed="false">` + ec + `</button>`
	}
	return out
}

// themeStoreCards renders one showcase card per deployable theme. Preview
// colours ride on data-color attributes (applied via CSSOM in JS, CSP-safe);
// data-category / data-name / data-haystack power the client-side filtering.
func themeStoreCards(active string) string {
	out := ""
	for _, e := range theme.Store() {
		out += themeStoreCard(e, e.Meta.Name == active)
	}
	return out
}

// themeStoreCard renders a single theme card.
func themeStoreCard(e theme.StoreEntry, isActive bool) string {
	t := e.Tokens
	m := e.Meta
	name := html.EscapeString(m.Name)
	cat := html.EscapeString(m.Category)

	// Colour-bearing preview elements — colours applied via CSSOM in JS.
	el := func(cls, color string) string {
		return `<span class="` + cls + `" data-color="` + html.EscapeString(color) + `" aria-hidden="true"></span>`
	}
	pill := func(color string) string {
		return `<span data-color="` + html.EscapeString(color) + `" aria-hidden="true"></span>`
	}

	// Tags.
	tags := ""
	for _, tg := range m.Tags {
		tags += `<span class="store-tag">` + html.EscapeString(tg) + `</span>`
	}

	// Search haystack (lowercased on the client; we pass the raw, escaped text).
	haystack := html.EscapeString(m.Name + " " + m.Tagline + " " + m.Description + " " + m.Category + " " + joinTags(m.Tags))

	cardCls := "store-card"
	if isActive {
		cardCls += " store-card--active"
	}

	// Deploy button: shown as a disabled "Active" state for the live theme.
	deploy := `<button type="button" class="btn btn--primary btn--sm store-card__deploy" data-store-deploy="` + name + `">Deploy</button>`
	if isActive {
		deploy = `<button type="button" class="btn btn--sm store-card__deploy" data-store-deploy="` + name + `" data-store-active="true" disabled>Active</button>`
	}

	badge := ""
	if isActive {
		badge = `<span class="store-card__badge" data-store-badge>Active</span>`
	} else {
		badge = `<span class="store-card__badge" data-store-badge hidden>Active</span>`
	}

	return `<article class="` + cardCls + `" data-store-item data-name="` + name + `" data-category="` + cat + `" data-haystack="` + haystack + `">
  <div class="store-card__preview" data-color="` + html.EscapeString(t.BgDark) + `" aria-hidden="true">
    ` + el("store-card__bar", t.AccentDark) + `
    <span class="store-card__lines">
      ` + el("store-card__line", t.TextDark) + `
      ` + el("store-card__line store-card__line--short", t.MutedDark) + `
    </span>
    <span class="store-card__pills">` + pill(t.AccentDark) + pill(t.Accent2Dark) + pill(t.HiDark) + pill(t.GreenDark) + `</span>
    ` + badge + `
  </div>
  <div class="store-card__info">
    <div class="store-card__head">
      <h3 class="store-card__name">` + name + `</h3>
      <span class="store-card__cat">` + cat + `</span>
    </div>
    <p class="store-card__tagline">` + html.EscapeString(m.Tagline) + `</p>
    <p class="store-card__desc">` + html.EscapeString(m.Description) + `</p>
    <div class="store-card__tags">` + tags + `</div>
    <div class="store-card__actions">
      ` + deploy + `
      <button type="button" class="btn btn--ghost btn--sm" data-store-preview="` + name + `">Preview</button>
      <a class="btn btn--ghost btn--sm" href="/os/theme?load=` + name + `">Customize</a>
    </div>
  </div>
</article>`
}

// joinTags joins tags with a space for the search haystack.
func joinTags(tags []string) string {
	out := ""
	for i, t := range tags {
		if i > 0 {
			out += " "
		}
		out += t
	}
	return out
}

// intToStr renders a small non-negative int without importing strconv at the
// call sites that only need this one conversion.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// findPreset returns the named preset (case-insensitive) and whether it exists.
func findPreset(name string) (theme.Tokens, bool) {
	for _, p := range theme.AllPresets() {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return theme.Tokens{}, false
}

// handleOSThemePreviewCSS serves a single theme's compiled stylesheet for the
// in-store live preview WITHOUT persisting it as the active theme. Same-origin,
// text/css, no-store (it's a transient preview, never cached as the live theme).
func (a *App) handleOSThemePreviewCSS(w http.ResponseWriter, r *http.Request) {
	tok, ok := findPreset(r.URL.Query().Get("preset"))
	if !ok {
		http.Error(w, "unknown preset", http.StatusNotFound)
		return
	}
	// Apply any customization options carried on the query string so the preview
	// faithfully reflects the operator's choices (scheme, width, corners, density,
	// heading size, …) — the exact same Options layer CompileCSS realises live.
	if opts := previewOptionsFromQuery(r); len(opts) > 0 {
		tok.Options = opts
	}
	css, err := theme.CompileCSS(tok)
	if err != nil {
		http.Error(w, "theme compile error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, css)
}

// handleOSThemePreview renders a self-contained sample page using the real
// public markup (the vayu-* classes) styled by the chosen theme, for display
// inside the store's preview iframe. It links the same base stylesheets as the
// live site (Pico, custom, article) and then the theme's preview.css last, so
// the preview is a faithful, isolated render — never touching the live site.
// CSP-safe: same-origin links only, no inline styles or scripts.
func (a *App) handleOSThemePreview(w http.ResponseWriter, r *http.Request) {
	tok, ok := findPreset(r.URL.Query().Get("preset"))
	if !ok {
		http.Error(w, "unknown preset", http.StatusNotFound)
		return
	}
	// This page is designed to be embedded in the Theme Store's preview iframe,
	// so relax the framing controls to SAME-ORIGIN ONLY. The strict global
	// baseline is frame-ancestors 'none' + X-Frame-Options: DENY (set by
	// securityHeadersMiddleware before this handler runs); we override both so
	// only our own admin page — and no third party — can frame the preview.
	nonce := render.CSPNonce(r)
	w.Header().Set("Content-Security-Policy",
		strings.Replace(render.BuildCSP(nonce, nil), "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	en := html.EscapeString(tok.Name)
	// Forward the preset + any customization options into the preview stylesheet
	// link so the embedded sample renders with the operator's chosen options.
	q := url.Values{}
	q.Set("preset", tok.Name)
	for _, k := range theme.OptionKeys() {
		if v := r.URL.Query().Get(k); v != "" {
			q.Set(k, v)
		}
	}
	page := `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>Preview — ` + en + `</title>` +
		string(render.PicoCSSLink()) + string(render.CustomCSSLink()) + string(render.ArticleCSSLink()) +
		`<link rel="stylesheet" href="/os/theme/preview.css?` + html.EscapeString(q.Encode()) + `">` +
		`</head><body><div class="container">` + themePreviewSampleHTML() + `</div></body></html>`
	writeOSHTML(w, page)
}

// previewOptionsFromQuery extracts theme customization option values from the
// request's query string, keyed by the canonical option keys. Returns nil when
// no recognised option is present so callers can fall back to preset defaults.
func previewOptionsFromQuery(r *http.Request) map[string]string {
	var opts map[string]string
	for _, k := range theme.OptionKeys() {
		if v := r.URL.Query().Get(k); v != "" {
			if opts == nil {
				opts = map[string]string{}
			}
			opts[k] = v
		}
	}
	return opts
}

// themePreviewSampleHTML returns representative home-page markup using the exact
// vayu-* class names the public templates emit, so a theme's component CSS
// (which targets those selectors) renders authentically in the preview.
func themePreviewSampleHTML() string {
	card := func(tag, title, excerpt, date string) string {
		return `<a class="vayu-post-card" href="#">` +
			`<div class="vayu-post-meta"><span>` + date + `</span><span class="vayu-post-dot"></span><span>` + tag + `</span></div>` +
			`<div class="vayu-post-title">` + title + `</div>` +
			`<div class="vayu-post-excerpt">` + excerpt + `</div>` +
			`<span class="vayu-post-arrow">→</span></a>`
	}
	return `<nav class="vayu-nav"><a class="vayu-nav-brand" href="#">Your Publication</a>` +
		`<div class="vayu-nav-links"><a href="#">Home</a><a href="#">Archive</a><a href="#">About</a></div></nav>` +
		`<section class="vayu-hero"><div class="vayu-hero-eyebrow">Live preview</div>` +
		`<h1>Words that move quietly, and land hard.</h1>` +
		`<p class="vayu-hero-tagline">A sample of how your home page looks in this theme — typography, spacing, cards and accent colours, exactly as readers would see it.</p>` +
		`<div class="vayu-stats"><div><div class="vayu-stat-val">128</div><div class="vayu-stat-label">Posts</div></div>` +
		`<div><div class="vayu-stat-val">12k</div><div class="vayu-stat-label">Readers</div></div></div></section>` +
		`<div class="vayu-section-label">Latest</div>` +
		`<div class="vayu-post-list">` +
		card("Essays", "The shape of a good idea", "Short, sharp, and built to be read on any device without friction.", "Jun 2026") +
		card("Notes", "On writing less", "Concision is a feature. Here is what we cut, and why it mattered.", "May 2026") +
		card("Field", "A quiet interface", "Design that gets out of the way so the words can do the work.", "Apr 2026") +
		`</div>` +
		`<footer class="vayu-footer"><span class="vayu-footer-brand">Your Publication</span>` +
		`<span class="vayu-footer-badge">VayuPress</span></footer>`
}
