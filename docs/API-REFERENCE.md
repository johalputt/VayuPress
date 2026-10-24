# VayuPress API Reference

**Last reviewed:** 2026-10-16, against VayuPress 3.18.7

## Overview

**Base URL**: `https://yourdomain.com/api/v1` — or `https://api.yourdomain.com/api/v1`,
the challenge-free host for scripts, CI and agents (keep its CDN proxy off).  
**Authentication**: an API key, as `X-API-Key: <key>` or `Authorization: Bearer <key>`.  
**Content-Type**: `application/json`  
**Machine-readable spec**: `GET /api/v1/openapi.json` (OpenAPI 3.0.3).  
**GraphQL**: `GET|POST /api/v1/graphql`, query-only — writes go through REST.

Reads of published content (`GET /api/v1/articles`, search, tags) are public.
Everything that writes, and every administrative read, needs a key.

## Keys

There are two kinds:

- **Scoped keys** — minted in VayuOS → API keys. Each grants chosen
  `section:action` pairs from a 12 × 6 grid (sections: posts, themes, design,
  plugins, domains, analytics, media, comments, members, mail, settings,
  backup; actions: read, write, delete, apply, install, export), with an
  optional expiry and its own per-minute budget. Stored only as a hash; shown
  once. Every call is checked against the key's grant and written to the audit
  log. [Reference →](compatibility/vayuapi.md)
- **The install key** — `API_KEY` in `/etc/vayupress/env`. It can do
  everything; keep it for the operator's own use and rotate it as RB-09 in
  [OPERATIONS.md](OPERATIONS.md) describes.

A key-authenticated request needs **no CSRF token**. The CSRF double-submit
(`vp_csrf` cookie echoed in `X-CSRF-Token`) applies to requests made with a
signed-in browser session.

## Rate limits

- **Per key:** 600 requests a minute by default (`VAYU_API_RATE_PER_MIN`), or
  the key's own budget.
- **Per address, on the write routes:** 100 requests an hour from one client
  address. Addresses listed in `TRUSTED_IPS` are exempt.

Over either limit the answer is `429` with `Retry-After`. Use
`POST /api/v1/articles/bulk` for many posts at once.

## Errors

```json
{
  "error": {
    "code": "invalid_slug",
    "message": "Slug must be lowercase alphanumeric with hyphens/underscores.",
    "request_id": "a1b2c3d4",
    "docs": "/docs/api/errors#invalid_slug"
  }
}
```

`request_id` matches the `X-Request-Id` response header and the server log line.

## Articles

### List articles

```text
GET /api/v1/articles?page=1&limit=20&tag=go
```

`limit` is at most 100. Response:

```json
{
  "articles": [
    {
      "id": "7ff8e4c056eb6015e09e9e4a148a7ca5",
      "title": "My Article",
      "slug": "my-article",
      "tags": ["go", "sqlite"],
      "created_at": "2026-10-16T16:41:52Z",
      "updated_at": "2026-10-16T16:41:52Z"
    }
  ],
  "page": 1,
  "limit": 20,
  "total": 234727,
  "pages": 11737
}
```

### Get an article

```text
GET /api/v1/articles/{slug}
GET /api/v1/articles/{slug}/toc
```

Returns the article with its `content`; `/toc` its table of contents.

### Create

```text
POST /api/v1/articles
X-API-Key: <key>

{
  "title": "My Article",
  "slug": "my-article",
  "content": "<p>Hello world</p>",
  "tags": ["go", "sqlite"]
}
```

The write is queued and applied by a worker: the answer is `202` with
`{"status": "queued", "id": "…", "slug": "my-article"}`, and the article is
readable a moment later. Content is HTML, sanitised to the
article allowlist; an inline `<svg>` diagram is kept under its own allowlist.
Tags are normalised to topics (one address each).

### Create many

```text
POST /api/v1/articles/bulk
```

An array of the same objects, up to 1,000 per request.

### Update, delete

```text
PUT    /api/v1/articles/{slug}     { "title"?, "content"?, "tags"? }
DELETE /api/v1/articles/{slug}
```

`PUT` changes only the fields it carries. Each write purges the article's
cached pages and updates the search index.

## Search and topics

```text
GET /api/v1/search?q=<query>&limit=20
GET /api/v1/tags
```

Search is VayuFind, built in. `/tags` lists topics with their counts.

## Comments

```text
GET  /api/v1/articles/{slug}/comments
POST /api/v1/articles/{slug}/comments        (a signed-in member)
GET  /api/v1/admin/comments                  (key)
```

A comment appears once it is approved.

## Media

Media is uploaded through the console API, which accepts a key as well as a
session:

