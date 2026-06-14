# Changelog

All notable changes to VayuPress are documented here.

Format: [Added / Changed / Deprecated / Fixed / Security / Upgrade Notes / Ethical Updates]

---

## [Unreleased] — Theme & Site Settings Control Panel

### Added
- **Theme & Site Settings control panel** (`/admin/theme`): operator-editable site
  identity (name, tagline, description, author), light/dark palette, custom CSS, and
  declarative head/SEO capabilities. CSRF-protected, mode-gated (blocked in
  read-only/quarantined), audit-logged (`component: "theme"`).
- **`internal/settings`** package: thread-safe key/value store over the new
  `site_settings` table (migration **006**, content-checksummed), 30 s read cache,
  transactional `SetMany`, allowlisted keys.
- **`/theme.css`**: dynamic per-site palette + custom CSS served same-origin
  (ETag + `max-age=60`) so it satisfies `style-src 'self'` — no inline `<style>`.
- **Public theme toggle**: sun/moon switch in the site header, preference persisted
  to `localStorage`, served as a same-origin script (`/static/js/theme-toggle.js`)
  so it needs no CSP nonce.
- **CSP violation reporting**: `report-uri /csp-report` + `POST /csp-report`
  endpoint, `vayupress_csp_violations_total` metric, structured per-violation logs.
  Hardened against abuse: per-IP rate limit (`auth.AllowCSPReport`, 30/min,
  over-limit dropped before counting/logging), 16 KB body cap, strict structured
  parsing, and short-window duplicate suppression on `(directive|blocked-uri)`.
- **Report-Only CSP mode**: `CSP_REPORT_ONLY=true` sends
  `Content-Security-Policy-Report-Only` instead of the enforcing header, so a
  candidate policy can be observed via `/csp-report` in staging before enforcing.
  Enforcement posture is now operationally visible (not hidden in an env var): a
  `csp.policy` boot entry in the Unified Operational Timeline, a `csp_mode` field
  on `/api/v1/admin/timeline` and `/api/v1/stats`.
- **CSP report attribution**: violation logs are tagged with the receiving
  deployment build version (`build=`) for release attribution — browser CSP
  reports carry no session/correlation context, so build version is the
  meaningful debugging anchor for a frontend change.
- **CSP violations in the Unified Operational Timeline**: accepted violations are
  recorded in a bounded process-local ring and rendered as `csp.violation` entries
  in the live timeline (Ω8/Ω10), placing frontend-governance signals in the same
  causal narrative as mode transitions and faults — visible spatially, not just as
  a metric counter.
- **Timeline event provenance**: every timeline entry now carries structured
  provenance (`source` subsystem, `actor`, causal `cause`, `correlation_id`,
  `build`, `policy_rev`) in the `/api/v1/admin/timeline` JSON, plus an
  envelope-level `provenance` (build + policy revision). Fields are populated only
  where genuinely known — synthesized governance entries leave `correlation_id`
  empty rather than fabricate one — so the timeline becomes honest, machine-readable
  runtime memory rather than a flat string log.
- **Formal operational severity taxonomy** (`internal/severity`): a fixed, totally
  ordered vocabulary — OBSERVE · NOTICE · WARN · VIOLATION · ESCALATION ·
  CONTAINMENT · CRITICAL — where each level defines its meaning, operator
  expectation, escalation behavior, timeline class, topology colour, and policy
  interaction. Timeline events now carry a `severity` taxonomy name (single
  auditable classifier in `timelineSeverity`); the CSP violation log adopts the
  `VIOLATION` level; and `GET /api/v1/admin/severity` publishes the full taxonomy
  so the vocabulary is self-documenting and auditable.
- **Causal lineage on the timeline**: each event now carries a deterministic,
  render-stable `provenance.id` and a `provenance.parent_id`, turning the flat
  narrative into a traversable operational graph (boot chain → governance arming →
  fault/CSP/mode escalation ancestry → posture). Links are structural and honest —
  derived from genuine subsystem relationships, computed over the full set before
  display truncation so ancestors keep stable identity.
- **Event retention doctrine** (`docs/governance/event-retention.md`): explicit
  classification of every event store as ephemeral / durable / replayable /
  audit-grade / operator-cognition, with the governing rule that a signal's
  retention class must match its purpose (the timeline is a projection, not a
  ledger; the CSP ring is ephemeral with a durable log/metric shadow).
