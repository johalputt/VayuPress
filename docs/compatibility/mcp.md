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

## One-click: the Claude Connector page

The fastest way to connect is **VayuOS → Claude Connector** (`/os/connector`):

- **Grant full control** — one click mints a superuser key so Claude can run the
  whole site.
- **Author** / **Read-only** presets — one click mints a safely-scoped key.
- Your live endpoint is shown, and your freshly-minted key is filled straight
  into ready-to-paste **Claude Desktop** and **Claude Code** configurations.
- Every active connector is listed with an instant **Revoke**.

The page is admin-only and adds no new write surface — it mints and revokes
through the same API-key endpoints as the API Keys page.

## Full control vs. limited

The connector's power equals its key's scope:

- **Full control** — a superuser key (full access / `*:*`). Every VayuMCP tool
  becomes available; Claude can do anything the platform can. Use this to let
  Claude run the whole site.
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

| Tool | Needs (section:action) | Does |
|---|---|---|
| `site_info` | any valid key | Report platform, version, and primary domain. |
| `create_post` | `posts:write` | Create + publish a post (HTML body, sanitized). |
| `update_post` | `posts:write` | Update a post by slug (only the fields you pass). |
| `delete_post` | `posts:delete` | Delete a post by slug. |
| `list_posts` | `posts:read` | List published posts (pagination, tag filter). |
| `get_post` | `posts:read` | Fetch one post, including its HTML. |
| `search_content` | `posts:read` | Full-text search across published posts. |
| `create_page` | `posts:write` | Create a standalone page (About, Contact, landing) at /<slug>; excluded from the blog feed. |
| `list_pages` | `posts:read` | List standalone pages (edit/read via update_post/get_post by slug). |
| `site_settings` | `settings:read` | Read the site's non-secret config (name, tagline, theme, nav, SEO meta). |
| `analytics_summary` | `analytics:read` | Privacy-first traffic overview + top pages for the last N days. |
| `update_site_settings` | `settings:write` | Update presentational config (branding, theme colours, nav, footer, custom CSS, SEO); changes apply live. |
| `list_media` | `media:read` | List media-library files (name, /media/<name> URL, size, type). |
| `list_themes` | `themes:read` | List built-in theme presets available to apply. |
| `apply_theme` | `themes:apply` | Apply a built-in theme preset to the whole site (live). |

More tools (themes, plugins, pages, media, domains) land in later stages; a
full-control key automatically gains each new tool. Write tools for a subsystem
are added only once they can reuse that subsystem's own validated path, so a tool
can never bypass a check the REST/admin route enforces. `update_site_settings` is
bounded to the same presentational allowlist as `site_settings`, so operational
config (Tor bridges, VayuShield thresholds) is never writable through it.

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

- **Stage 2 (shipped):** the VayuOS **Claude Connector** page (one-click
  full-control or scoped key + copy-paste connect steps) and broadened read tools
  (`site_settings`, `analytics_summary`).
- **Stage 3 (in progress):** safe write tools for the remaining sections, each
  reusing its subsystem's validated path. **3a shipped:** `update_site_settings`
  (branding/theme/nav/SEO writes). **3b shipped:** `create_page` + `list_pages`.
  **3c shipped:** `list_media`, `list_themes`, `apply_theme`.
- **Stage 4 (shipped):** **OAuth 2.1** authorization server — a true one-click
  **Connect** on claude.ai (sign in + approve, no key to copy), exactly like the
  GitHub connector. See "Connect from claude.ai" above and
  [ADR-0140](../adr/ADR-0140-vayu-mcp-oauth.md).
