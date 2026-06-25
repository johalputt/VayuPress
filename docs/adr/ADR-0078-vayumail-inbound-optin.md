# ADR-0078: VayuMail Inbound — SMTP/IMAP Daemon

**Status**: Accepted (amended — see "Update" below)  
**Date**: 2026-06-24 (amended 2026-06-25)  
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

## Update (2026-06-25): inbound enabled by default

Decision (2) is amended. In practice the strict opt-in meant a freshly
configured domain silently could not **receive** mail — operators reasonably
expected a configured mail domain to accept incoming messages, and the hidden
`VAYUOS_MAIL_INBOUND=on` step was a frequent "incoming mail not working"
surprise.

Inbound is now **on by default** once `DOMAIN` is set, and is disabled with
`VAYUOS_MAIL_INBOUND=off` (the toggle is inverted, not removed). To keep the
"no daemon should be able to break the binary" guarantee, **binding the mail
ports is best-effort**: if `:25`/`:143` cannot be opened (e.g. insufficient
privileges, or a port already in use) the engine records the reason
(`Engine.InboundError`), surfaces it in the VayuOS health panel, and continues
serving outbound delivery and local loopback delivery. A failed listener never
fails engine startup. This preserves the spirit of the Operational Simplicity
Doctrine (predictable, non-fatal, observable) while making a configured mail
domain actually receive mail out of the box.