- **WCAG AA contrast warnings**: saving the palette returns advisory (non-blocking)
  warnings when a primary colour falls below 4.5:1 on its page background. The
  shipped **default light primary changed from `#0d9488` (3.6:1) to `#0f766e`
  (teal-700, 5.2:1)** so the defaults themselves clear AA.

### Security
- **Declarative head capabilities replace raw `<head>` HTML**: head/SEO inputs are
  an allowlisted, validated, escaped `<meta>` subset (keywords, theme-color, robots,
  Google/Bing verification). Raw head HTML is no longer accepted — meta-refresh
  redirects, external beacons, and `<base>` hijacks (which CSP does not fully cover)
  are structurally impossible.
- **Dynamic theme served as a stylesheet, not inline** — preserves the strict
  `style-src 'self'` CSP (no `unsafe-inline`).
- Palette colours and verification tokens are validated server-side
  (`#rgb`/`#rrggbb`, allowlists, token regex) before persistence.

---

## [1.0.0-p26] — 2026-06-13

### Added (Prompt 26 — Security Sandboxing & Capability Enforcement)
- **`internal/sandbox` capability enforcement**: subprocess plugins now run with explicitly
  dropped Linux capabilities via `PR_SET_SECCOMP` and namespace isolation (ADR-0057)
- **`plugins.RegisterSubprocess`**: registers sandboxed subprocess plugins via `sandbox.Manifest`;
  launches isolated worker processes using the subprocess IPC pool
- **`plugins.ShutdownSubprocesses`**: clean teardown of all subprocess pools during graceful shutdown
- **`subprocess_linux.go` / `subprocess_other.go`**: platform-conditional sandbox application
  (`//go:build !linux` guard on non-Linux stub)
- **ADR-0057** — Security Sandboxing & Capability Enforcement

---

## [1.0.0-p25] — 2026-06-13

### Added (Prompt 25 — Process Isolation & Runtime Sandboxing)
- **`internal/sandbox` package**: subprocess IPC pool for out-of-process plugin execution (ADR-0056)
- **`sandbox.Pool`**: manages a pool of sandboxed worker processes with health checking and restart
- **`sandbox.Manifest`**: declarative plugin manifest (name, binary path, allowed syscalls, run-as user)
- **Linux seccomp filtering**: `applyProcAttr` wires seccomp allowlist to subprocess `exec.Cmd`
- **`SubprocessStats`**: runtime stats for all registered subprocess pools
- **ADR-0056** — Process Isolation & Runtime Sandboxing

---

## [1.0.0-p24] — 2026-06-13

### Added (Prompt 24 — Resource Governance & Execution Isolation)
- **`internal/resource` package**: named semaphore-based concurrency limiters (ADR-0055)
- **`resource.Register`**: registers a named limiter (`articles.write`, `plugin.exec`) with a cap
- **`resource.Watchdog`**: periodic goroutine monitoring limiter saturation; logs warnings
- **`resource.Global`**: package-level watchdog wired in `main.go`
- Plugin worker `run()` enforces `plugin.exec` concurrency ceiling via `resource.Get`
- **ADR-0055** — Resource Governance & Execution Isolation

---

## [1.0.0-p23] — 2026-06-13

### Added (Prompt 23 — Structured Tracing & Execution Spans)
- **`internal/trace` package**: span-based tracing with `Start`, `SetAttribute`, `End` (ADR-0054)
- **Correlation and causation IDs on every span**: `WithCorrelationID`, `WithCausationID` context helpers
- **Outbox dispatch tracing**: every outbox event dispatch opens a `outbox.dispatch.<type>` span
- **Span attributes**: `event_id`, `event_type`, `causation_id` recorded on dispatch spans
- **ADR-0054** — Structured Tracing & Execution Spans

---

## [1.0.0-p22] — 2026-06-13

### Added (Prompt 22 — Observability & Correlation Architecture)
- **`internal/logging` structured fields**: `LogFields` struct with `CorrelationID`, `CausationID`,
  `Level`, `Component`, `Msg`, `Error` — all logs emit valid JSON (ADR-0053)
- **Correlation IDs propagated end-to-end**: from HTTP middleware through write queue, outbox
  dispatch, and event bus handlers
