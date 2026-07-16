# ADR-0134: VayuAPI — fine-grained, per-key API permissions

- **Status**: Accepted
- **Date**: 2026-07-16
- **Deciders**: BDFL / Owner

## Context

VayuPress API keys (ADR-level history: migrations 041/042, `internal/apikeys`)
have always been all-or-nothing: any issued key authenticates every API route the
static bootstrap key can reach. That is the wrong shape for an ecosystem — a
theme-builder tool should be able to hold a key that can apply themes but can
never delete posts, export members, or touch mail. Autonomous clients (developer
tools, AI agents, VCB-built extensions — see ADR-0135) need to be provisioned
with *exactly* the access they declare, nothing more.

The requirements: per-key grants over concrete sections and actions; key
lifecycle beyond revoke (activate/deactivate without rotating); ownership so a
key belongs to the user who minted it; hard expiry; per-key rate budgets; usage
accounting; and hash-only storage throughout — all without breaking the live
automation that depends on today's keys.

## Decision

1. **Extend `internal/apikeys` — no sibling store.** Permissions, expiry, owner,
   rate and usage are attributes of a key; the existing store already owns the
   `vayu_api_keys` table, hash-only persistence, and the O(1) verification cache.
   A second store would mean two writers over key rows and two verifier hooks.
2. **Capability model = section × action.** Twelve closed sections
   (`posts, themes, design, plugins, domains, analytics, media, comments,
   members, mail, settings, backup`) × six actions
   (`read, write, delete, apply, install, export`), written `section:action`.
   Wildcards: `section:*` grants a section's every action; `*:*` is superuser.
3. **Deny by default, fail closed.** An empty grant set permits nothing. The
   at-rest JSON (`{"v":1,"grants":{…}}`, versioned) parses fail-closed: unknown
   sections/actions and malformed blobs grant nothing, ever.
4. **Additive migration 062** adds `permissions`, `expires_at`, `owner_user_id`,
   `rate_per_min`, `use_count`, `active`. A one-time backfill stamps every
   pre-existing key superuser (`{"v":1,"grants":{"*":["*"]}}`), so upgrades never
   strip access from running automation. The backfill predicate
   (`WHERE permissions='{}'`) can never match an API-minted key, because the
   store always writes the versioned envelope — even for deny-all.
5. **Implicit superusers are exactly two**: the static bootstrap `API_KEY` and
   the internal system key (`ScopeInternal`). Every operator-issued external key
   is grant-limited.
6. **Hard expiry is exact.** The resolver re-checks `expires_at` on every cache
   hit, so an expired credential is refused on the next request — never lagged by
   the 30-second verification-cache TTL (a defect caught and fixed in the
   pre-release adversarial review).
7. **Enforcement (Stage 2, shipped in v3.13.38).** A single route→capability
   table (`cmd/vayupress/api_capabilities.go`) maps every protected route on
   *both* surfaces — `/api/v1` (+legacy `/admin` API) and the `/os` twins — to a
   `section:action`, keyed by handler intent so a grant cannot be bypassed by
   switching prefixes. `auth.RequireAPIKey` resolves the key to its `KeyInfo` and
   stamps it into the request context; `requireAPIPermission` (on `/api`) and the
   `requireSessionOrAPIKey` gate (on `/os`) both call the shared `keyMayCall`
   predicate: mapped routes require the route's capability, **unmapped routes
   require a superuser key (fail-closed)**. Session-authenticated operators never
   touch this table — their access stays governed by session RBAC.
   Per-key rate limiting, per-call audit, and the scoped admin UI remain
   follow-up stages.

## Consequences

- Existing keys and the bootstrap/internal keys behave exactly as before the
  migration; nothing breaks on upgrade.
- New keys can be minted deny-all and granted precisely what an integration
  declares (the VCB provisioning flow, ADR-0135).
- `Verify(raw) bool` remains for the existing auth hook; `Resolve(raw)
  (KeyInfo, bool)` is the enforcement-time entry point.
- Deactivation is reversible and instant (cache invalidated on mutation);
  revocation stays terminal; expiry is exact.
- The permission vocabulary is the single source of truth for the admin
  permission grid, the VCB validator, and the public API docs.
