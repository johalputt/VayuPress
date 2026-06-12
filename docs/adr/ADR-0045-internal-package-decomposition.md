# ADR-0045: Internal Package Decomposition (P14)

**Status**: Accepted  
**Date**: 2026-06-12  
**Deciders**: Ankush Choudhary Johal (maintainer)  
**Supersedes**: N/A  
**Related**: ADR-0044 (Repository Decomposition), GOVERNANCE-CONSTITUTION.md §Prompt 14

---

## Context

Prompt 13 (ADR-0044) established the real Go source tree (`cmd/vayupress/main.go`,
`go.mod`, `go.sum`) with CI-enforced parity against the deploy script's embedded
heredoc. This unlocked `go build`, `go test`, `govulncheck`, and `golangci-lint` on
every push.

However, the single file `cmd/vayupress/main.go` remained ~3,659 lines of `package
main`. While syntactically valid, this created:

- **Zero testability at the package level** — you cannot import `package main` from
  a test; every test requires starting the whole server.
- **Zero dependency boundaries** — auth logic, render logic, queue logic, and DB
  logic all share the same global namespace with no enforced interface.
- **Accumulated coupling** — adding a feature required touching globals from multiple
  logical domains in a single file with no natural organization boundary.
- **Reviewer fatigue** — a 3,659-line diff context for any change to any subsystem.

P14 (Prompt 14) addresses this by mechanically extracting the monolith into bounded
`internal/` packages.

---

## Decision

Split `cmd/vayupress/main.go` into the following `internal/` packages, in dependency
order (most foundational first):

| Package | Responsibility |
|---------|---------------|
| `internal/logging` | Structured JSON logging; `LogFields`, `LogJSON`, `LogInfo`, `LogError` |
| `internal/config` | Configuration loading from env; `Cfg` struct, `Load()` |
| `internal/metrics` | All atomic counters, latency histogram type and instances |
| `internal/db` | SQLite init, migrations, WORM audit log; `Article` domain type |
| `internal/auth` | API key middleware, CSRF, rate limiting, Argon2id, bucket sweeper |
| `internal/render` | Article template rendering, cache, CSP nonce, CSS assets |
| `internal/queue` | Write queue, dead-letter, replay, poison job quarantine |
| `internal/health` | All `/health/*` HTTP handlers with schema versioning |

`cmd/vayupress/main.go` becomes the wiring layer only (≤ 500 lines): route
registration, dependency injection, shutdown orchestration.

---

## Dependency Graph

```
internal/logging   (no internal deps)
internal/config    (no internal deps)
internal/metrics   (no internal deps)
internal/db        → logging, config
internal/auth      → logging, config, metrics
internal/render    → logging, config, db, metrics
internal/queue     → logging, config, db, metrics  [render injected via callback]
internal/health    → config, db, metrics, queue, render
cmd/.../main.go    → all of the above
```

The graph is strictly acyclic. `internal/queue` accepts a `renderFn func(db.Article)
error` parameter rather than importing `internal/render` directly, avoiding the
queue→render→metrics→queue cycle that would otherwise form.

---

## Deploy Script Evolution

P13's canonical-heredoc model (single `main.go` embedded in the deploy script) is
superseded. With multiple packages in `internal/`, the deploy script now:

1. Clones the repository at the pinned release tag.
2. Builds with `CGO_ENABLED=1 go build -ldflags="-s -w" -trimpath -o /usr/local/bin/vayupress ./cmd/vayupress/`.
3. Installs the binary.

The `scripts/sync-source.sh` tool and `P13 · Source Sync` CI job are retired.
The `P14 · Native Go Build/Vet/Test` CI job (successor to the P13 job) enforces
`go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run ./...`,
and `govulncheck ./...` on every push.

---

## Consequences

### Positive

- Each `internal/` package can be unit-tested in isolation with a simple `go test ./internal/logging/` — no server startup required.
- Dependency graph is explicit and enforced by the Go compiler (`internal/` import rules prevent outside callers).
- `cmd/vayupress/main.go` shrinks from ~3,659 lines to ≤ 500 — reviewers see wiring, not business logic.
- `golangci-lint` becomes practical (running it on 3,659-line `main.go` generated many false positives from interleaved concerns).
- New contributors can understand one package (e.g., `internal/auth`) without reading the entire codebase.

### Negative / Risks

- **One-time migration churn**: All existing in-progress branches must rebase after P14 lands.
- **Deploy script change**: The `curl | bash` UX now requires a git clone step; the install is still a single script but no longer self-contained in the sense of embedding Go source. Documented in UPGRADING.md.
- **Additional packages to maintain**: 8 packages instead of 1 file means 8 `_test.go` requirements.

### Mitigations

- The migration is mechanical (no behavior changes) — rebases are straightforward.
- The deploy script still runs as `sudo ./scripts/deploy-vayupress.sh` (same UX).
- Test stubs are created for each package immediately after extraction (minimum one `TestXxx` per package).

---

## Alternatives Considered

### Alternative A: Keep single-file `main.go` forever

Rejected. A 3,659-line `package main` is untestable at the package level, making
automated quality guarantees (coverage, race detection per-subsystem) impossible.
P14 is the minimum viable structural investment for long-term maintainability.

### Alternative B: Split into multiple files in `package main`

Considered as a halfway step. Multiple `*.go` files in the same `package main`
directory would reduce file size but provide zero import-boundary enforcement and
zero per-package testability. The `internal/` model is strictly superior.

### Alternative C: Use a `pkg/` layout instead of `internal/`

`internal/` enforces that only code inside this module can import these packages —
consistent with VayuPress's "no external plugin surface" doctrine. `pkg/` would
expose these as public APIs, which is not intended.

---

## References

- [ADR-0044](ADR-0044-repository-decomposition.md) — P13: Real Go source tree
- [GOVERNANCE-CONSTITUTION.md §Prompt 14](../../GOVERNANCE-CONSTITUTION.md)
- [Go internal packages](https://pkg.go.dev/cmd/go#hdr-Internal_Directories)
