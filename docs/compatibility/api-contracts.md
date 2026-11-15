# API Contracts — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## Versioning Policy

VayuPress follows [Semantic Versioning 2.0.0](https://semver.org/):

- **MAJOR** — incompatible API changes (requires migration guide)
- **MINOR** — new backwards-compatible functionality
- **PATCH** — backwards-compatible bug fixes

Current version: `.release-version` at the repository root, compiled into the binary as `Version` in `cmd/vayupress/main.go` (reported by `/health`).

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
  "hook":           "article.create",
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
  "type":           "article.created",
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
- Events are append-only; published events are never modified or deleted.

---

## Trace Span Contract

Finished spans are kept in memory, in a ring of the last 2000
(`internal/trace/recorder.go`). They are never written to the database, so
they do not survive a restart, and they are served by two administrator
endpoints: `GET /api/v1/admin/traces?limit=N` (the newest N, default 100,
at most 2000) and `GET /api/v1/admin/traces/{trace_id}` (every span of one
trace). Each span is:

```json
{
  "trace_id":       "9b2f4c1e-5d7a-4c33-9f0e-2a6b8d1c7e44",
  "span_id":        "3f6c2a9d81e04b7c5a1d9e2f60b8c4d7",
  "parent_span_id": "a1c94e7b0d2f36580e9b7a4c1d6f2e38",
  "operation":      "http.GET /api/v1/articles",
  "start_time":     "2026-06-13T09:00:00.000Z",
  "end_time":       "2026-06-13T09:00:00.042Z",
  "duration_ms":    42,
  "status":         "ok",
  "error":          "database is locked",
  "attributes":     {"http.method": "GET", "http.path": "/api/v1/articles", "http.status": "200"}
}
```

- `trace_id` is the request's correlation id, so every span of one request
  shares it; a span started outside a request gets a random one.
- `span_id` is 16 random bytes in hex.
- `parent_span_id`, `error` and `attributes` are left out when empty.
- `status` is `ok`, `error` or `unset`; a request span is `error` when the
  response status is 500 or above.
- `operation` is `http.<METHOD> <path>` for a request,
  `outbox.dispatch.<event type>` for a dispatched event, and
  `ArticleService.<Create|Update|Delete|Get|List>` for the article service.
- Attribute values are strings. A request span never carries the client's
  address unless `VAYU_DEBUG_REQUESTS=1` is set.

`TestTheSpanContractIsTheShapeServed` (`internal/trace`) marshals a span
the way the endpoints do and fails if a field here is missing from it, or a
field it sends is missing here.
