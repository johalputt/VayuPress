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
}

var (
	trendingMu     sync.Mutex
	trendingCache  *trendingPayload
	trendingExpiry time.Time
)

// invalidateTrendingCache drops the memoised payload so the next request rebuilds
// it. Called when a post is pinned/unpinned so the change shows up promptly.
func invalidateTrendingCache() {
	trendingMu.Lock()
	trendingCache = nil
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
	if trendingCache != nil && time.Now().Before(trendingExpiry) {
		cached := *trendingCache
		trendingMu.Unlock()
		w.Header().Set("Cache-Control", "public, max-age=300")
		writeJSON(w, r, http.StatusOK, cached)
		return
	}
	trendingMu.Unlock()

	// Bounded so a slow DB can never hang the page's widget fetch.
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
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
	trendingMu.Lock()
	trendingCache = &payload
	trendingExpiry = time.Now().Add(ttl)
	trendingMu.Unlock()

	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, r, http.StatusOK, payload)
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
