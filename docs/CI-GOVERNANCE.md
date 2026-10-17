# CI and governance

**Last reviewed:** 2026-10-17, against VayuPress 3.18.7

The [Governance Constitution](../GOVERNANCE-CONSTITUTION.md) is organised in
numbered *Prompts* (P1 architecture, P2 performance … P12 ethics, P13–P14 later
additions). Some of what it asks for can be checked by a machine. This page
says which workflow checks what, and, as plainly, what each check does not
prove. The full list of code gates, from `gofmt` to the browser suite, is
[QUALITY-GATES.md](QUALITY-GATES.md).

## The two gates

| Gate job | Workflow | Waits for |
|----------|----------|-----------|
| `ci-pass` | `ci.yml` | Every job in the table below except the link check |
| `security-pass` | `security.yml` | `supply-chain`, `security-headers`, `csrf-check`, `auth-check`, `rate-limit-check`, `threat-model-check` |

## `ci.yml`, on every push and pull request

| Job | Checks | Does not prove |
|-----|--------|----------------|
| `go-native` | Build, vet, unit, race (shuffled), integration; staticcheck, golangci-lint, gosec, govulncheck; `go mod tidy` and `verify` | That a behaviour is right where no test asserts it |
| `cross-compile` | The module builds and vets for Linux on seven architectures | That it runs on them: releases are `linux/amd64` only |
| `check-reproducible-build` | Two clean builds are byte-identical | That a third party's build matches |
| `check-release-consistency` | `.release-version`, `var Version` and the CHANGELOG section agree | That the notes are accurate |
| `check-source-sync` | Entry points and required packages exist and the module builds | Anything about behaviour |
| `check-licenses` | Every dependency's licence is on the approved list | |
| `secret-scan` | No credential-shaped strings in the tree | Secrets in history, or of a novel shape |
| `lint-shell`, `lint-markdown` | shellcheck on the scripts; markdownlint on the docs | That a doc is true |
| `check-docs` | The required documents exist | Their content |
| `check-governance` | The Constitution is present and names Prompts 1–14 | That anything follows it |
| `check-adrs` | The 29 foundational ADRs (0001–0002, 0032–0058) exist | That later ADRs are current |
| `check-security-policy`, `check-ethics`, `check-community` | SECURITY.md, ETHICS.md, the RFC template and CODEOWNERS carry their required sections | That the policies are followed |
| `check-deploy-script` | The installer keeps `set -euo pipefail`, `--dry-run`, `--upgrade` and its integrity check | That an install succeeds |
| `check-links` | Markdown links resolve | Advisory; never fails the gate |

The `check-*` governance jobs are presence checks. They exist so a document
the project depends on cannot silently disappear, and they are named for what
they are so nobody mistakes them for enforcement.

## `security.yml`, on push, pull request and every Monday

| Job | Checks | Gate |
|-----|--------|------|
| `supply-chain` | Secret patterns in the source | Yes |
| `security-headers` | The installer names each security header | Yes |
| `csrf-check`, `auth-check`, `rate-limit-check` | The CSRF, lockout and hashing, and rate-limit code is present | Yes |
| `threat-model-check` | THREAT-MODEL.md carries its sections | Yes |
| `ssrf-check`, `audit-log-check`, `vuln-disclosure` | The same, for SSRF guards, the audit log and SECURITY.md | Advisory |

These too are greps. The behaviour behind each is tested in the Go suite,
which `go-native` runs: the CSRF double-submit, the lockout window, the SSRF
guard's refusals and the audit log's append-only triggers each have their own
tests.

## The other workflows

| Workflow | When | What |
|----------|------|------|
| `e2e.yml` | Push, pull request, on demand | The Playwright suite against a built binary |
| `race.yml` | Push, pull request | The race detector across the module |
| `lighthouse.yml` | Push, pull request, on demand | Lighthouse budgets for home, a post and four console pages |
| `screenshots.yml` | Scheduled, on demand | Halcyon and console visual baselines |
| `sbom.yml` | Push, pull request, scheduled | CycloneDX SBOM and a vulnerability scan of it |
| `dep-freshness.yml` | Weekly, on demand | Fails when a direct dependency has a newer release |
| `dependabot-automerge.yml` | Dependabot's pull requests | Enables auto-merge for minor and patch updates; majors wait for review |
| `tag-release.yml` | A push changing `.release-version` | The release ([RELEASES.md](RELEASES.md)) |

## Budgets

| Budget | Limit | Enforced by |
|--------|-------|-------------|
| Binary | 45 MB gzipped | `make check-size` |
| Halcyon stylesheet and script | Their recorded byte budgets | Halcyon's budget tests |
| Lighthouse | Per-page scores | `lighthouse.yml` |
| Known vulnerabilities | None reachable | govulncheck in `go-native` |
| Lint | Zero findings | golangci-lint, staticcheck |

A budget is a decision point, not a ceiling: when a requirement needs more,
the budget is raised in the same change with the reason beside it.

## Locally

```bash
make lint vuln test-race check-size     # the Go side
make check-docs check-adrs check-governance check-ethics check-security
```

`make help` lists every target.

## When CI fails

Fix the cause; do not weaken the check. If the check itself is wrong, change
it in its own commit, saying why, and show it failing on the thing it is meant
to catch before trusting it again.
