# Pipeline — planned, not scheduled

Work that is decided in outline and kept here so it is not lost. Nothing on
this page is being built yet, and nothing here is promised for a release.
When an item is picked up, it gets an ADR (and an RFC where the
[RFC template](rfc-template.md) applies). It leaves this page when that ADR
is accepted.

Each entry says when it may start, what it is, what it must not break, and
what is still open.

| # | Item | Earliest start | Status |
|---|---|---|---|
| P1 | [VayuAgent: a Go agent that runs VayuPress from the Buzz app](#p1--vayuagent-a-go-agent-that-runs-vayupress-from-the-buzz-app) | After January 2027 | Outline |
| P2 | [One model, two renderings: HTML for readers, Markdown for agents](#p2--one-model-two-renderings-html-for-readers-markdown-for-agents) | Not before P1 has an ADR | Outline |

---

## P1 — VayuAgent: a Go agent that runs VayuPress from the Buzz app

**Earliest start:** after January 2027. Not before.

### What it is

A fully open-source agent system (Apache-2.0, like VayuPress) written in pure
Go and shipped as **one static binary**. It runs on the smallest VPS there is,
because it does no model work itself: the thinking is done by an **external
model API** the operator chooses and pays for, and the binary only carries
the loop, the tools and the state.

The operator talks to it from the **Buzz mobile app**. VayuAgent joins the
operator's Buzz workspace as a participant with its own keypair, takes
instructions in a channel or a direct message, and answers there. Behind it,
it drives one or more VayuPress installs through **VayuMCP** with a scoped API
key per install. Whatever VayuPress can do over MCP, the operator can ask for
from their phone: write and publish a post, moderate comments, read analytics,
check the backup verdict, put a site into a mode.

### Why a separate binary

[ADR-0146](adr/ADR-0146-buzz-connector.md) shipped the Buzz connector in one
direction only (agents in, over MCP) and kept Nostr out of VayuPress: a new
signing primitive (BIP-340 Schnorr over secp256k1), a new secret, a new egress
path that must stay shut in a Tor Space, and a target that is still moving.

VayuAgent is where that work goes instead. The Nostr dependency, the relay
keypair and the relay connection all live in the agent's binary, on the
agent's VPS. **VayuPress itself gains no dependency, no secret and no egress**:
it sees one more MCP client with a scoped key, audited like every other.

### Shape

- **One binary, three parts:**
  - a Buzz side: a Nostr client (NIP-01 events, NIP-98 HTTP auth) that holds
    the agent's keypair and reads and writes the workspace's channels;
  - a model side: a small provider interface over the external API (one
    adapter per provider, chosen in config), with streaming and a hard
    per-task token and cost ceiling;
  - a tool side: an MCP client that lists each connected install's tools and
    hands them to the model, with the install named on every call.
- **Lightweight by default:** no database server, no container runtime, no
  browser. State (conversations, pending confirmations, the install list) in
  one embedded SQLite file. Target: under 30 MB resident when idle, a cold
  start under a second, and nothing running between messages but the relay
  connection.
- **The operator stays in charge:**
  - only keys the operator has paired may give it instructions; anyone else in
    a channel is ignored;
  - anything destructive or public (publish, delete, a mode change, sending
    mail) is proposed in the chat and waits for an explicit "yes" from the
    operator before it runs;
  - each install's key is scoped as tightly as the job allows (Author first,
    as on `/os/buzz`), and every call shows up in that install's audit log
    under the agent's key name.
- **No telemetry**, no phone-home, no hosted control plane. The only outbound
  connections are the Buzz relay(s), the chosen model API and the operator's
  own VayuPress installs.

### Must not break

- VayuPress stays one binary with no new dependency for this.
- A Tor-mode install is reached only over its onion address, through Tor, or
  not at all; the agent never asks it to call out.
- An agent can never do more than the key it holds; there is no "agent mode"
  in VayuPress that widens a key.

### Open questions

- Which providers ship first, and whether a local model (on a bigger box) is
  supported through the same interface.
- How the pairing handshake works from the Buzz app (a one-time code shown on
  the VayuPress console is the current idea).
- Whether VayuAgent lives in this repository under `cmd/` with its own
  module, or in its own repository.
- How it behaves when Buzz's channel event kinds change, given they are not
  yet publicly specified.

---

## P2 — One model, two renderings: HTML for readers, Markdown for agents

**Earliest start:** not before P1 has an accepted ADR. VayuAgent is the
consumer that makes this worth building.

### What it is

A small multi-target rendering layer in pure Go inside VayuPress. The data a
page is built from (a post, a card, the home listing, a topic, search
results and the like) is rendered **twice from the same model**: the HTML
human visitors already get, and a well-structured Markdown version meant for
AI agents and tools.

The idea of one data model producing both HTML and Markdown is inspired by
Proa's multi-target rendering. This is our own, simple implementation shaped
to VayuPress's architecture; **no Proa code or structure is copied**.

### How it fits what exists

- **HTML does not change.** Pages are still pre-rendered to disk, cached and
  served as static files, with the same invalidation (`CacheInvalidated`) and
  warming. Nothing a reader sees moves.
- **Markdown comes from the same model, not from the HTML.** Both targets are
  rendered from one view model per page type, so there is never drift between
  what a reader sees and what an agent is told. No HTML-to-Markdown scraping
  anywhere.
- **Same cache discipline.** A Markdown rendering is written beside its HTML
  rendering, invalidated by the same event and warmed by the same warmer. A
  page whose HTML is purged has its Markdown purged with it.
- **VayuMCP is the main consumer.** Tools that return content (reading a
  post, a listing, search results) return the Markdown rendering instead of
  HTML, so an agent gets clean, compact, structured content. A public
  `Accept: text/markdown` or `.md` route can follow, decided in the ADR.
- **No new dependency.** Rendering stays in the standard library and the
  packages already in `go.mod`.

### Must not break

- One binary, zero telemetry, the strict CSP: a Markdown response carries no
  script and needs no relaxation of any policy.
- Normal traffic costs the same: Markdown is rendered on publish and on
  invalidation, as HTML is, never per request.
- Members-only and paid content is never in a Markdown rendering a reader of
  that tier could not see; the tier rules apply to both targets from the one
  model.

### Open questions

- The per-type view models: which page types come first (a post and the
  home listing are the obvious pair).
- How embeds, diagrams and code blocks are written in Markdown so an agent
  loses nothing a reader has.
- Whether `llms.txt` is rebuilt on top of this layer.
