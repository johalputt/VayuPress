#!/usr/bin/env bash
#
# deadcode-gate.sh — fail CI only when NEW unreachable code is introduced.
#
# golang.org/x/tools/cmd/deadcode reports functions unreachable from the program
# entrypoints. The ones accepted today are listed, with the reason for each
# group, in scripts/deadcode-allow.txt.
#
# This gate compares the current report against that baseline and fails ONLY on
# entries that are not already allow-listed, so new dead code is blocked.
# Removing dead code is always welcome; refresh the baseline with --update when
# you do.
#
# Usage:  scripts/deadcode-gate.sh           # gate against the baseline
#         scripts/deadcode-gate.sh --update  # regenerate the baseline
set -euo pipefail

ALLOW="scripts/deadcode-allow.txt"

# Resolve the deadcode binary portably: it may live on PATH, in GOBIN (mise),
# or in GOPATH/bin (CI). Install it if missing, then locate it.
if ! command -v deadcode >/dev/null 2>&1; then
  go install golang.org/x/tools/cmd/deadcode@latest
  # Declare then assign so a failing `go env` surfaces instead of being masked
  # by export's own exit status (shellcheck SC2155).
  gobin="$(go env GOBIN)"
  gopath="$(go env GOPATH)"
  export PATH="$PATH:$gobin:$gopath/bin"
fi
BIN="$(command -v deadcode)"

# Normalise away line:col so the baseline survives unrelated line shifts.
# A deadcode that cannot load the packages (e.g. built by an older Go than the
# toolchain) reports nothing, which would read as "no dead code" and, under
# --update, empty the baseline. Its own failure stops the gate, with its error.
current="$(mktemp)"
report="$(mktemp)"
if ! "$BIN" ./... > "$report" 2> "$report.err"; then
  echo "deadcode could not analyse the module:"
  head -20 "$report.err"
  rm -f "$current" "$report" "$report.err"
  exit 1
fi
sed -E 's/:[0-9]+:[0-9]+:/:/' "$report" | sort -u > "$current"
rm -f "$report" "$report.err"

if [ "${1:-}" = "--update" ]; then
  header="$(mktemp)"
  # Only the leading comment block is the header; taking every comment line
  # anywhere and sorting the file once scrambled it into alphabetical order.
  awk '/^(#|$)/{print; next} {exit}' "$ALLOW" > "$header"
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