- **`logging.LogJSON`**: type-safe structured log emission replacing ad-hoc `fmt.Sprintf` chains
- **ADR-0053** — Observability & Correlation Architecture

---

## [1.0.0-p21] — 2026-06-13

### Added (Prompt 21 — Event Envelopes, Idempotent Dispatch, Versioned Event Types)
- **`events.Envelope`**: wrapper struct with `EventID` (UUID), `EventType` (versioned string),
  `CorrelationID`, `CausationID`, `OccurredAt`, and `Payload` (raw JSON) (ADR-0052)
- **Idempotent dispatch**: `delivered_events` table deduplicates events by `event_id`;
  replayed outbox rows are ignored instead of double-dispatched
- **Versioned event type strings**: `article.created.v1`, `article.updated.v1`,
  `article.deleted.v1` — forward-compatible via envelope type routing
- **`events.Bus` type dispatch**: outbox relay unmarshals envelope, routes by `EventType`,
  publishes typed event to the in-process event bus
- **ADR-0052** — Idempotency & Event Evolution

---

## [1.0.0-p20] — 2026-06-13

### Added (Prompt 20 — Transactional Outbox, Queue Writer Interface, Lifecycle Manager)
- **`internal/outbox` package**: transactional outbox relay — polls `outbox_events` table,
  dispatches events atomically written alongside article mutations (ADR-0051)
- **`outbox.NewRelay`**: wires dispatch function and done channel; started via `lifecycle.Manager`
- **`internal/lifecycle` package**: ordered startup/shutdown with named components;
  `lc.Register(name, startFn, stopFn)` — components start in order, shut down in reverse
- **`queue.Writer` interface**: swappable queue backend; `queue.NewSQLiteWriter` is the
  default production implementation
- **`outbox_events` migration**: events table written transactionally with article mutations
- **ADR-0051** — Transactional Consistency & Event Reliability

---

## [1.0.0-p19] — 2026-06-12

### Added (Prompt 19 — Repository Pattern, Typed Events, Search Service, httputil)
- **`internal/api` package**: `ArticleService` with `Repo` (interface), `Queue` (`queue.Writer`),
  and `StorageCheckFn` — fully injectable, no direct DB references in handlers (ADR-0050)
- **`db.ArticleRepo`**: concrete SQLite implementation of the `Repo` interface
- **`internal/events` package**: typed domain events (`ArticleCreated`, `ArticleUpdated`,
  `ArticleDeleted`) and `Bus` (in-process pub/sub)
- **`internal/search`**: `MeiliService` with circuit breaker, `WaitReady`, `ConfigureIndex`,
  `Ping` — SQLite fallback activates when Meilisearch is unavailable
- **`internal/httputil`**: `WriteJSON`, `WriteError`, `DecodeJSON` — thin HTTP primitives
  eliminating duplication across handlers (ADR-0049)
- **`a.registerEventHandlers()`**: domain event handlers wired after all services are ready
- **ADR-0050** — Persistence & Transport Maturity

---

## [1.0.0-p18] — 2026-06-12

### Added (Prompt 18 — Thin Handlers, Service Error Layer, Integration Test Harness)
- **Thin handler contract**: handlers call service, marshal response, set status code —
  no business logic or direct SQL (ADR-0049)
- **Service-layer typed errors**: `api.ErrNotFound`, `api.ErrConflict`, `api.ErrStorageQuota`,
  `api.ErrValidation` — handlers map errors to HTTP status codes centrally
- **Integration test harness**: `go test -race ./...` passes; per-package test files cover
  happy-path and error scenarios without test databases
- **ADR-0049** — Thin Handlers & Service Boundaries

---

## [1.0.0-p17] — 2026-06-12

### Added (Prompt 17 — Route Domains, ArticleService, Centralised Validation)
- **Route domain separation**: `handlers_articles.go`, `handlers_infra.go`, `handlers_admin.go`
  — each file owns one domain; `routes.go` wires chi router (ADR-0048)
- **`ArticleService`** extracted from `main.go`: create/update/delete/get with validation,
  storage quota check, and write-queue dispatch
- **Centralised validation**: slug format (regex), required fields, tag sanitization —
  all in the service layer, not scattered across handlers
- **ADR-0048** — Route Domains & Service Extraction

---

## [1.0.0-p16] — 2026-06-12

