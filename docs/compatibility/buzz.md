# Buzz — connect a Buzz agent to your site

[Buzz](https://github.com/block/buzz) is an open-source (Apache 2.0) workspace
from Block that combines team chat, Git hosting and agent coordination on top of
the **Nostr** protocol. Its distinguishing idea is identity: every participant,
human or agent, holds a cryptographic keypair that belongs to them rather than to
the platform, so an agent signs its own work instead of borrowing a human's
login.

VayuPress connects to it with **nothing extra installed on either side**. Buzz
agents run through an agent harness that bridges them into workspace channels
over **MCP tools**, and the agents Buzz ships with — Claude Code, Goose, Codex —
all speak MCP. VayuPress already serves its whole toolset over MCP through
[VayuMCP](mcp.md), so a Buzz agent is already a supported client.

See [ADR-0146](../adr/ADR-0146-buzz-connector.md) for the design and for why the
reverse direction is deliberately not built.

- **Console page:** VayuOS → Optimize → **Buzz** (`/os/buzz`), admin-only
- **Endpoint:** the same `POST https://<your-domain>/mcp` VayuMCP serves
- **Auth:** a VayuPress scoped API key — `Authorization: Bearer <key>`
- **Extra services required:** none

## What a Buzz agent can do here

Exactly what the key you grant allows, and nothing more. With an **Author** key
an agent can list, search, read, create and update posts and pages. With
**Read-only** it can read content and analytics but change nothing. With **Full
control** it can do anything the platform can.

It cannot reach email, chat, VayuShield or VayuTor — VayuMCP covers the
publishing, site and analytics surface only.

## Connect in four steps

### 1. Grant a key

Open **VayuOS → Optimize → Buzz** and pick a grant.

Start with **Author** unless you have a reason not to. It lets the agent write
and organise content but nothing else, and you can grant a wider key later
without disconnecting anything.

Give **each agent its own key**. The audit log then tells you which agent did
what, and revoking one does not disconnect the others.

### 2. Point the agent at your site

The page shows your endpoint and fills your new key into both configurations.

For **Claude Code** — which has its own page too, see
[claude-code.md](claude-code.md) — run this where the agent runs:

```bash
claude mcp add --transport http vayupress https://<your-domain>/mcp \
  --header "Authorization: Bearer <your-key>"
```

For **Goose, Codex or any other MCP client**, add a remote MCP server with the
same URL and header:

```json
{
  "mcpServers": {
    "vayupress": {
      "url": "https://<your-domain>/mcp",
      "headers": { "Authorization": "Bearer <your-key>" }
    }
  }
}
```

### 3. Run the agent in Buzz

Start the agent from your Buzz workspace as normal. Buzz bridges it into the
channel through its agent harness, and this site's tools become available to it
there — no extra bridge, relay or plugin in between.

Tool calls go from the agent **straight to your server over HTTPS**. Buzz relays
your team's messages; it does not proxy these calls, so your content never
travels through the workspace to reach your site.

### 4. Verify

Ask the agent to list your most recent posts. Real titles back means the
connector is live.

## If it cannot connect

The cause is almost always **in front of your server** rather than in it. An MCP
client is machine-to-machine — it has no browser, so it cannot answer a
JavaScript challenge, and when one appears the request never reaches VayuPress to
be logged.

Let these paths bypass the challenge: `/mcp`, `/oauth/*` and `/.well-known/*`.
The [VayuMCP page](mcp.md) has the full expression and the dedicated-host
workaround for proxies that cannot scope a challenge per path.

## What this connector does not do

It runs in **one direction**: a Buzz agent reaches into your site and uses its
tools.

It does **not** post from your site into a Buzz channel. That is a genuinely
different feature — it would mean signing Nostr events, holding a relay
credential, and opening an outbound network path that has to stay closed in a Tor
Space. ADR-0146 records why the two are kept apart and what building the second
one would involve.

## Notes on Buzz itself

Buzz is early software, moving quickly, and worth knowing a few things about
before you plan a rollout:

- A workspace runs through **a single relay** your team can host itself. Buzz
  calls this organisational sovereignty — self-host the relay and you hold the
  record. It is not a peer-to-peer mesh.
- Self-hosting the full stack is real infrastructure: a Rust relay plus Postgres,
  Redis, a search index and object storage.
- The **mobile client cannot yet create an identity on its own** — it pairs with
  an existing desktop install, so set up on desktop first.

None of this affects the connector, which only needs the agent to reach your
site over HTTPS.
