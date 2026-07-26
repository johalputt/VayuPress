// SPDX-License-Identifier: Apache-2.0

// cache_swr.go — stale-while-revalidate refresh for the public page cache.
//
// The public render cache is invalidated lazily: a global change (a deploy that
// edits templates/CSS or bumps the cache schema, or a theme/identity save)
// advances a "staleness cutoff" so every pre-rendered page is treated as stale
// on its next request (see render.CacheEntryFresh / CachePurgeAll). Historically
// the serve path then re-rendered a stale page SYNCHRONOUSLY, on the request
// goroutine. That is fine for one page, but after a global invalidation the
// ENTIRE catalog is stale at once: under real traffic (and crawlers walking the
// long tail) hundreds of cold renders fire concurrently, saturate the CPU and
// serialise on the single SQLite writer — tail latency blows out to tens of
// seconds and nginx starts returning 502s until the cache re-warms. That is the
// classic "cache-invalidation thundering herd", and it made every routine
// update briefly degrade the whole site (VayuOS included).
//
// This closes that hole. When a served entry is present-but-stale, the serve
// path now returns the stale bytes IMMEDIATELY (stale-while-revalidate) and asks
// the refresher below to re-render the page off the request path. Refreshes are:
//
//   - Single-flighted per key: concurrent hits on the same stale page schedule
//     exactly one re-render, not one per request.
//   - Globally bounded: at most N refreshes run at once (default max(2,
//     NumCPU/2), env VAYU_SWR_MAX). Past the cap, extra refreshes are dropped —
//     the page simply stays stale and is retried on its next hit or by the
//     periodic background warmer. Readers never wait either way.
//
// The net effect: a cache-invalidating deploy is latency-neutral. Visitors keep
// getting instant (briefly stale) pages while the cache catches up a few renders
// at a time, instead of a synchronous re-render storm. Truly-missing entries
// (e.g. a freshly published/edited post, whose cache file is deleted) still
// render synchronously so content edits appear immediately.
package main

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// swrRefresher performs bounded, de-duplicated background re-renders of stale
// public cache entries.
type swrRefresher struct {
	mu       sync.Mutex
	inflight map[string]bool
	sem      chan struct{}
}

func newSWRRefresher(maxConcurrent int) *swrRefresher {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &swrRefresher{inflight: make(map[string]bool), sem: make(chan struct{}, maxConcurrent)}
}

// refresh runs fn in the background for key, deduplicating concurrent requests
// for the same key and capping the total number of concurrent refreshes. If key
// is already refreshing, or the concurrency cap is reached, the call is dropped
// (the entry stays stale and is retried on the next request or by the periodic
// warmer). It never blocks the caller.
func (s *swrRefresher) refresh(key string, fn func()) {
	s.mu.Lock()
	if s.inflight[key] {
		s.mu.Unlock()
		return
	}
	select {
	case s.sem <- struct{}{}:
	default:
		// At capacity — skip; the periodic warmer or the next hit will catch it.
		s.mu.Unlock()
		return
	}
	s.inflight[key] = true
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.inflight, key)
			s.mu.Unlock()
			<-s.sem
			// A panic in a background render must never take the process down.
			if rec := recover(); rec != nil {
				// Swallow: the stale entry remains and will be retried later.
				_ = rec
			}
		}()
		fn()
	}()
}

var (
	swrOnce sync.Once
	swrInst *swrRefresher
)

func swrConcurrency() int {
	if v := strings.TrimSpace(os.Getenv("VAYU_SWR_MAX")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 64 {
			return n
		}
	}
	if n := runtime.NumCPU() / 2; n >= 2 {
		return n
	}
	return 2
}

// swrRefresh schedules a single-flighted, bounded background re-render for key.
// It lazily initialises the process-wide refresher so it works regardless of how
// the App was constructed.
func swrRefresh(key string, fn func()) {
	swrOnce.Do(func() { swrInst = newSWRRefresher(swrConcurrency()) })
	swrInst.refresh(key, fn)
}
