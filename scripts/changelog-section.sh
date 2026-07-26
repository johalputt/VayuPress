#!/usr/bin/env bash
# changelog-section.sh <version> — print one release's CHANGELOG section.
#
# Used by tag-release.yml to give the GitHub release a real body. That body is
# what the in-app updater shows on the Update & Backup card, so without it the
# operator is asked to install a build described only by a compare link.
#
# Prints the lines between "## [<version>]" and the next "## [" heading, with the
# heading itself and surrounding blank lines/rules trimmed. Exits non-zero only on
# usage error: a missing section prints a short placeholder instead, because a
# release must never fail over its notes.
set -euo pipefail

version="${1:-}"
if [ -z "$version" ]; then
  echo "usage: changelog-section.sh <version>   # e.g. 3.15.38" >&2
  exit 2
fi
version="${version#v}"

changelog="$(dirname "$0")/../CHANGELOG.md"
if [ ! -f "$changelog" ]; then
  echo "See CHANGELOG.md for the full list of changes."
  exit 0
fi

section="$(awk -v ver="$version" '
  # Match "## [3.15.38] — 2026-07-26" for our version and nothing else.
  $0 ~ "^## \\[" ver "\\]" { inside = 1; next }
  inside && /^## \[/       { exit }
  inside                   { print }
' "$changelog")"

# Trim leading/trailing blank lines and the "---" rule that separates sections.
section="$(printf '%s\n' "$section" | sed -e '/^---$/d')"
section="$(printf '%s\n' "$section" | sed -e '/./,$!d')"
section="$(printf '%s\n' "$section" | tac | sed -e '/./,$!d' | tac)"

if [ -z "$section" ]; then
  echo "See CHANGELOG.md for the full list of changes in ${version}."
  exit 0
fi

printf '%s\n' "$section"