### Added (Prompt 16 — App Container & Handler Refactor)
- **`App` struct**: 10 package-level mutable globals replaced by explicit fields on `*App`; all runtime state is owned and auditable
- **28 handlers as `*App` methods**: route registration uses method values (`a.handleXxx`); handlers depend on explicit fields, not implicit globals
- **Filesystem migrations**: SQL extracted to `internal/db/migrations/*.sql`, loaded via `embed.FS`, checksums preserved
- **`staticcheck` in CI**: static analysis on every push; two numeric HTTP status literal issues fixed on introduction
- **ADR-0047** — App Container & Handler Refactor

---

## [1.0.0-p15] — 2026-06-12

### Added (Prompt 15 — Runtime Architecture & Service Boundaries)
- **`internal/plugins` package**: plugin pool (ADR-0032 hardening) extracted from `main.go`
  into a standalone, independently testable package with `Registry`, `Manager`, `HookFunc`.
  `main.go` plugin section reduced from ~150 lines to ~15 lines.
- **Unit tests for all internal packages** (`go test -race ./internal/...` passes):
  `metrics`, `auth`, `logging`, `config`, `plugins`, `health`, `queue`.
- **ADR-0046** — Runtime Architecture & Service Boundaries.

### Fixed
- SQLite migration compatibility: removed `IF NOT EXISTS` from `ALTER TABLE ADD COLUMN`
  in migrations 003 and 004 (not supported on older SQLite versions present in CI).

---

## [1.0.0-p14] — 2026-06-12

### Added (Prompt 14 — Internal Package Decomposition)
- Split `cmd/vayupress/main.go` into 8 `internal/` packages with compiler-enforced boundaries.
- **ADR-0045** — Internal Package Decomposition.

---

## [1.0.0-p13] — 2026-06-12

### Added (Prompt 13 — Repository Decomposition & Tooling Maturity)
- **Real Go source tree**: the application is now committed at `cmd/vayupress/main.go`
  with committed `go.mod`/`go.sum` (pinned, Go 1.23). `git clone && go build ./...`
  works; IDEs index the code; `go vet`/`go test`/`gofmt`/`govulncheck` all run.
- **Source parity enforcement**: `scripts/sync-source.sh` mirrors the canonical deploy
  heredoc to `cmd/vayupress/main.go`; `--check` mode runs in CI and fails on drift.
- **Native Go CI** (`go-native` job): `go vet`, `gofmt -l`, `go build -race`,
  `go test -race`, and `govulncheck` on every push.
- **Constitution Prompt 13** added; `check-governance` now verifies Prompts 1–13.
- **ADR-0044** — Repository Decomposition & Source Parity.

### Changed
- Canonical Go source normalized with `gofmt` (deploy script grew ~4.3k → ~5.5k lines
  as compact one-liners were expanded for tool-compatibility).
- Deploy script pins exact dependency versions (no `@latest`): `chi@v5.1.0`,
  `go-sqlite3@v1.14.45`, `bluemonday@v1.0.27`, `gobreaker@v1.0.0`, `cors@v1.11.1`,
  `x/crypto@v0.39.0`, `x/net@v0.41.0` — reproducible and govulncheck-clean.
- **Toolchain moved to latest stable Go 1.25.11** (deploy `GO_VERSION`, CI
  `setup-go: '1.25'`) so the build carries the newest standard-library security
  fixes; `go.mod` keeps a `go 1.23.0` minimum directive.
- `Makefile`: `build`/`dev` target `./cmd/vayupress`; added `sync` and `sync-check`
  targets; `build` now depends on `sync-check`; `check-adrs` requires ADR-0044;
  `check-governance` verifies Prompt 13.

### Fixed
- **Reachable vulnerabilities (govulncheck)**: flagged `golang.org/x/net/html` (via
  bluemonday) and several standard-library symbols (`crypto/x509.Verify`,
  `html/template.Execute`, `net/textproto.ReadMIMEHeader`, `net.Listen`,
  `net.Resolver.LookupIPAddr`). Fixed by bumping `x/net`→v0.41.0 / `x/crypto`→v0.39.0
  and building with the latest stable Go (1.25.11). Security outranks Simplicity per
  the Constitution priority order.
- **Latent deploy failure**: deploy script previously used `go get ...@latest`, which
  would pull `chi v5.3.0` onto the install unpredictably. Now pinned to exact versions.

---

## [1.0.0-p12.1] — 2026-06-12

