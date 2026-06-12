package plugins

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryRegisterAndFire(t *testing.T) {
	reg := NewRegistry()
	var called int64
	reg.Register("test.event", func(ctx context.Context, payload map[string]interface{}) error {
		atomic.AddInt64(&called, 1)
		return nil
	})

	m := New(reg)
	m.Start(2, 16)
	defer m.Shutdown()

	m.Fire("test.event", map[string]interface{}{"key": "val"})
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt64(&called) != 1 {
		t.Fatalf("hook called %d times, want 1", called)
	}
}

func TestManagerFireUnknownEvent(t *testing.T) {
	reg := NewRegistry()
	m := New(reg)
	m.Start(1, 8)
	defer m.Shutdown()
	// Should not panic or block
	m.Fire("nonexistent.event", nil)
}

func TestPanicIsolation(t *testing.T) {
	reg := NewRegistry()
	var safeCallCount int64
	reg.Register("panic.event", func(ctx context.Context, payload map[string]interface{}) error {
		panic("intentional panic")
	})
	reg.Register("panic.event", func(ctx context.Context, payload map[string]interface{}) error {
		atomic.AddInt64(&safeCallCount, 1)
		return nil
	})

	m := New(reg)
	m.Start(2, 16)
	defer m.Shutdown()

	m.Fire("panic.event", nil)
	time.Sleep(100 * time.Millisecond)
	// The second handler should still execute despite the first panicking.
	if atomic.LoadInt64(&safeCallCount) < 1 {
		t.Fatal("safe handler should have been called despite panic in sibling")
	}
}

func TestHookDisabledAfterFailures(t *testing.T) {
	reg := NewRegistry()
	var calls int64
	reg.Register("fail.event2", func(ctx context.Context, payload map[string]interface{}) error {
		atomic.AddInt64(&calls, 1)
		return errors.New("permanent failure")
	})

	m := New(reg)
	m.Start(1, 64)
	defer m.Shutdown()

	// Fire failThresh times and wait for processing so the hook gets disabled.
	for i := 0; i < failThresh; i++ {
		m.Fire("fail.event2", nil)
		time.Sleep(20 * time.Millisecond) // let worker process each job
	}
	time.Sleep(100 * time.Millisecond)

	// Now the hook should be disabled; additional fires should be no-ops.
	beforeExtra := atomic.LoadInt64(&calls)
	for i := 0; i < 5; i++ {
		m.Fire("fail.event2", nil)
	}
	time.Sleep(100 * time.Millisecond)
	afterExtra := atomic.LoadInt64(&calls)

	if afterExtra > beforeExtra {
		t.Fatalf("hook should be disabled; got %d extra calls after disable", afterExtra-beforeExtra)
	}
}

func TestShutdownDrains(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	var processed int64
	reg.Register("drain.event", func(ctx context.Context, payload map[string]interface{}) error {
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt64(&processed, 1)
		wg.Done()
		return nil
	})

	m := New(reg)
	m.Start(2, 32)

	const n = 5
	wg.Add(n)
	for i := 0; i < n; i++ {
		m.Fire("drain.event", nil)
	}
	wg.Wait()
	m.Shutdown()

	if atomic.LoadInt64(&processed) != n {
		t.Fatalf("want %d processed, got %d", n, processed)
	}
}

func TestRegistryHandlersSnapshot(t *testing.T) {
	reg := NewRegistry()
	reg.Register("snap.event", func(_ context.Context, _ map[string]interface{}) error { return nil })
	reg.Register("snap.event", func(_ context.Context, _ map[string]interface{}) error { return nil })

	fns := reg.Handlers("snap.event")
	if len(fns) != 2 {
		t.Fatalf("want 2 handlers, got %d", len(fns))
	}
	if reg.Handlers("unknown") != nil {
		t.Fatal("unknown event should return nil")
	}
}
