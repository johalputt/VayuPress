#!/bin/bash
# =============================================================================
#  preflight-ci.sh — Run all CI & Security governance checks LOCALLY
# -----------------------------------------------------------------------------
#  Purpose: catch CI/security workflow failures BEFORE pushing, so we never
#  again discover a broken governance check only after it runs on GitHub.
#
#  This mirrors the hard-fail (exit 1) checks in:
#    .github/workflows/ci.yml
#    .github/workflows/security.yml
#
#  Run before every push:  bash scripts/preflight-ci.sh
#  CI parity: if this passes locally, the GitHub workflows should pass too.
# =============================================================================
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 1

FAILED=0
pass() { echo "✅ $*"; }
fail() { echo "❌ $*"; FAILED=1; }

DEPLOY="scripts/deploy-vayupress.sh"

echo "════════════════════════════════════════════════════════════"
echo "  VayuPress CI Preflight — local governance check"
echo "════════════════════════════════════════════════════════════"

# ── 1. Required documentation files (ci.yml: check-docs) ─────────────────────
echo ""
echo "── Required documentation ──"
REQUIRED_DOCS=(
  README.md CHANGELOG.md SECURITY.md GOVERNANCE.md ETHICS.md
  CONTRIBUTING.md CODE_OF_CONDUCT.md GOVERNANCE-CONSTITUTION.md
  docs/INSTALLATION.md docs/API-REFERENCE.md docs/ARCHITECTURE.md
  docs/DEVELOPMENT.md docs/OPERATIONS.md docs/RELEASES.md
  docs/CI-GOVERNANCE.md docs/THREAT-MODEL.md docs/MAINTAINERS.md
  docs/SUSTAINABILITY.md docs/ETHICAL-REVIEW-PROCESS.md
)
for f in "${REQUIRED_DOCS[@]}"; do
  [ -f "$f" ] || fail "missing doc: $f"
done
[ $FAILED -eq 0 ] && pass "all required docs present"

# ── 2. Community health files (ci.yml: check-community) ──────────────────────
echo ""
echo "── Community health files ──"
COMMUNITY=(
  CODE_OF_CONDUCT.md CONTRIBUTING.md .github/CODEOWNERS .github/FUNDING.yml
  .github/pull_request_template.md .github/ISSUE_TEMPLATE/bug_report.yml
  .github/ISSUE_TEMPLATE/feature_request.yml .github/ISSUE_TEMPLATE/rfc_proposal.yml
  docs/rfc-template.md
)
CSTART=$FAILED
for f in "${COMMUNITY[@]}"; do
  [ -f "$f" ] || fail "missing community file: $f"
done
[ $CSTART -eq $FAILED ] && pass "all community files present"

# ── 3. ADR completeness (ci.yml: check-adrs) ─────────────────────────────────
echo ""
echo "── ADR completeness ──"
ASTART=$FAILED
for n in 0001 0002 $(seq -f "%04g" 32 58); do
  ls "docs/adr/ADR-${n}"* >/dev/null 2>&1 || fail "missing ADR-${n}"
done
[ $ASTART -eq $FAILED ] && pass "all required ADRs present (0001,0002,0032-0058)"

# ── 4. Governance constitution prompts (ci.yml: check-governance) ────────────
echo ""
echo "── Governance constitution ──"
GSTART=$FAILED
for i in $(seq 1 14); do
  grep -q "PROMPT $i:" GOVERNANCE-CONSTITUTION.md || fail "Constitution missing PROMPT $i"
done
[ $GSTART -eq $FAILED ] && pass "all 14 prompts present in Constitution"

# ── 5. Deploy script hard-fail checks (ci.yml + security.yml) ────────────────
echo ""
echo "── Deploy script governance strings ──"
DSTART=$FAILED
# ci.yml: check-deploy-script REQUIRED_SECTIONS
for s in "ENGINE_VERSION" "set -euo pipefail" "dry-run" "upgrade" \
         "PRAGMA integrity_check" "BACKUP_RETAIN_DAYS" "STORAGE_QUOTA"; do
  grep -qF "$s" "$DEPLOY" || fail "deploy script missing: $s"
done
# security.yml HARD-fail jobs (those that exit 1):
#   Security Headers (all 7), CSRF, Rate-limit, Auth-lockout, API-auth
for h in "Strict-Transport-Security" "X-Content-Type-Options" "X-Frame-Options" \
         "X-XSS-Protection" "Content-Security-Policy" "Referrer-Policy" "Permissions-Policy"; do
  grep -q "$h" "$DEPLOY" || fail "deploy script missing header: $h"
