# VayuMail Account Recovery

What to do when someone forgets a VayuMail password — and, more importantly, what
to set up **before** that happens.

Design rationale and the threat model live in
[ADR-0144](adr/ADR-0144-vayumail-account-recovery.md). This page is the
operational one.

---

## The one thing to do today

> **Every recovery factor is enrolled in advance, while signed in.**

Trust cannot be created at the moment it is needed. A factor that could be
established *after* access is lost would be a factor an attacker could establish
too, so VayuPress does not offer one. That makes enrolment the whole game.

For each mailbox, in **VayuOS → VayuMail → Accounts**, open the account's card
and expand **Recovery**:

1. **Generate recovery codes.** Ten single-use codes. They are shown once. Copy
   them, download the `.txt`, or print the sheet — then store them somewhere that
   is not the mailbox they recover.
2. **Add a recovery address** — an address on a *different* mail provider. It is
   verified by a round-trip before it counts.

The **Recovery** card above the account list shows, at a glance, which mailboxes
have no factor at all. That view exists because this failure is silent until the
day it matters.

### Why the recovery address must be elsewhere

It has to be off **this install**, not merely off this domain. One VayuPress
install can serve many mail domains, so a second domain on the same server would
rebuild the exact loop recovery is meant to break: the reset link would arrive in
a mailbox reached through the mailbox you have lost. VayuPress checks the address
against the set of domains the install actually serves and refuses the ones it
hosts.

---

## Recovering an account

The public entry point is **`/mail/recover`** on your domain. It is linked from
the sign-in pages as *"Forgot your mailbox password?"*.

| Situation | Path |
|-----------|------|
| A recovery address is enrolled | `/mail/recover` — a one-time link is mailed there |
| Recovery codes are in hand | `/mail/recover/code` |
| A phone still syncs the mailbox in the app | Mobile app → the app's own reset |
| None of the above | `/mail/recover/ask` — asks the administrator |
| The last administrator is locked out | `vayupress mail passwd` on the host |

Codes and the mobile app work in a **Tor Space**; the recovery address does not,
because it needs clearnet egress that a Tor Space deliberately refuses. It is
disabled and labelled there rather than failing at the moment it is needed —
which is why codes are the default rather than the fallback.

### Trusted-device recovery

If the mailbox is still syncing on a phone, the app can set a new password
without the old one. The per-device app password already proves the holder
controlled the account when the device was enrolled and that an administrator
approved it. This is the only factor that needs no planning beyond having used
the app, so it is the one most people will actually have.

It gates on the device's **approval status**, not on last-used telemetry — that
field is best-effort and never a security decision, so a missed write must not
lock out a real holder. Pending and blocked devices are refused.

### Assisted recovery (`/mail/recover/ask`)

For the holder who enrolled nothing. They file a request; it appears in the
**Recovery** card for you to approve or decline.

Approving **does not set or reveal a password.** It mints a one-time link that
you hand over through a channel you trust, and the holder chooses their own
password. An administrator who types the password knows it, and a mailbox whose
password is known by someone other than its holder is not recovered — it is
shared.

The console asks you to confirm out loud that you have verified who you are
talking to, because this is precisely the step an attacker would try to talk you
through. **Verify out of band. A convincing story is not verification.**

### Break-glass, for the last administrator

If the only administrator loses their own password and holds no codes, no flow
can help — every path depends on an enrolment that no longer exists. Run this on
the host, as the service user:

```bash
vayupress mail passwd someone@yourdomain.com
```

It grants nothing new: a shell on the box is already equivalent to the database
and the Maildir. It warns, it prints exactly what it did *not* do, and it writes
an audit record.

Two related commands:

```bash
vayupress mail recovery someone@yourdomain.com   # what is enrolled for this mailbox
vayupress mail unrecoverable                     # mailboxes with no factor at all
```

---

## What a reset actually does

