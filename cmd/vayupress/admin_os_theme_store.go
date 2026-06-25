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
	"net/http"

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
