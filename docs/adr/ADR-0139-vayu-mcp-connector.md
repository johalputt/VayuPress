# ADR-0139 — VayuMCP: a built-in Model Context Protocol connector

Status: Accepted (Stage 1 — API-key transport; Stage 2 — connector page + broadened tools)
Date: 2026-07-18
Deciders: VayuPress core

## Context

VayuPress already exposes a fine-grained REST API (VayuAPI, ADR-0134) with
`section:action` scoped keys, a capability table, per-key rate budgets, and a
WORM audit log. That lets any script or agent drive the platform — but every AI
assistant integration has to be wired up by hand (write a client, manage the
key, format requests).

The industry-standard way to let an AI assistant use an external system is the
**Model Context Protocol (MCP)** — the same mechanism behind Claude's GitHub
connector. The AI connects to an MCP server once and gets a typed set of
**tools** it can call. We want VayuPress to be connectable to Claude (and any
MCP client) the same way: one endpoint, one connect step, native tools.

Because VayuPress is a single Go binary that already serves HTTP, the MCP server
belongs **inside the binary** — no second service, no separate deployment,
consistent with the whole product's "everything in one binary" design.

## Decision

Add **VayuMCP**: a built-in MCP server served by the VayuPress binary at
`POST /mcp`, exposing VayuPress capabilities as MCP tools, authenticated and
authorised by the **existing scoped-key model** so a connector can only ever do
what its key grants.

### Transport & protocol

- **Streamable HTTP**, JSON-RPC 2.0, single endpoint `POST /mcp`. Requests that
  are simple request/response return `application/json`; SSE streaming is not
  required for a tools-only server (may be added later for progress).
- Methods implemented: `initialize`, `notifications/initialized` (notification),
  `tools/list`, `tools/call`, `ping`. Protocol version is negotiated in
  `initialize`; unknown methods return a JSON-RPC "method not found".
- The protocol layer lives in a self-contained `internal/mcp` package with **no
  VayuPress-specific imports**, so it stays small, testable, and reusable. The
  host (`cmd/vayupress`) registers tools and their handlers into it.

### Authentication & authorisation (Stage 1)

- `POST /mcp` is wrapped by `auth.RequireAPIKey`, which validates the key
  (`Authorization: Bearer <key>` or `X-API-Key`) and stamps the `apikeys.KeyInfo`
  into the request context — exactly as the REST API does.
- Authorisation is **per tool, not per URL** (one URL serves every tool). Each
  registered tool declares the `section:action` it needs; before dispatch the
  server checks `KeyInfo.Can(section, action)`. A tool the key does not grant is
  hidden from `tools/list` and refused by `tools/call` — so the connector's
  surface is exactly the key's scope. No new permission logic is introduced; it
  reuses the ADR-0134 capability model verbatim.
- Every `tools/call` is written to the same WORM audit log as the REST twin.

### Tools

Each tool wraps an existing internal service so behaviour is identical to the
REST path. Stage 1 shipped the posts surface + `site_info`; Stage 2 broadened it
with site-configuration and analytics reads:

| Tool | Needs | Wraps | Since |
|---|---|---|---|
| `site_info` | (any valid key) | version/host/status | 1 |
| `create_post` | posts:write | `articles.Create` | 1 |
| `update_post` | posts:write | `articles.Update` | 1 |
| `delete_post` | posts:delete | `articles.Delete` | 1 |
| `list_posts` | posts:read | `articles.List` | 1 |
| `get_post` | posts:read | `articles.Get` | 1 |
| `search_content` | posts:read | `search.Search` | 1 |
| `create_page` | posts:write | `articles.CreatePage` (is_page atomic) | 3b |
| `list_pages` | posts:read | `articles.ListPages` | 3b |
| `site_settings` | settings:read | `siteSettings.GetAll` (presentational allowlist) | 2 |
| `analytics_summary` | analytics:read | `analytics.OverviewSince` + `TopPages` | 2 |
| `update_site_settings` | settings:write | `siteSettings.SetMany` + shared render refresh (presentational allowlist) | 3a |

