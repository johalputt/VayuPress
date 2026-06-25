# ADR-0082: Theme Studio Code Editor, Head Meta, and Import/Export

**Status**: Accepted
**Date**: 2026-06-25
**Author**: @johalputt

## Context

Operators wanted Tumblr/Ghost-style theme control — a custom CSS editor and HTML
head injection — on top of the token-driven Theme Studio (ADR-0071). VayuPress
enforces a strict CSP (`style-src 'self'`, `script-src 'self' 'nonce-…'`) and a
zero-telemetry/no-third-party constitution, which rules out inline `<style>` and
arbitrary `<script>`/`<head>` injection.

## Decision

1. **Custom CSS** is edited in Theme Studio (16 KB) and served **same-origin via
   `/theme.css`** — CSP-safe, no inline styles, no external origins, no script
   execution. It applies to every public page on save.
2. **Head/SEO** is exposed as declarative fields (keywords, theme-colour, robots,
   Google/Bing verification) rendered to a validated, escaped `<meta>` allowlist.
   **Raw `<head>` HTML is rejected** — it could smuggle redirects/beacons past
   the CSP. A dedicated `POST /os/api/theme/code` writes only these keys (it can
   never wipe identity/palette settings).
3. **Import/Export**: `GET /os/api/theme/export` streams the full theme (tokens +
   custom CSS + head meta) as a JSON envelope (`vayupress_theme: 1`);
   `POST /os/api/theme/import` applies one after **validating the tokens by
   compiling them**, capping custom CSS at 16 KB, and re-checking head meta — so
   a malformed file cannot break the site or bypass the CSP.

## Consequences

- Positive: full theme control (CSS + safe head + portability) within the CSP and
  privacy constitution; no new dependencies.
- Trade-off: no raw HTML/template editing and no third-party `<script>` injection
  by design — custom fonts/layout/colour are achieved through the Custom CSS box.
