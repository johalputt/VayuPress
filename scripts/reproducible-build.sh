#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Build the release binary twice and require the two to be byte-identical.
#
# Why this matters here: every release is cosign-signed and Ed25519-signed, and
# the in-app updater verifies those signatures. A signature proves the artifact
# came from this pipeline. It does NOT prove the artifact corresponds to the
# source at that tag — only a deterministic build can, by letting anyone rebuild
# and compare. That property is worth a gate precisely because nothing else
# announces its loss: a build that silently starts varying still signs, still
# publishes, and still installs.
#
# Two things this gets right, both of which are easy to get wrong:
#
#  1. Each build gets its OWN empty GOCACHE and TMPDIR. Two `go build` runs in a
#     row share the cache, so the second compiles almost nothing and matches the
#     first trivially. That tests the cache, not the build. Cold and independent
#     is the only honest form.
#
#  2. The flags are READ OUT OF .github/workflows/tag-release.yml rather than
#     copied here. A reproducibility gate that runs different flags from the
#     release proves determinism of a binary nobody ships. If the flags cannot
#     be found, this script FAILS rather than falling back to a guess.
#
# Usage: scripts/reproducible-build.sh [workflow-file]

set -euo pipefail

WF="${1:-.github/workflows/tag-release.yml}"
PROBE_VERSION="0.0.0-repro"

fail() { printf '❌ %s\n' "$1" >&2; exit 1; }

[ -r "$WF" ] || fail "$WF is missing or unreadable"

# The release's own build tags and ldflags, taken from the workflow.
TAGS=$(sed -n 's/.*-tags "\([^"]*\)".*/\1/p' "$WF" | head -1)
LDFLAGS=$(sed -n 's/.*-ldflags="\([^"]*\)".*/\1/p' "$WF" | head -1)

[ -n "$TAGS" ] || fail "could not read '-tags \"...\"' out of $WF.
   Refusing to guess: a reproducibility check that builds with different flags
   from the release proves nothing about the release."
[ -n "$LDFLAGS" ] || fail "could not read '-ldflags=\"...\"' out of $WF (same reason)."

# The workflow interpolates the real version; any fixed value does here, as long
# as both builds get the same one.
LDFLAGS=${LDFLAGS//\$\{VERSION#v\}/$PROBE_VERSION}
case "$LDFLAGS" in
	*'${'*) fail "unsubstituted shell expansion left in ldflags: $LDFLAGS" ;;
esac

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

printf 'tags   : %s\n' "$TAGS"
printf 'ldflags: %s\n' "$LDFLAGS"

build() {
	local tag="$1"
	mkdir -p "$WORK/cache_$tag" "$WORK/tmp_$tag"
	env GOCACHE="$WORK/cache_$tag" TMPDIR="$WORK/tmp_$tag" \
		CGO_ENABLED=1 go build \
		-trimpath -tags "$TAGS" -ldflags="$LDFLAGS" \
		-o "$WORK/$tag" ./cmd/vayupress
}

echo "building twice, each from an empty cache…"
build one
build two

a=$(sha256sum "$WORK/one" | awk '{print $1}')
b=$(sha256sum "$WORK/two" | awk '{print $1}')

if [ "$a" != "$b" ]; then
	printf '❌ the release build is not deterministic\n' >&2
	printf '   build 1: %s (%s bytes)\n' "$a" "$(stat -c%s "$WORK/one")" >&2
	printf '   build 2: %s (%s bytes)\n' "$b" "$(stat -c%s "$WORK/two")" >&2
	printf '   Two builds of identical source differ, so a published binary cannot be\n' >&2
	printf '   checked against its tag by rebuilding. Investigate before releasing.\n' >&2
	exit 1
fi

printf '✅ two independent cold builds are byte-identical: %s\n' "$a"
printf '   (same toolchain and host; a different Go or C toolchain legitimately\n'
printf '    produces a different — but equally deterministic — binary)\n'
