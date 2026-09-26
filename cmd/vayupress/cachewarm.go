// SPDX-License-Identifier: Apache-2.0

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
//   - Paced: it asks the pacer (internal/pace) before each batch of pages and
//     runs as fast as the host can spare: larger batches while the host has
//     room, smaller ones when it is busy, none while it is stalled. The fixed
//     250 ms pause it used before took a whole day to rebuild a large site
//     after a global invalidation, and did not slow down when visitors came.
//     In steady state it stops early once it meets a run of already-fresh
//     pages (newest-first), so a normal pass does almost nothing.
//   - Invisible: warmer requests do not record analytics and do not count
//     toward the cache hit/miss ratio, so the numbers reflect real visitors.
//
// Tunables (env): VAYUPRESS_CACHE_WARM=0 disables the periodic pass (Refresh
// every page still runs one); VAYUPRESS_CACHE_WARM_INTERVAL sets the re-scan
// period (default 5m, min 1m).
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/pace"
	"github.com/johalputt/vayupress/internal/render"
)

// warmHeader marks a request as an internal cache-warm probe. Handlers use it
// to skip analytics and the hit/miss counters while still rendering + caching.
const warmHeader = "X-VayuPress-Cache-Warm"

const (
	defaultWarmInterval = 5 * time.Minute
	cacheWarmStartDelay = 20 * time.Second // let startup settle before warming
	warmFreshStreakStop = 50               // stop a pass after this many fresh-in-a-row
)

// isCacheWarm reports whether r is an internal cache-warm probe.
func isCacheWarm(r *http.Request) bool { return r.Header.Get(warmHeader) == "1" }

func cacheWarmEnabled() bool {
	return strings.TrimSpace(os.Getenv("VAYUPRESS_CACHE_WARM")) != "0"
}

func warmInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VAYUPRESS_CACHE_WARM_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Minute {
			return d
		}
	}
	return defaultWarmInterval
}

// warmDone ends every pass, the periodic ones and a requested refresh alike:
// the process's shutdown channel, kept here because a refresh starts from a
// request, which ends long before the refresh does.
var warmDone <-chan struct{}

