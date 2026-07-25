package main

// handlers_trending.go — public "Trending & pinned posts" JSON endpoint.
//
// The homepage and every article page carry a tiny client-side widget
// (static/js/trending.js) that fetches this endpoint and renders two lists:
//   • Pinned   — the operator's featured posts (reuses the `featured` column),
//                capped at 4, newest first.
//   • Trending — the most-viewed published posts over the last 7 and 30 days,
//                taken from the built-in cookieless analytics (analytics_daily).
//
// Why client-side: public pages are cached to disk (home/index.html,
// posts/<slug>.html) and only re-rendered on content edits, whereas trending
// changes continuously. Serving the lists as JSON and hydrating them in the
// browser keeps the cache valid and the lists fresh without any invalidation
// churn. The payload is itself memoised in-process for a few minutes so a busy
// site answers from memory, and it carries a short public Cache-Control so the
// browser/proxy can reuse it too.

import (
	"context"
	"net/http"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

const (
	trendingPinnedLimit = 4
	trendingWindowLimit = 10
	// The trending ranking is a trailing-7-day analytics window recomputed on a
	// daily cadence, so the list stays stable through the day and refreshes at
	// least every 24 hours. Pin/unpin invalidates it immediately regardless.
	trendingCacheTTL = 24 * time.Hour
	// When analytics has no views yet and we fall back to recent posts, refresh
	// much sooner so real trending takes over as soon as views arrive.
	trendingFallbackTTL = 1 * time.Hour
)

type trendingItem struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Image string `json:"image,omitempty"`
	Views int64  `json:"views,omitempty"`
}

type trendingPayload struct {
	Enabled bool                      `json:"enabled"`
	Pinned  []trendingItem            `json:"pinned"`
	Windows map[string][]trendingItem `json:"windows"` // "7" and "30" → ranked posts

	// degraded marks a payload built on a cold/partial path (the cheap warming
	// response, or a compute whose queries came back empty). Not serialised — it
	// only decides the cache header, because a degraded payload must never be
	// stored by a browser or proxy. See writeTrending.
	degraded bool
}

// nothingToShow reports whether the payload would leave the widget hidden: the
// script bails out when there are no pinned posts and both windows are empty.
func (p trendingPayload) nothingToShow() bool {
	return len(p.Pinned) == 0 && len(p.Windows["7"]) == 0 && len(p.Windows["30"]) == 0
}

var (
	trendingMu     sync.Mutex
	trendingCache  *trendingPayload
	trendingExpiry time.Time
	// trendingComputing is the single-flight guard: exactly one goroutine may be
	// recomputing the payload at a time. Without it, a burst of widget fetches
	// after a restart (the in-process memo starts empty) or a cache expiry each
	// ran the trending queries concurrently and saturated the read pool — the
	// recurring "502 for ~10 minutes after an update" the memo could not absorb
	// because it is lost on every restart.
	trendingComputing bool
	// trendingGen is bumped on every invalidation (pin/unpin). A recompute that
	// started before an invalidation must not overwrite the cache with data that
	// predates the change, so it only stores its result if the generation is
	// unchanged — otherwise a pin toggle during an in-flight compute could be
	// masked for a full TTL.
	trendingGen uint64
)

// invalidateTrendingCache drops the memoised payload so the next request rebuilds
// it. Called when a post is pinned/unpinned so the change shows up promptly.
func invalidateTrendingCache() {
	trendingMu.Lock()
	trendingCache = nil
	trendingExpiry = time.Time{}
	trendingGen++
	trendingMu.Unlock()
}

