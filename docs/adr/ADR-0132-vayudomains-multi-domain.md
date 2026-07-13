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

### Stage 2 — Content scoping

**Stage 2a (shipped): content-ownership foundation.** Migration 060 adds an
additive `domain_id` column to `articles` (default `''` = the primary /
unassigned bucket), so an existing install is byte-identical — the primary
domain still serves exactly its historic rows. The repository and article
service gain domain-scoped reads (`ListScoped` / `GetScoped`, with `db.ScopeAll`
disabling the filter for the admin/JSON path), plus `SetDomain` and
`CountsByDomain`. VayuOS → **Domains** shows a per-domain post count and lets an
operator assign a post to a domain by slug. The public render path is
deliberately **not** rewired yet, so behaviour is unchanged and provably safe.

**Stage 2b (shipped): per-domain public homepage + article pages.** The public
homepage feed and article pages now serve only the active domain's content,
keyed off the Stage 1 host-resolution middleware. Safety was the design centre:
the per-domain code paths run **only when a secondary domain is registered**
(`multiDomain`), so a plain single-domain install takes its original route with
zero added work and is provably byte-identical (proven by the unchanged home/
article tests). When multi-domain is active, the homepage query is scoped by
`domain_id` and cached to a per-domain file (`home/d_<id>/index.html`; the
primary keeps `home/index.html`), and the article page gates on ownership — a
post is reachable only on its owning domain (slugs are globally unique, so this
is an exact one-owner lookup). Reassigning a post lazily purges the caches so
every domain re-renders on next request.

**Stage 2c (shipped): per-domain taxonomy, feeds, sitemap and search.** The
remaining public discovery surfaces are now scoped to the active domain, on the
same `multiDomain` gate as Stage 2b — a single-domain install keeps the original
global code path and artefacts, byte-identical:

- **Tag index + tag pages.** `/tags` counts only the active domain's published
  posts, and `/tags/<tag>` lists only that domain's matches (cached per domain at
  `tags/d_<id>/…`). A tag with no published post on the domain 404s as before.
- **Sitemap + feed + robots.** `/sitemap.xml`, `/feed.xml` and `/robots.txt` are
  served from per-domain artefacts (`sitemap_d_<id>.xml`, `feed_d_<id>.xml`,
  `robots_d_<id>.txt`) that list only the domain's posts/tags and carry the
  domain's own host, so no domain ever advertises a URL that would 404 under the
  Stage 2b ownership gate. They are generated lazily on serve within a short
  freshness window rather than wired into the per-post `CachePurge` fan-out,
  keeping the change self-contained; the historic global artefacts remain the
  single-domain path.
- **Search.** The VayuFind engine stays a single, domain-blind index (no engine
  fork). The server endpoint (`/api/v1/search`) over-fetches and post-filters
  hits by ownership; the downloadable client index (`/api/search-index.json`)
  that hydrates the instant-search modal is filtered to the domain's slugs and
  memoised per domain, rebuilt when the engine snapshot version changes.
  Reassigning a post between domains touches no indexed field, so that path
  explicitly drops the per-domain index memo.

The JSON/GraphQL query API (`gqlResolver.Search`/`Tags`/`Articles`) remains a
global surface, consistent with the `db.ScopeAll` admin/API convention — it is a
query interface, not a per-host rendered site.

**Still deferred:** per-domain canonical/branding on the rendered pages belongs
with Stage 3 (mail/identity); the `UNIQUE(domain_id, slug)` relaxation (below)
remains a later sub-stage.

**Constraint carried forward:** `articles.slug` is globally UNIQUE via an inline
(un-droppable) constraint, so two domains cannot yet share a slug. Relaxing that
to `UNIQUE(domain_id, slug)` needs an `articles`-table rebuild and is a later
sub-stage.

### Stage 3 — Mail branding + isolation

