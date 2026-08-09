#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# A release here is triggered by pushing .release-version — the push IS the
# release. Nothing downstream re-derives the version, and nothing checks that
# CHANGELOG.md was updated to match. Those two facts together are how a version
# gets bumped, announced, and never actually shipped: the changelog says the
# fixes went out, the tag says otherwise, and the two are only ever compared by
# a human who already believes the work is done.
#
# This is the comparison, run on every push.
#
# Usage: scripts/release-consistency.sh [version-file] [changelog]

set -euo pipefail

VERSION_FILE="${1:-.release-version}"
CHANGELOG="${2:-CHANGELOG.md}"

fail() { printf '❌ %s\n' "$1" >&2; exit 1; }

[ -r "$VERSION_FILE" ] || fail "$VERSION_FILE is missing or unreadable"
[ -r "$CHANGELOG" ] || fail "$CHANGELOG is missing or unreadable"

# tr -d strips the trailing newline whether or not the file has one.
raw=$(tr -d ' \t\r\n' < "$VERSION_FILE")
[ -n "$raw" ] || fail "$VERSION_FILE is empty"

case "$raw" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*) fail "$VERSION_FILE reads '$raw', expected a vX.Y.Z tag" ;;
esac
version="${raw#v}"

# An '## [Unreleased]' block on top is the normal state between releases —
# accumulated work that has deliberately not shipped. It is skipped, not
# rejected. The first *versioned* heading below it is the release that
# .release-version must name.
first_heading=$(grep -m1 -E '^## \[[0-9]' "$CHANGELOG" || true)
[ -n "$first_heading" ] || fail "$CHANGELOG has no '## [x.y.z]' section"

changelog_version=$(printf '%s' "$first_heading" | sed -n 's/^## \[\([^]]*\)\].*/\1/p')
[ -n "$changelog_version" ] || fail "could not read a version out of: $first_heading"

if [ "$changelog_version" != "$version" ]; then
	fail "$VERSION_FILE says $raw but $CHANGELOG leads with [$changelog_version].
   Whichever is right, a release cut now would publish one and announce the other."
fi

# A heading alone is not release notes. Require prose under it, or the release
# publishes an empty section.
body=$(awk -v v="## [$version]" '
	index($0, v) == 1 { seen = 1; next }
	seen && /^## \[/ { exit }
	seen' "$CHANGELOG" | tr -d '[:space:]')
[ -n "$body" ] || fail "$CHANGELOG section [$version] has no content under its heading"

printf '✅ .release-version (%s) matches CHANGELOG.md section [%s], which is non-empty\n' \
	"$raw" "$changelog_version"
