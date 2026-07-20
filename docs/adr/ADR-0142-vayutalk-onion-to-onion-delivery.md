# ADR-0142 — VayuTalk onion-to-onion delivery (Tor-world federation)

Status: Accepted (all phases shipped: opt-in + inbound endpoint + guard-railed
outbound lane + send wiring + admin toggle + inbound signature verification +
managed-tor loopback SOCKS auto-open. Live 2-onion delivery pending real-deploy
validation — it cannot be exercised in CI.)
Date: 2026-07-20
Deciders: VayuPress core

## Context

ADR-0141 (VayuOS Spaces) gave the Tor world an **anonymous, rotatable VayuTalk
identity** — a handle `anon<rand>@<onion>` with no mailbox behind it. What it did
**not** give it was a way for two people on **different** `.onion` installs to
actually exchange messages. The relay (`internal/vayuos/vayutalk`) is an
**in-process, in-memory hub**: `Send` enqueues/publishes an envelope to a channel
that only a subscriber **on the same instance** can read. Two operators, each on
their own onion, share nothing.

Two defects made this worse in practice (both fixed in v3.14.60, and they frame
the requirements here):

1. The recipient-key resolver **fabricated** a local keypair for any unknown
   address, so a message to a remote handle was encrypted to a made-up key,
   queued locally, and reported **"sent"** — a silent black hole.
2. Those fabricated keys (and every rotated handle) surfaced in the mailbox PGP
   manager.

Critically, the managed Tor process is configured **onion-services-only** with
`SocksPort 0` (`internal/vayuos/vayutor/managed.go`) — an intentional anti-leak
posture. So there is **no outbound-over-Tor path at all** today. Delivering to
another onion therefore requires adding a whole outbound lane, without ever
letting the process reach clearnet.

Operator requirement: two people, each running their own VayuPress Tor install,
should be able to exchange ephemeral, end-to-end-encrypted VayuTalk messages
using each other's anonymous codes.

## Decision

Add **onion-to-onion VayuTalk delivery**: when the recipient of a Tor-world
message is a handle on a **different** `.onion`, the sender's install fetches the
recipient's real public key over Tor, encrypts+signs to it locally, and delivers
the ciphertext envelope to the recipient's install over Tor, where it enters that
install's normal in-process hub and reaches the recipient's live stream.

This is **opt-in and off by default** (`talk.onion_federation`, default `off`).
When off, behaviour is exactly today's (an honest "can't reach over Tor" error);
production installs are unchanged unless the operator turns it on.

### The two directions

- **Inbound (serving).** Each install already publishes its handle's public key
  via WKD over its own onion (`/.well-known/openpgpkey/…`), so a remote peer can
  fetch it — no clearnet involved. A **new** unauthenticated delivery endpoint,
  `POST /api/v1/talk/onion/deliver`, accepts a ciphertext envelope from a remote
  onion and enqueues it via the existing engine `Send` path — but **only** for
  the install's current anonymous handle (never an open relay for arbitrary
  strings), under the existing per-recipient/global queue caps and the 64 KiB
  ciphertext limit, plus a dedicated rate limit.

- **Outbound (sending).** A **guard-railed onion-only Tor lane**: the managed
  torrc gains a loopback `SocksPort` **only when federation is enabled**, and a
  SOCKS5 dialer that **refuses any host that is not a `.onion`** (no IP literals,
  no clearnet FQDNs, no local DNS — Tor resolves the onion). Two calls ride this
  lane: fetch the peer key (WKD over Tor), then `POST` the envelope to the peer's
  `/api/v1/talk/onion/deliver`.

### Anti-leak guard-rails (non-negotiable — this is a privacy product)

- The outbound lane **only** dials `*.onion`. The dialer rejects everything else
  before a connection is attempted; this is unit-tested as the safety-critical
  invariant.
- No local DNS resolution ever happens for these requests (SOCKS5 remote
  resolution only).
- The SOCKS port is loopback-bound and exists only while federation is on.
- Nothing on this lane may fall back to `a.outboundClient` (the clearnet client).

### Authenticity

Messages are already sign+encrypt (`EncryptAndSignFromEmail`). The recipient's
install fetches the **sender's** key (from the sender onion, over Tor) to verify
the signature before display; a verification miss is shown, never silently
dropped. Users still confirm safety numbers out-of-band, exactly as in the app.

## Phases

- **P1 (this ADR):** guard-railed outbound onion-only Tor lane (SOCKS opt-in +
  `.onion`-only dialer + Tor HTTP client) with unit tests for the dialer guard;
  `talk.onion_federation` setting (default off). No behaviour change while off.
- **P2:** remote key fetch over Tor (WKD-over-onion client) + the inbound
  `POST /api/v1/talk/onion/deliver` endpoint (caps, rate limit, local-recipient
  only).
- **P3:** wire the send path — when the recipient host ≠ our onion and federation
  is on, fetch the key over Tor and deliver over Tor instead of the local hub;
  honest, specific errors on every failure branch.
- **P4:** sender-signature verification on inbound, admin toggle + status, and
  operator docs.

## Consequences

- **Testability.** End-to-end delivery needs two live onions and a Tor daemon and
  **cannot be verified in CI or the build sandbox**. Each phase ships with the
  unit tests that *are* possible (dialer guard, endpoint auth/caps, address
  classification, URL construction) and is explicitly marked experimental until
  validated on real deployments.
- **Attack surface.** A new unauthenticated inbound endpoint exists only when
  federation is enabled, is bounded by the existing queue caps + a rate limit,
  and accepts envelopes only for the local handle.
- **Reversal.** This reverses ADR-0141's implicit "no outbound Tor" only under an
  explicit opt-in, and only for `.onion` destinations — clearnet remains
  unreachable from this lane by construction.