// handleTrendingJSON serves the trending + pinned lists as JSON. It is public,
// cookieless and read-only (no CSRF). When the feature is disabled it returns an
// empty, disabled payload so the widget removes itself.
func (a *App) handleTrendingJSON(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings != nil && !a.siteSettings.FeatureEnabled(r.Context(), settings.KeyFeatureTrending) {
		writeJSON(w, r, http.StatusOK, trendingPayload{
			Enabled: false,
			Pinned:  []trendingItem{},
			Windows: map[string][]trendingItem{},
		})
		return
	}

	trendingMu.Lock()
	fresh := trendingCache != nil && time.Now().Before(trendingExpiry)
	if fresh {
		cached := *trendingCache
		trendingMu.Unlock()
		a.writeTrending(w, r, cached)
		return
	}
	// Not fresh (cold after a restart, or expired). Single-flight the recompute so
	// a burst of widget fetches can never run the trending queries concurrently
	// and saturate the read pool. Exactly one goroutine computes; everyone else is
	// served instantly from the last good payload (stale-while-revalidate) or,
	// on a truly cold start, a cheap pinned+recent "warming" payload — nobody
	// blocks on the DB behind the herd.
	stale := trendingCache
	iCompute := !trendingComputing
	if iCompute {
		trendingComputing = true
	}
	trendingMu.Unlock()

	if !iCompute {
		if stale != nil {
			a.writeTrending(w, r, *stale)
		} else {
			a.writeTrending(w, r, a.trendingWarming(r))
		}
		return
	}

	// This request owns the (single) recompute.
	if stale != nil {
		// Serve the stale payload immediately and refresh off the request path so
		// this caller never blocks on the DB either.
		go a.recomputeTrending()
		a.writeTrending(w, r, *stale)
		return
	}
	// Cold start: compute inline so the first caller gets real data; all the
	// concurrent callers during this window took the warming path above.
	a.writeTrending(w, r, a.recomputeTrending())
}

