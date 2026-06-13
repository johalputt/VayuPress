# VayuPress

[![CI](https://github.com/johalputt/vayupress/actions/workflows/ci.yml/badge.svg)](https://github.com/johalputt/vayupress/actions/workflows/ci.yml)
[![Security](https://github.com/johalputt/vayupress/actions/workflows/security.yml/badge.svg)](https://github.com/johalputt/vayupress/actions/workflows/security.yml)
[![Go](https://img.shields.io/badge/go-1.23%2B-blue)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Constitution](https://img.shields.io/badge/constitution-v6.0%20P1--P26-blueviolet)](GOVERNANCE-CONSTITUTION.md)

> **Ultra-lightweight, ethical publishing infrastructure.**
> SQLite-first, zero-trust, no tracking. Sandboxed plugins, transactional event outbox, structured tracing, resource governance. Built to outperform WordPress, Hugo, and Ghost.

## Quick Start

```bash
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash
```

Or clone and deploy manually:

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress
sudo ./scripts/deploy-vayupress.sh
```

---

## What Is VayuPress?

VayuPress ("Vayu" — Sanskrit for wind/speed) is modern publishing infrastructure for developers, writers, and AI-assisted content engines who need:

- **Static-file speed** with dynamic flexibility
- **Single-VPS efficiency** — runs on 12 GB RAM / 6 vCPU / 250 GB NVMe
- **Total control** over content, hosting, and data
- **No vendor lock-in** — SQLite, Go, Nginx, open standards only
- **Zero telemetry** — no tracking, no analytics harvesting, no third-party calls
- **Production-grade reliability** — transactional outbox, idempotent event dispatch, graceful shutdown with 6-phase drain
- **Security-first** — sandboxed subprocess plugins, capability enforcement, SSRF protection, immutable WORM audit log
- **Full observability** — structured JSON logging, distributed tracing, correlation/causation IDs throughout

---

## Requirements

| Requirement | Detail |
|-------------|--------|
| Go | 1.23+ (build from source; deploy script installs 1.25) |
| CGO / SQLite3 | `gcc` required (`libsqlite3-dev` or bundled via `go-sqlite3`) |
| OS | Ubuntu 24.04 LTS (recommended); Linux kernel 5.x+ for sandbox features |
| RAM | 8 GB minimum, 12 GB recommended |
| CPU | 4 vCPU minimum, 6 vCPU recommended |
| Disk | 50 GB NVMe minimum, 250 GB for 1M+ posts with media |
| Access | Root or sudo for deploy script |

---

## Architecture

```
                     +----------------------------------+
                     |           Internet               |
                     +---------------+------------------+
                                     | HTTPS (443)
                     +---------------v------------------+
                     |    Nginx (TLS termination,        |
                     |    static files, gzip, CSP)       |
                     +---------------+------------------+
                                     | HTTP (127.0.0.1:8080)
             +---------------------------v------------------------------+
             |                VayuPress Go Binary                       |
             |  +---------+  +----------+  +-------------------+        |
             |  | Router  |  | Plugin   |  | Write Queue       |        |
             |  | (chi)   |  | Pool     |  | (async workers)   |        |
             |  +----+----+  +----+-----+  +--------+----------+        |
             |       |            |                 |                   |
             |  +----v------------v-----------------v-----------+       |
             |  |         SQLite (WAL mode)                     |       |
             |  |  articles · media · write_jobs · audit_log   |       |
             |  |  outbox_events · delivered_events            |       |
             |  +-----------------------------------------------+       |
             |                                                           |
             |  Lifecycle Manager --> Outbox Relay --> Event Bus         |
             |  Resource Watchdog --> Sandbox Pool --> Subprocess IPC    |
             +---------------------------+------------------------------+
                                         |
              +--------------------------+---------------------------+
              |                          |                           |
   +----------v----------+  +-----------v---------+  +-------------v------+
   |  Meilisearch        |  |  Isso               |  |  fail2ban / UFW    |
   |  (optional search)  |  |  (self-hosted       |  |  (firewall)        |
   |  <50ms p95          |  |   comments)         |  |                    |
   +---------------------+  +---------------------+  +--------------------+
```

---

## Internal Package Architecture

The application is a proper Go module (`github.com/johalputt/vayupress`) with a single entry point at `cmd/vayupress/` and domain logic split across `internal/` packages with compiler-enforced boundaries (ADR-0045).

| Package | Role | ADR |
|---------|------|-----|
| `cmd/vayupress` | Bootstrap, route wiring, graceful shutdown | ADR-0045 |
| `internal/api` | `ArticleService`, repository pattern, typed domain errors | ADR-0050 |
| `internal/auth` | JWT, CSRF, Argon2id hashing, rate-limit buckets | — |
| `internal/config` | Env-driven config, version compatibility validation | ADR-0040 |
| `internal/db` | SQLite init, WAL checkpoint, migrations via `embed.FS`, `ArticleRepo` | ADR-0033/0034 |
| `internal/events` | Typed event structs, `Envelope`, `Bus`, idempotent dispatch | ADR-0052 |
| `internal/health` | Structured health contracts (`/health/*` endpoints) | ADR-0041 |
| `internal/httputil` | `WriteJSON`, `WriteError`, `DecodeJSON` — thin HTTP primitives | ADR-0049 |
| `internal/lifecycle` | Ordered startup/shutdown with named phases | ADR-0051 |
| `internal/logging` | Structured JSON logging with correlation/causation fields | ADR-0053 |
| `internal/metrics` | Atomic metric counters, snapshot collection | — |
| `internal/outbox` | Transactional outbox relay — poll + dispatch event envelopes | ADR-0051 |
| `internal/plugins` | Hook registry, worker pool, subprocess plugin management | ADR-0032/0046 |
| `internal/queue` | SQLite-backed async write queue, dead-letter replay | ADR-0035 |
| `internal/render` | Article renderer, cache writer, CSS asset generator | ADR-0002 |
| `internal/resource` | Semaphore-based concurrency limiters, resource watchdog | ADR-0055 |
| `internal/sandbox` | Subprocess IPC pool, Linux seccomp/namespaces, capability enforcement | ADR-0056/0057 |
| `internal/search` | Meilisearch client with circuit breaker, SQLite fallback | ADR-0050 |
| `internal/trace` | Span-based tracing with correlation/causation IDs | ADR-0054 |

---

## Feature List (P1–P26)

### Core Publishing (P1–P8)
- RESTful JSON API for articles (CRUD with slugs, tags, full-text content)
- Async write queue — SQLite-backed, crash-safe, with dead-letter replay (ADR-0035)
- Sitemap XML, RSS feed, and robots.txt auto-generation
- In-memory render cache with static-file output via Nginx
- SQLite WAL mode with adaptive checkpointing (ADR-0033)
- Migration checksum drift detection — halts startup on tampering (ADR-0034)
- Immutable WORM audit log via SQLite `ABORT` triggers
- Plugin hook system with worker pool, panic recovery, and circuit-breaker disable
- Magic-number file-type verification for media uploads (JPEG/PNG/GIF/WebP/PDF)
- SSRF-safe outbound HTTP client (blocks loopback, 169.254.169.254, RFC-1918)
- CSP nonce per request, 7-header security baseline
- Argon2id credential hashing with constant-time comparison
- Pprof endpoint — localhost-only, rate-limited, audit-logged (ADR-0037)

### Security & Governance (P9–P13)
- Automated CI governance — 13 CI jobs, `ci-pass` gate (ADR-0044)
- Supply-chain secret scanning (TruffleHog), license compliance, shell linting
- Structured health contracts: `/health/live`, `/health/ready`, `/health/dependencies`, `/health/storage`, `/health/search`, `/health/queue` (ADR-0041)
- `/health/ethics` — machine-readable ethics compliance endpoint
- Ethical AI Charter in `ETHICS.md` (no training on user data, no telemetry)
- Backup restore automation with nightly integrity validation (ADR-0042)
- Source parity: `cmd/vayupress/` is the canonical multi-package Go module

### Multi-Package Architecture (P14–P19)
- Decomposed from monolithic `main.go` into 18 `internal/` packages (ADR-0045)
- `App` struct owns all mutable runtime state — no package-level globals (ADR-0047)
- Route domains and `ArticleService` with typed service errors (ADR-0048)
- Thin handler layer — handlers delegate to service, not DB (ADR-0049)
- Repository pattern: `ArticleRepo` interface backed by SQLite (ADR-0050)
- Integration test harness with `go test -race ./...`

### Event-Driven Reliability (P20–P22)
- Transactional outbox — events written atomically with article mutations (ADR-0051)
- `lifecycle.Manager` — ordered startup/shutdown with registered components
- `queue.Writer` interface — swappable queue backends
- Typed event structs: `ArticleCreated`, `ArticleUpdated`, `ArticleDeleted` (ADR-0052)
- Event `Envelope` with `EventID`, `CorrelationID`, `CausationID`, `OccurredAt`
- Idempotent dispatch via `delivered_events` deduplication table
- Versioned event types (`article.created.v1`, etc.) — forward-compatible

### Observability & Tracing (P22–P23)
- Structured JSON logging with `LogFields` — correlation/causation IDs on every line (ADR-0053)
- `internal/trace` — span-based tracing: `Start`, `SetAttribute`, `End` (ADR-0054)
- Correlation IDs threaded through HTTP requests, outbox dispatch, and event handlers
- Causation IDs linking child spans to parent events

### Resource Governance & Sandboxing (P24–P26)
- `internal/resource` — named semaphore limiters (`articles.write`, `plugin.exec`) (ADR-0055)
- Resource watchdog with configurable polling interval
- `internal/sandbox` — subprocess IPC pool for out-of-process plugin execution (ADR-0056)
- Linux seccomp filtering and namespace isolation for subprocess plugins
- Capability enforcement — subprocess plugins run with dropped privileges (ADR-0057)
- `plugins.RegisterSubprocess` — sandboxed plugin registration via manifest
- `plugins.ShutdownSubprocesses` — clean pool teardown during graceful shutdown

---

## API Endpoints Overview

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/articles` | List articles (paginated, filterable by tag) |
| `POST` | `/api/articles` | Create article (async write queue) |
| `GET` | `/api/articles/{slug}` | Get article by slug |
| `PUT` | `/api/articles/{slug}` | Update article |
| `DELETE` | `/api/articles/{slug}` | Delete article |
| `GET` | `/api/search?q=...` | Full-text search (Meilisearch or SQLite fallback) |
| `GET` | `/health/live` | Liveness probe |
| `GET` | `/health/ready` | Readiness probe |
| `GET` | `/health/dependencies` | Dependency health (DB, search, queue) |
| `GET` | `/health/storage` | Storage quota and utilization |
| `GET` | `/health/search` | Meilisearch status and circuit-breaker state |
| `GET` | `/health/queue` | Write queue depth and worker stats |
| `GET` | `/health/ethics` | Machine-readable ethics compliance |
| `GET` | `/sitemap.xml` | Auto-generated XML sitemap |
| `GET` | `/feed.xml` | Auto-generated RSS feed |
| `GET` | `/robots.txt` | Auto-generated robots.txt |
| `GET` | `/metrics` | Internal metrics snapshot (admin auth required) |

Full reference: [docs/API-REFERENCE.md](docs/API-REFERENCE.md)

---

## Deployment

### Automated (recommended)

```bash
# Download and run the deploy script
curl -sSL https://raw.githubusercontent.com/johalputt/vayupress/main/scripts/deploy-vayupress.sh | bash

# Dry-run first (inspect what will be installed)
bash scripts/deploy-vayupress.sh --dry-run

# Upgrade an existing installation
bash scripts/deploy-vayupress.sh --upgrade
```

The deploy script (`scripts/deploy-vayupress.sh`) handles:
- Go toolchain installation
- CGO/SQLite3 dependencies
- Binary build with `-ldflags="-s -w" -trimpath`
- Nginx configuration with TLS, CSP headers, and static-file serving
- systemd service unit
- Meilisearch installation (optional)
- Nightly backup cron with integrity validation
- fail2ban rules

### Manual Build

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress
go build -race ./...            # development build
go build -ldflags="-s -w" -trimpath ./cmd/vayupress  # production binary
```

---

## Development Setup

```bash
git clone https://github.com/johalputt/vayupress.git
cd vayupress

# Build
go build ./...

# Test (with race detector)
go test -race ./...

# Format check
gofmt -l .

# Vet
go vet ./...

# Source integrity check (verifies multi-package structure and build)
bash scripts/sync-source.sh

# All-in-one via make
make build test lint
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VAYU_DOMAIN` | `localhost` | Public domain name |
| `VAYU_PORT` | `8080` | HTTP listen port |
| `VAYU_DB_PATH` | `/var/lib/vayupress/vayupress.db` | SQLite database path |
| `VAYU_WORKER_COUNT` | `4` | Write queue worker goroutines |
| `VAYU_PLUGINS_ENABLED` | `false` | Enable plugin worker pool |
| `VAYU_PLUGIN_TIMEOUT_MS` | `2000` | Per-hook execution timeout |
| `VAYU_PLUGIN_MAX_CONCURRENT` | `8` | Max concurrent plugin executions |
| `STATIC_DIR` | `/var/www/vayupress/static` | Static asset output directory |
| `VAYU_DOCS_DIR` | `/var/www/vayupress/docs` | ADR docs output directory |
| `MEILI_URL` | `http://127.0.0.1:7700` | Meilisearch base URL |
| `MEILI_MASTER_KEY` | — | Meilisearch master key |

---

## Repository Structure

```
vayupress/
├── cmd/vayupress/          # Application entry point (main.go + app.go + routes.go + handlers)
│   ├── main.go             # Bootstrap, graceful shutdown, lifecycle wiring
│   ├── app.go              # App struct owning all mutable runtime state
│   ├── routes.go           # Route registration
│   ├── handlers_articles.go
│   ├── handlers_infra.go
│   ├── handlers_admin.go
│   └── middleware.go
├── internal/               # Domain packages (compiler-enforced boundaries)
│   ├── api/                # ArticleService, repository pattern
│   ├── auth/               # JWT, CSRF, rate-limiting
│   ├── config/             # Env-driven configuration
│   ├── db/                 # SQLite init, WAL, migrations
│   ├── events/             # Typed events, Envelope, Bus
│   ├── health/             # Structured health endpoints
│   ├── httputil/           # HTTP response primitives
│   ├── lifecycle/          # Ordered startup/shutdown
│   ├── logging/            # Structured JSON logging
│   ├── metrics/            # Atomic metric counters
│   ├── outbox/             # Transactional outbox relay
│   ├── plugins/            # Hook registry, worker pool, subprocess plugins
│   ├── queue/              # Async write queue (SQLite-backed)
│   ├── render/             # Article renderer, cache writer
│   ├── resource/           # Semaphore limiters, watchdog
│   ├── sandbox/            # Subprocess IPC, seccomp, capability enforcement
│   ├── search/             # Meilisearch client + SQLite fallback
│   └── trace/              # Span-based distributed tracing
├── scripts/
│   ├── deploy-vayupress.sh # Canonical self-contained installer
│   └── sync-source.sh      # Multi-package source integrity check
├── docs/
│   ├── adr/                # Architecture Decision Records (ADR-0001…ADR-0057)
│   ├── INSTALLATION.md
│   ├── API-REFERENCE.md
│   ├── ARCHITECTURE.md
│   ├── DEVELOPMENT.md
│   ├── OPERATIONS.md
│   ├── THREAT-MODEL.md
│   └── ...
├── go.mod / go.sum         # Pinned dependencies
├── Makefile
├── GOVERNANCE-CONSTITUTION.md
├── CHANGELOG.md
├── SECURITY.md
├── ETHICS.md
└── CONTRIBUTING.md
```

---

## Performance

Target: ≤50ms p95 latency on a 4-vCPU / 8 GB VPS under sustained load.

| Metric | Target | Architecture Decision |
|--------|--------|-----------------------|
| Article page p95 | <50ms | Nginx static-file serving + in-memory render cache |
| Search p95 | <50ms | Meilisearch with pre-warmed index |
| API write p95 | <100ms | SQLite WAL + async write queue |
| Cold start | <500ms | Single static binary, no JVM/interpreter |
| Binary size (gzip) | <45 MB | `-ldflags="-s -w" -trimpath` |

Run benchmarks locally: `make bench`

---

## ADR Index

Architecture Decision Records live in `docs/adr/`. Key decisions:

| ADR | Title |
|-----|-------|
| [ADR-0001](docs/adr/ADR-0001-sqlite-first.md) | SQLite-first doctrine |
| [ADR-0002](docs/adr/ADR-0002-self-hosted-fonts.md) | Self-hosted fonts (zero external requests) |
| [ADR-0032](docs/adr/ADR-0032-plugin-pool-waitgroup.md) | Plugin pool WaitGroup + context cancellation |
| [ADR-0033](docs/adr/ADR-0033-wal-adaptive-checkpoint.md) | WAL adaptive checkpoint |
| [ADR-0034](docs/adr/ADR-0034-migration-checksum-drift.md) | Migration checksum drift detection |
| [ADR-0035](docs/adr/ADR-0035-dead-letter-queue-safety.md) | Dead-letter queue safety |
| [ADR-0036](docs/adr/ADR-0036-csp-nonce.md) | CSP nonce per request |
| [ADR-0037](docs/adr/ADR-0037-pprof-rate-limit.md) | Pprof rate-limiting + audit log |
| [ADR-0038](docs/adr/ADR-0038-vacuum-cooldown.md) | VACUUM cooldown guard |
| [ADR-0039](docs/adr/ADR-0039-deploy-sourced-components.md) | Deploy sourced component architecture |
| [ADR-0040](docs/adr/ADR-0040-config-versioning.md) | Config versioning + compatibility contracts |
| [ADR-0041](docs/adr/ADR-0041-health-contracts.md) | Structured health contracts |
| [ADR-0042](docs/adr/ADR-0042-backup-restore-automation.md) | Backup restore automation |
| [ADR-0043](docs/adr/ADR-0043-integration-tests.md) | Integration test suite |
| [ADR-0044](docs/adr/ADR-0044-repository-decomposition.md) | Repository decomposition + source parity |
| [ADR-0045](docs/adr/ADR-0045-internal-package-decomposition.md) | Internal package decomposition |
| [ADR-0046](docs/adr/ADR-0046-runtime-architecture-service-boundaries.md) | Runtime architecture + service boundaries |
| [ADR-0047](docs/adr/ADR-0047-app-container-handler-refactor.md) | App container + handler refactor |
| [ADR-0048](docs/adr/ADR-0048-route-domains-service-extraction.md) | Route domains + service extraction |
| [ADR-0049](docs/adr/ADR-0049-thin-handlers-service-boundaries.md) | Thin handlers + service boundaries |
| [ADR-0050](docs/adr/ADR-0050-persistence-transport-maturity.md) | Repository pattern + persistence maturity |
| [ADR-0051](docs/adr/ADR-0051-transactional-consistency-event-reliability.md) | Transactional outbox + event reliability |
| [ADR-0052](docs/adr/ADR-0052-idempotency-event-evolution.md) | Idempotent dispatch + event evolution |
| [ADR-0053](docs/adr/ADR-0053-observability-correlation-architecture.md) | Observability + correlation architecture |
| [ADR-0054](docs/adr/ADR-0054-structured-tracing-execution-spans.md) | Structured tracing + execution spans |
| [ADR-0055](docs/adr/ADR-0055-resource-governance-execution-isolation.md) | Resource governance + execution isolation |
| [ADR-0056](docs/adr/ADR-0056-process-isolation-runtime-sandboxing.md) | Process isolation + runtime sandboxing |
| [ADR-0057](docs/adr/ADR-0057-security-sandboxing-capability-enforcement.md) | Security sandboxing + capability enforcement |

---

## Governance

VayuPress is governed by the [VayuPress Governance Constitution v6.0](GOVERNANCE-CONSTITUTION.md).

**Priority order (non-negotiable):**
Security = Data Integrity > Ethical Compliance > Reliability > Simplicity > Performance > DX > Feature Velocity

All 26 Prompts (governance milestones) are CI-enforced. Every push must pass the `ci-pass` gate.

---

## Documentation

| Document | Description |
|----------|-------------|
| [INSTALLATION](docs/INSTALLATION.md) | Full installation guide |
| [ARCHITECTURE](docs/ARCHITECTURE.md) | System design and data flow |
| [API-REFERENCE](docs/API-REFERENCE.md) | REST API reference |
| [DEVELOPMENT](docs/DEVELOPMENT.md) | Local development setup |
| [OPERATIONS](docs/OPERATIONS.md) | Runbooks and incident response |
| [THREAT MODEL](docs/THREAT-MODEL.md) | Security threat analysis |
| [SECURITY](SECURITY.md) | Vulnerability disclosure policy |
| [CONTRIBUTING](CONTRIBUTING.md) | How to contribute |
| [GOVERNANCE](GOVERNANCE.md) | Governance overview |
| [ETHICS](ETHICS.md) | Ethical principles and AI charter |
| [CHANGELOG](CHANGELOG.md) | Version history |
| [UPGRADING](UPGRADING.md) | Upgrade procedures |

---

## Contact

| Purpose | Email |
|---------|-------|
| General | hello@vayupress.com |
| Support | support@vayupress.com |
| Security | security@vayupress.com |
| Ethics violations | ethics@vayupress.com |
| Governance / RFCs | governance@vayupress.com |

---

## License

MIT — see [LICENSE](LICENSE).

> *"Stay lightweight. Stay fast. Stay secure. Stay disciplined. Stay ethical."*
