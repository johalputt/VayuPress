# Changelog

All notable changes to VayuPress are documented here.

Format: [Added / Changed / Deprecated / Fixed / Security / Upgrade Notes / Ethical Updates]

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
- **Toolchain bumped Go 1.22.5 → 1.23.5** (`go.mod`, deploy `GO_VERSION`, CI
  `setup-go`) to carry the `x/net/html` security fix, which requires Go 1.23.
- `Makefile`: `build`/`dev` target `./cmd/vayupress`; added `sync` and `sync-check`
  targets; `build` now depends on `sync-check`; `check-adrs` requires ADR-0044;
  `check-governance` verifies Prompt 13.

### Fixed
- **Reachable dependency vulnerability**: `govulncheck` flagged `golang.org/x/net/html`
  (pulled in by bluemonday). Fixed by bumping `x/net` to v0.41.0 / `x/crypto` to v0.39.0
  (requires Go 1.23). Security outranks Simplicity per the Constitution priority order.
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
