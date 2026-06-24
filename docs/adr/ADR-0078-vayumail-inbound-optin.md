# ADR-0078: VayuMail Inbound — Opt-In SMTP/IMAP Daemon

**Status**: Accepted  
**Date**: 2026-06-24  
**Author**: @johalputt

## Context

"Stable private email" (v1.9.0) requires receiving and reading mail. A listening
SMTP (port 25) and IMAP (port 993/143) server is, by definition, a long-running
daemon — exactly the kind of always-on listener the Operational Simplicity
Doctrine treats with caution.

## Decision

1. Ship inbound as a **pure-Go SMTP-receive server** (RFC 5321 subset, no open
   relay — only local-domain recipients) and a **minimal IMAP read server**
   (RFC 3501 subset: LOGIN/SELECT/LIST/FETCH/STORE \Seen), with auth delegated
   to VayuPress accounts via the mail `Bridge`.
2. Both listeners are **strictly opt-in** via `VAYUOS_MAIL_INBOUND=on`. When
   disabled (default) no mail port is opened and the binary boots unchanged.
3. Messages are stored in Maildir; the IMAP path transparently **PGP-decrypts**
   for the owning account (best-effort, never blocks delivery).

## Consequences

- Positive: full self-hosted mailbox while honouring "no surprise daemons" —
  the operator consciously enables the listener.
- Follow-up: inbound SPF/DKIM/DMARC verification, greylisting, and IMAPS/TLS
  hardening are tracked for a subsequent milestone (`docs/ROADMAP-v1.9.md`).
