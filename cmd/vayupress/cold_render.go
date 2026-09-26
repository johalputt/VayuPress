// SPDX-License-Identifier: Apache-2.0

package main

// cold_render.go — a ceiling on public pages rendered from the database at once.
//
// A page with a cache file costs a file read. A page without one runs the
// render's queries on the public read pool, and nothing bounded how many did
// at once. After johal.in's page cache was cleared, crawlers asked for
// thousands of uncached pages; each render held a read connection, the pool
// was never free, and every other request that needed one, the console
// included, waited out the 30-second router deadline and became a 502. The
// console's reserved pool (db.AdminReader) did not help: most console queries
// use db.Reader(), the same pool.
//
// So every public page rendered from the database takes a slot first: a post,
// the home page or a tag page without a cache file, the deeper feed pages, the
// topic index and a search. There are a third of the read
// pool's connections in slots, and at most two per CPU, which leaves the rest
// of the pool to cached pages, the console and everything else however many
// renders are asked for. A request that finds no slot free within
// coldRenderWait is answered at once with 503 and Retry-After, which crawlers
// honour, instead of joining a queue that ends in a 502. Cached pages, the
// background warmer and the console never pass through here.

import (
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
)

// coldRenderWait is how long an uncached render waits for a slot. Long enough
// to ride out a burst; far short of the 30-second limit that turned the queue
// into 502s. A var so a test can shorten it.
var coldRenderWait = 5 * time.Second

var (
	coldRenderOnce  sync.Once
	coldRenderSlots chan struct{}
	coldRenderShed  atomic.Int64 // requests told to retry, since start
	coldRenderNoted atomic.Int64 // unix seconds of the last log line about it
)

// coldRenderLimit is a third of the public read pool, at least 2 and at most
// two per CPU: rendering is CPU work as well as queries.
func coldRenderLimit() int {
	n := dbpkg.Reader().Stats().MaxOpenConnections / 3
	if c := 2 * runtime.NumCPU(); n <= 0 || n > c {
		n = c
	}
	if n < 2 {
		n = 2
	}
	return n
}

// admitColdRender takes a render slot, waiting up to coldRenderWait. With a
// slot it returns the release func and true. Without one it has already
// answered the request (503, Retry-After) and returns false.
func admitColdRender(w http.ResponseWriter, r *http.Request) (func(), bool) {
	coldRenderOnce.Do(func() { coldRenderSlots = make(chan struct{}, coldRenderLimit()) })
	t := time.NewTimer(coldRenderWait)
	defer t.Stop()
	select {
	case coldRenderSlots <- struct{}{}:
		return func() { <-coldRenderSlots }, true
	case <-t.C:
	case <-r.Context().Done():
	}
	n := coldRenderShed.Add(1)
	// One line a minute at most: a crawler burst would otherwise write one per
	// request, and the count says what matters.
	now := time.Now().Unix()
	if last := coldRenderNoted.Load(); now-last >= 60 && coldRenderNoted.CompareAndSwap(last, now) {
		logging.LogWarn("render", "uncached pages at their ceiling; visitors asked to retry ("+itoaSafe(int(n))+" since start)")
	}
	w.Header().Set("Retry-After", "30")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>One moment</title><p>This page is being prepared. Please try again in a moment.</p>`))
	return nil, false
}
