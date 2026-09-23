// SPDX-License-Identifier: Apache-2.0

package render

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A burst of requests while a rebuild runs must produce exactly one more
// rebuild — not one per request (the sitemap walked every post once per write),
// and not zero (the last write would then be missing from the result). And no
// caller may return before a rebuild that postdates its request has finished:
// the regenerate endpoint reports "regenerated" when the call returns.
func TestCoalesceRunsOnceMoreForABurstAndNeverReturnsEarly(t *testing.T) {
	var runs atomic.Int32
	var released atomic.Bool
	release := make(chan struct{})
	started := make(chan struct{}, 32)
	fn := Coalesce(func() {
		runs.Add(1)
		started <- struct{}{}
		<-release
	})

	firstDone := make(chan struct{})
	go func() { fn(); close(firstDone) }()
	<-started

	var burst sync.WaitGroup
	var early atomic.Int32
	for i := 0; i < 10; i++ {
		burst.Add(1)
		go func() {
			defer burst.Done()
			fn()
			if !released.Load() {
				early.Add(1)
			}
		}()
	}
	// Let every burst call reach the coalescer while the first run is held.
	time.Sleep(200 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		close(release)
		t.Fatalf("%d runs while one was in progress — each call in the burst started its own rebuild", got)
	}

	released.Store(true)
	close(release)
	allDone := make(chan struct{})
	go func() { <-firstDone; burst.Wait(); close(allDone) }()
	select {
	case <-allDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the burst callers were never released — no follow-up run happened, so a write " +
			"made during a rebuild would never reach the sitemap")
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("fn ran %d times for one run plus a burst of 10 — want 2: the run, then one follow-up", got)
	}
	if n := early.Load(); n > 0 {
		t.Fatalf("%d burst calls returned before any run that postdates them had finished — "+
			"a caller reporting 'regenerated' would be reporting a file that does not include its change", n)
	}

	fn() // idle again: a lone call runs once, synchronously
	if got := runs.Load(); got != 3 {
		t.Fatalf("an idle call ran fn %d times in total, want 3", got)
	}
}
