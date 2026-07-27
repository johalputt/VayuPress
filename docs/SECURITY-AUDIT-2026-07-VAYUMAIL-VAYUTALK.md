# Security audit — VayuMail and VayuTalk

**Date:** 2026-07 · **Scope:** `internal/vayuos/mail`, `internal/vayuos/vayutalk`, and
their HTTP surfaces in `cmd/vayupress`. **Method:** adversarial source review of the
authentication, authorization, protocol-state, path-handling and resource-limit
boundaries, working from what an attacker can reach unauthenticated first.

Every finding below is **fixed** in the same change as this document, with a regression
test that encodes the attack. Findings are ordered by what an attacker gets, not by how
hard they were to spot.

---

## Summary

| ID | Severity | Area | Finding | Status |
|---|---|---|---|---|
| M-1 | **High** | Inbound auth | DMARC bypass via duplicate / multi-address `From` | Fixed |
| M-2 | **High** | Inbound auth | Forged `Authentication-Results` and `X-VayuMail-*` accepted | Fixed |
| M-3 | **High** | Brute force | Auth throttle defeated by opening more connections | Fixed |
| M-4 | **High** | DoS | Unbounded concurrent connections on SMTP/IMAP/POP3 | Fixed |
| M-5 | Medium | DoS | Accept loop spins at 100% CPU on descriptor exhaustion | Fixed |
| M-6 | Medium | SMTP state | `DATA` reachable without an accepted `MAIL FROM` | Fixed |
| T-1 | **High** | VayuTalk | One ordinary account can sign every user out | Fixed |

**What was checked and found sound** is recorded at the end — it is as much a part of an
audit as the defects, and several of these were the first places worth attacking.

---

## M-1 — DMARC bypass via `From` header abuse

**Where:** `internal/vayuos/mail/authverify.go`, `headerFromDomain`

**What it was.** DMARC was keyed on `msg.Header.Get("From")` parsed with
`netmail.ParseAddress`. Both halves are exploitable, and they are the two shapes real
phishing uses:

*Two `From` headers.* `Header.Get` returns the **first**. Many mail clients render the
**last**. So:

```text
From: attacker@evil.example      ← what VayuMail evaluated (passes its own DMARC)
From: ceo@bank.example           ← what the reader saw
```

Alignment passed against `evil.example`, the message was delivered unflagged, and the
recipient read a message from their bank.

*A multi-address `From`.* `ParseAddress` returns an error on
`From: a@evil.example, ceo@bank.example`. The old code returned `""`, the `if
fromDomain != ""` guard was false, and **the entire DMARC block was skipped**. The
verdict stayed `dmarc=none` — which everything downstream reads as *"this domain
publishes no policy"* rather than *"we could not tell"*. A domain with
`p=reject` was delivered as though it had no policy at all. Silently skipping the
check is strictly worse than failing it.

**Fix.** `headerFromDomain` now fails closed: exactly one `From` header carrying exactly
one address, or the message is reported malformed. A malformed `From` is treated as
`dmarc=fail` **and quarantined**, and the `Authentication-Results` header carries
`reason="malformed From header"` so an operator can tell it apart from an ordinary
alignment failure. Well-formed messages — including display names and quoted names
containing commas — are unaffected, which the tests pin.

---

## M-2 — Forged `Authentication-Results` and `X-VayuMail-*` headers accepted

**Where:** `internal/vayuos/mail/smtpd.go` (DATA), `authverify.go`

**What it was.** Inbound messages were never stripped of headers this server stamps and
later trusts. RFC 8601 §5 makes this mandatory for `Authentication-Results` — a verifier
**MUST** delete extant fields bearing its own authserv-id — for the obvious reason: a
sender who may write the header simply asserts `dkim=pass` about themselves.

The stamped copy was prepended, so `Header.Get` returned ours and the junk filter was not
fooled *today*. That is an accident of ordering, not a guarantee. Anything that iterates
headers, takes the last match, or reads the raw source is deceived — including a human
inspecting headers to decide whether a message is genuine, which is exactly what a
suspicious recipient does.

The same reasoning extends to the project's own assertions, and one is sharper than the
`Authentication-Results` case:

