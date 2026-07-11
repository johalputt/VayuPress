# ADR-0128: VayuMail Private-Key Sync for On-Device Decryption

**Status**: Accepted  
**Date**: 2026-07-11  
**Author**: @johalputt  
**Relates to**: [ADR-0126](ADR-0126-vayumail-app-password-console.md) (device app
passwords, the credential this endpoint authenticates with),
[ADR-0125](ADR-0125-vayumail-per-account-signatures.md) (own-mailbox self-service
boundary), the VayuPGP engine (`internal/vayuos/pgp`), the VayuPortal backend
(`cmd/vayupress/handlers_portal.go`)

## Context

VayuPress generates and stores a PGP keypair for every mailbox server-side: the
private half is armored and held as AES-256-GCM ciphertext at rest, decryptable
by the server from the master secret. This is what lets VayuPress transparently
decrypt inbound PGP mail when it serves it over IMAP to the owner (the decrypt
hook in `vayuos.go`).

The VayuMail-Mobile app onboards by **email + app password**, auto-discovers
server coordinates from `/.well-known/vayumail/autoconfig.json`, and auto-syncs
**public** keys via WKD (`/.well-known/openpgpkey/`). But WKD, by design, serves
only public keys. So the app can *encrypt* to a recipient and *verify* a
signature, but it **cannot decrypt received PGP mail** — it holds no private
key. The only key material the server has never handed the owner is the one the
owner needs to read their own encrypted mail on-device.

The gap: a mailbox holder can receive encrypted mail but, in the mobile app,
sees ciphertext they cannot open, even though the server already holds (and can
already decrypt with) their private key.

## Decision

Add one authenticated endpoint that returns the **caller's own** mailbox PGP
private key, armored, so the app can import it and decrypt on-device.

### Endpoint contract

`POST /api/v1/members/vayumail-privkey`

- Request JSON: `{"email": "...", "password": "..."}` — `password` is the
  mailbox password **or** a device app password (ADR-0126).
- On success (`200`): `{"email": "...", "armored_private_key":
  "-----BEGIN PGP PRIVATE KEY BLOCK-----\n..."}`, `Content-Type:
  application/json`, `Cache-Control: no-store`.
- On any auth failure (`401`): a uniform `invalid-credentials` error — identical
  status and body for an unknown mailbox and a wrong password.
- If the mailbox has no keypair yet (an account pre-dating auto-keygen), the
  handler calls `EnsureKeypair` (idempotent) first, so a key always exists to
  return.

### Authentication — reuse, don't reinvent

The handler authenticates through the **same path as `vayumail-login`**: the
shared `mailAuthThrottle` (a decaying per-mailbox delay applied before every
attempt, cleared on success) wrapping the canonical `verifyCredential` check —
CMS account password → mail-account Argon2id hash → device app passwords, with
the optional `VAYUMAIL_2FA_ENFORCE` policy honoured. No new credential logic and
no new storage were introduced.

A new thin engine accessor, `(*pgp.Engine).ArmoredPrivateKey(email)`, resolves
the mailbox's userID from the email index and returns the decrypted armored
private half. It reuses the existing `keyStore.load` path that VayuPGP already
uses to decrypt inbound mail — no new decryption route into the keystore.

## Security posture

- **Consistent with VayuPGP's existing trust model.** The server already stores
  every mailbox's private key and can already decrypt everything the mailbox
  receives (at rest and when serving IMAP). Returning the owner their own key
  over TLS to an authenticated caller reveals nothing the server did not already
  hold on the owner's behalf. This is a sync of *the owner's own* key, not an
  escalation.
- **Authenticated with the mailbox credential.** Only a caller who can prove the
  mailbox password (or a valid device app password, itself minted behind 2FA
  where enabled) receives the key.
- **Online guessing throttled.** Verification runs inside the shared
  `mailAuthThrottle`, the same brute-force defence as IMAP/SMTP/POP3, so failed
  attempts accrue a growing per-mailbox delay.
- **No enumeration.** Any failure returns a single generic `401` with an
  identical body whether the address exists or not, so the endpoint cannot be
  used to discover which mailboxes exist.
- **Audit-logged.** Every successful retrieval writes
  `vayumail.privkey.fetch` (actor + mailbox), so key exports are attributable.
- **No-store.** The response sets `Cache-Control: no-store` so no proxy or client
  cache retains the key, and it is served only over the site's TLS.

## Consequences

- VayuMail-Mobile can decrypt received PGP mail entirely on-device after a
  one-time key import, closing the read-side gap left by public-only WKD.
- No schema change and no new keystore decryption path: the ADR-0126 credential
  check, the `mailAuthThrottle`, and the existing `keyStore.load` are reused
  as-is; only the HTTP handler and a one-method engine accessor are new.
- The endpoint is reachable from a registered public route (satisfying the
  deadcode gate) and does not touch the byte-pinned autoconfig contract.

## Alternatives considered

- **Manual key paste.** Let the operator export a mailbox's private key from the
  VayuPGP console and have the user paste it into the app. Rejected: it is a
  manual, error-prone, admin-mediated step for what should be automatic
  onboarding, it has no per-mailbox self-service story, and copying an armored
  private key through clipboards and chat apps is *less* safe than a throttled,
  audited, no-store TLS fetch authenticated by the mailbox credential.
- **Serving the private key over WKD.** Rejected outright — WKD is a public
  discovery mechanism; private keys must never be published there.
- **Client-side keygen (app generates its own keypair).** Rejected: it would
  diverge the app's key from the server-held key that already decrypts mail at
  rest and is published via WKD, breaking the single-identity model and the
  transparent-decrypt IMAP hook.
