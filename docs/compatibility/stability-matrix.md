# Stability Matrix — VayuPress

**Version:** 1.0.0-omega  
**Date:** 2026-09-25  
**Applies to:** All interfaces, APIs, schemas, and protocols in VayuPress

---

## Stability Levels

| Level | Meaning | Compatibility guarantee |
|-------|---------|------------------------|
| **Stable** | Production-ready; guaranteed not to break | No breaking changes within a major version |
| **Beta** | Functional but may change | Breaking changes announced 2 releases ahead |
| **Experimental** | Actively evolving | May change or be removed without notice |
| **Deprecated** | Scheduled for removal | Removed after 2 major versions |
| **Internal** | Not a public contract | No compatibility guarantee; may change any commit |

---

## Go Packages

Everything under `internal/` is **Internal**. Go refuses an import of it from
outside this module, so no package there is a compatibility contract and any
of it may change in any commit. The contracts are the surfaces below.

---

## HTTP API Stability

| Endpoint | Stability | Notes |
|----------|-----------|-------|
| `GET /health`, `GET /health/ready` | **Stable** | Liveness and readiness |
| `GET /metrics` | **Stable** | Prometheus exposition format |
| `GET /api/v1/articles` | **Stable** | Pagination params stable |
| `POST /api/v1/articles` | **Stable** | Request/response schema stable |
| `GET /api/v1/articles/{slug}` | **Stable** | |
| `PUT /api/v1/articles/{slug}` | **Stable** | |
| `GET /api/v1/search` | **Beta** | Query params may grow |
| `GET /debug/pprof/` | **Internal** | Rate-limited; not a public API |

---

## Event Stability

Delivered to webhooks (with a `.v1` suffix) and to the live event stream.

| Event type | Stability | Schema version |
|------------|-----------|----------------|
| `article.created` | **Stable** | v1 |
| `article.updated` | **Stable** | v1 |
| `article.deleted` | **Stable** | v1 |

---

## Plugin Manifest Stability

The plugin and theme manifests, their fields and their versioning are specified
in [`vcb.md`](vcb.md). The plugin IPC request and response shapes are frozen
by `internal/compat/golden_test.go`.

---

## Database Schema Stability

| Table | Stability | Notes |
|-------|-----------|-------|
| `schema_migrations` | **Stable** | Migration engine internal |
| `articles` | **Stable** | Core content table |
| `users` | **Stable** | |
| `sessions` | **Stable** | |
| `event_outbox` | **Stable** | Transactional outbox |
| `write_jobs` | **Stable** | Async write queue; dead letters keep `dead_reason` |

---

## Deprecation Log

| Item | Deprecated in | Removed in | Replacement |
|------|--------------|-----------|-------------|
| _(none yet)_ | — | — | — |

---

## How to Propose a Breaking Change

1. Open an RFC using [`docs/rfc-template.md`](../rfc-template.md).
2. RFC must pass constitutional vote (≥ 2/3 supermajority).
3. Deprecation notice in next minor release.
4. Breaking change only in next major release.
5. Migration guide in `docs/migrations/` before breaking release.
