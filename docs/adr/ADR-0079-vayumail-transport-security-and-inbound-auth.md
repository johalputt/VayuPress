# ADR-0079: VayuMail Transport Security (TLS) + Inbound Authentication (SPF/DKIM/DMARC)

**Status**: Accepted
**Date**: 2026-06-25
**Author**: @johalputt

## Context

VayuMail (ADR-0078) delivers inbound receive (SMTP) and read (IMAP) plus
outbound direct-to-MX. Two gaps remained before it is a trustworthy mail server:

1. **Transport security.** Plaintext SMTP/IMAP exposes credentials and message
   content on the wire. Mail clients (Thunderbird, mobile) and peer MTAs expect
   STARTTLS / implicit TLS, and a missing submission service meant there was no
   standards-compliant way for an authenticated user to *send* through VayuMail.
2. **Inbound authentication.** Without SPF/DKIM/DMARC checks, the server cannot
   tell a spoofed `From:` from a legitimate one, so phishing lands in the Inbox.

Both are security-critical. The constraint is the project doctrine: single
binary, lightweight, no surprise daemons, predictable degradation.

## Decision

### 1. TLS for all mail listeners

- **STARTTLS** on SMTP `:25`, submission `:587`, and IMAP `:143`; implicit-TLS
  **IMAPS** on `:993`. A new RFC 6409 **submission** service requires STARTTLS
  before `AUTH PLAIN`/`LOGIN` (credentials delegated to VayuPress accounts via
  the mail `Bridge`) and relays only for authenticated senders.
- **Certificate:** operator-supplied (`VAYUOS_MAIL_TLS_CERT`/`KEY`, e.g. Let's
  Encrypt) when present; otherwise an **in-memory self-signed** cert generated
  for the hostname so opportunistic STARTTLS works out of the box (peer MTAs use
  opportunistic TLS and do not verify the certificate).
- **Best-effort:** a cert load or port bind failure is recorded and surfaced in
  the VayuOS health panel but **never** fails engine startup — outbound and
  local delivery stay available (consistent with the non-fatal inbound binding
  from ADR-0078).

### 2. Inbound SPF / DKIM / DMARC verification

- Each received message is authenticated **during the SMTP transaction**: SPF
  (connecting IP vs envelope sender), DKIM (signature verification), DMARC
  (policy lookup + identifier alignment with the `From` domain).
- The result is stamped as a standard **`Authentication-Results`** header. A
  DMARC failure under an enforcing policy (`p=quarantine`/`p=reject`) is flagged
  so the existing local junk filter files the message to **Junk** (annotate +
  score, not hard-reject — a misfiled legitimate message is worse than spam in
  the inbox, and avoids backscatter).
- **Best-effort:** every DNS lookup degrades to `none`/`temperror` on error and
  never blocks delivery.

### 3. New dependencies (governance: justified here per the PR checklist)

DKIM verification, DMARC evaluation, and SPF macro handling are security-
critical and error-prone to hand-roll. We adopt two small, focused, widely-used
libraries rather than reimplement RFC 6376/7208/7489 verification in-house:

- `github.com/emersion/go-msgauth` (MIT) — DKIM verification + DMARC records.
- `blitiri.com.ar/go/spf` (Apache-2.0) — SPF evaluation.

Both are permissively licensed (no GPL/AGPL), pass `govulncheck`, and are
confined to the inbound verification path. Outbound DKIM **signing** remains the
existing in-house implementation. Greylisting and DKIM-signing of submitted mail
remain future options.

## Consequences

- Positive: encrypted client/peer transport, a real submission endpoint, and
  spoof resistance — a materially more trustworthy mail server, still in one
  binary with non-fatal degradation.
- Trade-off: two new (small, vetted) dependencies on the inbound path, accepted
  here in preference to bespoke security crypto.
- Operational: receiving external mail still requires reachable ports + correct
  MX/A DNS; a CA-signed cert is recommended for verified client connections.
  Reverse DNS (PTR) and SPF/DKIM/DMARC records for the sending domain remain the
  operator's responsibility for outbound deliverability.
