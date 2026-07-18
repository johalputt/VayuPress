# VayuMCP — the built-in Claude / MCP connector

VayuPress serves a **Model Context Protocol (MCP)** server from the same binary,
so an AI assistant (Claude Desktop, Claude Code, or any MCP client) can drive
your site natively — publish posts, read analytics, search content, and more —
the same way Claude connects to GitHub. See
[ADR-0139](../adr/ADR-0139-vayu-mcp-connector.md) for the design.

- **Endpoint:** `POST https://<your-domain>/mcp`
- **Protocol:** MCP over Streamable HTTP (JSON-RPC 2.0)
- **Auth:** a VayuPress scoped API key — `Authorization: Bearer <key>` (or
  `X-API-Key: <key>`). The connector can do **exactly** what the key grants and
  nothing more.
- **Toggle:** on by default; set `VAYUOS_MCP=off` to disable the endpoint.

## Full control vs. limited

The connector's power equals its key's scope. Mint the key in **VayuOS → API
keys**:

- **Full control** — a superuser key (full access / `*:*`). Every VayuMCP tool
  becomes available; Claude can do anything the platform can. Use this to let
  Claude run the whole site.
- **Limited** — a scoped key (e.g. only `posts:write`). The connector then
  exposes *only* the tools that key grants; every other tool is hidden from
  `tools/list` and refused if called. Same enforcement as the REST API.

## Connect from Claude Desktop / Claude Code

Add a custom MCP server that points at your endpoint and sends your key as a
header. Example (Claude Desktop `claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "vayupress": {
      "url": "https://your-domain.com/mcp",
      "headers": { "Authorization": "Bearer vp_your_key_here" }
    }
  }
}
```

Claude Code: `claude mcp add --transport http vayupress https://your-domain.com/mcp --header "Authorization: Bearer vp_your_key_here"`.

## Tools (Stage 1)

| Tool | Needs (section:action) | Does |
|---|---|---|
| `site_info` | any valid key | Report platform, version, and primary domain. |
| `create_post` | `posts:write` | Create + publish a post (HTML body, sanitized). |
| `update_post` | `posts:write` | Update a post by slug (only the fields you pass). |
| `delete_post` | `posts:delete` | Delete a post by slug. |
| `list_posts` | `posts:read` | List published posts (pagination, tag filter). |
| `get_post` | `posts:read` | Fetch one post, including its HTML. |
| `search_content` | `posts:read` | Full-text search across published posts. |

More tools (themes, plugins, design, settings, media, domains, analytics) land in
later stages; a full-control key automatically gains each new tool.

## Security model

- **Same scoped-key enforcement as VayuAPI** ([ADR-0134](../adr/ADR-0134-vayuapi-fine-grained-keys.md)).
  Each tool declares the `section:action` it needs; the server checks
  `KeyInfo.Can(...)` before listing or calling it. A tool the key does not grant
  is reported as "unknown" — the connector never discloses a capability the key
  lacks.
- **No new inbound surface** beyond the one authenticated HTTP route; the same
  rate limiting and WORM audit log apply, and every tool call is audited with the
  calling key's label (never the secret).
- **Rotate the key** in VayuOS to revoke a connector instantly.

## Roadmap

- **Stage 2:** a VayuOS "Claude connector" page (one-click full-control or
  limited key + copy-paste connect steps), a generic `vayu_request` tool for the
  whole API surface, and tools for the remaining sections.
- **Stage 3:** OAuth 2.1 on the same endpoint for a true one-click **Connect**
  button on claude.ai, exactly like the GitHub connector.
