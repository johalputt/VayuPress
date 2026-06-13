#!/bin/bash
set -euo pipefail
FAILED=0
check() { echo "  checking: $1"; }
pass() { echo "  ✅ $1"; }
fail() { echo "  ❌ $1"; FAILED=1; }
check "disk space"
AVAIL=$(df /var/lib/vayupress 2>/dev/null | awk 'NR==2{print $4}' || echo 0)
[ "$AVAIL" -gt 1048576 ] && pass "disk space (${AVAIL}K available)" || fail "low disk space"
check "go version"
GOTOOLCHAIN=auto go version >/dev/null 2>&1 && pass "go available" || fail "go not found"
check "sqlite3"
sqlite3 --version >/dev/null 2>&1 && pass "sqlite3 available" || fail "sqlite3 not found"
[ $FAILED -eq 0 ] && echo "✅ validation passed" || { echo "❌ validation failed"; exit 1; }
