#!/usr/bin/env bash
#
# deadcode-gate.sh — fail CI only when NEW unreachable code is introduced.
#
# golang.org/x/tools/cmd/deadcode reports functions unreachable from the program
# entrypoints. VayuPress ships several forward-looking subsystems ahead of their
# wiring (federation/ActivityPub, the plugin registry, the sandbox worker pool +
# seccomp/capability hardening, clustering, the Arweave storage stub, etc.);
# those are intentional and enumerated in scripts/deadcode-allow.txt.
#
# This gate compares the current report against that baseline and fails ONLY on
# entries that are not already allow-listed — so it cannot regress (new dead
# code is blocked) while the known roadmap surface is accepted. Removing dead
# code is always welcome; refresh the baseline with --update when you do.
#
# Usage:  scripts/deadcode-gate.sh           # gate against the baseline
#         scripts/deadcode-gate.sh --update  # regenerate the baseline
set -euo pipefail

ALLOW="scripts/deadcode-allow.txt"

# Resolve the deadcode binary portably: it may live on PATH, in GOBIN (mise),
# or in GOPATH/bin (CI). Install it if missing, then locate it.
if ! command -v deadcode >/dev/null 2>&1; then
  go install golang.org/x/tools/cmd/deadcode@latest
  export PATH="$PATH:$(go env GOBIN):$(go env GOPATH)/bin"
fi
BIN="$(command -v deadcode)"

# Normalise away line:col so the baseline survives unrelated line shifts.
current="$(mktemp)"
"$BIN" ./... 2>/dev/null | sed -E 's/:[0-9]+:[0-9]+:/:/' | sort -u > "$current"

if [ "${1:-}" = "--update" ]; then
  header="$(mktemp)"
  grep -E '^(#|$)' "$ALLOW" > "$header" 2>/dev/null || true
  cat "$header" "$current" > "$ALLOW"
  rm -f "$current" "$header"
  echo "baseline updated: $(grep -vcE '^(#|$)' "$ALLOW") entries"
  exit 0
fi

allow="$(mktemp)"
grep -vE '^(#|$)' "$ALLOW" | sort -u > "$allow"
new="$(comm -13 "$allow" "$current" || true)"
rm -f "$current" "$allow"

if [ -n "$new" ]; then
  echo "NEW unreachable code detected (not in $ALLOW):"
  echo "$new"
  echo ""
  echo "Wire it up, delete it, or — if intentional — run:"
  echo "  scripts/deadcode-gate.sh --update   (then commit the refreshed baseline)"
  exit 1
fi

echo "deadcode gate: no new unreachable code"
