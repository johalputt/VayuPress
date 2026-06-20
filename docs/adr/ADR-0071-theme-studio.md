# ADR-0071 — Theme Studio: A Safe, Token-Driven Theme Editor that Surpasses Tumblr

**Status:** Proposed
**Date:** 2026-06-20
**Deciders:** VayuPress Maintainers
**Target release:** v1.4.0

---

## Context

VayuPress already has a token-driven theme console: a palette stored in settings
(`theme.primary_*`, `theme.accent_*`), operator CSS served at `/theme.css` (never
inlined, so `style-src 'self'` holds), favicon/logo upload, a WCAG AA contrast
advisory, and JSON export/import. The next goal is a full **Theme Studio** that
surpasses Tumblr — the reference point for "powerful theme editor" — in
**technology, design, and simplicity at once**, without breaking any invariant.

Tumblr's editor exposes a single raw-HTML template box (`{block:Posts}`) that can
inject arbitrary `<script>` — an XSS surface, no design consistency, no
accessibility floor, code-only. We must do better while staying sovereign and
CSP-strict (no `unsafe-inline`, no `unsafe-eval`, no third-party scripts).

## Decision

Build **Theme Studio** on three inventions: a **safe declarative template
engine**, a **design-token system**, and a **dual-mode (visual + code) editor
with live sandboxed preview** — all server-rendered, all sanitised, all portable.

### Safe template engine (`internal/themelang`)

A constrained, declarative template DSL — **not** raw HTML-with-JS and **not**
arbitrary Go templates with filesystem/reflection access. A small AST supports
blocks, loops, conditionals, and variables over a **read-only exposed data
model** (posts, tags, site, pagination, current view). Compiled and rendered
server-side; output is sanitised. Script injection is impossible by construction
— strictly more expressive than tokens, strictly safer than Tumblr's raw HTML.

### Design tokens

A typed token system (color, type-scale, spacing, radius, shadow, motion)
compiles to CSS custom properties served at `/theme.css` (never inline). The
editor enforces **WCAG AA contrast** at edit time (extending the existing
advisory into a gate). Themes are portable JSON bundles (extending the current
export/import), seeding a future sovereign theme marketplace.

### Editor experience

- **Dual-mode:** a visual token/section editor for everyone, plus a code view of
  the template DSL for power users. Tumblr is code-only; we are both.
- **Section/layout model:** composable sections (header, post-list, single,
  footer, custom) with per-section options and drag-to-reorder.
- **Live preview:** a `sandbox`-ed iframe renders the real site with the draft
  theme via a preview token; hot-reload over the existing SSE channel; responsive
  breakpoint toggles (mobile/tablet/desktop) and light/dark, with real content.
- **Versioning & safety:** draft vs published, theme version history, one-click
  revert, reset-to-default.
- **Intelligence:** a local-Ollama theme assistant (palette/typography from a
  prompt or an uploaded logo) and an automatic accessibility/performance linter
  (contrast, font-loading, CLS hints) — suggest-only, never auto-applied.
- **Performance:** a theme compiles to one minified CSS plus at most one
  nonce-gated JS file; introducing `eval` or a third-party script is impossible
  by construction → Lighthouse-perfect themes by default.

### Storage

`themes` table (migration 029): versioned bundles, draft/published state,
per-theme token + template + section data.

## How it surpasses Tumblr

| Dimension | Tumblr | VayuPress Theme Studio |
|---|---|---|
| Safety | arbitrary JS (XSS) | safe DSL, no script injection possible |
| Editing | code-only | visual **and** code dual-mode |
| Consistency | freeform | design-token system → CSS custom props |
| Accessibility | none | WCAG AA contrast gating at edit time |
| Preview | basic | sandboxed live iframe, responsive + light/dark, real data |
| Portability | per-account | portable JSON bundles |
| Intelligence | none | local-LLM assist + a11y/perf linter (suggest-only) |
| Performance | variable | one minified CSS; eval/3rd-party-script impossible |
| Sovereignty | hosted SaaS | single binary, zero CDN, strict CSP |

## Threat model

- **Template injection.** `internal/themelang` exposes only a read-only data
  model and a fixed instruction set — no filesystem, network, reflection, or
  arbitrary function calls. Output is sanitised; the no-raw-HTML invariant holds.
  Fuzz-tested against escape/injection payloads.
- **Preview isolation.** The live preview iframe is `sandbox`-ed and served
  same-origin with a preview token; draft themes cannot affect the live site or
  other sessions.
- **CSS injection.** Operator CSS continues to be served at `/theme.css`, never
  inlined; token compilation emits only custom-property declarations.

## Consequences

**Positive:** a theme editor that is safer, more accessible, more consistent, and
simpler than Tumblr's, while remaining sovereign and CSP-strict; portable themes;
AI-assisted design; Lighthouse-perfect output by construction.

**Negative / costs:** the safe template engine is the most security-sensitive
component in the project and requires a dedicated security review + fuzzing; the
visual editor + live preview are substantial UI work (phased separately from the
backend engine).

## Alternatives considered

- **Let operators write raw HTML/JS themes (Tumblr model)** — rejected: XSS, CSP
  violation, no accessibility floor.
- **Tokens only, no templating** — rejected: insufficient layout power for a
  "surpass Tumblr" goal.
- **Embed a third-party templating language** — rejected: dependency surface and
  weaker sandboxing guarantees than a purpose-built read-only DSL.