done
grep -q "X-CSRF-Token\|csrfToken\|csrf_token" "$DEPLOY" || fail "deploy script missing CSRF token"
grep -q "rateLimit\|rate_limit\|RateLimit\|allowPurge\|allowPprof" "$DEPLOY" || fail "deploy script missing rate limiting"
grep -q "lockout\|authFail\|brute" "$DEPLOY" || fail "deploy script missing auth lockout"
grep -q "API_KEY\|apiKey\|Bearer\|jwt" "$DEPLOY" || fail "deploy script missing API auth"
# Deploy script line count (ci.yml: >200)
LINES=$(wc -l < "$DEPLOY")
[ "$LINES" -gt 200 ] || fail "deploy script too short ($LINES lines, need >200)"
[ $DSTART -eq $FAILED ] && pass "deploy script governance strings present ($LINES lines)"

# ── 6. SECURITY.md required sections (ci.yml: check-security-policy) ──────────
echo ""
echo "── SECURITY.md sections ──"
SSTART=$FAILED
for section in "Reporting a Vulnerability" "security@vayupress.com" "72 hours" "CVSS" "Supported Versions"; do
  grep -q "$section" SECURITY.md || fail "SECURITY.md missing: $section"
done
[ $SSTART -eq $FAILED ] && pass "SECURITY.md sections present"

# ── 7. THREAT-MODEL.md sections (security.yml: threat-model-check) ───────────
echo ""
echo "── THREAT-MODEL.md sections ──"
TSTART=$FAILED
for section in "Trust Boundaries" "Entry Points" "Assets" "Threat Actors" "Mitigations"; do
  grep -q "$section" docs/THREAT-MODEL.md || fail "THREAT-MODEL.md missing: $section"
done
[ $TSTART -eq $FAILED ] && pass "THREAT-MODEL.md sections present"

# ── 8. Source integrity (ci.yml: check-source-sync) ──────────────────────────
echo ""
echo "── Source integrity ──"
if bash scripts/sync-source.sh --check >/dev/null 2>&1; then
  pass "sync-source.sh integrity check"
else
  fail "sync-source.sh integrity check failed (run: bash scripts/sync-source.sh)"
fi

# ── 9. Shell lint (ci.yml: lint-shell) ───────────────────────────────────────
echo ""
echo "── Shell lint ──"
if command -v shellcheck >/dev/null 2>&1; then
  SHSTART=$FAILED
  for sh in scripts/*.sh; do
    shellcheck --severity=warning "$sh" >/dev/null 2>&1 || fail "shellcheck warnings in $sh"
  done
  [ $SHSTART -eq $FAILED ] && pass "shellcheck clean"
else
  echo "⚠️  shellcheck not installed — skipping (CI will still run it)"
fi

# ── 10. Go native: build, vet, fmt, test (ci.yml: go-native) ─────────────────
echo ""
echo "── Go native toolchain ──"
if command -v go >/dev/null 2>&1; then
  GOSTART=$FAILED
  # CI builds with the toolchain pinned in go.mod (go1.25.x) which carries the
  # crypto stdlib security patches govulncheck enforces. GOTOOLCHAIN=auto lets
  # the go command fetch it if the local default is older.
  export GOTOOLCHAIN=auto
  go build ./... 2>/dev/null && pass "go build" || fail "go build"
  go vet ./... 2>/dev/null && pass "go vet" || fail "go vet"
  FMT=$(gofmt -l . 2>/dev/null)
  [ -z "$FMT" ] && pass "gofmt clean" || fail "gofmt issues: $FMT"
  go test ./... >/dev/null 2>&1 && pass "go test" || fail "go test"
  [ $GOSTART -eq $FAILED ] && echo "   (note: CI also runs -race, staticcheck, govulncheck against go1.25 stdlib)"
else
  fail "go not installed"
fi

# ── Result ────────────────────────────────────────────────────────────────────
echo ""
echo "════════════════════════════════════════════════════════════"
if [ $FAILED -ne 0 ]; then
  echo "❌ PREFLIGHT FAILED — fix the above BEFORE pushing."
  echo "════════════════════════════════════════════════════════════"
  exit 1
fi
echo "✅ PREFLIGHT PASSED — safe to push. CI & Security should go green."
echo "════════════════════════════════════════════════════════════"
