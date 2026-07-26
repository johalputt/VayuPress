# ADR-0144 — VayuMail Account Recovery

Status: Accepted (Phase 1)
Date: 2026-07-26
Deciders: VayuPress core

## Context

A VayuMail holder who forgets their mailbox password today is **permanently
locked out**, and the lockout is total rather than partial. There are three ways
into a mailbox and the forgotten password closes all of them:

| Surface | Credential | After the password is lost |
|---------|------------|----------------------------|
| IMAP / POP3 / SMTP | `vayumail_accounts.password_hash` | dead |
| Mobile app | per-device app password | survives only while a device stays enrolled |
| Webmail | member session — a **magic link emailed to the holder** | dead |

The webmail row is the trap, and it is worth stating precisely because it is not
obvious from any single file. A mailbox holder reaches webmail through the
membership portal, and the portal authenticates by mailing a one-time link
(`CreateLoginToken` → `sendMemberMagicLink`). For a VayuMail holder **that
address is a mailbox on this same install**. Reading the link requires webmail,
which requires the link. The loop never opens.

So the only remaining path is `AccountStore.SetPasswordHash`, which is reachable
only from an authenticated `/os` console session. A `mailbox`-role user — the
least-privileged, most numerous kind of account — cannot reach it at all. Every
recovery today is a human asking an administrator.

### What makes recovery tractable

A password reset is a **pure authentication event**. It destroys nothing:

- PGP private keys are sealed with a random server-side **DEK**, wrapped by a KEK
  drawn from `VAYU_SECRET` or a host-bound key file (ADR: envelope encryption in
  `internal/vayuos/pgp/keyenvelope.go`). The holder's password is nowhere in that
  derivation.
- The Maildir is plaintext on disk. There is no cipher anywhere in
  `internal/vayuos/mail/`.

Therefore no escrow scheme, no key rewrap and no data loss are involved. **If
mailbox-at-rest encryption is ever introduced, this ADR must be revisited** —
under that design a password reset would silently destroy mail, and every
decision below would change.

## Threat model

A mailbox is the **master key to every other account its owner holds** — the
reset channel for their bank, their registrar, their VPS. A mailbox recovery flow
is therefore held to a higher bar than the login it bypasses, because it is a
second front door with weaker locks by construction.

In scope:

1. A remote attacker who knows the address. Addresses are public by definition,
   so knowing one proves nothing and must grant nothing.
2. An attacker holding a **stale credential** — an old app password, a retired
   device — who wants to survive the victim's own recovery.
3. Brief physical access to an unlocked, signed-in device.
4. **Enumeration.** "Does `ceo@example.com` exist on this server" must not be
   answerable, by content or by timing.

Out of scope, deliberately: an administrator, or anyone with a shell on the host.
They already hold the DEK and the Maildir. Recovery must not *widen* that
boundary, but defending against it here would be theatre.

## Decision

### The governing rule

> **Every recovery factor is enrolled in advance, while authenticated, over a
> channel that is not the mailbox itself.**

Trust cannot be bootstrapped at recovery time. Everything below follows.

### Factors

**Recovery codes — the default.** Ten single-use codes, displayed exactly once,
stored Argon2id-hashed. No network, no third party, no outbound queue: they work
in a Tor Space and they work when the mail plane itself is the thing that broke.

**Recovery address — the common path.** An external address, verified by a
round-trip before it counts, and required to be **off this install**. Not merely
"a different domain": one VayuPress install serves *many* mail domains from
domain-partitioned Maildir trees, so a same-install second domain would rebuild
the loop exactly. The check is against the set of domains this install actually
serves.

**TOTP — a gate, not a factor.** Already present (`totp_secret`,
`totp_enabled`). It proves possession, not address control, so it never recovers
an account on its own; where it is enrolled it is *additionally* required, which
makes the address path genuinely two-factor.

### Rejected

Security questions (guessable, and unrevocable once answered). SMS (SIM swap, and
a third party in the trust path). Emailing a plaintext password. Any flow that
reveals a new password without re-authentication.

Third-party messengers (WhatsApp / Signal / Telegram) were evaluated and
rejected as recovery factors: each ultimately roots in a phone number through its
own account-recovery path, so adopting one would move the mailbox's security
floor to a SIM card. They also cannot function in a Tor Space, and they disclose
to a third party that a given person holds a mailbox here.

### The reset pipeline

Every reset — self-service, administrator, or break-glass — runs one path. A
reset that only changes the password is the classic incomplete fix: the attacker
who already holds an app password keeps their access *through* the victim's
recovery, and the victim believes they have locked the door.

1. Replace the password hash.
2. **Revoke every app password** for the mailbox. They are independent
   credentials, not derivatives of the password.
3. Invalidate every member session for the address.
4. Invalidate every outstanding recovery token for the address.
5. **Hold queued outbound mail** for review — sending is an attacker's first act.
6. Notify every known channel: the recovery address *and* a message filed into
   the mailbox itself.
7. Audit the **request** as well as the completion. A burst of requests against
   one address is the attack signal; recording only successes means the attempt
   is never seen.

Three invariants:

- **A reset never mints a session.** Otherwise the recovery link becomes a
  session-stealing link.
- **Never confirm existence.** Identical response and timing for known and
  unknown addresses.
- Tokens are 32 random bytes, stored SHA-256-hashed, 30-minute TTL, single-use
  with an atomic consume, bound to one account, and voided by any password change.

Single-use is enforced by the `RowsAffected() == 1` pattern already proven in
`members.ConsumeLoginToken`: on the single-writer SQLite, two concurrent
redemptions of one captured token both read the row, but only one `DELETE`
affects it, so the loser is rejected rather than racing into a second reset.

### Tor mode

The recovery address needs clearnet egress, and `safefetch.ClearnetBlocked()`
refuses the external relay in a Tor Space. The address factor is therefore
**disabled and labelled** there rather than failing silently at the moment it is
needed. Recovery codes remain fully functional, which is why they are the
default rather than the fallback.

### The last administrator

If the only administrator loses their own mailbox password and holds no codes, no
flow can help: every path depends on an enrolment that no longer exists. The
honest answer is a documented break-glass — `vayupress mail passwd <address>`,
run on the host as the service user. It grants nothing new, since a shell is
already equivalent to the DEK and the Maildir, and it follows the existing
`vayupress user passwd` precedent. It warns, and it writes an audit record.

## Consequences

**Good.** No mailbox holder is unrecoverable. The administrator stops being a
human password-reset endpoint. The reset pipeline closes the stale-app-password
hole that exists today even for administrator-driven resets.

**Cost.** Two new tables and a public unauthenticated surface — the highest-value
attack target in the product — which is why enumeration-safety, rate limiting and
audit-on-request are requirements rather than refinements.

**Accepted risk.** A holder who enrols nothing is still administrator-dependent.
This is inherent to the governing rule, not a gap in it: a factor that could be
established *after* access is lost would be a factor an attacker could establish
too. The mitigation is visibility — a recovery-readiness view naming the
mailboxes that have no factor at all, because the failure is silent until the day
it matters.

## Phases

- **Phase 1** — schema and store; recovery codes; verified off-install recovery
  address; the full reset pipeline; the enumeration-safe public flow; console
  enrolment UI and the readiness view; CLI break-glass.
- **Phase 2** — trusted-device reset from an enrolled mobile app; an
  administrator request/approve queue replacing today's silent reset; delivery of
  reset alerts over VayuTalk, which is self-hosted and survives Tor mode.
