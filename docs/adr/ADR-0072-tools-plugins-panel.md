# ADR-0072 — Tools & Plugins Panel: Operator-Toggleable Feature Flags

**Status:** Accepted
**Date:** 2026-06-21
**Deciders:** VayuPress Maintainers
**Target release:** v1.5.0

---

## Context

The v1.5.0 "VayuOS" direction is a single, supreme admin surface that replaces
the v1/v2/v3 split and exposes every operator capability in one place. One of its
pillars is a **Tools & Plugins** panel: a registry of every platform module where
the operator can see live status and switch toggleable modules on or off in a
single click.

VayuPress is a sovereign single binary with **zero CDN dependencies** and no
remote plugin marketplace. "Install" is therefore not a network action — every
module already ships inside the binary. A module is either:

- **built in** (always present; e.g. diagrams, version history, redirects), or
- **operator-toggleable** via a persisted feature flag (e.g. the public-facing
  comments, newsletter signup, and webmention receiver).

We needed a way to turn the public surfaces off without tearing down their
backing stores, so re-enabling is instant and lossless, and without inventing a
second configuration mechanism alongside the existing settings store.

## Decision

1. **Feature flags live in the settings store.** New `feature.*` keys
   (`feature.comments`, `feature.newsletter`, `feature.webmentions`) join the
   `settings.AllKeys` allowlist with a default of `"on"`. `Store.FeatureEnabled`
   treats unset/any-non-`"off"` value as enabled, so features default to
   available and only an explicit `"off"` disables them. Toggling reuses
   `SetMany`, which already invalidates the read cache, so the public gate sees
   the change immediately rather than waiting out the 30 s TTL.

2. **Enforcement happens at the request boundary.** The public handlers
   (`handleCommentSubmit`, `handleNewsletterSubscribe`, `handleWebmentionReceive`)
   consult `FeatureEnabled` after the store-nil check and return `403` when the
   feature is off. Disabling never deallocates the store — operator-side
   management (moderation, lists) keeps working, and existing subscribers can
   still unsubscribe.

3. **The panel is a static registry rendered server-side.** `toolRegistry`
   enumerates every module with an `id`, label, description, category, glyph, an
   optional `FlagKey`, and a `ready` predicate that reports whether the backing
   subsystem is wired. Toggleable modules render a switch; built-in modules render
   a static "Built-in" badge. All fields are `html.EscapeString`'d before emit, so
   the CSP posture (no inline styles, nonce'd scripts only, no external hosts) is
   preserved.

4. **The toggle endpoint is defence-in-depth.** `POST /admin/v3/api/tools/toggle`
   sits behind `requireSessionOrAPIKey` **and** `CSRFTokenMiddleware`. It resolves
   the posted `id` to a flag via the registry and rejects anything not present in
   `settings.FeatureKeys`, so a built-in module can never be switched off — even
   if the registry and the allowlist drift, `SetMany` ignores unknown keys.
   Every toggle is written to the structured log as an operator audit trail.

## Consequences

- Operators get one-click control over public-facing modules with no restart and
  no data loss.
- The pattern generalises: adding a new toggleable module is a registry entry, a
  `feature.*` key, and one `FeatureEnabled` check at its entry point.
- This panel is the first concrete step of the VayuOS consolidation; subsequent
  increments fold the remaining v1/v2/v3 surfaces (monitoring, governance, the
  unified editor) into the same shell.
