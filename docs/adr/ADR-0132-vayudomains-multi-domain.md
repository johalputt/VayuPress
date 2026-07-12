# ADR-0132: VayuDomains — multi-domain support (staged)

- **Status**: Accepted
- **Date**: 2026-07-12
- **Relates to**: ADR-0001 (SQLite-first), ADR-0033/0034 (migrations +
  checksums), ADR-0048 (route registration), the `site.mode` business-website
  topology, the in-process mail engine, the members/subscribers model

## Context

VayuPress is a single sovereign binary that today serves one primary domain
(e.g. `johal.in`) with optional `mail.<domain>`, `blog.<domain>` and
`talk.<domain>` subdomains. The want is **VayuDomains**: let one install answer
on *many* independent hostnames, each choosing what it serves (blog, business
site, static bundle, or mail-only) and — eventually — carrying its own branded
mail, its own members, its own TLS certificate, and its own content, all with
strict isolation between domains.

This is a large, cross-cutting change. A recon pass across the routing, mail,
content, member, TLS and governance subsystems surfaced several constraints
that make an all-at-once implementation risky:

1. **TLS privilege model.** The binary runs unprivileged on `:8080` behind
   nginx + certbot. It cannot run `certbot` or reload nginx itself. Let's
   Encrypt also caps a single certificate at 100 SANs, so N domains cannot
   share one cert — each needs its own (`--cert-name <host>`). Automated
   per-domain provisioning therefore requires a privileged root helper invoked
   out-of-process, not an in-binary call.
2. **Member auth is globally unique.** Subscribers/members are keyed by email
   across the whole install; they cannot currently be scoped per domain without
   a schema change and a careful data migration.
3. **Content scoping is a multi-table change.** `articles.slug` is globally
   UNIQUE and the slug is a shared key across `articles`, `article_access`,
   `article_sources`, `article_versions`, `scheduled_posts`, `collections` and
   `comments`. Per-domain content requires threading a domain key through all of
   them — a 7-table, PK-constrained change.
4. **Mail isolation.** The mail engine resolves a mailbox to a Maildir from the
   primary domain; a naïve second domain would read the primary's mail. Branded
   per-domain mail needs the Maildir/account resolution to be domain-aware.

Doing all of this in one release would be a high-blast-radius change to the
exact subsystems that carry the product's guarantees (isolation, no-plaintext
mail, immutable migrations). The primary domain must stay **byte-identical**
throughout.

## Decision

Ship VayuDomains in **stages**, each independently shippable and reversible,
with the registry as the stable foundation the later stages build on. The
primary domain is always described by the registry rather than changed by it.

### Stage 1 — Foundation (this release)

- **Migration 059** adds a `domains` table: `id`, `host` (unique, normalised),
  `site_type`, `mail_enabled`, `tls_state`, `config_json`, `is_primary`,
  `status`, timestamps. One statement per physical line per the migration
  runner's contract (ADR-0033/0034); the checksum is immutable once shipped.
- **`internal/domain`** is the registry service: an in-process, TTL-cached,
  host-keyed store with `Resolve`, `Primary`, `List`, `Create`, `Update`,
  `SetStatus`, `SetTLSState`, `Delete`, and `EnsurePrimary`.
- **Go-side seed.** At startup `EnsurePrimary(config.Cfg.Domain, site.mode)`
  idempotently inserts (or repairs) exactly one primary row, with
  `tls_state = 'primary'` (its cert is the existing certbot cert, managed
  outside the registry). The primary row *describes the existing install*, so
  `johal.in` is byte-identical. The migration cannot read env, so the seed is
  Go-side, not SQL.
- **Host-resolution middleware** annotates every request with the resolved
  domain and does nothing else. An unknown or disabled host falls back to the
  primary, preserving today's "answer on any host" behaviour. No handler yet
  branches on the resolved domain, so behaviour is unchanged.
- **`/os/domains`** admin page lists domains and manages secondary ones
  (add / enable / disable / remove), with the primary shown read-only. The page
  states plainly that content/mail/member scoping arrive in later stages so an
  operator is never surprised by what a new domain does and does not yet serve.

The primary domain is **protected**: it cannot be disabled, deleted, or edited
from this page (its `site_type` tracks the global `site.mode`; disabling it
would take the install offline).

### Stage 2 — Content scoping (deferred)

Thread a domain key through the content tables (constraint #3). Because
`articles.slug` is globally unique today, this is a migration + query-layer
change gated behind the registry; until it lands, secondary domains serve the
shared content set by `site_type`.

### Stage 3 — Mail branding + isolation (deferred)

Make mailbox/Maildir resolution domain-aware (constraint #4) so `mail_enabled`
domains carry their own branded mail without reading the primary's Maildir.

### Stage 4 — Member isolation (deferred)

Scope subscribers/members per domain (constraint #2).

### P4 — TLS + nginx automation (deferred, orthogonal)

A privileged root helper (in the shape of the existing
`scripts/setup-talk-subdomain.sh`) provisions a **per-domain** certificate
(`--cert-name <host>`, never sharing a cert past the 100-SAN cap) and writes the
vhost, driven by the registry's `tls_state`. The binary records state; the
helper acts on it out-of-process. Stage 1 only records `tls_state`; it never
provisions.

## Consequences

- **Safe foundation.** Stage 1 adds a registry and a pass-through middleware
  with zero behavioural change to the primary domain — a low-risk base that the
  higher-risk stages depend on but do not yet trigger.
- **Honest UI.** The admin page advertises the rollout state, so "add a domain"
  never over-promises isolation or mail that a later stage delivers.
- **No cert foot-guns.** The per-domain-cert + root-helper decision is recorded
  now, so P4 does not accidentally try to certbot from the unprivileged process
  or blow the 100-SAN cap.
- **Immutability respected.** Each stage is a *new* forward migration; migration
  059 is never edited after shipping (ADR-0034).
- The deferred stages are decisions the operator drives release by release, not
  surprises — this ADR is the roadmap and the hazard register for them.
