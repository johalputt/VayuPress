# ADR-0044: Repository Decomposition & Source Parity (P13)

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

Through Prompt 12, the entire VayuPress Go application lived embedded inside a bash
heredoc in `scripts/deploy-vayupress.sh`. The repository contained **zero `.go`
files**. This delivered an excellent self-contained install model
(`curl -sSL ... | bash`, per Prompt 5) but created severe engineering debt:

- No native Go tooling (`go build`, `go vet`, `go test`).
- No IDE indexing, jump-to-definition, or refactoring support.
- Weak static analysis — `golangci-lint` and `govulncheck` could not run.
- Poor contributor ergonomics — a contributor could not clone and build.
- Difficult debugging and code review of a 2,400-line heredoc.

Prompt 13 governs the transition to a real, navigable Go source tree **without**
breaking the self-contained install model.

## Decision

1. **Canonical source stays embedded.** The deploy script's heredoc
   (`cat > main.go << 'GOEOF' ... GOEOF`) remains the canonical source of truth so
   `curl -sSL ... | bash` keeps working unchanged (Prompt 5: Operational Simplicity).

2. **Mirror to a real tree.** The identical Go source is committed at
   `cmd/vayupress/main.go`, with committed `go.mod` and `go.sum`, enabling native
   `go build`, `go vet`, `go test`, `gofmt`, `golangci-lint`, and `govulncheck`.

3. **Enforced parity via `scripts/sync-source.sh`.** The script extracts the heredoc
   body and writes `cmd/vayupress/main.go`. In `--check` mode it runs in CI
   (`P13 · Source Sync`) and **fails the build** if the two ever drift. The deploy
   script and the real tree can never silently diverge (Prompt 10: Automated
   Governance).

4. **gofmt-clean canonical.** The canonical heredoc source was normalized once with
   `gofmt` so the mirror passes the formatting gate. Compactness was sacrificed for
   tool-compatibility; the deploy script grew accordingly.

5. **Pinned dependencies.** The deploy script pins exact dependency versions matching
   the committed `go.mod` (e.g. `go-chi/chi/v5@v5.1.0`, `golang.org/x/crypto@v0.31.0`)
   instead of `@latest`. This makes deploys reproducible and prevents pulling a
   version that requires a newer Go than the pinned `GO_VERSION` (1.22.5) — chi v5.3.0+
   requires Go 1.23 and would have broken the deploy.

6. **Progressive split.** This ADR covers the first stage: a single `main` package
   extracted with **zero behavior change** (verified: `go build`, `go vet`,
   `go test`, `gofmt -l` all clean). Splitting into `internal/` packages
   (auth, db, security, health, queue) is deferred to follow-up P13.x increments,
   each independently verified, to keep risk low.

## Rationale

Keeping the heredoc canonical preserves the project's signature install UX while the
mirrored tree unlocks the entire Go toolchain. The CI sync-check makes drift
impossible, so contributors get real tooling and operators get an unchanged install
path — both guarantees enforced by automation rather than discipline.

A single-package extraction first (rather than a full `internal/` split) means the
working binary is never at risk during the transition; the split can proceed
incrementally with each step independently green.

## Consequences

- Positive: `git clone && go build ./...` now works. IDEs index the code.
- Positive: CI runs native `go vet`, `gofmt`, `go build -race`, `go test -race`, and
  `govulncheck` — directly addressing the "weak CI" finding.
- Positive: Reproducible deploys via pinned dependency versions.
- Negative: The deploy script grew (~4,300 → ~5,500 lines) because gofmt expanded the
  previously compact one-line function bodies. Acceptable: tool-compatibility over
  byte-golfing.
- Negative: Two copies of the source exist. Mitigated entirely by the CI sync-check —
  divergence fails the build.
- Follow-up: `internal/` package split (P13.x), on-disk migration files, and full
  restore-boot validation remain future work.
