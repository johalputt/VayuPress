# Release Gate — VayuPress Stable Release

**Version:** 1.0  
**Last reviewed:** 2026-06-13  
**Authority:** VayuPress Maintainers (requires RFC + 2/3 vote to modify)

This document is the **mandatory checklist** for any release tagged as `Stable`.  
Beta and Experimental releases follow a lighter process documented in [`RELEASES.md`](../RELEASES.md).

---

## Pre-Release Gate (all items required for Stable)

### 1. CI Checks

All must pass on the release commit before tagging:

- [ ] `P16 · Native Go Build/Vet/Test/Staticcheck` — zero failures
- [ ] `gofmt -l .` — returns empty output (no unformatted files)
- [ ] `staticcheck ./...` — zero warnings
- [ ] `go test -race ./...` — zero failures and zero data races
- [ ] `govulncheck ./...` — zero known vulnerabilities in direct dependencies
- [ ] `Race & Deadlock Detection` workflow — passes
- [ ] `Supply Chain — SBOM & Vulnerability Scan` — passes
- [ ] `Security — Prompt 9 Enforcement` — passes
- [ ] Architecture validator (`internal/archcheck/`) — zero layer violations
- [ ] Golden tests (`internal/compat/`) — all goldens match (no `GOLDEN_UPDATE` required)

### 2. Compatibility Validation

- [ ] All packages marked **Stable** in [`docs/compatibility/stability-matrix.md`](../compatibility/stability-matrix.md) have no breaking API changes vs. previous release
- [ ] If any Stable API changed: RFC accepted, deprecation window observed, migration guide present in `docs/migrations/`
- [ ] Plugin manifest format backwards-compatible with previous release
- [ ] Event schema: no fields removed from Stable event types
- [ ] Database migrations: only additive (new tables/columns); no column renames or drops without migration pair

### 3. Migration Verification

- [ ] All `.sql` migration files pass SHA-256 checksum verification
- [ ] `Migrator.Up()` succeeds on clean database
- [ ] `Migrator.Down()` can reverse all reversible migrations
- [ ] No migration modifies data in irreversible ways without explicit documentation

### 4. Security Checklist

- [ ] `docs/security/attack-surfaces.md` reviewed; no new surfaces left unmitigated
- [ ] `docs/security/incident-response.md` playbooks tested against staging
- [ ] All plugin manifests in examples use `ExecutableHash`
- [ ] No private keys, tokens, or credentials appear in `git log --diff-filter=A` for release branch
- [ ] TruffleHog scan: zero findings
- [ ] SBOM generated for release artifacts

### 5. Performance Gate

Run benchmarks on the release candidate and compare against `testdata/bench/*.baseline.txt`:

```bash
go test ./internal/signing/ -bench=. -benchmem | tee /tmp/signing-release.txt
benchcmp testdata/bench/signing.baseline.txt /tmp/signing-release.txt
```

Thresholds (must not regress beyond these from baseline):

| Benchmark | Max allowed regression |
|-----------|----------------------|
| `BenchmarkSign` | +20% ns/op |
| `BenchmarkVerify` | +20% ns/op |
| `BenchmarkMerkleNew1024` | +30% ns/op |
| `BenchmarkMerkleProof` | +10% ns/op |

If thresholds exceeded: file a performance issue before releasing. Do not block release for ≤ 5% regressions confirmed as measurement noise.

### 6. Operational Readiness

- [ ] `docs/operations/backup-restore.md` restore procedure verified on staging
- [ ] `docs/operations/wal-corruption-recovery.md` recovery tested (inject corruption in staging)
- [ ] `/healthz` endpoint responds correctly after clean startup
- [ ] Graceful shutdown: all in-flight requests complete before process exits
- [ ] Systemd unit restarts correctly after SIGKILL

### 7. Release Artifacts

- [ ] Binary built with `CGO_ENABLED=1` and verified against `go.sum`
- [ ] SBOM attached as release asset (`vayupress-<version>.cdx.json`)
- [ ] CHANGELOG entry present for all user-visible changes
- [ ] Git tag is signed: `git tag -s v<version>`
- [ ] Release notes include: what changed, migration steps (if any), known issues

### 8. Rollback Verification

- [ ] Previous release binary confirmed to work against current DB (backwards schema compat)
- [ ] Rollback procedure documented in CHANGELOG for this release
- [ ] Time-to-rollback estimated and under 5 minutes for operator following `docs/operations/backup-restore.md`

---

## Stability Gate: Promoting an Interface to Stable

Before marking any interface **Stable** in the compatibility matrix:

1. **ADR required**: Document the interface design decision.
2. **Tests required**: Golden tests (see `internal/compat/`) covering the serialised form.
3. **Deprecation window**: Any previous version of the interface must have been deprecated for ≥ 1 minor release.
4. **Changelog entry**: Explicitly note the promotion.
5. **RFC vote**: If the interface is externally visible (HTTP API, plugin IPC, event schema), an RFC with 2/3 vote is required.

---

## Who Can Approve a Stable Release

Any maintainer listed in [`MAINTAINERS.md`](../MAINTAINERS.md) may approve after all gate items pass.  
No release may be tagged by the author of the majority of commits in that release (self-merge prevention).

---

## Gate Failure Protocol

If any gate item fails after a release tag is created:

1. Immediately yank the release (mark pre-release on GitHub, add `[YANKED]` to CHANGELOG).
2. File a P1 or P2 incident per [`docs/security/incident-response.md`](../security/incident-response.md).
3. Fix the failing item.
4. Re-run the full gate on the fixed commit.
5. Issue a new patch release: `v<major>.<minor>.<patch+1>`.
