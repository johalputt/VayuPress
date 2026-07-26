// SPDX-License-Identifier: Apache-2.0

// admin_dashcache.go — background-computed cache for heavy VayuOS dashboard
// sections (Analytics, Bot Shield engagement).
//
// Why: those panels run a dozen aggregate scans (COUNT(DISTINCT), GROUP BY, AVG)
// over the large analytics tables. Even on the dedicated admin DB lane a single
// tab load could exceed the 30s server write timeout and return 502 — because
// the work ran ON the request goroutine. This is the same failure the Monitoring
// tab avoids by reading a background snapshot (collectAdminMetrics).
//
// This generalises that snapshot idea: a request NEVER computes a heavy section
// inline. It reads the last-known HTML fragment instantly and, if that fragment
// is stale/missing, schedules exactly ONE bounded background recompute. A
// startup warmer keeps the default-window fragments hot so the panels are ready
// within moments of boot and stay fresh. Result: these tabs can no longer block
// a request into a 502, regardless of table size or load.
package main

import (
	"context"
	"runtime"
	"sync"
	"time"
)

// adminFragmentComputeTimeout bounds a single background recompute so a
// pathological scan cannot run forever and pin an admin-lane connection.
const adminFragmentComputeTimeout = 25 * time.Second

// analyticsFragmentTTL is how long a computed dashboard fragment is served
// before a background refresh is scheduled. Analytics is intentionally allowed
// to be a little stale (it is a report, not a live control) — this keeps the
// heavy scans to at most one per TTL no matter how often the tab is opened.
const analyticsFragmentTTL = 90 * time.Second

type adminFragment struct {
	html      string
	at        time.Time
	computing bool
}

// adminFragmentCache is a single-flighted, background-refreshed HTML fragment
// cache with bounded compute concurrency.
type adminFragmentCache struct {
	mu  sync.Mutex
	m   map[string]*adminFragment
	sem chan struct{}
}

func newAdminFragmentCache(maxConcurrent int) *adminFragmentCache {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &adminFragmentCache{m: make(map[string]*adminFragment), sem: make(chan struct{}, maxConcurrent)}
}

// adminDash is the process-wide dashboard fragment cache. Compute concurrency is
// capped low because each recompute draws several admin-lane connections; the
// cap keeps a burst of distinct windows from monopolising the reserved pool.
var adminDash = newAdminFragmentCache(maxInt(2, runtime.NumCPU()/2))

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// get returns the cached fragment for key. When the entry is missing or older
// than ttl it schedules a single bounded background recompute and returns the
// last-known HTML with ready = (html != ""). The caller NEVER blocks on compute.
// A compute that returns "" (hard error/timeout) is not stored, so it is retried
// on the next request or warm tick rather than caching an empty section.
func (c *adminFragmentCache) get(key string, ttl time.Duration, compute func(ctx context.Context) string) (html string, ready bool) {
	c.mu.Lock()
	e := c.m[key]
	if e == nil {
		e = &adminFragment{}
		c.m[key] = e
	}
	last := e.html
	stale := e.at.IsZero() || time.Since(e.at) >= ttl
	if stale && !e.computing {
		select {
		case c.sem <- struct{}{}:
			e.computing = true
			go func() {
				defer func() {
					<-c.sem
					c.mu.Lock()
					e.computing = false
					c.mu.Unlock()
					_ = recover() // a panic in a background render must never crash the process
				}()
				ctx, cancel := context.WithTimeout(context.Background(), adminFragmentComputeTimeout)
				defer cancel()
				h := compute(ctx)
				if h != "" {
					c.mu.Lock()
					e.html = h
					e.at = time.Now()
					c.mu.Unlock()
				}
			}()
		default:
			// All compute slots busy — skip; a later request or warm tick retries.
		}
	}
	c.mu.Unlock()
	return last, last != ""
}

// warm forces a background recompute regardless of freshness (used by the
// startup/interval warmer so the default-window fragments are always hot).
func (c *adminFragmentCache) warm(key string, compute func(ctx context.Context) string) {
	c.get(key, 0, compute)
}

// startDashboardWarmer keeps the default-window (30-day) Analytics and Bot
// Shield engagement fragments hot in the background, so those tabs are ready
// within moments of boot and never trigger an inline compute on a request. It
// runs off the request path on the dedicated admin DB lane and refreshes on a
// cadence shorter than the fragment TTL so the panels never go stale.
func (a *App) startDashboardWarmer(done <-chan struct{}) {
	warm := func() {
		if a.analytics != nil {
			label := analyticsLabelForDays(30)
			adminDash.warm("analytics:30", func(ctx context.Context) string {
				return a.renderAnalyticsBody(ctx, 30, label)
			})
		}
		if a.vaEngagement != nil {
			adminDash.warm("shield-engagement:30", func(ctx context.Context) string {
				return a.renderShieldEngagement(ctx, 30)
			})
		}
	}
	go func() {
		// Let the box settle after boot before the first (cold) compute.
		select {
		case <-time.After(10 * time.Second):
		case <-done:
			return
		}
		warm()
		t := time.NewTicker(analyticsFragmentTTL - 15*time.Second) // refresh before it expires
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				warm()
			}
		}
	}()
}
