# Stability Matrix — VayuPress

**Version:** 1.0.0-omega  
**Date:** 2026-06-13  
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

## Go Package Stability

| Package | Stability | Notes |
|---------|-----------|-------|
| `internal/db` | **Stable** | Config, Open, WALStats |
| `internal/logging` | **Stable** | LogJSON, LogInfo, LogFields |
| `internal/migrations` | **Stable** | Migrator.Up, Migrator.Down, Migrator.Status |
| `internal/sandbox` | **Stable** | Manifest, SubprocessPlugin, SetupConfinement |
| `internal/sandbox` (confinement) | **Beta** | Seccomp filter may gain syscalls; API stable |
| `internal/signing` | **Stable** | Sign, Verify, ArticlePayload, SignedArticle |
| `internal/merkle` | **Stable** | New, Tree.Proof, Verify |
| `internal/trace` | **Stable** | Span, Store, SaveSpan, Query |
| `internal/events/schema` | **Beta** | Registry.Register, Registry.Validate |
| `internal/ws` | **Stable** | Hub, New, Broadcast, Message |
| `internal/health` | **Stable** | Checker, ServeHTTP |
| `internal/metrics` | **Stable** | Exported metric names |
| `internal/did` | **Beta** | DIDDocument, Authenticator — spec evolving |
| `internal/cluster` | **Beta** | Node, IsLeader — consensus upgrade planned |
| `internal/federation` | **Experimental** | ActivityPub; HTTP Signature not yet complete |
| `internal/ai` | **Experimental** | LocalEmbedder, AgentRunner, Policy |
| `internal/search/semantic` | **Experimental** | Embedding model may change |
| `internal/governance/rfc` | **Beta** | Voting logic stable; UI layer experimental |
| `internal/graph` | **Experimental** | Knowledge graph schema may change |
| `internal/storage` | **Beta** | Backend interface stable; FallbackBackend beta |
| `internal/archive` | **Beta** | Snapshot stable; IPFS/Arweave stubs experimental |
| `internal/plugins` | **Beta** | Registry stable; bundle format beta |
| `internal/queue` | **Stable** | Enqueue, Dequeue, Dead-letter |
| `internal/outbox` | **Beta** | Transactional outbox wiring |
| `internal/config` | **Stable** | Config struct; version field |
| `internal/lifecycle` | **Stable** | Shutdown hooks |
| `internal/resource` | **Beta** | Cgroup integration |

---

## HTTP API Stability

| Endpoint | Stability | Notes |
|----------|-----------|-------|
| `GET /healthz` | **Stable** | Response schema frozen |
| `GET /metrics` | **Stable** | Prometheus exposition format |
| `GET /api/v1/articles` | **Stable** | Pagination params stable |
| `POST /api/v1/articles` | **Stable** | Request/response schema stable |
| `GET /api/v1/articles/:id` | **Stable** | |
| `GET /.well-known/webfinger` | **Beta** | ActivityPub WebFinger |
| `POST /federation/inbox` | **Experimental** | HTTP Signature verification pending |
| `GET /federation/outbox` | **Experimental** | |
| `GET /api/v1/search` | **Beta** | Query params may grow |
| `GET /api/v1/search/semantic` | **Experimental** | Embedding model not frozen |
| `GET /api/v1/plugins` | **Beta** | |
| `POST /api/v1/plugins` | **Beta** | |
| `GET /debug/pprof/` | **Internal** | Rate-limited; not a public API |
| `GET /api/v1/governance/rfcs` | **Beta** | |
| `POST /api/v1/governance/rfcs/:id/vote` | **Beta** | |

---

## Event Schema Stability

| Event type | Stability | Schema version |
|------------|-----------|----------------|
| `article.published` | **Stable** | v1 |
| `article.updated` | **Stable** | v1 |
| `article.deleted` | **Stable** | v1 |
| `plugin.started` | **Beta** | v1 |
| `plugin.crashed` | **Beta** | v1 |
| `plugin.quarantined` | **Beta** | v1 |
| `federation.received` | **Experimental** | v1-draft |
| `governance.vote.cast` | **Beta** | v1 |
| `archive.snapshot.created` | **Beta** | v1 |

---

## Plugin Manifest Stability

| Manifest field | Stability | Notes |
|----------------|-----------|-------|
| `name` | **Stable** | |
| `executable` | **Stable** | |
| `executable_hash` | **Stable** | |
| `hooks` | **Stable** | |
| `timeout` | **Stable** | |
| `max_restarts` | **Stable** | |
| `env` | **Stable** | |
| `allow_network` | **Stable** | |
| `allowed_read_paths` | **Stable** | |
| `allowed_write_paths` | **Stable** | |
| `confine_mounts` | **Beta** | Requires Linux CAP_SYS_ADMIN |
| `drop_caps` | **Beta** | Plugin-side enforcement |
| `memory_limit_mb` | **Beta** | cgroup v2 only |
| `cpu_quota` | **Beta** | cgroup v2 only |
| `run_as` | **Beta** | |

---

## Database Schema Stability

| Table | Stability | Notes |
|-------|-----------|-------|
| `schema_migrations` | **Stable** | Migration engine internal |
| `articles` | **Stable** | Core content table |
| `users` | **Stable** | |
| `sessions` | **Stable** | |
| `events` | **Stable** | Outbox events |
| `dead_letters` | **Stable** | DLQ table |
| `trace_spans` | **Beta** | Schema may gain columns |
| `plugin_registry` | **Beta** | |
| `governance_rfcs` | **Beta** | |
| `governance_votes` | **Beta** | |
| `federation_actors` | **Experimental** | |
| `federation_activities` | **Experimental** | |
| `federation_blocks` | **Experimental** | |

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