Changing only the password is the classic incomplete fix: an attacker who already
holds an app password keeps their access *through* the victim's recovery, while
the victim believes they have locked the door. So every reset — self-service,
administrator, or break-glass — runs one pipeline:

1. Replaces the password hash.
2. **Revokes every app password** for the mailbox. They are independent
   credentials, not derivatives of the password. This includes the app password
   that authorised a trusted-device reset: the server cannot distinguish a
   holder's phone from a thief's, so a carve-out for "the device that asked"
   would be the carve-out an attacker uses.
3. Invalidates every member session for the address.
4. Voids every outstanding recovery token.
5. **Holds queued outbound mail** for review — sending is an attacker's first act.
6. Notifies both known channels: the recovery address, and a message filed into
   the mailbox itself.
7. Writes the audit record.

**Expect to sign in again everywhere.** Every phone that used the mailbox needs
to be re-enrolled after a reset. That is the feature working.

Three invariants worth knowing, because they shape what you will see:

- **A reset never signs you in.** Otherwise the recovery link would be a
  session-stealing link. After a successful reset you are sent to the sign-in
  page to use the new password.
- **VayuPress never confirms whether an address exists.** The response to
  `/mail/recover` is identical for a real mailbox and a typo — same wording, same
  timing. If you mistype your own address you will still see "check your inbox".
  This is deliberate: mail addresses are public, so knowing one must prove
  nothing.
- Reset links are single-use, expire in 30 minutes, are bound to one account, and
  are voided by any password change.

### The reset trail

The **Recovery** card shows an append-only trail of resets, read from the WORM
`audit_log`. It is there because the holder-facing notices are both
destructible — the recovery-address mail needs an address that may not exist, and
the in-mailbox copy can be deleted by whoever holds the new password. The audit
row cannot be. It is the record to check when the question is *"was this reset
legitimate?"*.

---

## Rate limits and lockouts

The public surface is limited per address and per source IP. A holder who fumbles
several codes in a row will be asked to wait rather than blocked permanently. A
burst of requests against one address is recorded as the attack signal it is —
requests are audited, not just completions, because recording only successes
means the attempt is never seen.

The recovery pages are **not** exempt from VayuShield. They are a public,
unauthenticated, high-value surface; that is exactly the kind of route the shield
exists for.

---

## What VayuPress will not do

- **No security questions.** Guessable, and unrevocable once answered.
- **No SMS codes.** SIM swap, plus a carrier in the trust path.
- **No WhatsApp / Signal / Telegram.** Each roots in a phone number through its
  own recovery path, so adopting one would move your mailbox's security floor to
  a SIM card. They also cannot work in a Tor Space, and they disclose to a third
  party that a given person holds a mailbox here.
- **No emailed plaintext passwords**, and no flow that reveals a new password
  without re-authentication.
- **No reset alerts over VayuTalk.** It was planned and then rejected on
  inspection: VayuTalk authenticates with the *mailbox password*, so an alert
  sent there is unreadable by the person who just lost that password and readable
  by whoever caused the reset. Reasoning in ADR-0144.

## What a reset does not touch

Nothing is lost. Your mail, your PGP keys and your aliases all survive, because
none of them is encrypted with your mailbox password:

- PGP private keys are sealed with a random server-side key, wrapped by a key
  drawn from `VAYU_SECRET` or a host-bound key file. Your password is nowhere in
  that derivation.
- In a Tor Space, mail *is* encrypted at rest — under that same server-side key,
  still not your password.

The mailbox password protects **access**, never confidentiality at rest. This is
the invariant that makes recovery safe at all, and it must hold for any future
at-rest scheme: **no recovery-relevant secret may ever be derived from the
mailbox password.**

---

## See also

- [ADR-0144](adr/ADR-0144-vayumail-account-recovery.md) — the decision, the
  threat model, and what was rejected.
- [SECURITY.md](SECURITY.md) — reporting a vulnerability.
- [TROUBLESHOOTING.md](TROUBLESHOOTING.md) — mail delivery problems.
