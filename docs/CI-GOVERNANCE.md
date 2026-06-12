# VayuPress CI Governance

**Version**: 1.0.0 (Prompt 10 — Automated Governance)  
**Pipeline**: GitHub Actions  
**Gate job**: `ci-pass` (all checks must pass to merge)

---

## Philosophy

Every constraint in the VayuPress Governance Constitution v6.0 that can be mechanically verified **must** be verified in CI. No human reviewer is required to catch what a machine can catch.

CI is the enforcement layer of the Constitution. Maintainers cannot override CI for merges to `main`.

---

## Workflows

### `ci.yml` — Primary CI

Runs on every push and pull request to `main`.

| Job | Purpose | Hard Fail? |
|-----|---------|-----------|
| `check-docs` | All required docs exist | Yes |
| `lint-shell` | shellcheck on deploy script | Yes |
| `lint-markdown` | markdownlint-cli2 | Yes |
| `secret-scan` | Hardcoded credential patterns | Yes |
| `check-licenses` | go-licenses on all deps | Yes |
| `check-governance` | All 12 Prompts in Constitution | Yes |
| `check-adrs` | All 14 required ADRs exist | Yes |
| `check-security-policy` | SECURITY.md required sections | Yes |
| `check-ethics` | Ethical AI Charter sections | Yes |
| `check-community` | RFC template, CODEOWNERS | Yes |
| `check-deploy-script` | Required Go patterns in embed | Yes |
| `check-links` | Markdown link validation | Advisory (no hard fail) |
| `ci-pass` | Gate: all above must pass | Yes |

### `security.yml` — Security Enforcement (Prompt 9)

Runs on push to `main`, PRs to `main`, and weekly on Monday 02:00 UTC.

| Job | Purpose | Hard Fail? |
|-----|---------|-----------|
| `supply-chain` | Secret pattern scan in source | Yes |
| `security-headers` | All 7 headers in deploy script | Yes |
| `csrf-check` | CSRF token implementation | Yes |
| `ssrf-check` | SSRF protection present | Advisory |
| `auth-check` | Lockout + strong hashing + API auth | Yes |
| `audit-log-check` | Audit logging present | Advisory |
| `rate-limit-check` | Rate limiting present | Yes |
| `threat-model-check` | THREAT-MODEL.md sections | Yes |
| `vuln-disclosure` | SECURITY.md present + items | Advisory |
| `security-pass` | Gate: supply-chain + headers + csrf + auth + rate-limit + threat-model | Yes |

---

## Required Checks (Branch Protection)

The following checks must pass before merging to `main`:

1. `CI Pass — All Checks` (from ci.yml `ci-pass` job)
2. `Security Pass — All Checks` (from security.yml `security-pass` job)

These are enforced via GitHub branch rulesets. See `CONTRIBUTING.md` for setup.

---

## Constraint Budgets

These limits are enforced in CI or smoke tests:

| Constraint | Limit | Prompt | Enforcement |
|------------|-------|--------|------------|
| Go binary size | < 45 MB | P2 | `make check-size` |
| Memory idle | < 800 MB | P2 | Smoke test in deploy |
| Memory under load | < 1.6 GB | P2 | Load test advisory |
| JS bundle (gzip) | < 50 KB | P3 | `make check-size` |
| Known CVEs | 0 | P9 | `govulncheck` |
| Race conditions | 0 | P7 | `go test -race` |
| golangci-lint errors | 0 | P10 | `golangci-lint run` |
| Unapproved licenses | 0 | P10 | `go-licenses check` |
| Broken links | 0 | P8/P10 | `mlc` (advisory) |

---

## Adding a New CI Check

1. Add the check as a new job in `.github/workflows/ci.yml` or `security.yml`
2. Add the job name to the `needs:` list in the relevant gate job (`ci-pass` or `security-pass`)
3. Add an entry to this document
4. Update `Makefile` with a local equivalent target
5. Open an ADR if the check enforces a new architectural constraint

Do NOT add advisory-only jobs to the gate's `needs:` list.

---

## Local CI Equivalents

All CI checks can be run locally via `make`:

```bash
make check-docs       # verify all required docs exist
make lint             # golangci-lint
make vuln             # govulncheck
make test-race        # go test -race ./...
make check-size       # binary + JS size budgets
make check-governance # grep Constitution for all 12 Prompts
make check-ethics     # grep ETHICS.md for charter sections
make check-security   # grep SECURITY.md for required sections
make bench            # performance benchmarks
```

Run `make` (no target) to see all available targets.

---

## Governance Enforcement Matrix

This table maps each Constitution Prompt to its CI enforcement:

| Prompt | Area | CI Check |
|--------|------|---------|
| P1 Architecture | SQLite-first, single binary | `check-deploy-script` (searches Go source patterns) |
| P2 Performance | Size/memory budgets | `check-size`, smoke test |
| P3 UI/UX | Zero tracking, self-hosted fonts | `check-deploy-script` (ADR-0002 pattern) |
| P4 Platform Evolution | RFC process | `check-community` |
| P5 Operations | Health endpoints, logging | `check-deploy-script` |
| P6 Operations | Backup, alerting | `check-deploy-script` |
| P7 Testing | Race detector, integration tests | `test-race`, `check-adrs` (ADR-0043) |
| P8 Releases | Versioning, CHANGELOG | `check-docs`, `check-governance` |
| P9 Security | Headers, CSRF, auth, audit | `security.yml` jobs |
| P10 Automated Governance | CI itself | `ci-pass`, `security-pass` |
| P11 Community | CODEOWNERS, RFC template | `check-community` |
| P12 Ethics | Ethical AI Charter | `check-ethics` |

---

## CI Failure Response

When CI fails:

1. **Read the failure log** — every job emits ✅/❌ per item
2. **Fix the root cause** — do not suppress the check
3. **If the check is wrong**: open an issue, propose amendment via PR to `.github/workflows/ci.yml` with justification — requires BDFL approval
4. **Never merge with failing required checks** — branch protection enforces this

Bypassing CI requires BDFL + Security Lead + Architecture Lead consensus and is documented in the audit log.
