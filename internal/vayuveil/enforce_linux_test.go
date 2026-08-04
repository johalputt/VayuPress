// SPDX-License-Identifier: Apache-2.0

//go:build linux

package vayuveil

import (
	"testing"

	"golang.org/x/sys/unix"
)

// raiseCoreLimitForTest lifts the soft core limit so the test that follows has a
// precondition worth measuring. Reports false when the hard limit forbids it.
func raiseCoreLimitForTest(t *testing.T) bool {
	t.Helper()
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CORE, &rl); err != nil {
		return false
	}
	if rl.Max == 0 {
		return false
	}
	want := rl.Max
	if want > 4096 {
		want = 4096
	}
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: want, Max: rl.Max}) == nil
}
