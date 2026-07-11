# ADR-0130: VayuMail retention — auto-delete read mail

- **Status**: Accepted
- **Date**: 2026-07-11
- **Relates to**: ADR-0129 (device approval), the VayuMail Maildir store

## Context

Mailboxes accumulate read mail indefinitely. The operator wants hygiene
with teeth: mail that was read and never deliberately saved should be
permanently deleted after a configurable window — 30/90/180/365 days —
with no recovery, per mailbox, off by default.

Two design questions dominate: what counts as *saved*, and where the
*read time* comes from.

## Decision

### Saved means intent

The sweep only ever touches **Inbox, Junk, and Trash**. Sent, Drafts,
Archive, and Snoozed all encode deliberate intent (you wrote it, you're
still writing it, you archived it, you scheduled it) and are exempt.
Within the swept folders, a message survives when it is **unread** (still
in `new/`, or without the Maildir `S` flag) or **pinned** (the `F` flag —
the webmail's 📌 and IMAP's `\Flagged`). Everything else that has been
read for longer than the window is removed with `os.Remove` — permanent,
no Trash detour, exactly as specified.

### Read time is the file mtime, stamped at the Seen transition

`setMessageFlags` is the single chokepoint through which every flag
change passes — webmail mark-read, IMAP `STORE`, POP3-driven flag
updates. Maildir renames preserve mtime, so a file's timestamp normally
stays at delivery time. At the moment a message first gains `S`, the
store now stamps the file's mtime to *now*; every later flag rename
preserves that stamp. The retention sweep reads it back with `os.Stat`.

This gives a durable, restart-proof read time with **zero new
bookkeeping**: no table, no dual-write, no reconciliation between the
filesystem (the source of truth for mail) and SQLite. The trade-offs are
acknowledged:

- Mail read **before** this release keeps its delivery mtime, so for
  legacy mail the window counts from delivery rather than read time —
  retention can only fire *earlier* for such mail, never keep it longer.
  Operators enable retention after upgrading, so the first sweep's
  behaviour is predictable.
- Filesystem tools that rewrite mtimes (backup restores without
  `--times`, `touch`) would reset the clock. Acceptable for a
  self-hosted single-VPS product; the alternative (a read-times table)
  buys precision at the cost of a second source of truth.

### Configuration and audit

`vayumail_accounts.retention_days` (idempotent `ALTER`, default `0` =
off) with `RetentionDays`/`SetRetentionDays` accessors. The admin
Accounts page gains an "Auto-delete read" select per mailbox
(Off/30/90/180/365) posting through the existing account-update handler
(admin-only; audit `vayumail.retention.set`). The sweeper runs from
`Engine.Start` — first pass two minutes after boot, then hourly,
stopping with the engine's `done` channel — and every sweep that deletes
anything writes one `vayumail.retention.sweep` audit entry per mailbox
with the count, via a callback so the engine stays free of the database
layer.

## Consequences

- Opted-in mailboxes stay lean without manual cleanup; the exemption
  set (pinned/Archive/Sent/Drafts/Snoozed/unread) makes "save this"
  a one-tap action in every client.
- Deletion is genuinely unrecoverable, matching the operator's
  requirement — the UI copy says so explicitly.
- IMAP clients see the deletion as an expunged message on their next
  sync; the UID store already tolerates disappearing files.
- Tests pin the eligibility matrix (old-read deleted; fresh-read,
  unread, pinned, Archive, Sent survive), the off-is-noop guarantee,
  and the mtime stamping/preservation semantics.