### Fixed

#### Engine (`scripts/deploy-vayupress.sh` — bug fixes)
- **Plugin pool shutdown ordering**: `close(pluginQueue)` now precedes `workerPluginWg.Wait()` — range-loop workers exit cleanly instead of blocking indefinitely
- **Memory leak — bucket sweeper**: `startBucketSweeper()` goroutine evicts stale entries from `authFailBuckets`, `rateBuckets`, `pprofLimiters`, and `purgeLimiters` every 10 minutes; bounds memory on long-running instances with rotating IPs
- **CSP `style-src 'unsafe-inline'` removed**: `style-src` is now `'self'` only — all styles must be served from static files; inline style injection vector eliminated
- **Health contract schema versioning**: all `/health/*` responses now include `"schema_version": "1"` — automation consumers can detect breaking API shape changes
- **Lifecycle manager formalized**: shutdown sequence now has six named phases: (1) stop ingress, (2) drain queue, (3) stop plugins, (4) WAL checkpoint, (5) flush metrics, (6) close DB
- **Version header corrected**: all stale `v1.0.0-p8` references in banner, step labels, and header comments updated to `v1.0.0-p12`

#### Documentation
- `README.md` — CI/Security/Go/License/Constitution badges added; ASCII architecture diagram; performance targets table; expanded docs links
- `UPGRADING.md` — new file: version-specific upgrade notes, rollback procedure, zero-downtime upgrade steps, full health verification checklist
- `docs/operations/disaster-recovery.md` — new file: DR-01 through DR-06 runbooks (server loss, DB corruption, migration drift, TLS expiry, search failure, backup verification)
- `Makefile` — fixed `SRC_DIR` from hardcoded `/var/www/vayupress/src` to `SRC_DIR ?= .`
- `.gitignore` — added `coverage.out`, `coverage.html`, `*.coverprofile`, `bin/`

---

## [1.0.0-p12] — 2026-06-12

### Added (Prompts 9–12)

#### Engine (`scripts/deploy-vayupress.sh` → v1.0.0-p12)
- **SSRF protection**: all outbound HTTP now dials through a guarded `DialContext`
  (`ssrfSafeTransport`/`isPrivateOrReservedIP`) that blocks loopback, link-local
  (169.254.169.254 cloud metadata), and RFC-1918/ULA private ranges
- **Argon2id** credential hashing helpers (`hashSecretArgon2id`/`verifySecretArgon2id`)
  with constant-time comparison
- **Immutable WORM audit log**: migration `005-audit-log-worm` adds an `audit_log`
  table with `BEFORE UPDATE`/`BEFORE DELETE` triggers that `RAISE(ABORT)`; all
  admin article create/update/delete mutations now call `auditLog()`
- **Magic-number file verification** (`verifyMagicNumber`) for JPEG/PNG/GIF/WebP/PDF
- **`/health/ethics`** endpoint exposing machine-readable ethics compliance
  (no-tracking, privacy-by-design, audit-log present, charter version)
- Verified: full `go build ./...` + `go vet ./...` pass with real dependencies

#### Security (Prompt 9)
- Dedicated `security.yml` CI workflow: supply-chain scan, 7 security header checks, CSRF, SSRF, auth lockout, audit log, rate limit, threat model verification
- `docs/THREAT-MODEL.md` — Trust Boundaries, Entry Points, Assets, Threat Actors, Mitigations
- SSRF protection: 169.254.169.254 + private IP ranges blocked on all outbound fetches
- Immutable audit log (WORM): `audit_log` table insert-only, no UPDATE/DELETE grants
- Magic number file type verification on all media uploads
- `/health/ethics` endpoint returning ethics compliance status
- All 7 security headers verified in deploy script and CI

#### Automated Governance (Prompt 10)
- Complete rewrite of `ci.yml`: 13 jobs, `ci-pass` gate, all 12 Prompts + 14 ADRs verified
- `check-governance` job: verifies all 12 Prompts in Constitution
- `check-adrs` job: verifies ADR-0001, 0002, 0032–0043 all exist
- `check-docs` job: 19 required documentation files verified
- `check-ethics` job: Ethical AI Charter sections verified
- `check-community` job: RFC template, CODEOWNERS verified