Make mailbox/Maildir resolution domain-aware (constraint #4) so `mail_enabled`
domains carry their own branded mail without reading the primary's Maildir. The
recon that opened this stage found the on-disk Maildir layer is *already*
domain-partitioned (`<base>/maildir/<domain>/<user>/…`); every resolution path
simply collapses the domain to the primary `cfg.Domain`. Because existing storage
already sits under `<primaryDomain>/<user>`, threading the *real* domain leaves the
primary byte-identical — but doing so touches the acceptance gate, the IMAP/POP3
login path and the whole engine mailbox API at once, which is exactly the
high-blast-radius change on the mail guarantees this ADR exists to avoid. The mail
subsystem is therefore staged like content (2a → 2b → 2c):

**Stage 3a (shipped): mail-domain foundation.** The VayuOS **Domains** page now
reports, per domain, whether it carries mail and how many mailboxes it holds
(`AccountStore.CountsByHost`, a read-only derivation — mailboxes are keyed by full
address, so the host is derived, not stored). The **delivery, authentication and
Maildir-resolution paths are deliberately untouched**, so the primary domain's
mail is provably byte-identical (proven by the unchanged mail-engine tests). This
gives operators visibility into the rollout without putting a live mail server at
risk.

**Stage 3b (shipped): per-domain receive + read isolation.** A `mail_enabled`
secondary domain now receives external mail into its own isolated Maildir and its
users read only that mailbox over IMAP/POP3:

- **Acceptance.** A single predicate — `Config.AcceptsMailDomain` (primary via
  `cfg.Domain`, secondaries via an injected `MailAccepts` resolver backed by the
  registry) — gates the SMTP receive server (`recipientAccepted`), the engine's
  local-recipient check and the bridge's `IsLocalRecipient`. `MailAccepts` is nil
  in every single-domain install and test, so the predicate collapses to the
  historic primary-only check — byte-identical.
- **Delivery** already carried the parsed recipient domain to
  `maildir.Deliver(domain, …)`; opening acceptance is enough for a secondary's
  mail to land in `<base>/maildir/<secondary>/<user>/…`.
- **Read.** IMAP and POP3 sessions record the authenticated login's owning domain
  (`mailboxDomainFor`, which returns the primary unless a genuinely different —
  secondary — login domain is presented) and key every Maildir operation on it
  instead of `cfg.Domain`. A bare local part or `local@primary` login stays on the
  primary, so the primary path is unchanged. Auth was already domain-correct
  (`AuthUser` uses the full login address).
- **Provisioning.** The Accounts admin page lets an operator create a mailbox on a
  `mail_enabled` secondary (a domain picker appears only when one exists); the
  address, Maildir and PGP key are provisioned under that domain.
- **Webmail stays primary.** CMS-user mailboxes are always primary (team
  assignment hardcodes the primary domain), and the webmail `user` param is a
  local part resolving to `cfg.Domain`, so webmail never reads a secondary
  Maildir — no cross-domain leak. Full webmail access to secondary mailboxes rides
  with Stage 3c.

Covered by explicit cross-domain isolation tests (`bob@example.com` and
`bob@shop.example` are distinct stored messages), acceptance-predicate tests and
the login→domain resolution table. `safeSegment` already neutralises hostile
domain/username path segments, so feeding the real (attacker-influenceable)
recipient domain into the Maildir path adds no traversal surface.

**Stage 3c (shipped): per-domain outbound + autoconfig.** A `mail_enabled`
secondary domain now sends DKIM-aligned, deliverable mail and its clients
auto-configure with their own address:

- **Per-domain DKIM.** `Engine.dkimFor(domain)` returns a signer that reuses the
  selector's shared private key but carries the sender's domain in the signature's
  `d=` tag, so a secondary's outbound validates against the DKIM record published
  at `<selector>._domainkey.<secondary>`. The primary send path uses the signer
  loaded at start unchanged (byte-identical). Every send/compose/autoresponder
  sign site selects by the From domain, and the `Message-ID` and Sent-folder copy
  now carry the sender's own domain too (so a secondary sender's Sent copy stays
  in its isolated Maildir).
- **Per-domain autoconfig.** `/.well-known/vayumail/autoconfig.json` and the
  Thunderbird XML resolve the account domain from the request Host (a
  `mail_enabled` secondary, else primary), while keeping the advertised server
  host the primary mail host — its cert is valid and it serves every domain's
  mailboxes, whose users log in with their own address. No per-secondary cert is
  required for clients to connect today.

**Operator note (DNS).** Because the DKIM key is shared across domains, a
secondary domain publishes the **same** `<selector>._domainkey` TXT record as the
primary, plus its own MX (→ the primary mail host), SPF and DMARC. 

**Stage 3d (in progress): per-domain DNS panel (shipped).** VayuOS → mail now
renders, below the primary's records, a **DNS-records-to-publish card for each
`mail_enabled` secondary** (`Engine.PlannedRecordsForDomain`): its MX points at the
primary mail host, its SPF/DMARC are scoped to itself, and — because the DKIM key
is shared — it publishes the same key value at its own `<selector>._domainkey`
record. This closes the operability gap: an operator can now set a secondary
domain's mail up end to end (register → enable mail → create mailbox → send
DKIM-signed → publish the shown DNS).

**Stage 3d webmail access (shipped).** A CMS user can be assigned a mailbox on a
`mail_enabled` secondary domain (team assignment now takes an optional domain,
validated by `acceptsSecondaryMailDomain`), and that user reads/searches/acts on
their mail in the built-in **webmail**. The engine read API became domain-aware
(`Engine.mailboxKey` accepts a bare local part → primary, byte-identical, or a full
address → that domain), and the webmail handlers resolve the mailbox key
**server-side** from the authenticated identity (`ownMailboxKey`) — never from the
XSS-sanitised `user` param, so the domain carries no injection surface and the
primary path is byte-identical (the key collapses to the local part on the primary
domain). *Still deferred:* per-domain webmail/console branding from the registry's
reserved `config_json` (its meaning — public site identity vs console chrome — will
be pinned before building). Per-secondary TLS certs remain **P4**.

**Performance note.** Every per-domain path is gated behind
`Registry.HasSecondaries` (a precomputed cached bool — no map scan, no per-request
DB query) or the primary-first `Config.AcceptsMailDomain` check (the registry
resolver is consulted only for non-primary recipients), so a single-domain install
— the overwhelming common case — carries **zero** added work on the blog or VayuOS
hot paths.

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
