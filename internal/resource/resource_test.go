package resource

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiter_AcquireRelease(t *testing.T) {
	l := NewLimiter("test", 2)
	if err := l.Acquire(); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := l.Acquire(); err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	// Third should be at capacity
	if err := l.Acquire(); !errors.Is(err, ErrAtCapacity) {
		t.Fatalf("want ErrAtCapacity, got %v", err)
	}
	l.Release()
	// After release, should succeed again
	if err := l.Acquire(); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestLimiter_Stats(t *testing.T) {
	l := NewLimiter("stats-test", 3)
	l.Acquire() //nolint:errcheck
	l.Acquire() //nolint:errcheck
	stats := l.Stats()
	if stats.Active != 2 {
		t.Errorf("want Active=2, got %d", stats.Active)
	}
	if stats.Cap != 3 {
		t.Errorf("want Cap=3, got %d", stats.Cap)
	}
	if stats.Total != 2 {
		t.Errorf("want Total=2, got %d", stats.Total)
	}
}

func TestLimiter_Dropped(t *testing.T) {
	l := NewLimiter("drop-test", 1)
	l.Acquire() //nolint:errcheck
	l.Acquire() //nolint:errcheck // this should be dropped
	if l.Stats().Dropped != 1 {
		t.Errorf("want 1 dropped, got %d", l.Stats().Dropped)
	}
}

func TestGoroutineCount(t *testing.T) {
	n := GoroutineCount()
	if n < 1 {
		t.Errorf("goroutine count must be >= 1, got %d", n)
	}
}

func TestWatchdog_CancelsOverrun(t *testing.T) {
	w := NewWatchdog(10 * time.Millisecond)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	deregister := w.Watch("op-1", "test.op", 20*time.Millisecond, cancel)
	defer deregister()

	// Context should still be live immediately
	select {
	case <-ctx.Done():
		t.Fatal("context cancelled too early")
	default:
	}

	// After the budget + check interval, context should be cancelled
	time.Sleep(60 * time.Millisecond)
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Fatal("context not cancelled after budget exceeded")
	}
}

func TestWatchdog_DeregisterPreventsCancel(t *testing.T) {
	w := NewWatchdog(10 * time.Millisecond)
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	deregister := w.Watch("op-2", "test.op", 20*time.Millisecond, cancel)
	deregister() // deregister immediately

	time.Sleep(60 * time.Millisecond)
	select {
	case <-ctx.Done():
		t.Fatal("context was cancelled after deregistration")
	default:
		// expected: watchdog should not cancel after deregister
	}
}

func TestWatchdog_StopIdempotent(t *testing.T) {
	w := NewWatchdog(10 * time.Millisecond)
	w.Stop()
	w.Stop() // must not panic
}

func TestRegisterAndGet(t *testing.T) {
	l := Register("reg-test-unique", 5)
	if l == nil {
		t.Fatal("Register returned nil")
	}
	got := Get("reg-test-unique")
	if got != l {
		t.Fatal("Get returned different limiter than Register")
	}
	if Get("nonexistent-limiter-xyz") != nil {
		t.Fatal("Get should return nil for unknown name")
	}
}

func TestAllStats(t *testing.T) {
	Register("allstats-test", 2)
	stats := AllStats()
	found := false
	for _, s := range stats {
		if s.Name == "allstats-test" {
			found = true
			if s.Cap != 2 {
				t.Errorf("cap: want 2, got %d", s.Cap)
			}
		}
	}
	if !found {
		t.Error("allstats-test not found in AllStats()")
	}
}
