# VayuPress Release Process

**Version**: 1.0.0 (Prompt 8 — Release Engineering)  
**Release cadence**: Feature releases quarterly; patch releases as needed  
**Release Manager role**: See `docs/MAINTAINERS.md`

---

## Versioning policy — micro by default, roll over at 99

Every release bumps the third (micro) segment, however large the change:
`v3.13.96` → `v3.13.97`. Size never decides the bump; only the rollover does.
Each segment counts 0–99:

- micro at 99 → the next release bumps the minor and resets micro:
  `v3.13.99` → `v3.14.0`;
- minor at 99 as micro rolls over → the next release bumps the major and
  resets both: `v3.99.99` → `v4.0.0`.

A release changes three files in one commit, so they always agree:
`.release-version` (which starts the tag-release workflow),
`var Version` in `cmd/vayupress/main.go` (no `v` prefix), and a matching
section in `CHANGELOG.md`.

## Release Types

| Type | Trigger | Examples | Branch |
|------|---------|---------|--------|
| Micro | Every release | v3.13.1, v3.13.2 | main |
| Minor | Micro rolls over from 99 | v3.13.99 → v3.14.0 | main |
| Major | Minor rolls over from 99 | v3.99.99 → v4.0.0 | main |

Security patches skip RFC and go directly to release after Security Lead review.

---

## Pre-Release Checklist

### Code Freeze (T-7 days)
- [ ] All planned issues closed or deferred
- [ ] No open P1/P2 bugs
- [ ] All ADRs for this release accepted (not draft)
- [ ] `go mod tidy` run, go.sum committed

### CI Gate (must be green)
- [ ] `ci-pass` job green on release commit
- [ ] `security-pass` job green
- [ ] Binary size < 45 MB (`make check-size`)
- [ ] Memory < 800 MB idle (smoke test)
- [ ] JS bundle < 50 KB gzip
- [ ] Race detector: no races (`make test-race`)
- [ ] `govulncheck`: zero known vulnerabilities
- [ ] `golangci-lint`: zero errors
- [ ] `go-licenses check`: all licenses approved

### Documentation (T-3 days)
- [ ] `CHANGELOG.md` entry written (version, date, ADRs, breaking changes)
- [ ] `docs/API-REFERENCE.md` updated for any new/changed endpoints
- [ ] `docs/INSTALLATION.md` updated for any new env vars or config
- [ ] `scripts/README.md` updated with new ADR table entries
- [ ] Deploy script version string bumped (e.g., `v1.0.0-p9`)

### Security Review (T-2 days)
- [ ] Security Lead has reviewed any auth/crypto/network changes
- [ ] `docs/THREAT-MODEL.md` updated if new entry points or assets added
- [ ] `SECURITY.md` advisory section updated if relevant
- [ ] Ethical Review Board sign-off (if Prompt 12 applies)

---

## Release Steps

### 1. Tag the Release

```bash
# Ensure main is clean and CI is green
git checkout main
git pull origin main

# Create annotated tag
git tag -a v1.X.Y -m "VayuPress v1.X.Y — <one-line summary>

ADRs: ADR-XXXX
Constitution: Prompts 1-12 compliant
"

# Push tag
git push origin v1.X.Y
```

### 2. Build Release Artifact

```bash
# Reproducible build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags="-s -w -X main.Version=v1.X.Y" \
  -trimpath -o vayupress-linux-amd64 ./main.go

# Verify size
ls -lh vayupress-linux-amd64

# Compute checksum
sha256sum vayupress-linux-amd64 > vayupress-linux-amd64.sha256

# Also hash the deploy script
sha256sum scripts/deploy-vayupress.sh > deploy-vayupress.sh.sha256
```

### 3. Create GitHub Release

Create a release on GitHub with:
- Tag: `v1.X.Y`
- Title: `VayuPress v1.X.Y — <summary>`
- Body: copy the CHANGELOG.md entry for this version
- Attachments: binary + sha256, deploy script + sha256

### 4. Update CHANGELOG.md

```markdown
## [v1.X.Y] — YYYY-MM-DD

### Summary
<brief description>

### Governance
- Constitution: v6.0 Prompts 1–12 compliant
- New ADRs: ADR-XXXX — <title>

### Changes
- ...

### Security
- ...

### SHA-256 Checksums
- `vayupress-linux-amd64`: `<hash>`
- `deploy-vayupress.sh`: `<hash>`
```

### 5. Post-Release

- [ ] Announce on community channels (releases@vayupress.com)
- [ ] Update `scripts/README.md` version reference
- [ ] Close the GitHub milestone
- [ ] Open next milestone

---

## Hotfix Process

For P1 security or critical bug fixes:

```bash
# Branch from main (which is already at latest release)
git checkout main
git pull origin main
git checkout -b hotfix/v1.X.Y-description

# Fix the issue
# ...commit...

# Fast-track review: Security Lead + BDFL (2 person minimum)
# CI must still be green — no exceptions

# Merge to main
git checkout main
git merge --no-ff hotfix/v1.X.Y-description

# Tag immediately
git tag -a v1.X.Y -m "Hotfix: <description>"
git push origin main v1.X.Y
```

Security hotfixes: coordinate via security@vayupress.com before any public disclosure.

---

## Semantic Versioning Rules

VayuPress follows [SemVer 2.0](https://semver.org/):

| Change | Version bump |
|--------|-------------|
| Backward-compatible bug fix | Patch (0.0.X) |
| New endpoint (backward compatible) | Minor (0.X.0) |
| New env var with default | Minor (0.X.0) |
| Removed endpoint | Major (X.0.0) — RFC required |
| Changed DB schema (breaking) | Major (X.0.0) — RFC required |
| New required env var | Major (X.0.0) |

Breaking changes without a major version bump violate the governance contract.

---

## Version String Policy

The embedded Go binary version string must match the git tag exactly:

```go
var Version = "dev" // overridden at build time via -ldflags
```

Build command must pass: `-X main.Version=v1.X.Y`

The deploy script version string (`v1.0.0-p9`) includes the prompt level suffix for governance traceability.

---

## Release Schedule

| Quarter | Target | Focus |
|---------|--------|-------|
| Q1 | v1.1.0 | Performance (Prompt 2) |
| Q2 | v1.2.0 | Platform evolution (Prompt 4) |
| Q3 | v1.3.0 | Community governance (Prompt 11) |
| Q4 | v2.0.0 | (if breaking changes needed) |

Patch releases happen outside this schedule as needed.