- **`X-VayuMail-Forwarded`** is the forwarding loop breaker (`inbound.go`). A sender who
  sets it on the way in **silently suppresses the recipient's own auto-forward**. That is
  targeted, invisible mail loss that would present as a VayuMail bug, and it needs no
  privileges at all.
- **`X-VayuMail-Auth-Quarantine`** is read by the junk filter (`spam.go`).

**Fix.** `stripTrustedHeaders` removes every header in `trustedInboundHeaders` from
inbound mail before the real verdicts are prepended. Verification still runs against the
message **as received** — DKIM signatures cover the headers, so stripping before
verification would break every legitimate signature. Folded continuation lines are removed
with their parent header (dropping only the first line would leave the continuation behind
as a valid header of its own), and the body is preserved byte for byte.

---

## M-3 — The brute-force throttle is defeated by opening more connections

**Where:** `internal/vayuos/mail/auththrottle.go` + the three accept loops

**What it was.** `AuthThrottle` imposes a decaying **delay**, capped at 2s, keyed by
mailbox address, and deliberately never locks anyone out. Its own comment claims this
"crushes a brute-force rate from thousands/sec to well under one per second".

It did not, because nothing bounded concurrency. A delay only rate-limits an attacker who
waits, and an attacker does not wait — they open more sockets. With N connections each
sleeping its 2s in parallel, the aggregate rate is **N/2 guesses per second**. A thousand
sockets is roughly five hundred guesses per second against a single mailbox: three orders
of magnitude above what the design intended.

There was also no cap on attempts per connection. One socket bought unlimited guesses,
because a failed AUTH left the session open and ready for the next.

**Fix.** Three controls, which only work together:

1. `connLimiter` — a global cap (256) and a per-source cap (16) on concurrent connections,
   enforced *before* the connection is serviced. This is what serialises attempts so the
   delay means what it says.
2. `maxAuthTriesPerConn` (5) on SMTP, IMAP and POP3 — after which the connection is
   **dropped**, not merely refused, so an attacker pays a new TCP handshake per handful of
   guesses.
3. The per-source cap bounds how many of those handshakes can be in flight at once.

The delay was never the problem; the missing concurrency bound was.

---

## M-4 — Unbounded concurrent connections

**Where:** `acceptLoop` in `smtpd.go`, `imapd.go`, `pop3d.go`

**What it was.** Each accept spawned a goroutine with no limit. Every connection holds a
`bufio.Reader` sized against `MaxMessageBytes` and a five-minute deadline. On ports that
must face the public internet, that is an unauthenticated memory and file-descriptor
exhaustion with no credentials and no cleverness required.

**Fix.** The same `connLimiter`. Over-cap connections get a transient refusal
(`421` / `* BYE` / `-ERR`) so legitimate peers retry, then the socket is closed before any
work is done.

---

## M-5 — Accept loop spins at 100% CPU on descriptor exhaustion

**Where:** all three `acceptLoop` functions

**What it was.** On a non-shutdown `Accept()` error the loop ran a bare `continue`. The
way `Accept` fails persistently is `EMFILE` — descriptor exhaustion — which M-4 let an
attacker cause deliberately. The result is a hot loop burning a core, on every listener
simultaneously, so the resource-exhaustion attack escalates into a full CPU denial of
service and the box stops answering long before the descriptors free up.

**Fix.** `acceptBackoff`: 5ms doubling to a 1s ceiling, reset on the first successful
accept.

---

## M-6 — `DATA` reachable without an accepted `MAIL FROM`

**Where:** `internal/vayuos/mail/smtpd.go`

**What it was.** `DATA` gated on `len(rcpts) == 0` alone, and `RCPT` did not require a
prior `MAIL`. Worse, when the sender-login binding **rejected** a `MAIL FROM` (audit M5's
control, stopping one mailbox spoofing another), the handler cleared `from` but left
`rcpts` populated. So an authenticated submitter could:

```text
MAIL FROM:<someone-elses@mailbox>   → 553 rejected, from cleared, rcpts NOT cleared
RCPT TO:<target@example.com>        → accepted
DATA                                → accepted, submitted with a null envelope sender
```

The binding refused the address and the message went out anyway.

**Fix.** An explicit `mailSeen` flag tracks an **accepted** `MAIL`. `RCPT` and `DATA` both
require it, and a rejected `MAIL` clears the whole transaction rather than only the sender.

