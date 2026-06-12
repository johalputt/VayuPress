# VayuPress API Reference

## Overview

**Base URL**: `https://yourdomain.com/api/v1`  
**Authentication**: `Authorization: Bearer <API_KEY>` header  
**Content-Type**: `application/json`  
**Versioning**: Stable APIs use `/api/v1/`. Experimental APIs are behind feature flags.

## Authentication

All write endpoints require the `Authorization: Bearer <API_KEY>` header.

State-changing requests also require a CSRF token (double-submit cookie):
1. Make a `GET` request — the server sets a `vp_csrf` cookie.
2. Include the cookie value in the `X-CSRF-Token` header for `POST`/`PUT`/`DELETE`.

## Error Format

```json
{
  "error": {
    "code": "invalid_slug",
    "message": "Slug must be lowercase alphanumeric with hyphens/underscores.",
    "request_id": "a1b2c3d4",
    "docs": "https://docs.vayupress.com/api/errors#invalid_slug"
  }
}
```

## Articles

### List Articles

```
GET /api/v1/articles
```

Query parameters:
- `limit` — max 100, default 20
- `cursor` — pagination cursor (opaque string from previous response)
- `tag` — filter by tag

Response:
```json
{
  "articles": [
    {
      "id": "01J...",
      "title": "My Article",
      "slug": "my-article",
      "tags": ["go", "sqlite"],
      "created_at": "2026-06-12T00:00:00Z",
      "updated_at": "2026-06-12T00:00:00Z"
    }
  ],
  "next_cursor": "eyJpZCI6..."
}
```

### Get Article

```
GET /api/v1/articles/:slug
```

Returns full article including `content` field.

### Create Article

```
POST /api/v1/articles
Authorization: Bearer <API_KEY>
X-CSRF-Token: <token>

{
  "title": "My Article",
  "slug": "my-article",
  "content": "<p>Hello world</p>",
  "tags": ["go", "sqlite"]
}
```

- `slug`: lowercase alphanumeric, hyphens, underscores; 1–200 chars.
- `content`: HTML sanitized server-side (bluemonday).
- Write is enqueued asynchronously; returns `202 Accepted`.

Response:
```json
{
  "id": "01J...",
  "status": "queued"
}
```

### Update Article

```
PUT /api/v1/articles/:slug
Authorization: Bearer <API_KEY>
X-CSRF-Token: <token>
```

Same body as create. Partial updates supported — omit fields to keep existing values.

### Delete Article

```
DELETE /api/v1/articles/:slug
Authorization: Bearer <API_KEY>
X-CSRF-Token: <token>
```

Returns `204 No Content`.

## Search

```
GET /api/v1/search?q=<query>&limit=20&offset=0
```

Searches via Meilisearch if available, falls back to SQLite `LIKE` queries.

- Search queries are never logged or stored.
- Rate limited: 10 req/s per IP.

## Media

### Upload

```
POST /api/v1/media
Authorization: Bearer <API_KEY>
Content-Type: multipart/form-data
```

Allowed MIME types: `image/jpeg`, `image/png`, `image/gif`, `image/webp`, `image/svg+xml`, `application/pdf` (optional).

Returns:
```json
{
  "id": "abc123",
  "url": "/static/media/abc123.webp",
  "width": 1200,
  "height": 630
}
```

- Files processed in seccomp/apparmor sandbox via libvips.
- SHA-256 deduplication: identical files reuse the same URL.
- Max upload size governed by `STORAGE_QUOTA_GB`.

## Health

| Endpoint                  | Purpose                              |
|---------------------------|--------------------------------------|
| `GET /health`             | Liveness — 200 = alive               |
| `GET /health/ready`       | Readiness — checks DB, search, storage|
| `GET /health/db`          | Database connectivity                |
| `GET /health/meilisearch` | Search availability                  |
| `GET /health/workers`     | Queue worker health                  |
| `GET /health/storage`     | Storage quota (503 if >90% full)     |
| `GET /health/dependencies`| All subsystem status (structured)    |
| `GET /health/ai`          | AI subsystem health                  |
| `GET /health/security`    | Security subsystem health            |

Health response:
```json
{
  "status": "ok",
  "version": "1.0.0-p8",
  "uptime_seconds": 3600
}
```

Degraded:
```json
{
  "status": "degraded",
  "components": {
    "search": "unavailable",
    "db": "ok"
  }
}
```

## Metrics

```
GET /metrics
```

Prometheus-format metrics (counters, gauges, histograms). Local-first — no external telemetry.

## Cache

```
POST /api/v1/cache/purge
Authorization: Bearer <API_KEY>
X-CSRF-Token: <token>

{ "slug": "my-article" }
```

Rate-limited to 5 purges/minute per IP.

## Webhooks

VayuPress supports outgoing webhooks on article create/update/delete events.

Configure via environment. Payload signed with HMAC-SHA256; verify the `X-VayuPress-Signature` header.

## API Stability Tiers

| Tier         | Endpoints                          | Guarantee                           |
|--------------|------------------------------------|-------------------------------------|
| Stable       | `/api/v1/articles`, `/api/v1/search`, `/health` | Backward-compatible in MAJOR version |
| Beta         | `/api/v1/ai/*`                     | May change with notice              |
| Experimental | Feature-flagged endpoints          | No guarantees; may disappear        |
| Internal     | `/admin/*` internals               | No public contract                  |

Deprecated endpoints include a `Sunset: <date>` response header.
