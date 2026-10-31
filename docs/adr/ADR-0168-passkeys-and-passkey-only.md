# ADR-0168 — Passkeys, and an account that signs in with nothing else

- **Status:** Accepted
- **Date:** 2026-10-31
- **Code:** `internal/passkey`, `cmd/vayupress/passkey.go`, `static/js/passkey.js`; migrations 126 and 127.
- **Dependency:** `github.com/go-webauthn/webauthn` (BSD-3-Clause), named in NOTICE.

## 0. The question

A password can be guessed or phished, and ADR-0167 only makes guessing
expensive. During an attack on an address its holder may also find
sign-ins to it paused, and needs a way in that the attacker cannot share.

## 1. Decision

**An account adds a passkey on My profile, and a mailbox's holder on Mailbox
settings.** "Sign in with a passkey" at `/os/login` then signs in with the
device's own lock, with no name or password typed. The registration and
sign-in signatures are checked by `go-webauthn`, not by code of our own:
WebAuthn's attestation and assertion formats are a specification, and a
maintained implementation of it is safer than a second one.

**A passkey signs in only as the account or mailbox that added it.** It
cannot be guessed or phished, so it still works while sign-ins to the
address are paused; blocks and the country fence (ADR-0167) still refuse it.
A passkey whose signature counter went back, a copied authenticator, is
refused. The browser is never told which addresses have passkeys.

**Passkey only.** An account or mailbox with two passkeys (one to use, one
spare) can turn its password off: its own password is then refused at every
door, while its app passwords and passkeys keep working. Removing passkeys
below two turns the password back on, so one lost device never shuts anyone
out, and an administrator can turn it back on from Shield › Sign-in
attempts, which the install's key always opens. It belongs to the account
that turned it on: a mailbox at the same address keeps its own password.

## 2. Consequences

- Mail apps keep working through app passwords; IMAP and SMTP have no
  passkey sign-in to offer.
- The rule of two is what makes passkey-only safe to offer to everyone, and
  it is enforced where passkeys are removed, not only where the switch is.
