#!/usr/bin/env bash
#
# dep-freshness.sh — report third-party Go modules that are behind their latest
# release. Prints a table and, under GitHub Actions, writes a job summary.
#
# The RED signal (non-zero exit) fires when a DIRECT dependency — one this repo
# imports and maintains itself — is behind. Indirect/transitive modules are
# listed for information only (they update automatically when their parents do),
# so the check stays actionable instead of perpetually red.
#
# Safe (minor/patch) bumps are opened automatically as Dependabot pull requests
# (.github/dependabot.yml); this script is the visible "what is behind" signal.
#
set -euo pipefail

cd "$(dirname "$0")/.." || exit 2

# Emit to the GitHub step summary when running in Actions, else to stdout.
emit() {
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    cat >>"$GITHUB_STEP_SUMMARY"
  else
    cat
  fi
}

echo "Checking Go module freshness (go list -u -m all)…"

# One line per outdated module: path|current|latest|direct|indirect. `.Update`
# is set by -u only when a newer version within the current major exists; the
# main module and up-to-date modules produce nothing.
tmpl='{{if and (not .Main) .Update}}{{.Path}}|{{.Version}}|{{.Update.Version}}|{{if .Indirect}}indirect{{else}}direct{{end}}{{end}}'

errfile="$(mktemp)"
trap 'rm -f "$errfile"' EXIT

set +e
raw="$(go list -u -m -f "$tmpl" all 2>"$errfile")"
rc=$?
set -e
if [ "$rc" -ne 0 ] && [ -z "$raw" ]; then
  echo "❌ go list failed to query module versions:" >&2
  cat "$errfile" >&2
  exit 2
fi

direct=()
indirect=()
while IFS= read -r row; do
  [ -n "$row" ] || continue
  case "$row" in
    *'|direct') direct+=("$row") ;;
    *'|indirect') indirect+=("$row") ;;
  esac
done < <(printf '%s\n' "$raw" | sort)

# Render a "path|cur|latest|scope" list as a Markdown table body to the summary.
emit_table() {
  {
    echo "| Module | Current | Latest |"
    echo "|---|---|---|"
    local r path cur latest
    for r in "$@"; do
      IFS='|' read -r path cur latest _ <<<"$r"
      printf '| `%s` | %s | %s |\n' "$path" "$cur" "$latest"
    done
  } | emit
}

# Console summary of a list.
echo_list() {
  local r path cur latest
  for r in "$@"; do
    IFS='|' read -r path cur latest _ <<<"$r"
    echo "  ${path}  ${cur} -> ${latest}"
  done
}

{
  echo "## 📦 Dependency freshness"
  echo
} | emit

if [ "${#direct[@]}" -eq 0 ] && [ "${#indirect[@]}" -eq 0 ]; then
  echo "✅ All Go modules are up to date."
  echo "✅ **All Go modules are up to date.**" | emit
  exit 0
fi

if [ "${#direct[@]}" -gt 0 ]; then
  echo "❌ ${#direct[@]} DIRECT dependency(ies) behind latest:"
  echo_list "${direct[@]}"
  {
    echo "### ❌ Direct dependencies behind (action needed)"
    echo
    echo "These are imported and maintained here. Update with \`go get -u <module>@latest && go mod tidy\`, or merge the Dependabot PR."
    echo
  } | emit
  emit_table "${direct[@]}"
else
  echo "✅ All direct dependencies are up to date."
  echo "✅ **All direct dependencies are up to date.**" | emit
fi

if [ "${#indirect[@]}" -gt 0 ]; then
  echo "ℹ ${#indirect[@]} indirect/transitive module(s) behind (informational)."
  {
    echo
    echo "<details><summary>ℹ ${#indirect[@]} indirect/transitive module(s) behind — these usually update via their parents</summary>"
    echo
  } | emit
  emit_table "${indirect[@]}"
  echo "</details>" | emit
fi

# RED only when a DIRECT dependency is behind — that is the actionable signal.
if [ "${#direct[@]}" -gt 0 ]; then
  echo
  echo "❌ Direct dependencies are behind — see the summary. (Intended RED signal.)"
  exit 1
fi
exit 0
