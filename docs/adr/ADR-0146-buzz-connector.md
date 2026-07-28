# ADR-0146 — Buzz connector: agents in, over the MCP surface we already serve

Status: Accepted (shipped in v3.15.86)
Date: 2026-07-28
Deciders: VayuPress core

## Context

Block released [Buzz](https://github.com/block/buzz) on 21 July 2026 under
Apache 2.0: an open-source workspace combining team chat, Git hosting and agent
coordination, built on the Nostr protocol. Its distinguishing idea is identity —
every participant, human or agent, holds a keypair that belongs to them rather
than to the platform, so an agent signs its own work instead of borrowing a
human's credentials.

Operators asked whether VayuPress can be used from Buzz. It can, and the useful
finding is that most of the work was already done.

### Two integrations wear the same name

"Buzz connector" describes two features pointing in opposite directions, with
very different costs:

| | Agents **in** | Posts **out** |
|---|---|---|
| What it does | A Buzz agent uses this site's tools | This site publishes into a Buzz channel |
| Protocol | MCP over HTTPS | Nostr events to a relay |
| New dependency | none | secp256k1 Schnorr (BIP-340) |
| New credential | none | relay keypair, sealed at rest |
| New egress path | none | outbound, must close in a Tor Space |
| Cost | a page | a subsystem |

Conflating them makes the cheap half wait for the expensive half.

### The inbound half needs no protocol work

Buzz agents connect through an ACP harness that bridges them into workspace
channels **through MCP tools**, and the agents Buzz ships with — Claude Code,
Goose, Codex — all speak MCP.

VayuPress already serves its entire toolset over MCP at `/mcp`, with an OAuth
2.1 authorization server in front of it (ADR-0139, ADR-0140). A Buzz agent is
therefore already a supported client. Nothing new has to be spoken, signed or
dialled for one to publish a post or read analytics here.

What was actually missing was not a capability. It was an operator being told
this works, and being shown which key to mint and where to paste it.

### Why the outbound half is not bundled

Publishing events into a Buzz relay is a real feature, but it carries a
different risk profile:

- **A new signing primitive.** Nostr identity is BIP-340 Schnorr over secp256k1.
  Go's standard library has neither, so it means a new dependency in a project
  whose headline is one binary.
- **A new secret.** A relay keypair would have to live in the sealed keystore
  with everything else, and be rotatable.
- **A new egress path.** Under `VAYUOS_MODE=tor`, a clearnet relay connection is
  exactly the callback ADR-0141 exists to prevent. It would have to route
  through `safefetch` and refuse via `ClearnetBlocked()`.
- **An unstable target.** Buzz is at v4.x of nothing — it shipped a week before
  this ADR at **v0.4.21**. NIP-01 and NIP-98 are stable and safe to build on,
  but Buzz's *channel* semantics are custom event kinds, workspace-scoped and
  not publicly specified. Those will churn.

None of that is a reason never to build it. It is a reason not to make the free
half wait for it.

## Decision

**Ship the inbound half only, as a console page over the existing MCP surface.**

`/os/buzz` walks an operator through four steps: grant a scoped key, point the
agent at this site's endpoint, run the agent in Buzz, verify. It reuses
`connectorEndpoint()` so it inherits the dedicated-host preference and the
proxy-challenge diagnosis already built for VayuMCP.

The page **adds no backend surface**. Minting goes through the existing
CSRF-protected `/os/api/apikeys/create`, exactly as `/os/connector` does. A Buzz
agent is precisely as powerful as the key granted here, never more, and every
call it makes is written to the audit log.

Three decisions inside that are worth recording:

1. **Author is the primary grant, not full control.** The VayuMCP page leads
   with "Grant full control" because its common case is an operator connecting
   their own assistant. A Buzz workspace is a team, and an agent in a shared
   channel is acting for more than one person. The safe grant should be the one
   that looks like the default; a test pins this so a later edit cannot quietly
   promote the superuser button.
2. **Keys minted here are labelled `Buzz agent (…)`.** The stat strip counts
   only those. A key granted to Claude Desktop is not a Buzz agent, and an
   operator auditing which agents can reach their site needs them separated
   rather than summed.
3. **The page is admin-gated.** It mints API keys, so it belongs with
   `connector` and `apikeys` in `osPathMinLevel`. Without that entry it would
   inherit the permissive author default — a privilege escalation, confirmed by
   deleting the entry and watching the gate test fail with level 1.

## Consequences

**Good.** Operators get a working Buzz integration with no new dependency, no
new secret, no new egress path, and nothing to run alongside the binary. The
Tor-Space posture is untouched, because nothing outbound was added. The feature
cannot break in a Tor Space because it does nothing there that it does not do
everywhere.

**Limits, stated plainly.** This is one-directional. VayuPress does not post
into Buzz channels, and the page says so rather than leaving an operator to
discover it. Tool calls go from the agent straight to this server over HTTPS —
Buzz relays the team's messages but does not proxy these calls, so site content
never travels through the workspace.

**Deferred.** The outbound half stays unbuilt until Buzz's channel event kinds
settle. When it is built, the shape is already clear: `buzz-relay` exposes REST
alongside WebSocket and authenticates with NIP-98 (HTTP auth), so publishing can
go over ordinary HTTPS through `safefetch` and inherit the clearnet kill-switch,
rather than needing a hand-guarded WebSocket path. That is the design to revisit,
not a fresh question.

**Buzz's own maturity is a risk we carry but do not own.** Its mobile client
cannot yet create a standalone identity — it pairs from desktop. The page says
so, because an operator planning a rollout should hear it from us and not from a
stuck onboarding screen.

## References

- ADR-0139 — VayuMCP connector
- ADR-0140 — VayuMCP OAuth 2.1
- ADR-0141 — VayuOS Spaces (clearnet / Tor), the egress kill-switch
- [block/buzz](https://github.com/block/buzz) — Apache 2.0
- NIP-01 (events), NIP-42 (relay auth), NIP-98 (HTTP auth), NIP-34 (git events)
