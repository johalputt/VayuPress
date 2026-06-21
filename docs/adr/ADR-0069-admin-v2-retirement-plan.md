# ADR-0069 — Admin v2 Retirement Plan

**Status:** Accepted — **fully executed in v1.6.0** (all three stages complete)
**Date:** 2026-06-20
**Deciders:** VayuPress Maintainers
**Supersedes (eventually):** the operator-facing surface of ADR-0065

---

## Context

Admin v3 (ADR-0068, shipped in 1.3.0) is the next-generation admin and is
mounted in parallel with Admin v2 (`/admin/v2`, ADR-0065). Running two admin
surfaces indefinitely doubles maintenance and review surface and risks drift
(e.g. a 2FA bypass if one surface lags — already mitigated by enforcing TOTP on
both). We want a deliberate, non-disruptive path to a single admin.

Admin v2 currently still owns one capability v3 delegates to it: the **lossless
legacy editor** for articles authored as raw Markdown/HTML (no block document),
and brand-new posts (create path). v3 intentionally routes those to v2 to avoid
any content-loss risk. Retirement cannot complete until v3 owns these.

## Decision

Retire Admin v2 gradually over three releases, gated on v3 reaching full parity.
Each stage is independently shippable and reversible until the final removal.

### Stage 1 — Parity (target: 1.4.0 → completed for create in 1.6.0-dev)

- ✅ **Native create path** (1.6.0-dev): brand-new posts open the `/os` block
  editor and are created on first Save through the article service
  (`handleV3EditorSave` with an empty slug). The "New Post" route no longer
  delegates to the v2 editor. (Originally targeted at 1.4.0 but in practice the
  create path still routed to v2 until this change.)
- ✅ **"Convert to blocks"** (ADR-0073): an explicit, confirmed, reversible action
  imports a legacy article's HTML into a block document. Until converted, legacy
  posts still open losslessly — no automatic rewrite.
- ⏳ **Remaining gate:** editing an *existing legacy (non-block)* post still opens
  the v2 editor (`serveV3LegacyEditor`) for lossless HTML/Markdown source editing.
  Because there is deliberately no raw-HTML block (all blocks pass through
  bluemonday), a native lossless legacy-edit surface must be built before the v2
  handlers can be deleted. This is the last blocker for Stage 3.

### Stage 2 — Soft deprecation & the VayuOS move (target: 1.5.0) — **IN EFFECT**

The 1.5.0 work went further than the original plan: rather than making Admin v3
the destination, the admin was rebranded and **remounted as VayuOS at `/os`**,
and **all three** historical surfaces — the classic console (`/admin`), Admin v2
(`/admin/v2`) and Admin v3 (`/admin/v3`) — became legacy.

- ✅ The canonical admin is **VayuOS at `/os`**.
- ✅ `/admin`, `/admin/v2[/...]` **and** `/admin/v3[/...]` **redirect (302) to the
  `/os` equivalent** by default (`legacyToOSPath` + `legacyRedirect`); each hit
  also emits a structured deprecation **warning to the server log**. The
  `ADMIN_LEGACY=1` (or `true`) escape hatch keeps the v2 pages reachable for one
  release, with a dismissible deprecation banner.
- ✅ Stage-1 prerequisite met: convert-to-blocks (ADR-0073) lets legacy posts
  adopt the block editor losslessly.
- Docs and the website point exclusively to `/os`; v2 screenshots are archived.

Implemented in `cmd/vayupress/admin_legacy.go`. The redirects are 302 (not 301)
during soft deprecation so Stage 3 can switch them to permanent without clients
having cached an early 301.

### Stage 3 — Removal (1.6.0) — **COMPLETE**

- ✅ Prerequisite met: the `/os` block editor owns **create** (native create path)
  and **legacy edit** (auto-import legacy HTML → blocks on open, non-destructive
  until save). Nothing authoring-related depends on v2 any more.
- ✅ Deleted the v2 handlers and assets: `admin_ui.go`, the v2 login handlers,
  `static/css/admin-v2.css`, `static/js/admin-v2.js`, and the v2 e2e specs.
- ✅ `/admin`, `/admin/v2[/...]` and `/admin/v3[/...]` permanently redirect (301)
  to the `/os` equivalent.
- ✅ Removed the `ADMIN_LEGACY` escape hatch and the deprecation banner.
- ✅ CHANGELOG **Upgrade Notes** document the removal; removal is additive-safe
  because `/os` covers all flows.

Note: the v1 **operator console** sub-pages (`/admin/modes`, `/admin/faults`,
`/admin/topology`, `/admin/replay`, `/admin/policy`, `/admin/adr`, …) are
intentionally retained — they have a separate lifecycle and were never part of
the Admin v2 editorial surface.

## Guardrails

- **No data loss, ever.** A legacy article is never silently converted; block
  adoption is an explicit, confirmed, reversible-by-restore action.
- **No security regression.** TOTP and CSRF enforcement remain on every surface
  for as long as it exists; the redirect in Stage 2/3 must preserve auth.
- **Reversibility.** Each stage before removal is controlled by config/env so a
  problem can be rolled back without a code change.
- **Gate on green.** Each stage ships only after `go test`, integration tests,
  `staticcheck`, and CI (incl. CodeQL) are clean.

## Consequences

- One admin surface to maintain and review after Stage 3.
- A short window (Stage 2) where both surfaces exist but v3 is the default.
- The API-key auth path and all `/api/v1/*` endpoints are unaffected — this ADR
  concerns only the human admin UI.

## Alternatives considered

- **Remove v2 immediately in 1.3.0** — rejected: would either lose the lossless
  legacy-edit path or force a rushed HTML→blocks importer, risking content loss.
- **Keep both forever** — rejected: permanent double maintenance and drift risk.