// startCacheWarmer runs the warmer in the background until done is closed.
func (a *App) startCacheWarmer(done <-chan struct{}) {
	warmDone = done
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
		a.routineWarm(ctx)
		for {
			select {
			case <-done:
				return
			case <-t.C:
				a.routineWarm(ctx)
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

// warmRun is one pass, as the Storage page reports it.
type warmRun struct {
	Refresh   bool // asked for by Refresh every page, not the periodic pass
	Started   time.Time
	Finished  time.Time // zero while running
	Total     int       // published posts the pass looks at
	Checked   int
	Rebuilt   int
	Removed   int // cached pages whose post no longer exists
	Freed     int64
	Pace      pace.Status // read live from job while the pass runs
	Cancelled bool

	job *pace.Job
}

// warmState holds the pass in progress, or the last one. One pass runs at a
// time: a refresh asked for while the periodic pass runs joins it.
var warmState struct {
	sync.Mutex
	run *warmRun
}

// newWarmJob paces a pass. A var so a test can pace it with its own pacer.
var newWarmJob = func() *pace.Job {
	return pace.Host().NewJob(pace.JobConfig{Min: 1, Max: 64, Step: 4})
}

// warmProgress is a copy of the pass in progress or the last one, nil if none.
func warmProgress() *warmRun {
	warmState.Lock()
	defer warmState.Unlock()
	if warmState.run == nil {
		return nil
	}
	c := *warmState.run
	// A pass told to wait is blocked inside job.Next, so the job itself is
	// the only current account of its pace.
	if c.job != nil && c.Finished.IsZero() {
		c.Pace = c.job.Status()
	}
	return &c
}

func (w *warmRun) update(f func(*warmRun)) {
	warmState.Lock()
	f(w)
	warmState.Unlock()
}

// refresh reports whether a refresh was asked for, which can happen while the
// pass runs.
func (w *warmRun) refresh() bool {
	warmState.Lock()
	defer warmState.Unlock()
	return w.Refresh
}

// routineWarm runs the periodic pass unless another pass is running.
func (a *App) routineWarm(ctx context.Context) {
	warmState.Lock()
	if warmState.run != nil && warmState.run.Finished.IsZero() {
		warmState.Unlock()
		return
	}
	run := &warmRun{Started: time.Now()}
	warmState.run = run
	warmState.Unlock()
	a.warmPass(ctx, run)
}

// startRefresh begins a refresh pass in the background, or turns the pass
// already running into one. It reports whether it joined a running pass.
func (a *App) startRefresh() (joined bool) {
	warmState.Lock()
	defer warmState.Unlock()
	if warmState.run != nil && warmState.run.Finished.IsZero() {
		warmState.run.Refresh = true
		return true
	}
	run := &warmRun{Refresh: true, Started: time.Now()}
	warmState.run = run
	done := warmDone
	if done == nil {
		done = make(chan struct{}) // no shutdown channel: the pass runs to its end
	}
	ctx, cancel := contextUntil(done)
	go func() {
		defer cancel()
		a.warmPass(ctx, run)
	}()
	return false
}

// warmPass rebuilds the homepage and every published post whose cache entry
// is missing or stale, newest first, in batches the pacer sizes. A routine
// pass stops early at a run of fresh pages; a refresh goes through every post
// and then removes cached pages whose post no longer exists.
func (a *App) warmPass(ctx context.Context, run *warmRun) {
	job := newWarmJob()
	run.update(func(w *warmRun) { w.job = job })
	defer func() {
		var rebuilt, removed int
		run.update(func(w *warmRun) {
			w.Finished = time.Now()
			w.Pace = job.Status()
			w.Cancelled = ctx.Err() != nil
			rebuilt, removed = w.Rebuilt, w.Removed
		})
		if rebuilt > 0 || removed > 0 {
			logging.LogInfo("cachewarm", "rebuilt "+strconv.Itoa(rebuilt)+" page"+plural(rebuilt)+", removed "+strconv.Itoa(removed))
		}
	}()
	budget := 0 // pages this batch may still rebuild
	next := func() bool {
		if budget > 0 {
			return true
		}
		n, err := job.Next(ctx)
		if err != nil {
			return false
		}
		budget = n
		return true
	}

	if !cacheFresh(filepath.Join("home", "index.html")) {
		if !next() {
			return
		}
		a.warmHome(ctx)
		budget--
		run.update(func(w *warmRun) { w.Rebuilt++ })
	}

	// Materialise the slug list up front and release the read-pool connection
	// immediately: holding the cursor for the whole pass pinned a read
	// connection and a WAL snapshot for hours on a large catalogue.
	slugs, err := publishedSlugs(`SELECT slug FROM articles WHERE status='published' AND is_page=0 ORDER BY created_at DESC`)
	if err != nil {
		return
	}
	run.update(func(w *warmRun) { w.Total = len(slugs) })

	fresh := 0
	for _, slug := range slugs {
		if ctx.Err() != nil {
			return
		}
		run.update(func(w *warmRun) { w.Checked++ })
		if cacheFresh(filepath.Join("posts", slug+".html")) {
			// Newest-first: a run of already-warm pages means the tail is warm
			// too, unless every page was asked for.
			if fresh++; fresh >= warmFreshStreakStop && !run.refresh() {
				return
			}
			continue
		}
		fresh = 0
		if !next() {
			return
		}
		if a.warmArticle(ctx, slug) {
			budget--
			run.update(func(w *warmRun) { w.Rebuilt++ })
		}
	}
	if run.refresh() {
		a.removeOrphanPages(ctx, next, &budget, run)
	}
}

// publishedSlugs reads a list of slugs, releasing the connection at once.
func publishedSlugs(q string) ([]string, error) {
	rows, err := dbpkg.Reader().Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			out = append(out, slug)
		}
	}
	return out, rows.Err()
}

// removeOrphanPages deletes cached pages nobody can ask for any more, in the
// pass's paced batches: post pages whose post is no longer published, and the
// per-domain pages of domains no longer registered. They are the space a
// clear used to free; every other page is kept. Tag pages of unused tags stay:
// they are stale, answer 404 once asked for, and hold little.
func (a *App) removeOrphanPages(ctx context.Context, next func() bool, budget *int, run *warmRun) {
	live, err := publishedSlugs(`SELECT slug FROM articles WHERE status='published'`)
	if err != nil {
		return
	}
	keep := make(map[string]bool, len(live))
	for _, s := range live {
		keep[s+".html"] = true
	}
	remove := func(path string, size int64) bool {
		if ctx.Err() != nil || !next() {
			return false
		}
		if os.Remove(path) == nil {
			*budget--
			run.update(func(w *warmRun) { w.Removed++; w.Freed += size })
		}
		return true
	}

	// posts/ is read through ReadDir, which follows it if the operator moved
	// it to another disk behind a symlink.
	posts := filepath.Join(config.Cfg.CacheDir, "posts")
	if entries, err := os.ReadDir(posts); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !e.Type().IsRegular() || !strings.HasSuffix(name, ".html") || keep[name] {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if !remove(filepath.Join(posts, name), info.Size()) {
				return
			}
		}
	}

	registered := map[string]bool{}
	if a.domains != nil {
		ds, err := a.domains.List(ctx)
		if err != nil {
			return // unsure which domains exist: remove none
		}
		for _, d := range ds {
			registered["d_"+domCacheDir(d.ID)] = true
		}
	}
	// Retired domains' pages: CACHE_DIR/d_<domain>/… and home/d_<domain>/….
	for _, parent := range []string{config.Cfg.CacheDir, filepath.Join(config.Cfg.CacheDir, "home")} {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || !strings.HasPrefix(name, "d_") || len(name) < 3 || registered[name] {
				continue // a symlinked directory is not IsDir here, and is left alone
			}
			dir := filepath.Join(parent, name)
			stopped := false
			var dirs []string
			_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err == nil && d.IsDir() {
					dirs = append(dirs, path)
				}
				if err != nil || !d.Type().IsRegular() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				if !remove(path, info.Size()) {
					stopped = true
					return filepath.SkipAll
				}
				return nil
			})
			if stopped {
				return
			}
			// Deepest first; os.Remove leaves any directory still holding a
			// file that could not be removed.
			for i := len(dirs) - 1; i >= 0; i-- {
				_ = os.Remove(dirs[i])
			}
		}
	}
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
