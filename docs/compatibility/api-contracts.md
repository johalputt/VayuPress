# API Contracts — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## Versioning Policy

VayuPress follows [Semantic Versioning 2.0.0](https://semver.org/):

- **MAJOR** — incompatible API changes (requires migration guide)
- **MINOR** — new backwards-compatible functionality
- **PATCH** — backwards-compatible bug fixes

Current version: tracked in `internal/config/version.go`.

---

## HTTP Response Format Contract

All JSON responses follow this envelope:

```json
{
  "data":  <payload>,
  "error": null | "human-readable string",
  "meta": {
    "correlation_id": "uuid",
    "version":        "1.2.3"
  }
}
```

**Guarantees:**
- `data` is always present on success (may be `null` for 204-equivalent responses).
- `error` is always `null` on success; always a string on error.
- `meta.correlation_id` is always present for traceability.
- HTTP status codes: 200 OK, 201 Created, 400 Bad Request, 401 Unauthorized, 403 Forbidden, 404 Not Found, 409 Conflict, 429 Too Many Requests, 500 Internal Server Error.
- 5xx errors never expose internal stack traces.

---

## Pagination Contract

All list endpoints accept:

| Parameter | Type | Default | Max |
|-----------|------|---------|-----|
| `page` | int | 1 | — |
| `per_page` | int | 20 | 100 |
| `cursor` | string | — | — |

Response includes:

```json
{
  "data": [...],
  "pagination": {
    "page":        1,
    "per_page":    20,
    "total":       142,
    "next_cursor": "opaque-string-or-null"
  }
}
```

Cursor-based pagination is preferred for large datasets. Offset pagination is kept for backwards compatibility.

---

## Plugin IPC Contract

The stdin/stdout JSON protocol between host and plugin is a **Stable** contract.

**Request (host → plugin):**
```json
{
  "hook_name":      "on_article_publish",
  "payload":        {},
  "correlation_id": "uuid",
  "causation_id":   "uuid",
  "trace_id":       "uuid",
  "capabilities": {
    "allow_network":       false,
    "allowed_read_paths":  ["/data/public"],
    "allowed_write_paths": []
  }
}
```

**Response (plugin → host):**
```json
{
  "ok":    true,
  "error": null,
  "log_lines": [
    {"level": "info", "message": "processed"}
  ]
}
```

**Invariants:**
- Each request gets exactly one response line.
- Response line is `\n`-terminated.
- Response size is bounded by `Manifest.MaxMessageBytes` (default 4 MiB).
- `ok: false` with `error: "..."` is a graceful failure; host logs and continues.
- No response within `Manifest.Timeout` → host kills plugin; restart budget decremented.

---

## Event Schema Contract

Events emitted to the outbox follow this envelope:

```json
{
  "type":           "article.published",
  "version":        "v1",
  "id":             "uuid",
  "correlation_id": "uuid",
  "causation_id":   "uuid",
  "occurred_at":    "2026-06-13T09:00:00Z",
  "payload":        {}
}
```

**Invariants:**
- `type` is always `<aggregate>.<verb>` format.
- `version` is always `v<n>`.
- `id` is a UUID v4.
- `occurred_at` is always RFC-3339 UTC.
- `payload` schema is governed by the event schema registry (`internal/events/schema`).
- Events are append-only; published events are never modified or deleted.

---

## Trace Span Contract

```json
{
  "trace_id":    "uuid",
  "span_id":     "uuid",
  "parent_id":   "uuid-or-null",
  "name":        "handler.articles.create",
  "start_time":  "2026-06-13T09:00:00.000Z",
  "end_time":    "2026-06-13T09:00:00.042Z",
  "duration_ms": 42,
  "attrs":       {"http.status": 200}
}
```

Span names follow `<layer>.<domain>.<operation>` convention.  
`attrs` keys follow OpenTelemetry semantic conventions where applicable.

---

## Signed Article Contract

```json
{
  "payload": {
    "id":           "uuid",
    "title":        "string",
    "body":         "string",
    "author_did":   "did:key:z...",
    "published_at": "2026-06-13T09:00:00Z",
    "version":      1
  },
  "public_key_hex": "hex-encoded-32-bytes",
  "signature_hex":  "hex-encoded-64-bytes"
}
```

**Invariants:**
- `version` is monotonically increasing; no rollback accepted.
- `author_did` must resolve to a valid DID document.
- Signature covers canonical JSON of `payload` (keys sorted; no whitespace).
- Any field change requires re-signing (new `version`).
