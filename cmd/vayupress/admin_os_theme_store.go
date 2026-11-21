// SPDX-License-Identifier: Apache-2.0

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
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/theme"
)

// handleOSThemeStore renders the Theme Store page: a filterable gallery of
// deployable themes. The currently active theme (theme.Load) is highlighted and
// its Deploy button is shown as the active state.
func (a *App) handleOSThemeStore(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	lang := i18n.Lang(r.Context())
	t := func(s string) string { return esc(s) }

	// Best-effort active-theme detection; on error we simply badge nothing.
	active := ""
	if th, err := theme.Load(r.Context(), dbpkg.DB); err == nil {
		active = th.Name
	}

	store := theme.Store()

	activeLabel := i18n.T(lang, "Default")
	if active != "" {
		activeLabel = active
	}

	// A gallery: the title with its
	// count, one line of state, then the themes. The four figure tiles it
	// opened on are gone: the counts read as the title's, and "0 external
	// assets" was a zero drawn as a figure for what is a property of every
	// theme, said in the line instead.
	body := `<div data-page-kind="gallery"><div class="page-header">
  <h1>` + t(i18n.T(lang, "Theme store")) + ` <span class="sa-list__count">` + t(i18n.T(lang, "{themes, plural, one {# theme} other {# themes}} in {styles, plural, one {# style} other {# styles}}", "themes", len(store), "styles", len(theme.Categories()))) + `</span></h1>
  <div class="page-actions">
    <span class="text-sm muted" data-store-status></span>
    <a class="btn btn--ghost btn--sm" href="/os/theme">` + t(i18n.T(lang, "Open Theme Studio")) + `</a>
  </div>
</div>
<p class="sa-list__note">` + escFill(i18n.T(lang, "Your site uses {theme}. Any theme deploys in one click and loads nothing from outside your server.", "theme", htmlMark(0)), `<strong>`+t(activeLabel)+`</strong>`) + `</p>

<div class="store" data-theme-store data-active-theme="` + html.EscapeString(active) + `">
  <div class="store-toolbar">
    <div class="store-filters" role="group" aria-label="` + t(i18n.T(lang, "Filter themes by category")) + `">` + themeStoreFilterChips(lang) + `</div>
    <label class="store-search">
      <input type="search" class="input" data-store-search placeholder="` + t(i18n.T(lang, "Search themes…")) + `" aria-label="` + t(i18n.T(lang, "Search themes")) + `" autocomplete="off">
    </label>
  </div>
  <div class="store-meta text-sm muted" data-store-count>` + t(i18n.T(lang, "{n, plural, one {# theme} other {# themes}}", "n", len(store))) + `</div>
  <div class="store-grid" data-store-grid>` + themeStoreCards(lang, active) + `</div>
  <div class="store-empty" data-store-empty hidden>` + t(i18n.T(lang, "No themes match your filters.")) + `</div>
</div>

<div class="store-preview" data-store-overlay hidden>
  <div class="store-preview__bar">
    <span class="store-preview__title" data-store-preview-title>` + t(i18n.T(lang, "Preview")) + `</span>
    <span class="store-preview__spacer"></span>
    <button type="button" class="btn btn--primary btn--sm" data-store-preview-deploy>` + t(i18n.T(lang, "Deploy this theme")) + `</button>
    <a class="btn btn--ghost btn--sm" data-store-preview-customize href="/os/theme">` + t(i18n.T(lang, "Customize")) + `</a>
    <button type="button" class="store-preview__close" data-store-preview-close aria-label="` + t(i18n.T(lang, "Close preview")) + `">&times;</button>
  </div>
  <iframe class="store-preview__frame" data-store-preview-frame title="` + t(i18n.T(lang, "Live theme preview")) + `" loading="lazy" referrerpolicy="no-referrer"></iframe>
</div>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-theme-store.js?v=` + assetVer("js/admin-os-theme-store.js") + `"></script>`

	writeOSHTML(w, r, adminOSLayout(nonce, i18n.T(lang, "Theme store"), "theme-store", cfg, htmpl.HTML(body)))
}

// themeStoreFilterChips renders the category filter buttons. "All" is the
// default-active chip; the rest come from the curated theme.Categories() list.
// A chip filters by the category as the theme package names it, and shows it
// in lang.
func themeStoreFilterChips(lang string) string {
	out := `<button type="button" class="store-chip store-chip--active" data-store-filter="all" aria-pressed="true">` + esc(i18n.T(lang, "All")) + `</button>`
	for _, c := range theme.Categories() {
		out += `<button type="button" class="store-chip" data-store-filter="` + html.EscapeString(c) + `" aria-pressed="false">` + esc(themeWord(lang, c)) + `</button>`
	}
	return out
}

// themeStoreCards renders one showcase card per deployable theme. Preview
// colours ride on data-color attributes (applied via CSSOM in JS, CSP-safe);
// data-category / data-name / data-haystack power the client-side filtering.
func themeStoreCards(lang, active string) string {
	out := ""
	for _, e := range theme.Store() {
		out += themeStoreCard(lang, e, e.Meta.Name == active)
	}
	return out
}

