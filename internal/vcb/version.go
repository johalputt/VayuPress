// SPDX-License-Identifier: Apache-2.0

package vcb

// version.go — the host-compatibility contract. A VCB manifest may declare
// MinHost/MaxHost bounds; the validator checks that the running VayuPress
// version falls inside them. Versions are the project's own release shape —
// "3.13.41" or "v3.13.41" — compared numerically part by part.

import (
	"strconv"
	"strings"
)

// CompareVersions compares two dotted numeric versions (an optional leading
// "v" and any pre-release/build suffix after "-" or "+" are ignored). It
// returns -1, 0, or 1 as a is lower than, equal to, or higher than b. Missing
// parts count as zero, so "3.13" == "3.13.0". A part that is not numeric
// compares as zero (fail-safe: garbage never sorts above a real release).
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var na, nb int
		if i < len(pa) {
			na = pa[i]
		}
		if i < len(pb) {
			nb = pb[i]
		}
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
	}
	return 0
}

// versionParts parses "v3.13.41-rc1" into [3, 13, 41].
func versionParts(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V"))
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil
	}
	fields := strings.Split(v, ".")
	out := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			n = 0
		}
		out[i] = n
	}
	return out
}

// ValidVersion reports whether v looks like a release version this contract
// can compare: at least one dot-separated numeric part after the optional "v".
func ValidVersion(v string) bool {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V"))
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return false
	}
	for _, f := range strings.Split(v, ".") {
		if f == "" {
			return false
		}
		if _, err := strconv.Atoi(f); err != nil {
			return false
		}
	}
	return true
}

// HostInRange reports whether host satisfies the optional [minHost, maxHost]
// bounds. An empty bound is open on that side.
func HostInRange(host, minHost, maxHost string) bool {
	if minHost != "" && CompareVersions(host, minHost) < 0 {
		return false
	}
	if maxHost != "" && CompareVersions(host, maxHost) > 0 {
		return false
	}
	return true
}
