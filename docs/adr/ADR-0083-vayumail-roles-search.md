# ADR-0083: VayuMail Role-Based Accounts and Local Mailbox Search

**Status**: Accepted
**Date**: 2026-06-25
**Author**: @johalputt

## Context

VayuMail's admin-managed accounts were a flat list (email + password + active),
with no notion of permissions, and the mailbox had no way to find a message
beyond paging through folders. Moving toward a Gmail-like experience requires
both, but without violating the single-binary / low-resource / zero-telemetry
constitution.

## Decision

1. **Role-based accounts.** `vayumail_accounts` gains a `role` column (idempotent
   `ALTER TABLE`, default `author`). Built-in roles are `administrator`,
   `editor`, `author`, and `reviewer` (read-only); operators may also assign a
   custom role (lowercased, `[a-z0-9-_]`, capped at 32 chars). Permission
   helpers — `RoleCanSend` (everyone except reviewer), `RoleCanDelete`
   (administrator/editor), `RoleCanManageAccounts` (administrator) — express
   capabilities. Account **creation and deletion remain restricted to the
   VayuPress admin session** (the `/os` routes are admin-only); roles add a finer
   permission layer for per-account actions and future SMTP submission.
2. **Bounded local search.** `Maildir.Search` scans an account's folders for a
   query in From/To/Subject, falling back to a body read only on a header miss.
   It is capped (`maxScan` files, result `limit`) so it stays cheap on a
   4–8 GB VPS. There is **no external search index and no extra service** — it
   reads the same Maildir files already on disk.

## Consequences

- Positive: foundational Gmail-like capabilities (permissions + findability)
  with zero new dependencies and no daemon; backward compatible (the role column
  migrates in place, defaulting existing accounts to `author`).
- Trade-off: search is linear over Maildir (bounded), not a full inverted index;
  adequate for personal/team mailboxes, revisited if very large mailboxes need
  it. Role enforcement on outbound SMTP submission lands with the per-account
  submission path (v1.14.0 roadmap).
