# ADR-0129: VayuMail Device Approval — No Mail Sync Without Web Approval

**Status**: Accepted  
**Date**: 2026-07-11  
**Author**: @johalputt  
**Relates to**: [ADR-0126](ADR-0126-vayumail-app-password-console.md) (app
passwords, the credential rows device identities build on),
[ADR-0128](ADR-0128-vayumail-private-key-sync.md) (the private-key sync
endpoint this ADR hardens), the mail auth bridge (`cmd/vayupress/vayuos.go`),
the VayuPortal backend (`cmd/vayupress/handlers_portal.go`)

## Context

IMAP, POP3 and SMTP submission authenticate with a single static credential.
They cannot prompt for a second factor, show a push notification, or run any
interactive challenge — the protocol handshake is `LOGIN user pass` and that
is the whole conversation. So even with TOTP 2FA enabled on a mailbox and on
the web console, a **stolen mailbox password gave full mail access from any
device on earth**: an attacker points any IMAP client at the server, types the
password, and the entire mailbox syncs.

App passwords (ADR-0126) reduced the blast radius of *device* compromise — a
per-device credential is individually revocable — but minting one happens in
the console, and the raw mailbox password itself still authenticated the mail
protocols. The credential the attacker is most likely to hold (the password,
via phishing, reuse or a leak) remained sufficient to sync everything.

The operator wants the bulletproof model: **a new device must be explicitly
approved from the 2FA-protected VayuPress web console before any mail syncs
to it**, and a password alone must never be enough.

## Decision

Make the credential itself the enforcement point, since the mail protocols
offer nothing else — the same insight as the 2FA-enforce policy, now applied
per device with an explicit approval lifecycle.

### Device identities on top of app passwords

`vayumail_app_passwords` gains four idempotently migrated columns:
`device_id` (random 32-hex identity, unique when set), `platform`, `status`
(`pending` | `approved` | `blocked`, default `'approved'`), and `last_used_at`.
A **device** is an app-password row with a device identity. Existing rows keep
`status='approved'` by the migration default — they were minted from the
2FA-protected console, so grandfathering them is safe.

`vayumail_accounts` gains `require_device_approval INTEGER NOT NULL DEFAULT 1`
with a getter/setter on the `AccountStore` and a per-mailbox toggle in the
console. The secure default is ON; an identity without an account row (a
CMS-only user) is treated as ON too.

### Registration and status — the member API

- `POST /api/v1/members/vayumail-device-register`
  `{email, password, device_name, platform}` — authenticates with the raw
  mailbox credential (web-bootstrap scope, see below), then creates a device
  row: fresh `device_id`, a freshly generated device password (same
  generator/format as app passwords, stored only as an Argon2id hash),
  `status='pending'` — or `'approved'` immediately when the mailbox has
  approval switched off. Responds `{device_id, device_password, status}` with
  `Cache-Control: no-store`; the secret is shown exactly once. Audit:
  `vayumail.device.register`.
- `POST /api/v1/members/vayumail-device-status`
  `{email, device_id, device_password}` — the device proves possession of its
  OWN credential (verified against that one row's hash) and learns its state,
  so the app can tell the user to go approve it — and start syncing the moment
  it flips. Bumps `last_used_at`.

Both reuse the shared `mailAuthThrottle` and return a **uniform 401** on any
failure — byte-identical for an unknown mailbox, an unknown device and a wrong
secret — so neither endpoint can enumerate addresses or devices.

### Enforcement — two credential scopes at the single chokepoint

`verifyCredential` (the one place every mail login already flowed through) is
split into two explicit scopes over a shared core:

- **Mail-sync scope** (`verifyCredential`): IMAP/POP3/submission via
  `AuthUser`, **and the private-key sync endpoint** (ADR-0128), because the
  private key decrypts the same mail those protocols serve. When the mailbox
  requires device approval, the raw CMS/mailbox password is rejected here;
  a credential matching a device row authenticates only if that row is
  `approved` (bumping `last_used_at`). `pending` and `blocked` rows fail
  uniformly.
- **Web-bootstrap scope** (`verifyCredentialWeb`): the member HTTP endpoints
  (`vayumail-login`, `vayumail-device-register`) keep accepting the raw
  password — a new device needs it exactly once, to register; a member needs
  it to sign into the portal (where TOTP is enforced separately). Throttling
  is identical in both scopes.

### Console — the approval anchor

A **Devices** card on the admin Accounts page lists every registered device
(mailbox, label, platform, status chip, registered, last used) with
Approve / Block / Remove actions (`POST /os/vayumail/devices/action`,
CSRF-protected, admin-only, audited as `vayumail.device.approve` /
`.block` / `.remove` / `.require`), plus the per-mailbox
"Require device approval" toggle. Pending devices sort first and wear a
prominent `badge--pending` chip (styled for both themes). Approval is
deliberately **admin-only**: the session behind the console is protected by
interactive 2FA, which is precisely the factor IMAP cannot carry — a mailbox
holder must not be able to approve their own device with the same password
the attacker may hold.

## Threat model

- **Stolen mailbox password, attacker's device**: the password no longer
  authenticates IMAP/POP3/submission or the private-key endpoint. The
  attacker can register a device — which sits `pending`, syncs nothing, and
  announces itself on the Accounts page (an intrusion signal, audited with a
  register entry). Approval requires the 2FA-protected console session the
  attacker does not have.
- **Stolen device credential**: revocable individually (Block/Remove) without
  touching the mailbox password; blocking takes effect on the next auth.
- **Enumeration**: register/status failures are byte-identical 401s; the
  register response for a *valid* password necessarily differs (that is the
  authenticated path), matching the `totp-required` precedent.
- **Lockout**: the password always works for member login and device
  registration, and the operator can toggle enforcement off per mailbox, so
  no one is ever locked out of *creating* a path back in; only unattended
  sync is gated.

## Consequences

- New devices (including any generic IMAP client using a freshly registered
  device credential) do not sync until approved — this is the point, and it
  is one console click.
- Mailboxes that relied on raw-password IMAP sign-in must either register +
  approve a device (or mint an app password in the console) or have
  `require_device_approval` switched off — the toggle exists exactly for
  operators who prefer the old model.
- Existing app passwords keep working unchanged (`approved` by migration), so
  already-connected devices survive the upgrade without interruption.
- The private-key endpoint (ADR-0128) is strictly hardened: with approval
  required, only an approved device credential unlocks the key.
