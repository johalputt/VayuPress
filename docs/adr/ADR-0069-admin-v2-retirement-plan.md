# ADR-0069 — Admin v2 Retirement Plan

**Status:** Accepted (plan; execution staged across future releases)
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

### Stage 1 — Parity (target: 1.4.0)

- v3 block editor gains a **create path** so brand-new posts no longer need v2.
- Add a one-time, explicit **"Convert to blocks"** action that imports a legacy
  article's HTML into a block document (parse → blocks) behind a confirmation,
  so legacy posts can be adopted into v3 without the current "open in v2" detour.
  Until converted, legacy posts still open losslessly — no automatic rewrite.
- v3 reaches feature parity for every task an operator can do in v2.

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

### Stage 3 — Removal (target: 1.6.0, no sooner than ~2 minor releases after 1.3.0)

- Delete the v2 handlers, templates, and `static/*/admin-v2.*` assets.
- `/admin`, `/admin/v2` and `/admin/v3` permanently redirect (301) to `/os`.
- Remove the `ADMIN_LEGACY` escape hatch.
- A CHANGELOG **Upgrade Notes** entry documents the removal; the major-version
  policy is respected (removal is additive-safe because `/os` covers all flows).

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
