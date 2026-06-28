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
	trendingCacheTTL    = 5 * time.Minute
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

	payload := trendingPayload{
		Enabled: true,
		Pinned:  a.pinnedItems(ctx, trendingPinnedLimit),
		Windows: map[string][]trendingItem{
			"7":  a.trendingItems(ctx, 7, trendingWindowLimit),
			"30": a.trendingItems(ctx, 30, trendingWindowLimit),
		},
	}

	trendingMu.Lock()
	trendingCache = &payload
	trendingExpiry = time.Now().Add(trendingCacheTTL)
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
	return out
}

// trendingItems returns the most-viewed posts over the trailing window via the
// analytics store. Always returns a non-nil slice so the JSON encodes "[]".
func (a *App) trendingItems(ctx context.Context, days, limit int) []trendingItem {
	out := []trendingItem{}
	if a.analytics == nil {
		return out
	}
	arts, err := a.analytics.TrendingArticles(ctx, days, limit)
	if err != nil {
		return out
	}
	for _, t := range arts {
		out = append(out, trendingItem{Slug: t.Slug, Title: t.Title, Image: t.Image, Views: t.Views})
	}
	return out
}
