# ADR-0065 — Modern Admin UI on a CSP-Compliant, Vendored Stack

**Status:** Accepted
**Date:** 2026-06-19
**Deciders:** VayuPress Maintainers

---

## Context

The admin panel is server-rendered inline HTML built inside Go handlers under a
strict Content-Security-Policy (ADR-0036):

```
script-src 'self' 'nonce-<nonce>'; style-src 'self'; default-src 'self'; img-src 'self' data:
```

Operators asked for a modern editor-first redesign using Tailwind + Alpine.js +
Pico. Two constraints make a naïve "add the CDN script tags" approach incompatible
with the constitution:

1. **No external CDNs.** Sovereignty requires assets be self-hosted; `default-src
   'self'` forbids third-party origins outright.
2. **No `unsafe-eval` / `unsafe-inline`.** The standard Alpine.js build evaluates
   attribute expressions with `new Function`, which requires `unsafe-eval`.
   Tailwind's CDN build injects an inline `<style>`, which requires
   `unsafe-inline` for styles. Both would gut the CSP that protects the admin.

## Decision

Build the modern admin (`/admin/v2`) on a **fully vendored, CSP-clean** stack,
served alongside the existing `/admin` so the redesign is **non-breaking**.

- **Styling:** Tailwind is **precompiled** to a static `admin-v2.css` served
  same-origin under `style-src 'self'`. No inline `<style>`, no `style="…"`
  attributes anywhere in the markup.
- **Interactivity:** Alpine.js is used via its **CSP build** (`@alpinejs/csp`),
  vendored locally — components are registered as JS objects (`Alpine.data`),
  not eval'd from attribute strings. Where Alpine is unavailable, the bundled
  `admin-v2.js` implements the needed behavior with plain DOM APIs. Neither path
  uses `eval` or `new Function`.
- **Nonce discipline:** every inline `<script>` carries the per-request
  `render.CSPNonce(r)`. External JS is served same-origin.
- **Fonts:** Space Grotesk / Inter are self-hosted woff2 under `font-src 'self'`
  (operator-droppable), falling back to a system font stack.
- **Auth & CSRF:** `/admin/v2` pages reuse `auth.RequireAPIKey`; the editor's
  autosave reuses the existing `/api/v1/articles` endpoints with the established
  CSRF cookie/header handshake. No new privileged write surface is introduced.

The editor — the highest-value page — provides split-view live preview,
distraction-free mode, a slash-command palette, formatting toolbar, word
count / reading time, an SEO preview, debounced autosave, and version-history
access (reusing `/api/v1/admin/articles/{slug}/versions`).

`/admin/v2` is additive: the legacy `/admin` handlers are untouched, so the new
UI can mature behind a stable, working panel and be promoted to default once
proven.

## Consequences

- The redesign delivers a best-in-class editor without weakening the CSP — no
  `unsafe-eval`, no `unsafe-inline`, no third-party origins.
- Assets are 100% self-hosted, preserving sovereignty and offline operability.
- Cost: we hand-maintain a precompiled CSS artifact and use Alpine's more
  verbose CSP component style instead of inline expressions. This is the price
  of keeping a strict CSP, and it is paid once in the build, not per request.
- Non-breaking rollout: `/admin` and `/admin/v2` coexist; promoting v2 to the
  default `/admin` is a follow-up once feature parity is confirmed.
- Trade-off vs. the original prompt: the one-click web "upgrade" button is
  deliberately **not** part of this UI — see ADR-0064. The settings page only
  surfaces the read-only update *check*.
