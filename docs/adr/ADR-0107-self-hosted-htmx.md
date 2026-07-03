# ADR-0107: Self-Hosted HTMX in the VayuPress Binary

**Status**: Accepted  
**Date**: 2026-07-03  
**Author**: @johalputt  
**Extends**: [ADR-0099](ADR-0099-self-contained-one-click-update.md)

## Context

The VayuOS admin panel is a server-rendered surface with a strict
Content-Security-Policy — `script-src 'self' 'nonce-…'` and `style-src 'self'`,
with no `unsafe-inline`, no `unsafe-eval`, and no third-party host (see
`internal/render/csp.go`). Every first-party script is served same-origin so it
satisfies `script-src 'self'`, and vendored assets (DOMPurify, Pico CSS) are
compiled into the binary and mirrored to `STATIC_DIR` on boot so a binary-only
self-update never leaves stale or missing assets behind (ADR-0099).

We want light, declarative progressive enhancement for admin interactions —
live-refreshing a fragment, inline saves — without adopting a front-end build
step or a SPA framework. [htmx](https://htmx.org) fits: it drives AJAX and
partial DOM swaps from `hx-*` HTML attributes, with the server returning HTML
fragments. But htmx is normally loaded from a CDN, which would violate both the
sovereignty goal (no external requests) and the strict `script-src 'self'` CSP.

Two CSP details matter:

1. htmx must be **same-origin**. A CDN `<script>` would need `script-src` to
   admit an external host; we refuse to widen the policy.
2. By default htmx injects a `<style>` element for its request indicators. Under
   `style-src 'self'` (no `unsafe-inline`) that injection is blocked and logged
   as a violation.

## Decision

Self-host htmx inside the binary, exactly like the other vendored assets, and
serve it same-origin.

- **Ship it in the binary.** `static/js/htmx.min.js` (htmx 2.0.4, 0BSD) is
  committed under `static/js/`, so it is compiled in via the existing module-root
  `//go:embed static` → `StaticFS` (`embedded_assets.go`) and written to
  `STATIC_DIR` on boot by `syncEmbeddedStatic` (`cmd/vayupress/static_sync.go`).
  A binary-only self-update therefore carries htmx with it — no separate copy
  step, no stale-asset window (ADR-0099). The 0BSD license text is vendored at
  `static/js/htmx.LICENSE` and attributed in `NOTICE`.
- **Serve it same-origin.** `handleHTMXJS` (mirroring `handleThemeToggleJS` in
  `cmd/vayupress/handlers_theme.go`) serves the embedded bytes at
  `/static/js/htmx.min.js` with `Content-Type: application/javascript;
  charset=utf-8` and a long `Cache-Control`. The route is registered next to the
  other `/static/js/*` routes in `registerRoutes`
  (`cmd/vayupress/routes.go`). Because it is same-origin it satisfies
  `script-src 'self'` with **no nonce and no external host**.
- **Keep style-src intact.** The admin layout head carries
  `<meta name="htmx-config" content='{"includeIndicatorStyles":false}'>`, so htmx
  never injects its indicator `<style>` and `style-src 'self'` holds unchanged.
  The layout loads htmx with `<script src="/static/js/htmx.min.js?v=…" defer>`,
  content-hash-versioned like every other admin asset.
- **Actually use it.** The Members page "Recent activity" card gains a Refresh
  button (`hx-get="/os/members/activity"`, `hx-target="#os-activity-feed"`,
  `hx-swap="innerHTML"`) served by `handleOSMembersActivityFragment`, a
  session-guarded read-only GET returning just the feed fragment. Without htmx
  the card still renders the server-side feed, so the page degrades gracefully.

## Consequences

- The admin panel gets declarative, framework-free progressive enhancement while
  the strict CSP is unchanged: no `unsafe-inline`, no `unsafe-eval`, no CDN, no
  external request.
- The binary grows by htmx's minified size (~51 KB). Acceptable for a sovereign
  single-binary product and consistent with the "assets ship inside the binary"
  rule (ADR-0099); a self-update advances htmx atomically with everything else.
- Fragment endpoints follow the existing convention — session-guarded, read-only
  GETs need no CSRF token; writes stay CSRF-protected.
- Future admin interactions can be enhanced with `hx-*` incrementally without any
  new dependency, build step, or CSP change.
