# ADR-0068 — Admin v3: Next-Generation Admin & Block Editor

**Status:** Accepted
**Date:** 2026-06-20
**Deciders:** VayuPress Maintainers

---

## Context

Admin v2 (ADR-0065) delivered a CSP-clean, editor-first redesign on a vendored
stack. The next goal is an admin experience that surpasses Ghost, WordPress, and
Substack in design quality, feature depth, and security — while keeping every
VayuPress invariant: a sovereign single binary, zero CDN dependencies, and a
strict Content-Security-Policy with no `unsafe-eval` and no `unsafe-inline`.

A naïve path (ship a React/Tailwind SPA, add a rich-text editor library, pull a
QR/2FA package) would violate those invariants and balloon the dependency
surface. We need a ground-up design system, a block editor, a media library,
memberships, and two-factor auth — all server-rendered or vanilla-JS, all
same-origin, all sanitised server-side.

## Decision

Build **Admin v3** at `/admin/v3`, mounted in parallel with `/admin/v2` so the
work is **non-breaking** and can be cut over gradually.

- **Design system.** A single hand-authored `admin-v3.css` scoped to a `.vp-v3`
  body class, driven by CSS custom properties, with dark/light/auto themes via
  `[data-theme]`. No inline `<style>` and no `style="…"` attributes anywhere —
  width/spacing/colour are utility/component classes so `style-src 'self'`
  holds. Self-hosted fonts under `font-src 'self'` with system fallbacks.
- **Interactivity.** Vanilla JS in same-origin files; the only inline `<script>`
  is the nonce-gated bootstrap. All DOM mutation uses `createElement` /
  `textContent` — never `innerHTML` with untrusted data. The one place rendered
  HTML is injected (editor live preview) consumes server-sanitised output and is
  additionally re-sanitised with the vendored DOMPurify.
- **Block editor.** The canonical document is a JSON array of typed blocks stored
  in `articles.blocks_json` (migration 025). `articles.content` remains the
  authoritative rendered HTML, kept in sync on save, so every reader, feed, and
  search path is unchanged. `internal/blockrender` converts blocks → HTML by
  HTML-escaping every field and running the result through a bluemonday UGC
  policy. There is **no raw-HTML block** escape hatch. A safe migration rule
  opens the block editor only for articles that already carry a block document
  or are empty drafts; legacy content and brand-new posts keep the lossless v2
  editor, so a stray save can never wipe existing content.
- **Media library.** Reuses the hardened upload backend (content-addressed,
  type-allowlisted, SVG-refused, CSRF-protected). The listing endpoint only ever
  surfaces server-generated content-addressed names (`safeMediaName`), so there
  is no path-traversal or info-leak vector.
- **Two-factor auth.** `internal/totp` implements RFC 6238 (over RFC 4226) using
  only the standard library — no third-party dependency — validated against the
  official RFC test vectors. Enrolment is a two-step ceremony (store secret
  disabled → verify code → enable) so an abandoned setup never locks an operator
  out. Sign-in enforcement is wired into **both** the v2 and v3 login handlers so
  an enrolled account cannot bypass 2FA via the older surface. QR rendering is
  intentionally deferred; manual key entry (every authenticator app supports it)
  avoids adding a QR-encoder dependency.
- **Intelligence.** Native SEO readiness and privacy-preserving analytics are
  computed only from the local DB and on-disk cache — no third-party services,
  consistent with VayuPress's zero-telemetry stance.

The work shipped in seven phases; each phase was independently built, tested
(`go test`, `-tags integration`, `staticcheck`, `gofmt`), and only advanced once
CI was green.

## Consequences

- v2 and v3 coexist; operators can adopt v3 incrementally and v2 remains the
  fallback for legacy Markdown/HTML editing until a later cutover.
- New schema: migrations 025 (`articles.blocks_json`) and 026 (`users.totp_secret`,
  `users.totp_enabled`). Both are additive with safe defaults.
- The block document and the rendered HTML are stored together; `blockrender` is
  the single trusted boundary for block → HTML and must remain the only path.
- Zero new third-party dependencies were added for 2FA, QR, or the editor.

## Alternatives considered

- **SPA + component library / rich-text editor dependency** — rejected: violates
  zero-CDN/sovereign posture and the strict CSP, and expands the supply chain.
- **Replacing the v2 editor outright** — rejected: risks data loss for legacy
  articles; the state-based editor selection is safer.
- **Third-party TOTP/QR libraries** — rejected: the stdlib implementation is
  small, auditable, and dependency-free.
