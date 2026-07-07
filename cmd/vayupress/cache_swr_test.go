package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestSWRRefresherSingleFlight proves a second refresh for a key already in
// flight is dropped, so concurrent hits on the same stale page trigger exactly
// one background re-render.
func TestSWRRefresherSingleFlight(t *testing.T) {
	r := newSWRRefresher(4)
	var calls int64
	started := make(chan struct{})
	release := make(chan struct{})

	r.refresh("k", func() {
		atomic.AddInt64(&calls, 1)
		close(started)
		<-release // hold the slot so the key stays in-flight
	})
	<-started // first refresh is now running

	// Same key while the first is in-flight → must be dropped, not queued.
	r.refresh("k", func() { atomic.AddInt64(&calls, 1) })
	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("single-flight: want 1 call while in-flight, got %d", got)
	}

	close(release)
	// Wait for the first refresh's cleanup to release the key before scheduling
	// again (otherwise the second is legitimately deduped and dropped).
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		inflight := r.inflight["k"]
		r.mu.Unlock()
		if !inflight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first refresh never cleared the in-flight key")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The key is free again and a new refresh may run.
	done := make(chan struct{})
	r.refresh("k", func() { atomic.AddInt64(&calls, 1); close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh after completion did not run")
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("want 2 total calls, got %d", got)
	}
}

// TestSWRRefresherBounded proves the total number of concurrent refreshes never
// exceeds the configured cap — the guard against a re-render herd. Refreshes
// past the cap are dropped (not queued), so readers never wait and the box is
// never saturated.
func TestSWRRefresherBounded(t *testing.T) {
	r := newSWRRefresher(2)
	var concurrent, maxSeen int64
	release := make(chan struct{})

	fn := func() {
		c := atomic.AddInt64(&concurrent, 1)
		for {
			m := atomic.LoadInt64(&maxSeen)
			if c <= m || atomic.CompareAndSwapInt64(&maxSeen, m, c) {
				break
			}
		}
		<-release
		atomic.AddInt64(&concurrent, -1)
	}

	// Schedule more distinct keys than the cap; at most `cap` may run at once,
	// the rest are dropped.
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		r.refresh(k, fn)
	}
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt64(&maxSeen); got > 2 {
		t.Fatalf("bounded: concurrency exceeded cap: max=%d", got)
	}
	close(release)
	time.Sleep(40 * time.Millisecond) // let the running refreshes drain
}

// TestSWRRefreshNonBlocking proves the top-level swrRefresh helper returns
// immediately (the caller — a request goroutine — must never block on it).
func TestSWRRefreshNonBlocking(t *testing.T) {
	done := make(chan struct{})
	block := make(chan struct{})
	go func() {
		swrRefresh("x", func() { <-block })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("swrRefresh blocked the caller")
	}
	close(block)
}
