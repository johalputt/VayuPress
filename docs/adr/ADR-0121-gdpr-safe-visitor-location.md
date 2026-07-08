# ADR-0121: GDPR-Safe Visitor Location (No IP) for Messages, Comments & Members

**Status**: Accepted  
**Date**: 2026-07-08  
**Author**: @johalputt  
**Relates to**: [ADR-0097](ADR-0097-vayuanalytics-no-pii.md) (VayuAnalytics no-PII), [ADR-0001](ADR-0001-sqlite-first.md) (SQLite-first)

## Context

The contact-form inbox (Messages) stored the sender's raw IP address and showed
it on the message detail view. A raw IP is personal data under GDPR, and there
was no lawful-basis or retention story for keeping it — it was a leftover
convenience field. Comments likewise stored a raw `ip` column. Meanwhile the
operator genuinely benefits from knowing *roughly* where a message, comment, or
member comes from, and VayuAnalytics already solves exactly this problem the
privacy-safe way: resolve a coarse country (offline, embedded table, IP never
persisted) plus region/city from trusted proxy headers, and store only the
coarse result (ADR-0097).

## Decision

Stop storing or displaying any raw IP for contact messages and comments, and add
a coarse, GDPR-safe location to messages, comments, and members instead.

1. **Capture at write time, store only the coarse result.** The existing
   `geoFromHeaders(r)` helper (country offline via the embedded iploc table;
   region/city only from trusted proxy headers) is called at contact-form
   submit, comment submit, and member sign-in. The result — `country`, `region`,
   `city` — is stored; the IP is never persisted. For contact rate limiting the
   IP is still read, but only in-process.

2. **Schema (migration 058).** Add `country/region/city` columns to
   `contact_messages`, `comments`, and `members`. As data minimisation, the
   migration also **purges** the historical `ip` values from `contact_messages`
   and `comments` (`UPDATE … SET ip=''`); the now-unused `ip` column is left in
   place (kept empty) rather than dropped, to avoid a risky column-drop on older
   SQLite. Nothing writes it any more.

3. **Members capture once, never overwrite.** `Member` gains the three columns
   (in the canonical `memberCols`/`scanMember`), and a `SetGeoIfEmpty` records a
   member's join location the first time it is known — best-effort, and it never
   overwrites an existing value (so a later sign-in from elsewhere doesn't rewrite
   the join origin).

4. **Display: one shared helper.** `geoDisplayHTML(country, city)` renders the
   self-hosted SVG flag + full country name (reusing `countryDisplayHTML`) with
   the city appended when known, or a muted "Unknown". It is used by the Messages
   inbox (list + detail), the Comments manager, and the Members console. Flags
   are the same MIT-licensed self-hosted SVGs analytics uses, so there is no
   external request and no Windows emoji-font gap.

## Consequences

- **No raw IP is stored or shown** for contact messages or comments; existing
  IPs are erased on upgrade. This closes the GDPR concern directly.
- Operators still get useful provenance — country (always, offline) and city/
  region (when the proxy provides them) — with a flag, across all three consoles.
- **Region/city remain proxy-dependent by design.** VayuPress ships no
  city-level GeoIP database and performs no city lookup (privacy + binary-size),
  so region/city are populated only when a reverse proxy sends them (e.g.
  Cloudflare's "Add visitor location headers" managed transform). The analytics
  Locations card already documents this exact setting; country still resolves
  offline everywhere.
- The change is additive at the schema level and backward compatible; no
  feature depends on the removed IP.
