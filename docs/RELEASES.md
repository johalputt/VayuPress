# How VayuPress is released

**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

A release is one push to `main` that changes `.release-version`. Nobody tags
by hand and nobody uploads a file. The workflow builds, signs, attaches and
publishes, and every install's updater takes it from there.

## Version numbers: micro by default, roll over at 99

Every release bumps the third segment, however large the change:
`v3.18.6` → `v3.18.7`. Size never decides the bump; only the rollover does.
Each segment counts 0–99:

- micro at 99 → the next release bumps the minor and resets micro:
  `v3.13.99` → `v3.14.0`;
- minor at 99 as micro rolls over → the next release bumps the major and
  resets both: `v3.99.99` → `v4.0.0`.

So a version says *when*, not *how much*. What a release changes, and whether
it changes anything an operator must act on, is in its CHANGELOG section and
its **Upgrade Notes**. A stable API route never breaks inside a major version
whatever the number does
([compatibility/api-contracts.md](compatibility/api-contracts.md)).

A version with a hyphen (`v3.19.0-rc.1`) is published as a pre-release. The
console offers it only to an operator who asks for pre-releases.

## The release commit

Three files change in one commit, so they always agree:

| File | Holds |
|------|-------|
| `.release-version` | `vX.Y.Z`; the push of this file starts the workflow |
| `cmd/vayupress/main.go` | `var Version = "X.Y.Z"` (no `v`) |
| `CHANGELOG.md` | A `## [X.Y.Z] — YYYY-MM-DD` section; the `[Unreleased]` notes move into it |

`scripts/release-consistency.sh` compares the three on every push (CI job
*Release Metadata Consistency*). A version file ahead of the changelog fails
the build, so a version is never announced without its notes, or bumped
without being shipped.

## What the workflow does

`.github/workflows/tag-release.yml`, on a push to `main` that touches
`.release-version`:

1. **Reads the version** and refuses one that is not `vX.Y.Z`.
2. **Skips** if that release is already published, so a re-run is harmless.
3. **Builds** with the Go version `go.mod` pins: CGO for SQLite, linked
   statically (`-linkmode external -extldflags '-static'`), `-trimpath`, with
   `-X main.Version`. One `linux/amd64` binary with libc linked in, so it does
   not depend on the host's.
4. **Signs it twice.** Cosign signs keyless, through GitHub's OIDC identity
   for this workflow, with an RFC 3161 timestamp from Sigstore's timestamp
   authority, and the entry is recorded in the Rekor transparency log. The Ed25519 release key signs it too (`scripts/release-sign`). The
   workflow refuses to publish a build that pins a key without the key's
   secret, because every install would refuse that release and no later one
   could rescue them.
5. **Writes and signs a CycloneDX SBOM.**
6. **Packages and signs** the provisioning helpers, the VayuShield helper
   agent and the self-hosted marketing site.
7. **Lifts the version's CHANGELOG section** into the release body
   (`scripts/changelog-section.sh`).
8. **Checks that no attachment sorts before `vayupress`.** Installs older than
   the asset-selection fix take the first installable file. A ZIP that sorted
   first was once written over a running binary.
9. **Creates the tag and publishes the release.** Releases are immutable once
   published.

The assets of every release:

| Asset | |
|-------|--|
| `vayupress` | The binary |
| `vayupress.sha256` | Its checksum |
| `vayupress.cosign.bundle` | The Sigstore signature, certificate and Rekor entry |
| `vayupress.sig` | The Ed25519 signature |
| `vayupress-vX.Y.Z.sbom.json` (+ `.cosign.bundle`) | The SBOM |
| `vayuprovision-helpers.tar.gz`, `vayushield-agent.tar.gz` (+ `.sha256`, `.cosign.bundle`) | The root helper scripts and the shield agent |
| `vayupress-selfhosted-site.zip` (+ `.sha256`) | The project site |

What an install checks before it runs any of this is in
[UPGRADING.md](UPGRADING.md#what-every-release-must-prove).

## Verifying a release yourself

```bash
cosign verify-blob vayupress \
  --bundle vayupress.cosign.bundle \
  --certificate-identity https://github.com/johalputt/VayuPress/.github/workflows/tag-release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c vayupress.sha256
```

The identity and issuer are the ones the updater pins
(`update.ReleaseSignerIdentity`, `update.ReleaseSignerIssuer`): a release
signed from any other workflow, or from any branch but `main`, is refused. A
test fails if this page and the code disagree.

## Before the release commit

A release is cut only from a tree that has passed, on that tree:

- the Go gates: `gofmt`, `go vet`, `go build`, unit, race and integration
  tests, staticcheck, golangci-lint, gosec, govulncheck, the binary-size
  budget;
- the browser suite against a built binary (Playwright, Chromium);
- a review of everything under `[Unreleased]` as an attacker would read it,
  with each finding fixed or recorded before the version is bumped.

Then the version is bumped and the commit is pushed. Every gate CI runs, and
what each does *not* prove, is in [QUALITY-GATES.md](QUALITY-GATES.md).

## A fix that cannot wait

There is no separate hotfix branch: the fix is committed to `main`, gated the
same way, and released with the next micro number. A vulnerability is handled
privately first, by the process in [SECURITY.md](../SECURITY.md), and
disclosed with the release that fixes it.
