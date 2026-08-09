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
MAIN_GO="${3:-cmd/vayupress/main.go}"

fail() { printf '❌ %s\n' "$1" >&2; exit 1; }

[ -r "$VERSION_FILE" ] || fail "$VERSION_FILE is missing or unreadable"
[ -r "$CHANGELOG" ] || fail "$CHANGELOG is missing or unreadable"
[ -r "$MAIN_GO" ] || fail "$MAIN_GO is missing or unreadable"

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

# The third source. main.go's Version is overridden at release time by
# -X main.Version, so a stale literal does not reach a published binary — but it
# does reach anyone who builds from source without those ldflags, and it is the
# value every in-tree caller (health, render) reads. The first version of this
# script checked only two of the three files and passed while this one was a
# release behind; a gate that is greener than reality is worse than no gate.
main_version=$(sed -n 's/^var Version = "\([^"]*\)".*/\1/p' "$MAIN_GO" | head -1)
[ -n "$main_version" ] || fail "could not read 'var Version' out of $MAIN_GO"

if [ "$main_version" != "$version" ]; then
	fail "$MAIN_GO declares Version $main_version but $VERSION_FILE says $raw.
   A source build would report the wrong version, and every in-tree reader of
   Version would agree with it."
fi

printf '✅ .release-version (%s), CHANGELOG.md section [%s] and %s Version (%s) all agree; the section is non-empty\n' \
	"$raw" "$changelog_version" "$MAIN_GO" "$main_version"
