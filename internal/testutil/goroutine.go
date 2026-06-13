// Package testutil provides test helpers for VayuPress unit tests.
package testutil

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// AssertNoGoroutineLeak checks that no goroutines are leaked after fn runs.
// It snapshots the goroutine count before and after, allowing a short
// settling time for goroutines started during test setup to finish.
func AssertNoGoroutineLeak(t *testing.T, fn func()) {
	t.Helper()
	before := goroutineCount()
	fn()
	// Give goroutines up to 500ms to finish.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		after := goroutineCount()
		if after <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	after := goroutineCount()
	if after > before {
		t.Errorf("goroutine leak: started with %d, ended with %d (+%d)", before, after, after-before)
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Logf("goroutine stacks:\n%s", buf[:n])
	}
}

func goroutineCount() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "goroutine ")
}
