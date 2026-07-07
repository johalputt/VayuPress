# ADR-0116: Vayu Flagship Theme & Self-Hosted (Sovereign) Web Fonts

**Status**: Accepted  
**Date**: 2026-07-07  
**Author**: @johalputt  
**Relates to**: the theme catalogue (presets.go / catalog.go), [ADR-0107](ADR-0107-self-hosted-htmx.md) (self-hosted HTMX)

## Context

The blog themes were competent but generic, and none matched the vayupress.com
marketing site — so a VayuPress operator's blog and website looked unrelated.
The marketing site's distinctive identity (deep cosmic "ink" canvas, teal +
saffron accents, gradient display headings in Space Grotesk) is defined with CDN
Tailwind + Google Fonts + Alpine.js — the opposite of VayuPress's fast, sovereign,
zero-external-dependency ethos and impossible to reuse verbatim on a blog that
must score 100/100 PageSpeed under a strict `default-src 'self'` CSP.

## Decision

Ship a flagship blog theme, **Vayu**, that reproduces the site's *look* with
hand-written, same-origin CSS, and self-host its one display font.

- **Theme:** `internal/theme/vayu.css` styles the real public `.vayu-*` markup —
  cosmic background glows, gradient headings, glassy post cards with a teal
  glow-lift, gradient-bordered code blocks, refined byline/footer/404 — in light
  and dark. Registered as a preset (`Vayu()`) and a Flagship Theme-Store entry,
  so it deploys in one click. Pure CSS, no JavaScript.
- **Fonts (sovereign):** the marketing site's Space Grotesk display face is
  **self-hosted**, not loaded from Google Fonts. A latin-subset woff2 per weight
  (500/600/700, ~13 KB each, OFL-1.1) is embedded in the binary and served
  same-origin at `/static/fonts/{file}` from an allowlist, with `font-display:
  swap` so text is always instantly visible and the font is never render-
  blocking. This keeps the theme fast (100/100), privacy-preserving (no request
  to a third party), and offline-capable — mirroring the self-hosted-HTMX
  decision (ADR-0107).
- **No external requests, ever:** the theme's CSS and fonts are all `'self'`;
  the compiled `/theme.css` contains no `@import`/CDN/`https:` references (guarded
  by a test), so it satisfies `style-src 'self'` / `font-src 'self'` with no CSP
  relaxation.

## Consequences

- Positive: an operator can make their blog match vayupress.com in one click, at
  full speed, with no third-party font/CDN dependency and no privacy leak. The
  self-hosted font pattern is reusable by future themes.
- Licensing: Space Grotesk is OFL-1.1 (permissive, Apache-compatible); the
  license ships at `static/fonts/OFL.txt` and is attributed in `NOTICE`.
- Trade-off: `font-display: swap` means a brief fallback flash before the ~13 KB
  font paints on a cold cache (no render-blocking, text always visible). If a
  deployment needs zero heading CLS on the very first paint, a `<link
  rel="preload">` for the active theme's fonts can be added later; the current
  design intentionally avoids per-theme `<head>` injection to stay simple.
