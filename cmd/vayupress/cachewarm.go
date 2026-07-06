// cachewarm.go — polite background cache warmer.
//
// The public render cache (CacheDir/home/index.html, posts/<slug>.html, …) is
// populated lazily: the FIRST visitor to a cold URL pays the render cost and
// primes the file; everyone after hits warm cache. That means the steady-state
// hit ratio never reaches 100% on its own — every newly published or edited
// page has a cold window.
//
// This warmer closes that window. In the background it walks published pages
// and, for any whose cache entry is MISSING or STALE, drives the real render
// handler once to prime the file — so the next real visitor is a cache hit. It
// is deliberately:
//
//   - Incremental: only missing/stale entries are warmed (CacheEntryFresh); a
//     page that is already cached is skipped. It never rebuilds the whole site.
//     After a global invalidation the entries go stale and are re-warmed a few
//     at a time, not all at once.
//   - Polite: one page at a time, with a pause between each, off the hot path,
//     using the read pool — so it never spikes CPU or blocks the writer. In
//     steady state it stops early once it meets a run of already-fresh pages
//     (newest-first), so a normal pass does almost nothing.
//   - Invisible: warmer requests do not record analytics and do not count
//     toward the cache hit/miss ratio, so the numbers reflect real visitors.
//
// Tunables (env): VAYUPRESS_CACHE_WARM=0 disables it;
// VAYUPRESS_CACHE_WARM_DELAY_MS sets the per-page pause (default 250ms);
// VAYUPRESS_CACHE_WARM_INTERVAL sets the re-scan period (default 5m, min 1m).
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

// warmHeader marks a request as an internal cache-warm probe. Handlers use it
// to skip analytics and the hit/miss counters while still rendering + caching.
const warmHeader = "X-VayuPress-Cache-Warm"

const (
	defaultWarmDelay    = 250 * time.Millisecond
	defaultWarmInterval = 5 * time.Minute
	cacheWarmStartDelay = 20 * time.Second // let startup settle before warming
	warmFreshStreakStop = 50               // stop a pass after this many fresh-in-a-row
)

// isCacheWarm reports whether r is an internal cache-warm probe.
func isCacheWarm(r *http.Request) bool { return r.Header.Get(warmHeader) == "1" }

func cacheWarmEnabled() bool {
	return strings.TrimSpace(os.Getenv("VAYUPRESS_CACHE_WARM")) != "0"
}

func warmDelay() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VAYUPRESS_CACHE_WARM_DELAY_MS")); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms >= 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultWarmDelay
}

func warmInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VAYUPRESS_CACHE_WARM_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Minute {
			return d
		}
	}
	return defaultWarmInterval
}

// startCacheWarmer runs the warmer in the background until done is closed.
func (a *App) startCacheWarmer(done <-chan struct{}) {
	if !cacheWarmEnabled() || config.Cfg.CacheDir == "" {
		return
	}
	go func() {
		select {
		case <-time.After(cacheWarmStartDelay):
		case <-done:
			return
		}
		ctx, cancel := contextUntil(done)
		defer cancel()
		t := time.NewTicker(warmInterval())
		defer t.Stop()
		a.warmPass(ctx)
		for {
			select {
			case <-done:
				return
			case <-t.C:
				a.warmPass(ctx)
			}
		}
	}()
}

// contextUntil returns a context cancelled when done is closed, so in-flight
// warm renders abort promptly on shutdown.
func contextUntil(done <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// warmPass warms the homepage and every published article whose cache entry is
// missing or stale, newest first, one at a time. It stops early once it meets a
// run of already-fresh pages, so a steady-state pass is nearly free.
func (a *App) warmPass(ctx context.Context) {
	warmed := 0
	if a.warmHome(ctx) {
		warmed++
		if !a.pausePolitely(ctx) {
			return
		}
	}

	rows, err := dbpkg.Reader().Query(`SELECT slug FROM articles WHERE status='published' AND is_page=0 ORDER BY created_at DESC`)
	if err != nil {
		return
	}
	defer rows.Close()

	fresh := 0
	for rows.Next() {
		if ctx.Err() != nil {
			return
		}
		var slug string
		if rows.Scan(&slug) != nil {
			continue
		}
		if cacheFresh(filepath.Join("posts", slug+".html")) {
			// Newest-first: a run of already-warm pages means the tail is warm too.
			if fresh++; fresh >= warmFreshStreakStop {
				break
			}
			continue
		}
		fresh = 0
		if a.warmArticle(ctx, slug) {
			warmed++
			if !a.pausePolitely(ctx) {
				return
			}
		}
	}
	_ = rows.Err()
	if warmed > 0 {
		logging.LogInfo("cachewarm", "primed "+strconv.Itoa(warmed)+" page(s)")
	}
}

// pausePolitely waits the configured per-page delay, returning false if the
// context was cancelled during the wait (caller should stop).
func (a *App) pausePolitely(ctx context.Context) bool {
	if d := warmDelay(); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}

// cacheFresh reports whether the on-disk cache entry at rel (relative to
// CacheDir) is present and fresh.
func cacheFresh(rel string) bool {
	fi, err := os.Stat(filepath.Join(config.Cfg.CacheDir, rel))
	return err == nil && render.CacheEntryFresh(fi)
}

// warmHome primes the homepage cache when its entry is missing or stale.
// Returns true if it performed a render.
func (a *App) warmHome(ctx context.Context) bool {
	if cacheFresh(filepath.Join("home", "index.html")) {
		return false
	}
	req := warmRequest(ctx, "/")
	a.renderHomeAt(&discardWriter{h: make(http.Header)}, req, 1)
	return true
}

// warmArticle primes a published article's cache when its entry is missing or
// stale and the article is publicly cacheable (member-gated articles are never
// disk-cached, so warming them would waste a render). Returns true if it
// performed a render.
func (a *App) warmArticle(ctx context.Context, slug string) bool {
	if a.members != nil && a.members.GetAccess(ctx, slug) != members.AccessPublic {
		return false
	}
	req := warmRequest(ctx, "/"+slug)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	a.handleArticlePage(&discardWriter{h: make(http.Header)}, req)
	return true
}

// warmRequest builds an internal GET carrying the warm marker header.
func warmRequest(ctx context.Context, target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
	req.Header.Set(warmHeader, "1")
	return req
}

// discardWriter is a minimal http.ResponseWriter that drops the body — the
// warmer only wants the render's side effect (the written cache file), not the
// bytes.
type discardWriter struct {
	h http.Header
}

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(int)             {}
