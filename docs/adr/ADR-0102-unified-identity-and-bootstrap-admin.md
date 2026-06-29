# ADR-0102: Unified VayuMail + CMS Identity, Bootstrap Admin, Author Handles

**Status**: Accepted
**Date**: 2026-06-29
**Author**: @johalputt
**Owner**: Core
**Relates to**: [ADR-0098](ADR-0098-role-scoped-vayuos-access.md)

## Context

VayuPress has two account systems that overlap for staff:

1. **CMS users** (`internal/users`) — email + password accounts with roles,
   editable public profiles, and a stable `/author/<id>` page.
2. **VayuMail accounts** (`internal/vayuos/mail`) — sovereign mailboxes that can
   also sign in to the console, scoped by their mail role (ADR-0098).

A person commonly has both at the **same address** (e.g. `ankush@johal.in` is a
CMS admin *and* a mailbox). Signing in through the mailbox synthesised a
throwaway identity with id `vmail:<email>` that was never persisted. Three
problems followed:

- **Profile edits could not be saved** — there was no persistent row to write to.
- **The public author URL became `/author/vmail:<email>`** instead of the stable
  `/author/<id>`, and looked broken.
- Operators perceived "two different accounts" for one person.

Separately, two onboarding rough edges existed: a fresh install required the CLI
(`vayupress user add …`) before anyone could log in, and author URLs exposed an
opaque content-addressed id rather than a readable handle.

## Decision

1. **One identity, keyed by email.** When a console-capable mailbox session (or a
   portal member) resolves and a **persisted CMS user exists with that email**,
   the request is served as that CMS user — editable profile, stable
   `/author/<id>`, single identity. The mailbox role still governs console vs
   mail-only access. A pure mailbox with no CMS counterpart keeps the synthesised
   `vmail:` identity as before, so reader/mailbox-only accounts are unchanged.

2. **Bootstrap administrator.** On a database with zero users, the engine creates
   `admin@<domain>` with a strong random password, writes the credentials to a
   root-only `initial-admin.txt` beside the database and logs them once, and sets
   a `must_change_password` flag. The console gates that account to a
   `/os/change-password` page (allow-listing only that page, logout, and static
   assets, so it can never lock the operator out) until a new password is set.

3. **Human-readable author handles.** A `username` column (migration 051),
   derived from the email local-part and uniquified (`-2`, `-3`), powers
   `/author/<username>`. The public handler resolves by username first, then
   falls back to the id, so existing links keep working; handles are backfilled
   at startup for pre-existing accounts.

## Consequences

- A staff member is one account however they authenticate; profiles save and
  author URLs are stable and readable.
- A fresh install is usable immediately without the CLI, while still forcing the
  default password to be replaced before anything else.
- Migrations `050-user-must-change-password` and `051-user-username` add the two
  columns; both default to safe values so existing accounts are unaffected.
- Identity remains email-keyed; merging two *different* emails for one person is
  explicitly out of scope.
