# ADR-0086: Themes Restyle the Whole Public Site via Real Markup

**Status**: Accepted
**Date**: 2026-06-26
**Author**: @johalputt
**Supersedes**: none (extends ADR-0071 Theme Studio, ADR-0082 Theme Studio code editor)

## Context

VayuPress ships a Theme Studio (ADR-0071) with a preset gallery and a token
compiler that emits CSS custom properties consumed by the public site. Built-in
"design" themes additionally ship a block of component `CustomCSS` that is
compiled into `/theme.css` alongside the token bridge.

Two structural problems undermined the promise that "applying a theme restyles
your blog":

1. **Dead component CSS.** The design themes were originally authored against
   their own class prefixes (`.apex-*`, `.dispatch-*`, …) — markup the public
   templates never emit. The public pages render a fixed `vayu-*` markup
   vocabulary (`.vayu-hero`, `.vayu-post-list`, `.vayu-post-card`,
   `.vayu-prose`, `.vayu-footer-*`, `.vayu-author-box`, …). The bulk of each
   theme's CSS therefore styled elements that do not exist, so applying a theme
   only recoloured the site (tokens) while the layout barely moved — the
   recurring "a theme switch only changes fonts/colours" report.

2. **Studio fragility.** The Theme Studio page dereferenced the settings store
   without the nil-guard every other settings-dependent handler uses. When the
   store was not ready (startup race / init failure) the page panicked (HTTP
   500), so the gallery "did not show anywhere" while the rest of the console
   kept working.

## Decision

1. **Themes target the real `vayu-*` public markup.** Every design theme carries
   a "Live public-site styling" section whose selectors match the markup the
   templates actually render, and that section now covers the **whole** page —
   navigation, homepage hero, post list/cards (including cover-image cards), the
   article body and headings, the author box, related posts, the comments
   section, and the multi-column footer — in that theme's own visual language.
   Applying a theme transforms every section, not just the homepage.

2. **Layout archetypes for colour presets.** Colour-only presets are each
   assigned a reusable layout archetype (Minimal / Classic / Magazine /
   Editorial / Bold) — scoped CSS over the same `vayu-*` markup — carried as the
   `archetype` customization option, so even a palette swap changes structure
   and spacing, not just colour.

3. **The apply→persist→serve pipeline is authoritative.** Applying a preset
   compiles the full token set **including** its `CustomCSS`, persists the whole
   `Tokens` value as JSON (`theme_tokens` row), and recompiles it into the
   in-memory `/theme.css` on both apply and startup. Switching themes swaps the
   served stylesheet with no stale CSS.

4. **The Studio degrades gracefully.** Settings-dependent rendering in the
   Theme Studio handler falls back to defaults when the store is unavailable, so
   the page (and gallery) always render.

## Guards

- `internal/theme/realmarkup_test.go` — every theme that ships `CustomCSS` must
  style the real markup (`.vayu-hero`, `.vayu-post-card`).
- `internal/theme/wholesite_coverage_test.go` — every design theme must style
  the author box, footer columns, and cover-image cards, and no two design
  themes may compile to identical CSS.
- `cmd/vayupress/theme_apply_distinct_integration_test.go` — applying a design
  theme pushes its full layout CSS to the public `/theme.css`, and switching
  themes swaps it.
- `cmd/vayupress/admin_os_theme_resilience_test.go` — the Theme Studio renders
  (HTTP 200, gallery present) even with no settings store.

## Consequences

- Deploying any built-in theme now visibly changes the entire public site.
- New design themes MUST include a whole-site `vayu-*` section; the guard tests
  fail the build otherwise, making the old "recolour-only" regression
  impossible to reintroduce.
- Themes are compiled into the binary, so theme changes require a rebuild and
  redeploy to take effect — unchanged from prior behaviour.
