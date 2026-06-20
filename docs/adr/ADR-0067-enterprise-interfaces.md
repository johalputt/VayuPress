# ADR-0067 — Enterprise Interfaces: Read-Only GraphQL, i18n, Email Templates, Live Stream

**Status:** Accepted
**Date:** 2026-06-20
**Deciders:** VayuPress Maintainers
**Owner:** Core / API

---

## Context

Larger operators and integrators asked for four "enterprise" capabilities that
competing platforms gate behind paid tiers or plugins: a GraphQL content API,
user-facing internationalisation, customisable transactional emails, and a
real-time event feed for dashboards/collaboration. Each had to fit the sovereign,
single-binary, governed-write posture rather than fight it.

## Decision

### Read-only GraphQL (`/api/v1/graphql`)

A **query-only** GraphQL endpoint (`internal/graphqlapi`, built on the pure-Go
`graphql-go/graphql`) exposes the public content model: `article(slug)`,
`articles(tag, limit, offset)`, `tags`, and `searchArticles(query, limit)`, with
computed `wordCount`, `readingMinutes`, and `excerpt(length)` fields.

There are **deliberately no mutations.** All writes remain on the existing
API-key-protected, CSRF-guarded, mode-gated REST path, so the GraphQL surface can
never become a second, weaker way to mutate state. Bodies are capped
(`MaxBytesReader`, 64 KB) and offset paging is bounded to prevent unbounded
scans.

### Internationalisation (`internal/i18n`)

A thread-safe, BCP-47-keyed message catalog with built-in English strings,
HTTP `Accept-Language` negotiation (quality-value aware, primary-subtag
fallback), and graceful fall-through to English for any missing key. Operators
add/override languages via `PUT /api/v1/admin/i18n/{lang}` (CSRF-guarded,
persisted in the `i18n_messages` table, hot-reloaded in memory); clients fetch a
merged bundle via the public `GET /api/v1/i18n/{lang}`.

### Customisable transactional emails (`internal/emailtmpl`)

The three transactional emails (magic-link sign-in, comment-approved,
newsletter-confirm) are now operator-editable. Each kind has an independently
overridable subject / text / HTML; unset parts fall back to a built-in default,
and a template that fails to parse falls back rather than breaking delivery. HTML
parts render through `html/template` (auto-escaping); overrides persist in the
`email_templates` table and hot-reload on save. All transactional senders route
through a single `renderEmail()` seam.

### Real-time stream (`/api/v1/stream`)

The previously-unwired SSE hub (`internal/ws`) is connected to the event bus and
exposed as an API-key-gated Server-Sent Events feed that broadcasts
`article.created` / `updated` / `deleted` events as JSON. It is read-only from
the client side (clients cannot push), making it safe for live dashboards and
multi-editor presence without a WebSocket dependency.

### CDN edge push (confirmed, pre-existing)

Cloudflare cache purge (`CF_ZONE_ID` + `CF_API_TOKEN`) and IndexNow submission
already fire on every article mutation via the event bus; this is documented here
as the "CDN push" capability rather than re-implemented.

## Consequences

- **Positive:** A standards-based query API, real i18n, branded emails, and a
  live event feed — all single-binary, all honouring the governed-write and
  strict-CSP posture.
- **Positive:** Two new pure-Go dependencies (`graphql-go/graphql`,
  `dlclark/regexp2` transitively via chroma); no CGO, no services.
- **Negative:** GraphQL adds schema-maintenance surface; bounded by keeping it
  read-only and small.
- **Negative:** Persisted i18n/email overrides add two tables (migration 024);
  both load lazily at boot and are no-ops when empty.

## Alternatives considered

- **Full read-write GraphQL:** rejected — would duplicate (and risk diverging
  from) the governed REST write path's authz, CSRF, and mode gating.
- **WebSocket live stream:** rejected — SSE needs no extra dependency, no
  upgrade handshake, and is sufficient for one-way event delivery.
- **Hosted email/templating service:** rejected — violates sovereignty and
  zero-telemetry; templates live in the operator's own SQLite.
