# Claude Code — connect Claude to your site

Connect **Claude Code**, **Claude Desktop** or **claude.ai** to VayuPress and run
your site by chat: publish and edit posts, build pages, search content, read
analytics, switch themes.

It goes through [VayuMCP](mcp.md), the Model Context Protocol server VayuPress
serves from the same binary, so there is nothing to install on either side. See
[ADR-0147](../adr/ADR-0147-per-client-connector-pages.md) for why this has its own
page.

- **Console page:** VayuOS → Optimize → **Claude Code** (`/os/claudecode`),
  admin-only
- **Endpoint:** `POST https://<your-domain>/mcp`
- **Extra services required:** none

## Three routes in

### 1. One-click Connect — no key at all (recommended)

The Connect button lives on **Claude's side**, not in VayuOS. VayuPress runs the
OAuth 2.1 authorization server that Claude signs into.

1. On **claude.ai** or Claude Desktop, open *Settings → Connectors → Add custom
   connector*.
2. Paste your endpoint: `https://<your-domain>/mcp`
3. Click **Connect**. Claude signs you in through your site and shows an
   **Approve & connect** screen where you pick Full control, Author or Read-only.

This is the best route where it is available. No token is created, stored on
disk, or left behind to revoke later. Technical detail:
[ADR-0140](../adr/ADR-0140-vayu-mcp-oauth.md).

Custom connectors on claude.ai may require a paid plan (Pro/Max/Team/Enterprise).
The two routes below remain for clients that use a pasted key.

### 2. Claude Code (CLI)

Grant a key on the console page, then run one command:

```bash
claude mcp add --transport http vayupress https://<your-domain>/mcp \
  --header "Authorization: Bearer <your-key>"
```

This is also the route a Claude Code agent uses when it is driven from a
[Buzz](buzz.md) workspace.

### 3. Claude Desktop (config file)

Grant a key, add this to `claude_desktop_config.json` (Settings → Developer →
Edit config), then restart Claude Desktop:

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

Prefer route 1 where you can — it stores no token on disk.

## How much access to grant

The connector's power equals its key's scope. The console page offers three
one-click grants:

| Grant | What Claude can do |
| --- | --- |
| **Full control** | Everything the platform can — posts, pages, media, analytics, settings, and every future tool |
| **Author** | Create, edit, search and list posts and pages. Nothing else |
| **Read only** | Read content and analytics. Change nothing |

Full control is the default choice on this page because the common case is
connecting your own assistant to your own site. (The [Buzz](buzz.md) page leads
with Author instead — an agent in a shared team channel acts for more than one
person.)

Need something more precise? Build a custom scoped key on the **API Keys** page;
the connector honours it exactly.

Every call is rate-limited and written to the WORM audit log, and any grant can be
paused or revoked from the [VayuMCP](mcp.md) page at any time.

## If Connect fails

The cause is almost always **in front of your server**, not in it. Claude reaches
VayuPress machine-to-machine — no browser is in the loop for the API calls — so it
cannot pass a JavaScript challenge page. When one appears, the request never
reaches VayuPress to be logged, which is what makes it hard to diagnose.

Let these exact paths bypass the challenge:

```text
starts_with(http.request.uri.path, "/mcp") or
starts_with(http.request.uri.path, "/oauth/") or
starts_with(http.request.uri.path, "/.well-known/")
```

`/mcp` matters *after* connecting too — every tool call runs over it, so a
challenge there breaks the connector on first use.

Verify with a plain `curl` of your site's `/health` endpoint: it must return
JSON, **not** a challenge page. When curl gets through, Claude will too.

If your proxy cannot scope a challenge per path, point a dedicated
`mcp.<your-domain>` record straight at the server with the proxy **off**. The
[VayuMCP page](mcp.md) has the full workaround.

## Managing connected clients

Every client that has connected is listed on the [VayuMCP](mcp.md) page with what
it can reach, when it last called, and controls to pause, disconnect or remove it.

A key that has never been used is almost always a leftover from a connect attempt
that did not finish — safe to remove.
