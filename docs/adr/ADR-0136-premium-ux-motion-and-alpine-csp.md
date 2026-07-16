# ADR-0136: Premium UX — a sovereign motion/design-token layer, native View Transitions, and the Alpine.js CSP build

- **Status**: Accepted
- **Date**: 2026-07-16
- **Deciders**: BDFL / Owner
- **Relates to**: Prompt 1 (Core Architecture: "Public Paths: HTMX + Alpine.js only"; "Minimal Runtime Dependencies … approved via RFC"), ADR-0133 (public docs), ADR-0135 (VCB theme contract)

## Context

VayuPress wants a genuinely premium, elegant UI/UX — themes and the VayuOS
console that feel on par with Linear/Vercel/Raycast/Tailwind UI (the aesthetics
the constitution already names as the bar) — **without weakening the strict
CSP, the zero-third-party guarantee, the no-build-step architecture, or single-
VPS performance.**

Two popular options were evaluated against those constraints:

- **Tailwind (CDN)** is disqualified: it is a CDN (violates `connect-src 'self'`
  and the zero-third-party rule), it is a *runtime* JIT that injects `<style>`
  elements (violates `style-src 'self'`), and it ships a large in-browser
  compiler that runs on every load (violates single-VPS performance).
- **Tailwind (build-time, purged, self-hosted)** produces CSP-safe CSS, but
  requires a Node/PostCSS build step, which collides directly with "one binary,
  one command" and "minimal runtime dependencies." VayuPress already has a
  superior sovereign styling engine — the Go theme compiler emitting CSS custom
  properties — so Tailwind-the-framework would fork the system for no gain.
- **Alpine.js (standard build)** is disqualified under our CSP: it evaluates
  directive expressions with `new Function()`, which requires
  `script-src 'unsafe-eval'` — forbidden by the baseline policy.
- **Alpine.js (`@alpinejs/csp` build)** is the eval-free official build:
  components are registered as real functions (`Alpine.data(...)`) and referenced
  by name, so it runs under `script-src 'self' 'nonce-…'`. It is small
  (~15 KB gzipped), self-hostable, and telemetry-free. The constitution already
  sanctions Alpine.js for public paths; this ADR pins *which* build and the
  conditions.

The admin (`admin-os.css`) already carries a mature token system — spacing
(`--sp-*`), radius (`--r-*`), elevation (`--sh-sm/--sh/--sh-lg`, light-mode
adapted), and motion/easing (`--t-fast/--t/--t-slow/--t-spring/--t-enter/
--t-exit`). The **public themes** compiled by `internal/theme` do not: the
compiler emits only colour/font/radius/width variables. That asymmetry is the
real gap.

## Decision

1. **Adopt Tailwind's *aesthetic*, not its runtime.** No Tailwind dependency in
   the product, in any mode. Instead, extend the Go theme compiler to emit a
   shared design-primitive block — an elevation/shadow scale, easing curves,
   duration tokens, a spacing scale, and motion tokens — so every public theme
   inherits the same premium vocabulary the admin already uses. Pure CSS custom
   properties, served same-origin via `/theme.css`, zero build step, zero deps.
   Shadow tokens adapt per colour scheme (subtler on dark, more present on
   light), mirroring the admin's existing light-mode shadow overrides.
2. **Respect motion preferences.** A global `@media (prefers-reduced-motion:
   reduce)` rule neutralises non-essential animation everywhere. Motion is an
   enhancement, never a requirement.
3. **Native View Transitions for app-like navigation.** Use the platform View
   Transitions API — cross-document `@view-transition { navigation: auto }` for
   the public site and named transitions for hero/article elements, plus
   HTMX-swap transitions in VayuOS. These run on the compositor (GPU), off the
   main thread, with no JavaScript animation loops and no framework. Everything
   degrades cleanly where unsupported.
4. **HTMX stays the server-interaction backbone.** Add `hx-boost` + view-
   transition swap wiring for buttery navigation without an SPA. No new
   dependency — HTMX is already self-hosted.
5. **Alpine.js CSP build, vendored, for genuinely client-side islands only.**
   Self-host the eval-free `@alpinejs/csp` build under `static/js/` (with its
   MIT LICENSE), loaded via a nonce'd `<script>` on admin pages, wired through a
   small `Alpine.data()` registry — never inline expressions. It is used only
   where reactive client state genuinely beats an HTMX round-trip (command
   palette, the permission grid, theme-studio live preview). It stays within the
   admin "heavy-framework" budget (< 50 KB gzipped). HTMX remains the default;
   Alpine is the exception, not the rule.
6. **The VCB theme contract validates the new tokens.** As themes gain motion
   and elevation tokens, the VCB theme validator (ADR-0135) is extended so
   third-party themes declaring them are checked, keeping the polish safe to
   scale across the ecosystem.

## Consequences

- Premium, cohesive UX across public themes and VayuOS, achieved with **native
  CSS + a tiny sanctioned island runtime** — no CDN, no build step, no
  `unsafe-eval`, no telemetry. The single-binary, sovereign story is intact.
- **Faster, not slower.** Avoiding a runtime CSS framework removes hundreds of
  KB of in-browser compilation and a third-party round-trip; compositor-driven
  CSS animations and View Transitions replace JS animation work, improving TTI
  and INP.
- The public theme output grows by a small, static token block (shared across
  every theme); the payload cost is negligible and cached.
- Alpine (CSP build) is the first new client dependency admitted since HTMX;
  it is self-hosted, nonce'd, budgeted, and scoped to the admin. Standard Alpine
  and any CDN delivery remain prohibited by the CSP.
- The marketing site (`docs/site`, GitHub Pages) is unaffected: it already uses
  CDN Tailwind + Alpine and lives outside the product's CSP; nothing here
  touches it.

## Vendor provenance

- **`static/js/alpine-csp.min.js`** — `@alpinejs/csp` **3.15.12**, fetched
  canonically from the npm registry (CDN hosts `unpkg`/`jsdelivr` are egress-
  blocked, which is itself consistent with the no-CDN rule), tarball verified
  against the registry shasum. SHA-256 of the vendored file:
  `566167134bb2347110904e2ced6e816d2e8d837200c158f98b72372b3bb0b9a6`.
  Proven eval-free (zero `eval(` / `new Function`), enforced going forward by
  `TestVendoredAlpineIsEvalFree`. MIT — license text in
  `static/js/alpine-csp.LICENSE`.
- **`static/js/vayu-islands.js`** — first-party `Alpine.data()` registry
  (`filterList`, `disclosure`, `copyable`), registered on `alpine:init`,
  components referenced by name only (no inline expressions). Loaded before the
  Alpine build; both deferred, same-origin, nonce-threaded.
