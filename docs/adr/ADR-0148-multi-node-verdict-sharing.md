# ADR-0148 — Multi-node verdict sharing

- **Status:** Accepted
- **Date:** 2026-07-29
- **Supersedes:** the placeholder `internal/cluster` package (removed)

## Context

VayuShield defends one origin. Running the binary on several machines is the
only mechanism available to a self-hoster that touches ingress capacity at all,
and until now the nodes shared almost nothing.

One thing was already free. The challenge signer is derived from the install
secret, so a visitor who solves a proof of work on node A walks onto node B
unchallenged with no code written. Everything else was per-process memory:

- reputation standing and its escalation ladder,
- the O(1) jail,
- learned signatures,
- the kernel banlist.

Two consequences, both bad. A distributed swarm gets one free run at every node
instead of one at the fleet — with five nodes an attacker pays a fifth of the
escalation cost. And a reader who solves a challenge and then lands on a
different node is challenged again, which reads as a broken site.

## Decision

Nodes share **verdicts**, and only verdicts: a jail, a reputation loss, a
pardon. Not content, not sessions, not learned signatures, not accounts.

### The key is derived, never the shared secret

`API_KEY` also guards the REST API and the MCP server. Handing it raw to N edge
nodes would mean one compromised edge compromises all three, everywhere. The
gossip key is `HKDF-SHA256(API_KEY, salt="vayushield/gossip/v1",
info="verdict-seal")`. A node holding it can vouch for verdicts and cannot
recover the secret, mint an API token, or speak to the MCP server.

Deriving rather than distributing also means there is no new secret for an
operator to rotate, store or leak — the peer list is the only configuration.

### Sealed, not signed

Messages carry visitor IP addresses. AES-256-GCM under the derived key, not an
HMAC over cleartext: the cost is identical, and a product that ships a Tor Space
should not put its audience on the wire in the clear between its own machines.

### No forwarding, ever

`Message` has no hop count and no TTL, and the absence is the design. Verdicts
are pushed directly to configured peers and never relayed. A relaying mesh turns
one message into N, and a loop in the peer graph is a self-inflicted flood on the
machines already under attack. N is a small number an operator configures; a full
mesh of direct pushes is O(N) and needs no cleverness to stay safe.

### The threat is a compromised node, not a stranger

Authentication proves a message came from a key-holder. It says nothing about
whether that key-holder is still trustworthy, and in a fleet of N edges the
realistic compromise is exactly one of them. A valid key can issue verdicts —
that is inherent, since any node that can jail locally can jail remotely — so the
design bounds the blast radius instead of pretending to prevent it:

| Control | What it stops |
| --- | --- |
| Receiver applies its **own** jail TTL | A peer cannot issue a thousand-year sentence |
| The receiving operator's **allow list wins** | A compromised node cannot lock the operator out of their own fleet |
| Per-origin budget (600 verdicts/minute) | One node cannot enumerate the internet into every jail |
| Peer table refuses rather than evicts | A node cannot rotate its name to mint a fresh budget |
| Inbound reputation deltas clamped, and must be positive | A peer cannot collapse a source into an auto-jail, nor **raise** a source's standing to whitelist its own swarm |
| Peers never reach the kernel offload | A kernel drop cannot be un-dropped; a remote node must not install one |
| Size and count bounded on the **receiving** side | A peer cannot make the receiver expensive |
| Freshness bounded in both directions, nonces remembered | A captured message cannot be replayed, and a future-dated one cannot be minted to replay forever |
| Replay cache written only **after** authentication | A stranger cannot fill it and deny the real peers |
| Uniform 403 with no body on every rejection | A prober gets no oracle for tuning attempts |

### Configuration

Two settings, both on the VayuShield console:

- `shield.cluster_peers` — one base URL per line. Empty means single-node, which
  costs nothing on the request path.
- `shield.cluster_node` — optional name, used only for a peer's per-node rate
  accounting. Derived automatically when blank, from the domain and peer list
  rather than the hostname, so a misconfigured peer list cannot leak a hostname
  to another operator's machine.

Endpoint: `POST /__vayushield/gossip`, mounted **only** when peers are
configured. An install with no fleet does not expose a route that always refuses
and invites probing.

## The honest ceiling

N nodes multiply ingress **linearly** — N uplinks instead of one. That is a real
improvement in availability and it is routinely sold as something much larger.

It is **not** anycast, **not** scrubbing, and **no defence at all** against an
attacker who brings more bandwidth than the sum of your links — and renting that
capacity scales faster and cheaper than adding nodes does.

The posture report computes the aggregate from the operator's own measured link
speed and states the assumptions it rests on (equal uplinks, evenly spread
traffic). DNS-based routing moves traffic in minutes, not milliseconds, so a node
failing is a minutes-long event; resolvers that ignore TTL make it longer. The
permanent volumetric-absorption `Fail` row stays red and now adds that nodes
raise the ceiling without removing it.

True anycast is a different category: an ASN, portable address space, transit or
IX contracts at multiple sites, and ongoing BGP operational competence. Most
self-hosters cannot enter it, and this ADR says so rather than listing it as
future work.

## Consequences

- A mismatched `API_KEY` across nodes fails authentication **silently** — correct
  for security, terrible for diagnosis. The posture report therefore carries a
  `Warn` row when clustering is configured and no peer verdict has ever been
  applied, naming the shared secret as the likely cause.
- Verdicts are advisory in one direction only: they can jail and suspect, and
  they can pardon. A peer can never widen what this node does beyond what this
  node could decide itself.
- Dropped verdicts are acceptable. The outbound queue is bounded and drops the
  excess under a flood; peers reach the same conclusions from the same traffic,
  so a dropped verdict costs a little speed and nothing else.
- The placeholder `internal/cluster` (leader election, "replace with Raft") is
  removed. Nothing imported it, and verdict sharing needs no leader: there is no
  state to agree on, only decisions to propagate.