#### Community (Prompt 11)
- `docs/MAINTAINERS.md` — 7 maintainer roles, nomination process, burnout prevention
- `docs/rfc-template.md` — full RFC template with ethical impact assessment
- `CONTRIBUTING.md` updated with all 7 maintainer roles and burnout prevention policy

#### Ethics (Prompt 12)
- `docs/ETHICAL-REVIEW-PROCESS.md` — ERB process, decision types, annual metrics, incident response
- `ETHICS.md` expanded with 7-point Ethical AI Charter
- Annual ethics metrics publication process defined

#### Documentation
- `docs/OPERATIONS.md` — runbooks RB-01 through RB-09, monitoring metrics, incident classification
- `docs/RELEASES.md` — release types, pre-release checklist, hotfix process, SemVer rules
- `docs/CI-GOVERNANCE.md` — workflow documentation, constraint budgets, governance enforcement matrix
- `docs/SUSTAINABILITY.md` — financial model, environmental footprint, long-term viability
- ADR-0036: CSP nonce centralized template helpers
- ADR-0037: Pprof explicit handler + rate limit + audit log
- ADR-0038: VACUUM cooldown + write-threshold guard
- ADR-0039: Deploy sourced component architecture
- ADR-0040: Config versioning + compatibility contracts
- ADR-0041: Structured health contracts (6 endpoints)
- ADR-0042: Backup restore automation + checksum registry
- ADR-0043: Integration test suite (8 test files)

### Changed
- `Makefile` — added: `test-integration`, `test-migrations`, `test-storage`, `test-api-contracts`, `bench`, `check-adrs`, `check-governance`, `check-ethics`, `check-security`, `check-complexity`, `check-threat-model`
- `scripts/README.md` — updated compliance table to ADR-0043

### Governance
- Constitution: v6.0 Prompts 1–12 fully implemented and CI-enforced
- All 14 required ADRs present and accepted

### SHA-256 Checksums
- To be published with binary release artifact

---

## [1.0.0-p8] — 2026-06-12

### Added
- Plugin pool WaitGroup drain + context cancellation propagation (ADR-0032)
- WAL adaptive checkpoint with size threshold triggers >32 MB (ADR-0033)
- Migration checksum drift verification at startup — halts boot on tampering (ADR-0034)
- Dead-letter replay limits (max 100/call), poison-job quarantine after MAX_REPLAY_COUNT (ADR-0035)
- CSP nonce centralized template helpers — `CSPNonce(r)` exported (ADR-0036)
- Pprof explicit handler registration, localhost-only binding, rate limiting, audit logging (ADR-0037)
- VACUUM cooldown window (10 min) + active write threshold guard (ADR-0038)
- Deploy scaffold sourced components (`deploy/install.sh` etc.) (ADR-0039)
- Config versioning + compatibility validation, deprecated setting warnings (ADR-0040)
- Structured health contracts: `/health/dependencies`, `/health/storage`, `/health/search`, `/health/queue` (ADR-0041)
- Backup restore automation: nightly restore validation cron, integrity check, checksum registry (ADR-0042)
- 8 new integration test files covering shutdown race, WAL recovery, plugin panic flood, migration corruption, replay abuse, CSP nonce, vacuum rate-limit, health contracts (ADR-0043)
- Repository governance structure aligned to Constitution v6.0
- `ETHICS.md` — Ethical AI Charter and principles
- `GOVERNANCE.md` — Governance overview and amendment process
- `SECURITY.md` — Vulnerability disclosure policy
- `CONTRIBUTING.md` — Contributor guide
- `docs/ARCHITECTURE.md`, `docs/INSTALLATION.md`, `docs/API-REFERENCE.md`, `docs/DEVELOPMENT.md`, `docs/TROUBLESHOOTING.md`
- `docs/EMAILS.md` — Official contact addresses
- `docs/adr/` — Architecture Decision Records directory

### Security
- Automated CSP nonce per request for all inline scripts
- Pprof rate-limited and localhost-only by default
- Migration tampering detection halts startup

### Upgrade Notes
- `QUEUE_MAX_RETRIES` env var deprecated — use `MAX_REPLAY_COUNT` instead
- `ConfigVersion=1.0` validation added; incompatible configs log a warning

---

## [0.9.0-p7] — Previous

### Added
- Decomposition, reliability, and operational contracts (Prompt 7 compliance)
- Deploy script modularisation

---

*Older entries omitted for brevity. Full history available via `git log`.*
