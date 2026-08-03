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

# ---------------------------------------------------------------------------
# Major versions, which `go list -u` CANNOT see.
#
# FINDING, and the reason this section exists: `-u` only reports an update
# WITHIN the current major, because in Go a new major is a different module
# path (…/v2). A dependency two majors behind therefore produced no row at all,
# and this script printed "All Go modules are up to date" — a green badge on a
# workflow named Dependency Freshness, asserting something it had never checked.
# That is the kind of untrue reassurance that makes every other line here worth
# less.
#
# Majors are reported but do NOT turn the build red. A major bump is a migration
# somebody has to schedule, and a check that stays red until unrelated work is
# done is one people learn to ignore — which costs more than it catches.
majors=()
prerelease_majors=()
major_probe_failed=0

# probe_one asks the proxy whether ONE module path exists, and distinguishes
# "the proxy says no" from "the proxy could not be reached".
#
# That distinction is the whole point. A network failure that reads as "this
# major does not exist" turns every probe into a silent pass and puts the green
# badge back on a claim nothing verified — the exact defect this section was
# added to remove, reintroduced by the code removing it.
#   0 = exists (prints its latest version)   1 = definitively absent
#   2 = could not tell
probe_one() {
  local out
  if out="$(go list -m -f '{{.Version}}' "$1@latest" 2>&1)"; then
    printf '%s' "$(printf '%s' "$out" | tail -n1)"
    return 0
  fi
  case "$out" in
    *"no matching versions"*|*"not found"*|*"unknown revision"*|\
    *"invalid version"*|*"does not contain package"*|*"410 Gone"*|*"404 Not Found"*)
      return 1 ;;
  esac
  return 2
}

# probe_majors prints "path@version" for the highest newer major that exists.
# Self-contained: it derives its own starting point from the path it is given.
probe_majors() {
  local path="$1" base next misses=0 found="" try rc ver
  base="${path%/v[0-9]}"; base="${base%/v[0-9][0-9]}"
  next=2
  case "$path" in
    */v[0-9]|*/v[0-9][0-9]) next=$(( ${path##*/v} + 1 )) ;;
  esac
  local limit=$(( next + 6 ))
  # Bounded: six candidates, and give up after two consecutive definite misses.
  # Majors are contiguous in practice, and an unbounded probe would hammer the
  # proxy for every module in the graph.
  while [ "$next" -lt "$limit" ] && [ "$misses" -lt 2 ]; do
    try="${base}/v${next}"
    set +e; ver="$(probe_one "$try")"; rc=$?; set -e
    case "$rc" in
      0) found="${try}@${ver}"; misses=0 ;;
      1) misses=$(( misses + 1 )) ;;
      # "?" rather than a variable. This function is called inside $( ), which is
      # a SUBSHELL — a flag assigned here never reaches the caller, so the
      # could-not-tell path was unreachable and a network failure read as a
      # clean probe. Exactly the false-green this section exists to prevent,
      # reintroduced by the code preventing it.
      *) printf '?'; return 0 ;;
    esac
    next=$(( next + 1 ))
  done
  printf '%s' "$found"
}

# Only DIRECT modules are probed: an indirect one is its parent's problem, and
# probing every module in the graph would turn a fast check into a slow one.
#
# Captured into a variable BEFORE the loop rather than piped into it. A `while`
# fed by process substitution runs its body in this shell, but the command on
# the other side does not — so a failure flag set there is set in a subshell and
# lost, which is how the "could not tell" path came to be unreachable in the
# first draft of this very block.
set +e
directs="$(go list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}} {{.Version}}{{end}}' all 2>/dev/null)"
list_rc=$?
set -e
if [ "$list_rc" -ne 0 ]; then
  major_probe_failed=1
  directs=""
fi

