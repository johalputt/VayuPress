package profiling_test

import (
	"runtime"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/profiling"
)

// TestSnapshotFields verifies RuntimeSnapshot captures expected fields.
func TestSnapshotFields(t *testing.T) {
	snap := profiling.Snapshot()

	if snap.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
	if snap.Goroutines <= 0 {
		t.Errorf("Goroutines = %d, want > 0", snap.Goroutines)
	}
	if snap.HeapAllocBytes == 0 {
		t.Error("HeapAllocBytes = 0")
	}
}

// TestGoroutineLeakDetector is a test helper pattern used across the test suite.
// It captures goroutine count before and after a test body, failing if goroutines
// increase permanently (with a short stabilisation window).
func TestGoroutineLeakDetector(t *testing.T) {
	before := runtime.NumGoroutine()

	// Simulate a transient goroutine that cleans up correctly.
	done := make(chan struct{})
	go func() { <-done }()
	close(done)

	// Allow scheduler to reap the goroutine.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before+1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Errorf("goroutine leak: started with %d, ended with %d", before, after)
	}
}

// GoroutineDelta returns a func that fails t if goroutine count grew by more
// than allowance after call. Use as: defer GoroutineDelta(t, 0)() in tests.
func GoroutineDelta(t *testing.T, allowance int) func() {
	t.Helper()
	before := runtime.NumGoroutine()
	return func() {
		t.Helper()
		deadline := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(deadline) {
			if runtime.NumGoroutine() <= before+allowance {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		after := runtime.NumGoroutine()
		if after > before+allowance {
			t.Errorf("goroutine leak: before=%d after=%d allowance=%d", before, after, allowance)
		}
	}
}
