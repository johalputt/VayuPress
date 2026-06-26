# ADR-0085: VayuMail Optional Outbound Smarthost Relay

**Status**: Accepted
**Date**: 2026-06-25
**Author**: @johalputt

## Context

VayuMail delivers outbound mail sovereignly — direct-to-MX from the host's own
IP (ADR-0077). ADR-0084 made the message itself maximally correct (vetted DKIM
signing, well-formed MIME). However, inbox placement at large providers
(Gmail, Outlook) is dominated by **sending-IP reputation**, which is outside the
binary's control:

- A brand-new self-hosted IP (e.g. a fresh VPS) has no sending history and is
  treated with suspicion regardless of perfect SPF/DKIM/DMARC and PTR.
- Building reputation requires a slow warm-up at steady volume; until then,
  legitimate mail can be spam-filed.

Operators need a pragmatic escape hatch that does not abandon the project's
sovereignty doctrine.

## Decision

Add an **optional** authenticated outbound smarthost relay, off by default.

- **Activation is opt-in and environment-driven.** Setting
  `VAYUOS_MAIL_RELAY_HOST` switches the outbound queue's transport from
  direct-to-MX to the relay. With no relay configured, behaviour is unchanged
  (sovereign direct-to-MX remains the default).
- **What stays sovereign.** Inbound SMTP receive, IMAP read, local delivery
  (loopback to Maildir), Maildir storage, and **DKIM signing with the domain
  key** are all unchanged. Because VayuMail still signs every message before it
  is queued, **DMARC alignment via DKIM is preserved end-to-end** even when a
  third party performs the final hop. The domain, keys and mailboxes remain
  self-owned.
- **Transport security.** STARTTLS submission (`:587`) and implicit TLS
  (`:465`) are supported. An encrypted channel is **required before AUTH/DATA by
  default** (`VAYUOS_MAIL_RELAY_TLS=off` opts out only for a trusted relay on a
  private network). `AUTH PLAIN` is preferred, with the widely-deployed `LOGIN`
  mechanism as a fallback; both refuse to transmit credentials over an
  unencrypted, non-local connection.
- **Credentials are never persisted.** Relay host/port/username/password are
  read from the environment at boot, consistent with how other secrets are
  handled — VayuMail does not write them to disk or the database.
- **Single transaction.** All recipients of a queued message are submitted in
  one relay session (relays accept any destination domain), reusing the existing
  durable SQLite retry queue for backoff and failure accounting.

## Consequences

- Positive: a one-line configuration restores deliverability for operators on
  reputation-poor IPs, without giving up inbound, storage, identity or DKIM
  signing. The relay sees only what any SMTP hop sees (an already-signed
  message); no additional data is surrendered.
- Trade-off: when enabled, the relay provider observes outbound message metadata
  and content in transit (inherent to any smarthost). This is an explicit,
  opt-in operator choice, clearly documented, and disabled by default.
- Operational: the deliverability self-check reports when a relay is active and
  notes that the relay's IP reputation — not the local host's PTR/FQDN — then
  governs outbound placement. Direct-to-MX sovereignty is one unset environment
  variable away at any time.