// writeTrending sends a trending payload. A GOOD payload carries the short
// public cache header so browsers/proxies can reuse it for a few minutes.
//
// A degraded or empty payload is explicitly NOT cacheable. This is the fix for
// the widget appearing on some page loads and not others: the widget hides itself
// when a payload has nothing to show, and the old unconditional
// `public, max-age=300` let that empty answer be stored — so a reader who
// happened to fetch during a cold start (or whose warming response came back
// empty) kept getting the hidden widget from their own browser cache for the next
// five minutes, across every post they opened. Serving no-store for the
// degraded case means the very next page load re-asks and gets real data.
func (a *App) writeTrending(w http.ResponseWriter, r *http.Request, p trendingPayload) {
	switch {
	case p.Enabled && (p.degraded || p.nothingToShow()):
		w.Header().Set("Cache-Control", "no-store")
	default:
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	writeJSON(w, r, http.StatusOK, p)
}

// recomputeTrending rebuilds the payload from analytics, stores it with the
// right TTL, releases the single-flight guard, and returns it. It uses a
// detached, bounded context so a client disconnecting mid-flight cannot cancel
// the shared cache fill, and it always clears the guard — even on panic — so a
// failed compute can never wedge trending in the "computing" state forever.
func (a *App) recomputeTrending() trendingPayload {
	trendingMu.Lock()
	gen := trendingGen
	trendingMu.Unlock()

	defer func() {
		trendingMu.Lock()
		trendingComputing = false
		trendingMu.Unlock()
		_ = recover() // a background render/query panic must never crash the process
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// Trending = most-viewed posts over the trailing 7 (and 30) days from the
	// cookieless analytics. On a young or low-traffic site there may be no views
	// yet, which would leave the widget empty; fall back to the most-recent posts
	// so the section always shows, and cache that fallback only briefly so real
	// analytics trending takes over as soon as views arrive.
	seven := a.trendingItems(ctx, 7, trendingWindowLimit)
	thirty := a.trendingItems(ctx, 30, trendingWindowLimit)
	fallback := false
	if len(seven) == 0 {
		seven = a.recentItems(ctx, trendingWindowLimit)
		fallback = true
	}
	if len(thirty) == 0 {
		thirty = a.recentItems(ctx, trendingWindowLimit)
		fallback = true
	}

	payload := trendingPayload{
		Enabled: true,
		Pinned:  a.pinnedItems(ctx, trendingPinnedLimit),
		Windows: map[string][]trendingItem{"7": seven, "30": thirty},
	}
	ttl := trendingCacheTTL
	if fallback {
		ttl = trendingFallbackTTL
	}
	// A payload with nothing in it is never worth memoising: on a site that HAS
	// posts, an empty result means the queries failed or timed out transiently, and
	// caching it would hide the widget for everyone until the TTL expired (up to an
	// hour). Mark it degraded — so it is not stored client-side either — and leave
	// the memo empty so the next request retries.
	if payload.nothingToShow() {
		payload.degraded = true
		return payload
	}
	trendingMu.Lock()
	// Only publish if no invalidation (pin/unpin) landed while we were computing;
	// otherwise our result predates the change and would mask it for a full TTL.
	if trendingGen == gen {
		trendingCache = &payload
		trendingExpiry = time.Now().Add(ttl)
	}
	trendingMu.Unlock()
	return payload
}

// trendingWarming builds a cheap payload from only indexed queries (pinned +
// most-recent posts, no analytics aggregation) to serve during the brief cold
// window while the first real recompute is in flight. It never runs the heavy
// trending queries, so a cold-start herd cannot saturate the DB.
// It uses a DETACHED context, not the request's: a reader who navigates away
// mid-fetch would otherwise cancel these queries, and the resulting empty payload
// used to be sent with a five-minute public cache header — hiding the widget for
// that visitor across subsequent posts. The payload is always marked degraded so
// it is never stored by a browser or proxy either way.
func (a *App) trendingWarming(_ *http.Request) trendingPayload {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	recent := a.recentItems(ctx, trendingWindowLimit)
	return trendingPayload{
		Enabled:  true,
		Pinned:   a.pinnedItems(ctx, trendingPinnedLimit),
		Windows:  map[string][]trendingItem{"7": recent, "30": recent},
		degraded: true,
	}
}

// pinnedItems returns the operator's pinned (featured) published posts, newest
// first, capped at limit. Reuses the existing `featured` column + idx.
func (a *App) pinnedItems(ctx context.Context, limit int) []trendingItem {
	out := []trendingItem{}
	if dbpkg.DB == nil {
		return out
	}
	rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT slug, title, COALESCE(feature_image,'') FROM articles
		 WHERE featured = 1 AND status = 'published' AND is_page = 0
		 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var it trendingItem
		if rows.Scan(&it.Slug, &it.Title, &it.Image) == nil {
			out = append(out, it)
		}
	}
	_ = rows.Err() // best-effort widget data; partial results are acceptable
	return out
}

// recentItems returns the most-recent published, non-page posts as trending
// items — the fallback used when analytics has no views in the window yet, so
// the widget always renders something instead of hiding itself.
func (a *App) recentItems(ctx context.Context, limit int) []trendingItem {
	out := []trendingItem{}
	if dbpkg.DB == nil {
		return out
	}
	rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT slug, title, COALESCE(feature_image,'') FROM articles
		 WHERE status = 'published' AND is_page = 0
		 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var it trendingItem
		if rows.Scan(&it.Slug, &it.Title, &it.Image) == nil {
			out = append(out, it)
		}
	}
	_ = rows.Err() // best-effort widget data
	return out
}

// trendingItems returns the most-viewed posts over the trailing window via the
// analytics store. Always returns a non-nil slice so the JSON encodes "[]".
func (a *App) trendingItems(ctx context.Context, days, limit int) []trendingItem {
	out := []trendingItem{}
	if a.analytics == nil {
		return out
	}
	// Prefer the SAME source the admin Analytics "Top pages" panel uses (the
	// per-pageview event log) so the public Trending list matches it exactly.
	// Fall back to the daily aggregate only if that log has no data yet.
	arts, err := a.analytics.TrendingArticlesByViews(ctx, days, limit)
	if err != nil || len(arts) == 0 {
		if fb, ferr := a.analytics.TrendingArticles(ctx, days, limit); ferr == nil {
			arts = fb
		}
	}
	for _, t := range arts {
		out = append(out, trendingItem{Slug: t.Slug, Title: t.Title, Image: t.Image, Views: t.Views})
	}
	return out
}
