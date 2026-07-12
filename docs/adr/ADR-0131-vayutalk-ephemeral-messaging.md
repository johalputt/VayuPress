# ADR-0131: VayuTalk — ephemeral, end-to-end-encrypted messaging

- **Status**: Accepted
- **Date**: 2026-07-11
- **Relates to**: ADR-0128 (private-key sync), ADR-0129 (device approval),
  the VayuPGP engine, the VayuMail credential scope

## Context

VayuMail gives a sovereign identity its own mail. The next want is a
*conversation* channel — instant, one-to-one, private — without adding a
third-party messenger, a phone number, or a new account. It should reuse the
identity a mailbox already has (its address and its PGP keypair) and it must
not weaken the product's core promise: the server sees no plaintext and keeps
no durable record of what was said.

The design tension is between a real-time transport and the no-storage promise.
A messenger normally needs presence, delivery, and read state — all of which
tempt a database. VayuTalk resolves this by making **ephemerality the design
centre**: the relay carries opaque ciphertext, holds it in RAM only, and a
process restart erases everything.

## Decision

### SSE + REST over the standard library, not WebSockets

The client needs to *receive* pushed messages and receipts, and to *send*
messages, ack them, and fetch a recipient's key. Receiving is the only part
that needs a server→client push. We use **Server-Sent Events** for that one
stream (`GET /stream`) and plain **REST** for everything else
(`connect`/`send`/`ack`/`pubkey`).

Why not WebSockets:

- SSE is one-directional server→client, which is exactly the shape of the push
  channel; the client→server direction is ordinary authenticated POSTs.
- SSE rides on `net/http` with `http.Flusher` and `http.NewResponseController`
  for per-write deadlines — **zero new dependencies**, no upgrade handshake, no
  framing library, no separate proxy configuration. It passes through the same
  reverse proxies and CSP as the rest of VayuPress.
- Auto-reconnect is built into the browser/`EventSource` contract; our at-least-
  once store-mode queue makes reconnection safe (see below).
- A bidirectional socket would buy nothing here and cost a dependency plus a
  second, harder-to-reason-about lifecycle.

### In-memory only, no SQLite, restart purges everything

The entire VayuTalk state — pending envelopes, live subscribers, bearer tokens
— lives in bounded in-memory structures guarded by a mutex. **Nothing touches
the database.** An `Envelope` holds only the opaque ciphertext plus minimal
routing metadata (id, from, to, timestamps, mode); the server never has the
plaintext and never logs message content. A restart drops every envelope and
every token by construction, which is the strongest possible "delete" story:
there is nothing on disk to subpoena, image, or recover.

Two delivery modes:

- **live** — deliver only if the recipient holds a stream *now*; if offline,
  drop. Fire-and-forget, never stored, no receipt.
- **store** — always placed in the in-memory queue (so an ack can read-destroy
  it and the purge can expire it), and also pushed live if the recipient is
  online. An unacknowledged store envelope is re-flushed when the recipient
  reconnects, giving **at-least-once** delivery until it is read or it expires.

Read-destruction: `ack` deletes the envelope immediately and, if the sender is
streaming, emits a `receipt{read}` to them. Expiry: exactly **one** purge
goroutine (ticker ~2s, context-stopped with the engine) removes expired
envelopes and emits `receipt{expired}` to a streaming sender, and prunes
expired tokens in the same pass.

### Resource caps bound server load

Because the store is memory, every dimension is capped so a hostile or buggy
client cannot exhaust the host:

| Bound | Value | Enforced by |
| --- | --- | --- |
| Decoded ciphertext | ≤ 64 KiB | `413` on `send` |
| TTL | clamped to [60, 3600] s | silent clamp on `send` |
| Per-recipient queue | ≤ 50 | `429` on `send` |
| Global queue | ≤ 5000 | `429` on `send` |
| Streams per user | ≤ 3 | `429` on `stream` |
| Global streams | ≤ 500 | `429` on `stream` |
| Bearer tokens | ≤ 20 000, 12 h TTL | evict-soonest on mint |

Every per-connection goroutine is bounded: the SSE handler is tied to
`r.Context()` (client disconnect cancels it), carries a per-write deadline, and
runs a ~25 s heartbeat so dead intermediaries are noticed. The single purge
goroutine is the only background loop.

### Authentication reuses the mail-sync credential scope

`connect` authenticates through the **same** `verifyCredential` chokepoint the
mail listeners and private-key sync use — so device approval (ADR-0129) is
enforced: a mailbox that requires an approved device credential will not hand
out a VayuTalk token to a raw password. It is wrapped in the shared brute-force
throttle and returns a **uniform 401** on any failure (anti-enumeration). A
successful connect mints a random 32-byte base64url bearer token (12 h). All
other endpoints require that bearer. Public keys come from the VayuPGP engine
(`GetPublicKey`, minting on demand), keeping the package free of `cmd/` and DB
imports through injected `Verify`/`PubKey` function dependencies.

## Threat model

- **Server compromise** reveals only what is in RAM at that instant: a bounded
  set of short-lived, opaque ciphertext blobs and their routing metadata. There
  is no plaintext, no key material for the messages (PGP private keys live in
  the VayuPGP keystore, encrypted at rest, and are never in the VayuTalk store),
  and nothing on disk. TTL and read-destruction keep the live window small.