// themeStoreCard renders a single theme card, its words in lang. The theme's
// name and the category the filter matches on are the theme package's own.
func themeStoreCard(lang string, e theme.StoreEntry, isActive bool) string {
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

	// Tags, as the card shows them; the search reads the same words.
	tags := ""
	shown := make([]string, 0, len(m.Tags))
	for _, tg := range m.Tags {
		word := themeWord(lang, tg)
		shown = append(shown, word)
		tags += `<span class="store-tag">` + esc(word) + `</span>`
	}
	tagline, desc, category := themeWord(lang, m.Tagline), themeWord(lang, m.Description), themeWord(lang, m.Category)

	// Search haystack (lowercased on the client; we pass the raw, escaped text).
	haystack := html.EscapeString(m.Name + " " + tagline + " " + desc + " " + category + " " + joinTags(shown))

	cardCls := "store-card"
	if isActive {
		cardCls += " store-card--active"
	}

	// Deploy button: shown as a disabled "Active" state for the live theme.
	deploy := `<button type="button" class="btn btn--primary btn--sm store-card__deploy" data-store-deploy="` + name + `">` + esc(i18n.T(lang, "Deploy")) + `</button>`
	if isActive {
		deploy = `<button type="button" class="btn btn--sm store-card__deploy" data-store-deploy="` + name + `" data-store-active="true" disabled>` + esc(i18n.T(lang, "Active")) + `</button>`
	}

	badge := ""
	if isActive {
		badge = `<span class="store-card__badge" data-store-badge>` + esc(i18n.T(lang, "Active")) + `</span>`
	} else {
		badge = `<span class="store-card__badge" data-store-badge hidden>` + esc(i18n.T(lang, "Active")) + `</span>`
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
      <span class="store-card__cat">` + esc(category) + `</span>
    </div>
    <p class="store-card__tagline">` + esc(tagline) + `</p>
    <p class="store-card__desc">` + esc(desc) + `</p>
    <div class="store-card__tags">` + tags + `</div>
    <div class="store-card__actions">
      ` + deploy + `
      <button type="button" class="btn btn--ghost btn--sm" data-store-preview="` + name + `">` + esc(i18n.T(lang, "Preview")) + `</button>
      <a class="btn btn--ghost btn--sm" href="/os/theme?load=` + name + `">` + esc(i18n.T(lang, "Customize")) + `</a>
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
	// Draft mode: serve a transient, pre-compiled stylesheet by id (the Studio's
	// live preview). Falls through to preset mode otherwise (the Store preview).
	if id := r.URL.Query().Get("draft"); id != "" {
		css, ok := previewDraftGet(id)
		if !ok {
			http.Error(w, "preview expired", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(w, css)
		return
	}

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
	// This page is designed to be embedded in an admin preview iframe, so relax
	// the framing controls to SAME-ORIGIN ONLY. The strict global baseline is
	// frame-ancestors 'none' + X-Frame-Options: DENY (set by
	// securityHeadersMiddleware before this handler runs); we override both so
	// only our own admin page — and no third party — can frame the preview.
	nonce := render.CSPNonce(r)
	w.Header().Set("Content-Security-Policy",
		strings.Replace(render.BuildCSP(nonce, nil), "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	// Determine the stylesheet to apply: a live Studio draft (by id) or a named
	// preset (the Store preview), optionally carrying customization options.
	var cssHref, title string
	var tok theme.Tokens
	if draft := r.URL.Query().Get("draft"); draft != "" {
		d, ok := previewDraft(draft)
		if !ok {
			http.Error(w, "preview expired", http.StatusNotFound)
			return
		}
		tok = d.tok
		cssHref = "/os/theme/preview.css?draft=" + url.QueryEscape(draft)
		title = "Live preview"
	} else {
		var ok bool
		tok, ok = findPreset(r.URL.Query().Get("preset"))
		if !ok {
			http.Error(w, "unknown preset", http.StatusNotFound)
			return
		}
		q := url.Values{}
		q.Set("preset", tok.Name)
		for _, k := range theme.OptionKeys() {
			if v := r.URL.Query().Get(k); v != "" {
				q.Set(k, v)
			}
		}
		cssHref = "/os/theme/preview.css?" + q.Encode()
		title = tok.Name
		if opts := previewOptionsFromQuery(r); len(opts) > 0 {
			tok.Options = opts
		}
	}
	if tok.Layout == theme.LayoutHalcyon {
		a.writeHalcyonPreview(w, r, tok, cssHref)
		return
	}

	// The preview iframe hot-swaps its stylesheet on a same-origin postMessage.
	// That logic lives in an EXTERNAL same-origin script (script-src 'self'), so
	// it is always allowed by the strict CSP — unlike an inline nonce'd script,
	// which can silently fail and make live updates appear broken.
	swap := `<script src="/os/static/js/theme-preview-frame.js"></script>`

	// Colour scheme for the preview. The public site keys its palette off
	// data-theme on <html>, so honouring a ?scheme= param lets the Studio preview
	// the light palette without the operator changing their OS setting — the only
	// way to see the light-mode colours they are editing. Dark stays the default.
	previewTheme := "dark"
	if strings.EqualFold(r.URL.Query().Get("scheme"), "light") {
		previewTheme = "light"
	}
	page := `<!doctype html><html lang="en" data-theme="` + previewTheme + `"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		`<title>Preview — ` + html.EscapeString(title) + `</title>` +
		string(render.PicoCSSLink()) + string(render.CustomCSSLink()) + string(render.ArticleCSSLink()) +
		`<link id="vayu-theme-css" rel="stylesheet" href="` + html.EscapeString(cssHref) + `">` +
		`</head><body><div class="container">` + themePreviewSampleHTML() + `</div>` + swap + `</body></html>`
	writeOSHTML(w, r, page)
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
