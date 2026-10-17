# VayuMCP — the built-in MCP connector (Claude, Claude Code & any MCP client)

**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

VayuPress serves a **Model Context Protocol (MCP)** server from the same binary,
so an AI client can drive your site natively — publish posts, read analytics,
search content, and more — the same way Claude connects to GitHub. See
[ADR-0139](../adr/ADR-0139-vayu-mcp-connector.md) for the design.

**claude.ai is just one client.** Claude and Claude Code connect in one click, but
VayuMCP is a standard MCP + OAuth 2.1 endpoint — **any MCP client works the same
way** (Claude Desktop, Claude Code, Cursor, or anything that speaks MCP over HTTP).
Nothing here is Claude-specific except the example screenshots.

**What a connected client can do** (scoped to the key you grant): write,
edit and publish posts and pages; upload media and turn links into embeds;
read traffic, audiences and referrers; switch themes and site settings; build,
edit, preview and publish the websites of every hosted domain, follow a GitHub
repository, and diagnose or provision their certificates; and read, never
change, VayuShield's and VayuVeil's state. It cannot read or send mail, read
chat, change VayuShield or Tor, or touch accounts and keys: those surfaces
have no tools.

- **Endpoint:** `POST https://<your-domain>/mcp`
- **Protocol:** MCP over Streamable HTTP (JSON-RPC 2.0)
- **Auth:** a VayuPress scoped API key — `Authorization: Bearer <key>` (or
  `X-API-Key: <key>`). The connector can do **exactly** what the key grants and
  nothing more.
- **Toggle:** on by default; set `VAYUOS_MCP=off` to disable the endpoint.

## The console pages

**VayuMCP** (`/os/connector`) is the protocol surface:

- Your live endpoint, and whether it is on a dedicated host.
- **Grant full control** — one click mints a superuser key so the client can run
  the whole site.
- **Author** / **Read-only** presets — one click mints a safely-scoped key.
- The generic MCP configuration, with your freshly-minted key filled in.
- Every connected client, with pause / disconnect / remove.
- The proxy-and-WAF reference for when Connect fails.

Two clients have their own guided pages, because their setup differs enough to be
worth its own walkthrough ([ADR-0147](../adr/ADR-0147-per-client-connector-pages.md)):

- **[Claude Code](claude-code.md)** (`/os/claudecode`) — Claude Code, Claude
  Desktop and claude.ai, including the one-click route that needs no key.
- **[Buzz](buzz.md)** (`/os/buzz`) — agents in a Buzz workspace.

Anything else — Cursor, Cline, a custom client — uses the generic configuration
on the VayuMCP page unchanged.

All three pages are admin-only and add no new write surface — they mint and revoke
through the same API-key endpoints as the API Keys page.

## Full control vs. limited

The connector's power equals its key's scope:

- **Full control** — a superuser key (full access / `*:*`). Every VayuMCP tool
  becomes available; the client can do anything the platform can. Use this to
  let it run the whole site.
- **Limited** — a scoped key (e.g. only `posts:write`). The connector then
  exposes *only* the tools that key grants; every other tool is hidden from
  `tools/list` and refused if called. Same enforcement as the REST API.

For a precise custom grant, build a scoped key on the **VayuOS → API keys** page.

## Connect from claude.ai (one-click OAuth)

VayuPress runs a built-in **OAuth 2.1** authorization server ([ADR-0140](../adr/ADR-0140-vayu-mcp-oauth.md)),
so a client like **claude.ai** can Connect with a real sign-in + approve button —
no key to copy. The client discovers everything automatically:

1. It calls `POST /mcp`, gets a `401` with a `WWW-Authenticate` header pointing at
   `/.well-known/oauth-protected-resource`.
2. It registers itself (`POST /oauth/register`) and sends you to `/oauth/authorize`.
3. You sign in to **VayuOS** and pick an access level — **Full control**, **Author**,
   or **Read-only** — the same choices as the connector page.
4. Claude receives a token and is connected. The token is a scoped VayuPress API
   key under the hood, so you can revoke it anytime from **VayuOS → API Keys**.

Everything is PKCE-protected (S256), redirect URIs are exact-match, and only a
signed-in administrator can approve a connection.

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

## Tools

Forty-three tools, each visible only to a key holding its capability
(`a.mcpVisible`, `cmd/vayupress/mcp*.go`):

| Area | Tools | Needs |
|------|-------|-------|
| The connection | `site_info` | any valid key |
| Posts and pages | `list_posts`, `get_post`, `search_content`, `list_pages` | `posts:read` |
| | `create_post`, `update_post`, `create_page` | `posts:write` |
| | `delete_post` | `posts:delete` |
| Media | `list_media` | `media:read` |
| | `upload_media`, `embed_url` | `media:write` |
| Analytics | `analytics_summary`, `analytics_audience`, `analytics_referrers` | `analytics:read` |
| Themes | `list_themes` | `themes:read` |
| | `apply_theme` | `themes:apply` |
| Settings | `site_settings`, `get_performance` | `settings:read` |
| | `update_site_settings` | `settings:write` |
| VayuShield, VayuVeil (read-only) | `vayushield_status`, `vayushield_posture`, `vayushield_settings`, `vayushield_history`, `vayuveil_posture`, `vayuveil_unit_controls` | `settings:read` |
| Hosted sites | `list_sites`, `get_site`, `preview_site`, `list_site_templates`, `get_site_document`, `list_site_files`, `read_site_file`, `diagnose_certificate` | `domains:read` |
| | `update_site`, `build_site`, `restore_previous_site`, `save_site_draft`, `publish_site_document`, `edit_site_document`, `restore_site_revision`, `edit_site_files`, `follow_repository`, `sync_site`, `set_release_mirror`, `provision_certificates` | `domains:write` |

Each write tool reuses its subsystem's own validated path, so a tool can never
bypass a check the REST or console route enforces. `update_site_settings` is
bounded to the presentational allowlist `site_settings` reads, so operational
configuration (Tor bridges, VayuShield thresholds) is not writable through it.
The VayuShield tools report allow and deny lists as counts, never as values.

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

## History

The connector shipped in stages ([ADR-0139](../adr/ADR-0139-vayu-mcp-connector.md)):
the endpoint and key-scoped tools, then the console page and read tools, then
write tools section by section, then OAuth 2.1 one-click Connect
([ADR-0140](../adr/ADR-0140-vayu-mcp-oauth.md)), then hosted sites, the shield
and the observation tools. A full-control key gains each new tool
automatically.
