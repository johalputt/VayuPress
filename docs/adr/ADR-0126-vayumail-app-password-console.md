# ADR-0126: App-Password Console for VayuMail Mobile Direct Connect

**Status**: Accepted  
**Date**: 2026-07-09  
**Author**: @johalputt  
**Relates to**: [ADR-0106](ADR-0106-vayumail-namespace-rotating-qr.md) (device app
passwords, whose QR-based minting was removed in v3.9.16),
[ADR-0108](ADR-0108-vayumail-autoconfig-and-discovery-hardening.md) (autoconfig
discovery), the VayuMail engine (`internal/vayuos/mail`), the mail console
(`cmd/vayupress/vayuos_mail.go`)

## Context

The VayuMail-Mobile redesign connects by **email + app password**: the app asks
for an address and a device credential, auto-discovers server settings from
`/.well-known/vayumail/autoconfig.json`, and auto-syncs PGP keys via WKD.

But when the rotating device-setup QR was removed in v3.9.16 (its Rotate button
was broken, and the QR was the only thing minting app passwords), the *entire*
creation path for app passwords went with it. The storage layer
(`vayumail_app_passwords`, ADR-0106) and the verification path in the mail auth
bridge survived intact — yet no HTTP handler could create a credential anymore.
Two consequences:

1. **No self-service device credential.** Connecting a phone meant handing it
   the mailbox's main password.
2. **`VAYUMAIL_2FA_ENFORCE` was uncreatable.** The opt-in "app-password only"
   mode requires at least one app password to exist before it retires the main
   password on IMAP/SMTP/POP3 — and nothing could create one.

## Decision

### 1. First-class app-password management on the Connect tab

The Connect tab gains an **App passwords** card (create form + per-account list
with revoke), served by two new session routes in the same auth + CSRF chain as
the existing account-management POSTs:

- `POST /os/vayumail/accounts/apppassword` — mint a credential for a mailbox
  (form: account email + optional label, default `VayuMail Mobile`).
- `POST /os/vayumail/accounts/apppassword/delete` — revoke one credential by id,
  scoped to its mailbox.

Both swap the card in place (HTMX), the same pattern as the alias / autoreply /
filter cards. The list shows **label + created date only** — hashes never leave
the store. The official-app card now describes the real flow: install the app →
enter your email + an app password → settings auto-discover via
`/.well-known/vayumail/autoconfig.json` and PGP keys auto-sync via WKD.

### 2. Secret format — 20 alphanumerics, displayed dash-grouped, stored dashless

The secret is 20 characters drawn from a 62-character alphanumeric alphabet with
`crypto/rand` (rejection-sampled, no modulo bias; ~119 bits of entropy). It is
displayed **once**, grouped in 4-character blocks (`abcd-efgh-ijkl-mnop-qrst`)
for readability. The dashes are pure presentation: the Argon2id hash is computed
over the dashless form, and the auth bridge strips dashes before verifying in
its app-password branch, so the credential signs in pasted with or without them.
Because the alphabet contains no dashes, stripping can never eat a real secret
character.

### 3. Authorisation — admin for any mailbox, holders for their own

An administrator may create/revoke app passwords for any mailbox. A non-admin
session with an assigned mailbox may manage **only its own** — the same narrow
self-service boundary as per-account signatures (ADR-0125). A holder minting a
credential for their own mailbox gains no access they do not already have, and
the console session that mints it has itself already passed 2FA where enabled.
A guard refuses to mint for an address with no active mail account, since the
auth bridge accepts any address holding a matching app-password hash.

## Security posture

- **Argon2id at rest.** Only the hash (`internal/auth.HashSecretArgon2id`) is
  stored; the plaintext exists solely in the single creation response, which is
  marked "shown only once".
- **Revocable per device.** Each credential is deleted individually without
  touching the mailbox password; revocation takes effect on the next login.
- **Online guessing throttled.** Verification happens inside the existing
  `mailAuthThrottle` path on IMAP/SMTP/POP3, so failed attempts accrue a
  decaying per-account delay.
- **Creation is authenticated + CSRF-protected** (session route in the
  `CSRFTokenMiddleware` chain), audit-logged
  (`vayumail.apppassword.create` / `.revoke`), and capped at 20 live
  credentials per mailbox — the same LIMIT the auth path reads back, so every
  stored secret is guaranteed to authenticate.

## Consequences

- Connecting VayuMail-Mobile is a 30-second self-service job again, and
  `VAYUMAIL_2FA_ENFORCE` is usable: an operator (or the holder) mints an app
  password, then enforcement can retire the main password on the protocols.
- No schema change: the ADR-0106 table, store functions, and bridge
  verification are reused as-is; only the console surface and the dash
  normalisation in the bridge are new.
- The one-time reveal means a lost secret is unrecoverable by design — the
  card's remedy is revoke-and-recreate.

## Alternatives considered

- **Reinstating the rotating setup QR.** Rejected — v3.9.16 removed it
  deliberately (broken rotate UX, photographed-QR credential concerns), and the
  app's onboarding is now email-first; a QR would reintroduce the machinery the
  removal simplified away.
- **Returning the secret via JSON + page JavaScript** (the API-keys-console
  pattern). Rejected for the mail console: the HTMX card swap renders the
  server-built reveal exactly once with no extra page script, matching how the
  rest of the VayuMail console mutates state.
- **Admin-only minting** (the strict TOTP-route model). Rejected: the Connect
  tab is exactly the page every mailbox holder uses to set up their phone, and
  own-mailbox minting grants nothing the holder's password does not already.
- **Storing the dash-grouped form.** Rejected: two spellings of one secret
  would either double the hash work or make pasted-with-dashes fail; a dashless
  canonical form plus display-time grouping keeps verification single-pass.
