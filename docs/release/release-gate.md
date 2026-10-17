# The release gate

**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

Every release is cut from a tree that has passed everything below, on that
tree, before `.release-version` is bumped. There is one kind of release: a
version is either published, and gated, or it is not published.
[RELEASES.md](../RELEASES.md) covers what the workflow then does.

## 1. The code gates

On the release tree, locally, and again in CI on the pushed commit:

- [ ] `gofmt -l .` prints nothing; `go vet ./...` is clean
- [ ] `go build ./...`, unit tests, `go test -race ./...`, integration tests
      (`-tags integration`), and the suite in shuffled order
- [ ] staticcheck and golangci-lint: zero findings; gosec at high severity:
      none
- [ ] govulncheck: nothing reachable
- [ ] `go mod tidy` leaves no diff; `go mod verify` passes
- [ ] The layer rules hold (`internal/archcheck`)
- [ ] The compatibility goldens match (`internal/compat`), with no
      `GOLDEN_UPDATE`
- [ ] `make check-size`: the binary under 45 MB gzipped
- [ ] TruffleHog finds nothing in the tree

## 2. The browser suite

- [ ] The Playwright suite passes against a binary built from the release
      tree, in Chromium (`tests/e2e`), including the design lint and the
      visual baselines it checks

## 3. The attacker's read

- [ ] Everything under `## [Unreleased]` has been read as an attacker would
      read it: each new route, input, file write and privilege boundary. Each
      finding is fixed in this release or recorded with its reason, before the
      version is bumped.

## 4. Compatibility

- [ ] No route marked stable in
      [stability-matrix.md](../compatibility/stability-matrix.md) changed
      incompatibly ([api-contracts.md](../compatibility/api-contracts.md))
- [ ] Migrations only add. No released migration file was edited (start-up
      refuses a changed checksum, ADR-0034).
      `go test ./internal/db/ -run TestAllMigrationsApplyCleanly` passes.
- [ ] The previous release's binary still starts on the migrated database, so
      a rollback from System › Updates works
- [ ] The VCB contract and plugin manifests stay readable by the previous
      release ([vcb.md](../compatibility/vcb.md))

## 5. The release commit

- [ ] `.release-version`, `var Version` and the CHANGELOG section agree
      (`scripts/release-consistency.sh`)
- [ ] The CHANGELOG section says what changed in the reader's words, and an
      **Upgrade Notes** paragraph says whether an operator must act
- [ ] Docs the release makes untrue are corrected in the same release

## After publishing

- [ ] The release carries the binary, `.sha256`, `.cosign.bundle`, `.sig` and
      the signed SBOM
- [ ] CI is green on the release commit. If it goes red, fixing it is the next
      thing done.

## If a published release is bad

Releases are immutable once published, and installs verify what they
download, so a bad release is replaced, not edited:

1. Fix the cause on `main`, through this gate.
2. Release the next micro version. Installs take it as they took the last.
3. An install already on the bad release can roll back from **System ›
   Updates** while the fix is cut.
4. If the defect is a vulnerability, follow
   [incident-response.md](../security/incident-response.md) and
   [SECURITY.md](../../SECURITY.md).