The set grows in later stages; the registry makes adding a tool a few lines. Read
tools are added freely; a **write** tool for a subsystem is added only once it can
reuse that subsystem's own validated path (so a tool can never bypass a check the
REST/admin route enforces). `update_site_settings` (Stage 3a) is the first such
write beyond posts: it reuses `SetMany` (which validates against the settings
vocabulary) and the same `reloadRenderSettings` helper the VayuOS settings API
uses, and it is bounded to the **same presentational allowlist** as its read twin —
so operational config (Tor bridges, VayuShield thresholds) is never writable through
it. Stage 3b adds page writes (`create_page`, `list_pages`): to make the create
race-free, `is_page` now flows through the async write pipeline (a new
`dbpkg.Article.IsPage` field persisted by the queue worker's insert) instead of a
follow-up `UPDATE` that could briefly publish a page as a post — the same fix also
removed the racy `setArticleIsPage` path from the VayuOS quick-create-page handler.
Editing/removing a page reuses the slug-based `update_post`/`delete_post`/`get_post`
tools. Theme/media writes follow in later Stage-3 increments.

### Full control vs. limited (operator's choice)

The connector's power equals its key's scope, so the operator picks at connect
time:

- **Full control (one click).** Mint a **superuser** key (`*:*`, `IsSuperuser()`).
  Every VayuMCP tool is then available and Claude can do anything the platform
  can — publish posts, build the website, edit themes, manage plugins, change
  settings, and more — as the toolset grows to cover every section. This is the
  "give Claude the keys" mode the operator explicitly opts into.
- **Limited.** Mint a scoped key (e.g. `posts:write` only); the connector then
  exposes *only* the tools that key grants — everything else is hidden and
  refused. Same enforcement as the REST API.

A generic **`vayu_request`** tool that proxies to any VayuAPI endpoint (method +
path + body) was evaluated for "do anything before a named tool exists" and
**deferred**: re-dispatching a synthetic request through the live router would
re-run host/bot-protection/redirect middleware on an in-process request (fragile),
and the CSRF gate on the browser-oriented admin routes makes a raw passthrough
behave inconsistently. The chosen path is instead a **curated, self-describing
toolset** — each tool a thin adapter over a subsystem's already-validated service
method — which an LLM uses far more reliably than a raw HTTP passthrough, and
which can never bypass a validation the underlying path enforces. A safe
passthrough (dispatched through a dedicated internal API mux built from a single
source of truth) remains a future option if a gap appears.

### One-click experience (Stage 2 — shipped)

- **VayuOS "Claude connector" page** (`/os/connector`): shows the connector URL
  (`https://<domain>/mcp`); offers **"Grant full control"** (mints a superuser
  key) plus **Author** and **Read-only** one-click presets — with a link to the
  API Keys grid for a custom grant; fills the freshly-minted key into ready-to-
  paste Claude Desktop / Claude Code configurations; and lists active connectors
  with an instant Revoke. Admin-only, and adds no new write surface — it mints and
  revokes through the existing CSRF-protected API-key endpoints.
- **Stage 3: OAuth 2.1** authorisation on the same endpoint, so claude.ai shows a
  real "Connect → sign in → authorise" button like the GitHub connector — the true
  one-click. The API-key transport ships first and is usable immediately from
  Claude Desktop/Code and any header-capable MCP client.

## Consequences

- One binary still: no new service, no new port. The MCP server is another
  handler on the existing router.
- Security posture is unchanged and reused: scoped keys, capability table, rate
  budget, audit log. A connector is exactly as powerful as its key — never more.
- The `internal/mcp` package is host-agnostic and unit-testable without a live
  server. Tool handlers are thin adapters over already-tested services.
- Enables autonomous, native AI operation of a VayuPress site (draft → publish →
  measure) from any MCP client, with a clean path to a claude.ai one-click
  connector via Stage 3 OAuth.

## Alternatives considered

- **A separate MCP sidecar service.** Rejected: breaks the single-binary model
  and duplicates auth.
- **A third-party MCP Go SDK.** Deferred: the tools-only Streamable-HTTP surface
  is small enough to implement directly, avoiding a heavy dependency and keeping
  govulncheck/license surface minimal (consistent with the rest of VayuPress).
- **OAuth-first.** Rejected for Stage 1 only: it is the larger build; shipping
  API-key transport first delivers working value immediately, OAuth follows.