while IFS= read -r line; do
  [ -n "$line" ] || continue
  mpath="${line%% *}"
  mver="${line##* }"
  [ -n "$mpath" ] || continue
  newer="$(probe_majors "$mpath")"
  if [ "$newer" = "?" ]; then
    major_probe_failed=1
    continue
  fi
  [ -n "$newer" ] || continue
  newer_ver="${newer##*@}"
  newer_path="${newer%@*}"
  # A major that exists only as an alpha or a beta is NOT something anyone is
  # behind on. Both majors this probe found on its first run were pre-release —
  # goldmark v2.0.0-beta.9 and chroma v3.0.0-alpha.5 — and listing those as
  # "available" would tell an operator to migrate a production markdown renderer
  # onto a beta. A check that recommends the wrong thing confidently is the
  # failure this whole script is written against.
  #
  # Any version carrying a "-" qualifies: that is a semver pre-release, or a
  # pseudo-version, and both mean the major has no tagged stable release yet.
  case "$newer_ver" in
    *-*) prerelease_majors+=("${mpath}|${mver}|${newer_path} ${newer_ver}|major") ;;
    *)   majors+=("${mpath}|${mver}|${newer_path} ${newer_ver}|major") ;;
  esac
done <<<"$directs"

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

if [ "${#direct[@]}" -eq 0 ] && [ "${#indirect[@]}" -eq 0 ] && [ "${#majors[@]}" -eq 0 ]; then
  echo "✅ All Go modules are up to date, and no direct dependency has a newer stable major."
  echo "✅ **All Go modules are up to date**, and no direct dependency has a newer stable major version." | emit
  # NO early exit. A pre-release major is not something anyone is behind on — so
  # it must not suppress this all-clear — but it still has to be PRINTED, and an
  # `exit 0` here skipped the section that prints it. The repo would have been
  # told everything was current while the check silently knew about two majors
  # in beta.
  if [ "${#prerelease_majors[@]}" -eq 0 ] && [ "$major_probe_failed" -eq 0 ]; then
    exit 0
  fi
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

if [ "${#prerelease_majors[@]}" -gt 0 ]; then
  echo "ℹ ${#prerelease_majors[@]} direct dependency(ies) have a next major in PRE-RELEASE (nothing to do):"
  echo_list "${prerelease_majors[@]}"
  {
    echo
    echo "<details><summary>ℹ ${#prerelease_majors[@]} next major(s) exist only as alpha/beta — nothing to do yet</summary>"
    echo
    echo "Listed so the migration is not a surprise later. Nobody is behind on these: there is no tagged stable release for that major."
    echo
  } | emit
  emit_table "${prerelease_majors[@]}"
  echo "</details>" | emit
fi

if [ "${#majors[@]}" -gt 0 ]; then
  echo "⚠ ${#majors[@]} direct dependency(ies) have a NEWER MAJOR available:"
  echo_list "${majors[@]}"
  {
    echo
    echo "### ⚠ Newer major versions available (review, not automatic)"
    echo
    echo "A new major is a different module path in Go, so \`go list -u\` never reports it and Dependabot opens it as its own PR rather than folding it into the grouped minor/patch bump. Each is a migration to schedule deliberately — these do **not** fail this check."
    echo
  } | emit
  emit_table "${majors[@]}"
fi

# What this check does and does not cover, stated where the result is read.
# A green badge that is trusted for more than it verifies is worse than an
# absent one, and this script printed exactly that for two years of majors it
# had never looked at.
{
  echo
  echo "<sub>Green here means: every direct dependency is at the newest version <em>within its current major</em>, and no direct dependency has a newer <em>stable</em> major. A next major that exists only as an alpha or beta is listed above and does not count as being behind. This does not audit vulnerabilities — <code>govulncheck</code> in CI does that — and indirect modules are listed for information only.</sub>"
} | emit

if [ "${major_probe_failed}" -ne 0 ]; then
  echo "⚠ could not enumerate direct modules to probe for new majors — nothing is claimed about them."
  echo "<sub>⚠ The major-version probe could not run, so nothing is claimed either way about newer majors.</sub>" | emit
fi

# RED only when a DIRECT dependency is behind — that is the actionable signal.
if [ "${#direct[@]}" -gt 0 ]; then
  echo
  echo "❌ Direct dependencies are behind — see the summary. (Intended RED signal.)"
  exit 1
fi
exit 0