---

## T-1 — One ordinary VayuTalk account can sign every user out

**Where:** `internal/vayuos/vayutalk/tokens.go`

**What it was.** The token table is global, capped at `maxTokens` (20,000). Every
`/api/v1/talk/connect` mints a **new** token and never invalidates the old one, and
eviction picked the soonest-to-expire entry **across all users**.

So the cheapest asset an attacker can obtain — one ordinary mailbox on the install, no
privileges — could call connect in a loop and evict every other user's token on the way
past the cap. One account, and VayuTalk signs the whole install out. Credential
throttling does not help: the attacker's own credentials are valid.

**Fix.** `maxTokensPerEmail` (10) caps live tokens per mailbox, and eviction is ordered so
pressure created by an account is paid for by that account: expired entries first, then
that email's own oldest, and only then the global soonest-to-expire. Ten covers phone,
desktop, tablet and stale sessions with room to spare, which the tests pin so the fix
cannot become a sign-out bug.

---

## Checked and found sound

Recording these matters: several were the first places worth attacking, and knowing they
were examined is part of the result.

- **Maildir path traversal — closed.** `safeSegment` reduces domain and username to a
  single path component via `filepath.Base(filepath.Clean("/" + s))`. Folder names go
  through `canonicalFolder`, an **allowlist** that maps anything unrecognised to `Inbox`
  rather than sanitising it. Message IDs go through `resolveMessage`, which applies
  `filepath.Base` and rejects `..`. All three layers hold independently.
- **Open relay — closed.** `recipientAccepted` restricts inbound to served domains, and
  `recipientExists` refuses unknown local-parts (closing the Maildir-creation
  amplification), with `maxRcptsPerTxn` bounding the envelope.
- **AUTH over cleartext — refused.** Submission requires STARTTLS before AUTH
  (`538 5.7.11`), and both STARTTLS and IMAP `STARTTLS` correctly discard prior session
  state per RFC 3207 / RFC 2595 — a step often missed, and its absence is an
  authentication-stripping bug.
- **Private PGP keys — no administrator path.** The VayuOS surface exposes public halves
  only; the sole release path is the owner's own device under the MAIL-SYNC scope. Locked
  by test in `cmd/vayupress/vayuos_pgp_pubonly_test.go`.
- **VayuTalk credential verification — throttled.** `/api/v1/talk/connect` routes through
  the same `mailAuthThrottle` as the mail listeners, so it cannot be used as a
  softer target for guessing.
- **Token randomness — sound.** 32 bytes from `crypto/rand`, and the failure path returns
  empty rather than degrading to a time seed.
- **Sender-login binding — present.** An authenticated submitter cannot use another local
  mailbox as the envelope sender (its state-handling bug is M-6, not the control itself).

---

## Residual risk

Stated plainly, because an audit that reports everything closed is not describing
software.

- **The per-source connection cap is keyed on IP.** A distributed attacker with many
  source addresses still consumes the global budget. The global cap bounds the damage to
  refused connections rather than exhaustion, but a large botnet can deny service. Closing
  this properly needs reputation or proof-of-work at the connection layer, which is a
  larger design decision than an audit fix.
- **`AuthThrottle` is keyed by mailbox, not by source.** Password *spraying* — one
  password against many accounts — accrues no delay, because each address has its own
  counter. The connection caps now bound the rate, but a per-source failure counter would
  be a genuine improvement and is not yet implemented.
- **Inbound message parsing runs in the SMTP transaction.** DKIM verification and DMARC
  lookups happen inline, so a slow DNS path holds a connection. The five-minute deadline
  and the connection caps bound this; moving verification off the transaction would be
  better.
- **`stripTrustedHeaders` allowlists by name.** A future header that the pipeline starts
  trusting must be added to `trustedInboundHeaders` or it inherits the M-2 problem. The
  list carries a comment saying so; that is a process guard, not a technical one.

---

## See also

- [`SECURITY.md`](../SECURITY.md) — how to report a vulnerability
- [`docs/THREAT-MODEL.md`](THREAT-MODEL.md) — the standing threat model
- [`GOVERNANCE-CONSTITUTION.md`](../GOVERNANCE-CONSTITUTION.md) — the binding rules