```text
POST /os/api/media/upload      (multipart: file)
GET  /os/api/media
```

Files are stored content-addressed, checked by magic number, and downscaled
when oversized.

## Analytics

```text
GET /api/v1/analytics/overview | pageviews | pages | referrers | browsers |
    devices | os | utm | events | realtime | sessions | funnels | retention |
    revenue | goals | journey
GET /api/v1/analytics/export     (Parquet)
GET /api/v1/sites/{site}/analytics/overview | realtime | pages | sources |
    events | searches | goals | funnels | funnels/{id} | performance |
    content
```

The `/api/v1/analytics` family adds up every site, so a key bound to one site
is refused it. The `/api/v1/sites/{site}/analytics` family reads one site,
named by its id or its host, with the analytics grant: an install key reads
any site, and a site-bound key reads its own site only. Any other site answers
`404`, exactly as a site that does not exist. `?days=` sets the window (1 to
365, 30 by default). A site's `searches` gives each query's searches,
visitors and searches that found nothing, and `follow_up`: how many of the
window's searches were followed in their visit by a page, by another search
first, or by nothing, the pages opened after searching, and the queries tried
again as others. `performance` gives the readers' vitals at p75 (a vital no
reader reported is `null`, with each vital's own `samples_*`), and `server`
and `server_by_day`: the site's requests by status and its pages' server
time (the mean, and p50, p75 and p95 as the bound of their bucket,
`_at_most`), with the pages the page cache answered and those it rendered,
each with its mean (`cached_pages`, `cached_mean_ms`, `rendered_pages`,
`rendered_mean_ms`). `content` gives each of the seven categories of
what the content did (docs/ANALYTICS.md) with its rule, its `total`, the 25
pages it lists first with their figures and `reason`, or `not_compared` when
the site's pages were too few for it.

No cookie is set, and no IP address or user agent is stored. A visit is keyed
by a hash whose salt rotates daily, so a reader cannot be followed from one day
to the next. For the same reason `retention` answers
`{"measurable": false, "identity": "daily", "reason": …, "cohorts": []}`:
no visitor can be seen to return on a later day, so there are no cohorts to
report.

## VayuShield

The shield is set from the console, so its endpoints are the console's: a
signed-in administrator's session, with the CSRF token on every write, for
the install's and for each site's (`docs/VAYUSHIELD.md`).

```text
GET  /os/api/shield/export              the shield's settings and lists
POST /os/api/shield/settings            save the install's settings and rules
POST /os/api/shield/simulate            replay recorded views against a draft
POST /os/api/shield/release             release a jailed address
POST /os/d/{id}/api/shield              save one site's settings and rules
POST /os/d/{id}/api/shield/simulate     replay that site's views against a draft
GET  /os/d/{id}/shield/feed             that site's newest refusals
```

A client with a key reads the shield through MCP: `vayushield_status`,
`vayushield_posture`, `vayushield_settings` and `vayushield_history`.

## Operations

```text
GET  /api/v1/queue                      the write queue
POST /api/v1/queue/replay               replay dead-lettered jobs
GET  /api/v1/admin/mode                 the install's mode
GET  /api/v1/admin/outbox/stats         the event outbox
GET  /api/v1/admin/search/drift         search index against the database
POST /os/api/search/reindex             rebuild the search index
POST /os/api/storage/refresh-pages      redraw every cached page
```

## Health and metrics

```text
GET /health, /health/live, /health/ready     public
GET /health/db | /workers | /queue | /search | /storage | /migrations | /dependencies
GET /metrics                                  Prometheus format
```

The detail probes and `/metrics` answer only a request carrying the API key or
coming from the machine itself; anyone else gets `404`.

## Webhooks

`POST /api/v1/admin/webhooks` registers an endpoint for article events. Each
delivery carries `X-VayuPress-Signature: sha256=<hex>`, an HMAC of the raw body
under the webhook's secret, so the receiver can verify it;
`GET /api/v1/admin/webhooks/{id}/deliveries` shows its history.

## The extension contract (VCB)

```text
GET  /api/v1/vcb/contract     the live contract
POST /api/v1/vcb/validate     validate a plugin.json or theme.json
```

[The Bible →](compatibility/vcb.md)

## MCP

The same capabilities, as tools for Claude and any MCP client, are at `/mcp`
with OAuth 2.1. [The connector →](compatibility/mcp.md)

## API Stability Tiers

Which routes are stable, and what a stable route promises, is in
[compatibility/stability-matrix.md](compatibility/stability-matrix.md) and
[compatibility/api-contracts.md](compatibility/api-contracts.md).
