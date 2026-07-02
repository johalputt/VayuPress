# ADR-0106: VayuMail Clean Namespace, Rotating Setup QR & Device App Passwords

**Status**: Accepted
**Date**: 2026-07-02
**Author**: @johalputt
**Owner**: Core
**Relates to**: [ADR-0096](ADR-0096-vayumail-imap-pop3-clients.md), [ADR-0083](ADR-0083-vayumail-roles-search.md), [ADR-0098](ADR-0098-role-scoped-vayuos-access.md)

## Context

Two rough edges in VayuMail undermined an otherwise strong mail stack:

1. **Inconsistent URLs.** Every other VayuOS surface lives at `/os/<area>`
   (`/os/posts`, `/os/media`, `/os/theme`). Mail alone lived under a doubled
   `/os/vayuos/mail/...` prefix — a leftover from the original VayuOS grouping —
   which read as a bug and made the mail area feel bolted on.
2. **Device setup was either fiddly or over-sharing.** Connecting a phone meant
   typing the mailbox's real password into the device, and the existing
   convenience QR either carried no credential (so the user still typed the
   password) or, if it did, a photographed QR would remain valid forever. There
   was no per-device credential and no way to revoke one device without
   re-passwording the mailbox.

## Decision

### Clean `/os/vayumail` namespace

Move the entire mail panel to `/os/vayumail/*` (inbox, compose, accounts,
connect, sent, pgp, security; DKIM/DNS at `/os/vayumail/dns`). Every legacy
`/os/vayuos/*` URL **308-redirects** (method-preserving, query kept) to the new
namespace, so bookmarks, old clients and in-flight POSTs keep working. RBAC and
the mail-only confinement rules are updated to the new prefix.

### Device app passwords

Add `vayumail_app_passwords` (id, email, label, Argon2id hash, created_at). An
app password is a **device-scoped credential** for IMAP/POP3/SMTP: generated
server-side, stored only as a hash, and verified in the auth bridge *after* the
mailbox's main password (so the main password stays the fast path). App
passwords are revocable individually, and by label for rotation.

### Rotating setup QR

The Connect tab mints a setup QR per mailbox. One tap generates a fresh app
password (labelled `setup-qr`), shows it **exactly once** as a scannable QR
carrying username + server settings + the credential, and revokes any previous
`setup-qr` credential for that mailbox in the same action. Consequences:

- A phone signs in from a single scan **without ever seeing the mailbox's main
  password**.
- **Rotating kills the old QR instantly** — a photographed or leaked QR goes
  stale the moment a new one is minted. This is the property a fixed QR can
  never have, and the reason the QR is dynamic rather than static.
- Rotate/revoke are audit-logged and never touch the main password.

### CSRF for no-JS forms

The setup-QR actions are plain server-rendered `<form>` POSTs. `CSRFTokenMiddleware`
now also accepts the token as a `csrf_token` form field (form-encoded bodies
only) in addition to the `X-CSRF-Token` header, applying the identical
double-submit + HMAC validation. This lets security-sensitive forms work
without JavaScript while keeping the CSP strict and the CSRF guarantees intact.

### PGP unchanged and automatic

None of this alters the privacy posture: keys are still auto-generated per
mailbox, person-to-person mail auto-encrypts when the recipient key is known,
and IMAP decrypts transparently for the owner (ADR-0076/0077).

## Consequences

- Mail is a first-class, consistently-addressed VayuOS surface; nothing breaks
  because every old URL redirects.
- Connecting a device is a one-scan operation that never exposes the real
  password and is revocable per device — a meaningful security upgrade over
  typing the mailbox password into every client.
- A leaked or photographed setup QR is defused by rotation, so the QR can be
  shown on screen or shared briefly without becoming a permanent credential.
- The `csrf_token` form-field path is a small, general capability that other
  no-JS admin forms can now use safely.
