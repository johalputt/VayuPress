# ADR-0088: Newsletter Console — Operator Page, Subscriber Management & Tracked Broadcasts

**Status**: Accepted
**Date**: 2026-06-26
**Author**: @johalputt
**Relates to**: ADR-0068 (VayuOS), migration 015 (newsletter subscribers)

## Context

VayuPress has had a working newsletter *backend* since migration 015: a
double-opt-in subscriber store and HTTP endpoints to subscribe, confirm,
unsubscribe, list, and broadcast. But the VayuOS sidebar's **Newsletter** item
linked to `/os/newsletter`, for which **no page handler or route existed** — the
link dead-ended (falling through to the catch-all `/os/*` redirect). Subscriber
management and broadcasts were only reachable via API-key JSON endpoints, with
no operator UI, no audience insight, no way to delete a record from the browser,
and no record of what broadcasts had been sent or how they performed.

## Decision

Build a first-class **Newsletter console** at `/os/newsletter`, mirroring the
Members console pattern (server-rendered within the strict-CSP VayuOS shell,
behaviour in a same-origin nonce-free JS file, all dynamic values escaped).

1. **A real page.** `handleOSNewsletter` renders audience-health stat cards
   (total / active / pending double-opt-in / unsubscribed / 30-day new /
   confirmation rate), a dependency-free growth sparkline (reusing `osSparkline`),
   a broadcast composer, a broadcast-history table, and a subscriber table.

2. **Subscriber management.** The store gains `Stats`, `List(filter, search,
   limit)` across all states, `Delete` (GDPR erasure / spam cleanup),
   `GrowthByDay`, and `ExportCSV`. The table supports client-side segment tabs
   and instant search; delete and CSV export are wired to session-authed
   endpoints.

3. **Tracked broadcasts.** A new `newsletter_broadcasts` table (migration 041)
   persists each send with its audience size and final sent/failed tallies.
   The console exposes a composer with a **send-test** action and a one-click
   broadcast that records the run and delivers in the background, marking the
   record complete with the tallies. This replaces the previous fire-and-forget
   broadcast that left no trace beyond a log line.

4. **Session-authed surface.** New `/os/api/newsletter/*` routes
   (`stats`, `subscribers`, `broadcasts`, `export.csv`, delete, `test`,
   `broadcast`) sit under `requireSessionOrAPIKey` with CSRF on writes, so a
   browser operator never needs an API key — matching the Members console.

5. **SMTP awareness.** The console surfaces a clear banner and disables the send
   actions when SMTP is unconfigured, while signups and confirmations keep
   working (the mailer remains a safe no-op).

## Guards

- `internal/newsletter/console_test.go` covers `Stats` segmentation, `List`
  filter + case-insensitive search, `Delete`, `ExportCSV` shape, `GrowthByDay`
  bucketing, and the broadcast create/finish/list lifecycle.

## Consequences

- Migration `041-newsletter-broadcasts` is additive and backward-compatible; it
  only adds a history table. The down migration drops that table.
- The newsletter feature is now fully operable from the browser, with audience
  insight and an auditable broadcast history.
- Delivery remains sovereign: plain-SMTP via the Go standard library, no hosted
  sender, no third-party SDK; every broadcast still appends an unsubscribe link.
