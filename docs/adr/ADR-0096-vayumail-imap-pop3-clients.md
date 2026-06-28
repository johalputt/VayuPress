# ADR-0096: Real Mail-Client Access (IMAP + POP3)

**Status**: Accepted  
**Date**: 2026-06-28  
**Author**: @johalputt

## Context

VayuMail already received mail (SMTP), stored it in Maildir, sent it (DKIM-signed,
direct-to-MX or via a relay), and let the operator read/compose in the VayuOS
panel. But the value of running your own mail is being able to use it from the
apps you already use — the Gmail app, Apple Mail, Thunderbird, Outlook — over the
standard protocols. The existing IMAP server was a minimal read-only stub: it
exposed only INBOX, used the message's position as its UID (so every reconnect
looked like a brand-new mailbox to a client), supported no flags beyond \Seen,
and had no APPEND/COPY/MOVE/SEARCH/IDLE. There was no POP3 at all. In practice no
mainstream client could sync against it reliably.

## Decision

Build a real, standards-grade client surface on top of the existing Maildir,
account, TLS and PGP machinery — hand-rolled on the standard library, consistent
with the existing SMTP/IMAP servers (no third-party mail-server framework).

### Stable UIDs (the linchpin)

A new SQLite-backed `UIDStore` (tables `vayumail_uidvalidity`, `vayumail_uids`)
assigns a strictly-ascending UID per (account, folder) and remembers it. UIDs are
keyed on the **immutable** part of the Maildir filename — the base name before
the `":2,<flags>"` info suffix — so a message keeps its UID when it moves
`new/`→`cur/` or its flags change. UIDVALIDITY is the row's creation time and
never changes. This is what lets a client sync incrementally rather than
re-downloading the mailbox on every connection.

### IMAP (RFC 3501 + extensions)

A full IMAP4rev1 server: `LOGIN` and `AUTHENTICATE PLAIN` (SASL-IR); `LIST`/`LSUB`
of all standard folders with RFC 6154 SPECIAL-USE attributes; `STATUS`;
`SELECT`/`EXAMINE` of any folder; `FETCH`/`UID FETCH` of FLAGS, UID, RFC822.SIZE,
INTERNALDATE, ENVELOPE, BODY/BODYSTRUCTURE and `BODY[<section>]<partial>` (driven
by a small MIME walker that parses the message into a part tree); `STORE`/`UID
STORE` flag updates mapped to Maildir flags (S/R/F/T/D); `COPY`/`UID COPY` and
`MOVE`/`UID MOVE` (RFC 6851) with COPYUID/APPENDUID (UIDPLUS); `APPEND` so a
client can save Sent/Draft copies; `EXPUNGE`; a bounded `SEARCH`/`UID SEARCH`
subset; `IDLE` (RFC 2177) with poll-based new-mail notification; `NAMESPACE`;
`CLOSE`/`UNSELECT`. STARTTLS on 143 and implicit TLS (IMAPS) on 993.

### POP3 (RFC 1939 + STLS)

A new POP3 server for download-style clients: `USER`/`PASS`, `STAT`, `LIST`,
`UIDL` (the stable Maildir base name), `RETR`, `TOP`, `DELE`, `RSET`, `NOOP`,
`QUIT`, and `STLS` (RFC 2595). POP3 is single-folder by design, so it serves
INBOX only. STLS on 110 and implicit TLS (POP3S) on 995.

### Shared posture

- Authentication for both protocols is delegated to the existing Bridge
  (`AuthUser` → CMS user or admin-managed mail account, Argon2id), so there is one
  credential source across SMTP submission, IMAP and POP3.
- The transparent PGP decrypt hook is applied on read, so a client downloads
  readable mail.
- Every listener is best-effort: a failed bind (e.g. privileged port, port in
  use) is recorded and surfaced in the VayuMail health check but never blocks
  engine startup. Listen addresses are configurable via `VAYUOS_MAIL_*_LISTEN`.

## Consequences

- VayuMail is usable from mainstream mail apps with correct, incremental sync,
  flags, folders, and sent-mail capture — the experience the project is meant to
  deliver.
- UID assignment adds a small amount of SQLite state per message; it is created
  lazily (only when mail is enabled) and keyed so flag changes never churn UIDs.
- The IMAP feature surface is large; it is covered by tests for UID stability
  across reconnect, multi-folder LIST/SELECT, APPEND round-trip, flag
  persistence, COPY/MOVE/EXPUNGE, SEARCH and ENVELOPE/BODYSTRUCTURE, plus a POP3
  session/delete/auth suite. Parsing is defensive: a malformed message degrades
  to a usable single-part structure rather than breaking a client's sync.
- SEARCH and BODYSTRUCTURE implement a pragmatic subset; unknown SEARCH criteria
  match broadly (the client filters) rather than erroring, and parsing failures
  fall back gracefully. Further fidelity can be added incrementally.