- **Forward secrecy** today comes from *ephemerality plus per-message PGP
  session keys*: each message is encrypted to the recipient under a fresh
  symmetric session key (standard PGP), and the ciphertext is destroyed on read
  or expiry, so compromising the relay later yields nothing. This is not the
  ratcheted forward secrecy of Signal — a compromise of a long-term PGP private
  key still decrypts any ciphertext an attacker separately captured in flight.
- **Metadata** minimisation: the server holds sender/recipient/timestamps for
  routing only, in memory, purged on restart; message content is never logged.
- **Transport** confidentiality and integrity are TLS's job, as everywhere else
  in VayuPress.

### Future work

A **double-ratchet** (X3DH + symmetric-key ratchet, Signal-style) would add
per-message forward secrecy and post-compromise security independent of the
long-term PGP key. It is deliberately out of scope for this first cut: it
requires client-side session state and a key-agreement handshake that the
current PGP-only clients do not implement. The wire protocol is frozen at v1;
a ratcheted mode would be negotiated as a new envelope `mode`/version so the
in-memory relay itself — which only ever moves opaque bytes — need not change.

### Web client (VayuOS console)

The mobile app holds the mailbox's PGP private key on the device and does its
own crypto. The **VayuOS web client** (`/os/talk`) cannot — a browser
under our strict CSP has no private key and we will not ship a megabyte of
vendored OpenPGP.js to give it one. Instead the web surface reuses the trust
model webmail *already* relies on: **the server decrypts on the user's behalf**,
exactly as it does when the reader opens an encrypted mail. This keeps the
browser client tiny (vanilla JS, EventSource + `fetch`, no crypto library) so
the console stays butter-smooth.

Concretely, two session-authenticated routes bridge the browser to the *same*
in-process relay the app uses:

- `POST /os/talk/send` — the server signs+encrypts the plaintext to the
  recipient with `EncryptAndSignFromEmail(text, to, self)` and hands the armored
  ciphertext to the relay. This is byte-identical to what VayuMail Mobile's
  `keyring.Encrypt(text, [peer], selfEmail)` produces, so a web sender and an app
  sender are indistinguishable to the relay and to the recipient's signature
  check.
- `GET /os/talk/stream` — a server-side-decrypting SSE bridge:
  `Subscribe` to the relay for the signed-in mailbox, decrypt each envelope with
  that mailbox's key, push **plaintext** to the browser, then `Ack`
  (read-destroy) so a delivered message is a read message.

The relay itself is unchanged and still only ever sees opaque ciphertext; the
network and every intermediary do too. Plaintext exists only for the lifetime of
one request or one SSE write and is never logged or persisted. The browser keeps
conversations in tab memory only — a reload wipes them, matching the ephemeral
promise. **Web and app share one relay and interoperate end to end**: a message
sent from either surface arrives, decrypts, and read-destroys on the other.
Bidirectional interop is pinned by `TestTalkWebToApp` / `TestTalkAppToWeb`.

VayuTalk is a **top-level system in the console** (its own sidebar entry and
`/os/talk` route), not a VayuMail sub-tab — it reuses the mailbox identity but is
a distinct product surface. Chat identity is resolved by `talkIdentity`, not
`ownMailbox`: the latter re-reads the user from the database and so drops the
in-memory mailbox address a mailbox login attaches to a unified CMS account, and
a pure CMS admin has no assigned-mailbox row at all — which produced a spurious
"no mailbox assigned" dead-end for a signed-in owner. `talkIdentity` resolves,
most-specific first, (1) the session's bound mailbox, (2) the account's own email
when it is on the mail domain ("chat as who I signed in as"), then (3) for an
admin, the first mailbox on the server (an admin owns them all) or
`postmaster@domain`. Every branch is an address the caller controls, so nobody
can chat as an identity that isn't theirs; resolution is pinned by
`TestTalkIdentityResolution`.

**Verification parity.** The web shows a safety number (the peer's PGP
fingerprint) for out-of-band comparison, formatted by `formatSafety` with the
same normalisation as VayuMail Mobile's `chat.SafetyNumber` (colons/spaces
stripped, uppercased, groups of four) so the number on the web panel and the one
on the app's Verify-contact screen match character for character. A read-only
`GET /os/talk/peer` resolves a recipient's key so the browser can both preflight
reachability (turning a doomed send into an upfront explanation) and render that
number. Verified state is per-tab memory only — fingerprints are public, and
nothing about a verification is persisted.

## Consequences

- A sovereign identity gains private messaging with **no new dependency, no new
  table, and no new plaintext exposure** — on both the app and the web console,
  over one shared relay.
- Delivery is best-effort by design: `live` can be dropped, and a restart
  clears the `store` queue. Clients treat VayuTalk as ephemeral chat, not as an
  archive — mail remains the durable channel.
- The relay is trivially horizontal-unfriendly (state is in one process's RAM);
  that is an accepted trade for the no-storage guarantee on the single-VPS
  product. Clustering would require a shared ephemeral bus and is out of scope.
