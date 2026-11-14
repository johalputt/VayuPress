// SPDX-License-Identifier: Apache-2.0

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
	"reflect"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
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

// trendingEntry is one site's memo. Each site has its own: one memo for the
// whole install served every hosted blog the install's own most-read and
// pinned posts, at addresses the blog does not serve.
type trendingEntry struct {
	payload *trendingPayload
	expiry  time.Time
	// computing is the single-flight guard: exactly one goroutine may be
	// recomputing a site's payload at a time. Without it, a burst of widget
	// fetches after a restart (the in-process memo starts empty) or a cache
	// expiry each ran the trending queries concurrently and saturated the read
	// pool — the recurring "502 for ~10 minutes after an update" the memo could
	// not absorb because it is lost on every restart.
	computing bool
}

var (
	trendingMu sync.Mutex
	// trendingSites holds an entry per site ("" is the install's own). It
	// grows only by contentScope's answers, which are the registered sites.
	trendingSites = map[string]*trendingEntry{}
	// trendingGen is bumped on every invalidation (pin/unpin). A recompute that
	// started before an invalidation must not overwrite the cache with data that
	// predates the change, so it only stores its result if the generation is
	// unchanged — otherwise a pin toggle during an in-flight compute could be
	// masked for a full TTL.
	trendingGen uint64
)

// invalidateTrendingCache drops every site's memoised payload so the next
// request rebuilds it. Called when a post is pinned/unpinned so the change
// shows up promptly. A compute in flight keeps its guard: dropping the entry
// would let a second one start beside it.
func invalidateTrendingCache() {
	trendingMu.Lock()
	for _, e := range trendingSites {
		e.payload, e.expiry = nil, time.Time{}
	}
	trendingGen++
	trendingMu.Unlock()
}

// trendingEntryFor returns site's memo, made on first use. Called with
// trendingMu held.
func trendingEntryFor(site string) *trendingEntry {
	e := trendingSites[site]
	if e == nil {
		e = &trendingEntry{}
		trendingSites[site] = e
	}
	return e
}

// handleTrendingJSON serves the trending + pinned lists of the site being read
// as JSON. It is public, cookieless and read-only (no CSRF). When the site has
// the feature off it returns an empty, disabled payload so the widget removes
// itself.
func (a *App) handleTrendingJSON(w http.ResponseWriter, r *http.Request) {
	site := a.contentScope(r)
	if a.siteSettings != nil && !a.siteSettings.FeatureEnabled(r.Context(), siteSettingsScope(site), settings.KeyFeatureTrending) {
		writeJSON(w, r, http.StatusOK, trendingPayload{
			Enabled: false,
			Pinned:  []trendingItem{},
			Windows: map[string][]trendingItem{},
		})
		return
	}

	trendingMu.Lock()
	e := trendingEntryFor(site)
	if e.payload != nil && time.Now().Before(e.expiry) {
		cached := *e.payload
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
	stale := e.payload
	iCompute := !e.computing
	if iCompute {
		e.computing = true
	}
	trendingMu.Unlock()

	if !iCompute {
		if stale != nil {
			a.writeTrending(w, r, *stale)
		} else {
			a.writeTrending(w, r, a.trendingWarming(site))
		}
		return
	}

	// This request owns the (single) recompute.
	if stale != nil {
		// Serve the stale payload immediately and refresh off the request path so
		// this caller never blocks on the DB either.
		go a.recomputeTrending(site)
		a.writeTrending(w, r, *stale)
		return
	}
	// Cold start: compute inline so the first caller gets real data; all the
	// concurrent callers during this window took the warming path above.
	a.writeTrending(w, r, a.recomputeTrending(site))
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

// recomputeTrending rebuilds site's payload from analytics, stores it with the
// right TTL, releases the single-flight guard, and returns it. It uses a
// detached, bounded context so a client disconnecting mid-flight cannot cancel
// the shared cache fill, and it always clears the guard — even on panic — so a
// failed compute can never wedge trending in the "computing" state forever.
func (a *App) recomputeTrending(site string) trendingPayload {
	trendingMu.Lock()
	gen := trendingGen
	trendingMu.Unlock()

	defer func() {
		trendingMu.Lock()
		trendingEntryFor(site).computing = false
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
	seven := a.trendingItems(ctx, site, 7, trendingWindowLimit)
	thirty := a.trendingItems(ctx, site, 30, trendingWindowLimit)
	fallback := false
	if len(seven) == 0 {
		seven = a.recentItems(ctx, site, trendingWindowLimit)
		fallback = true
	}
	if len(thirty) == 0 {
		thirty = a.recentItems(ctx, site, trendingWindowLimit)
		fallback = true
	}

	payload := trendingPayload{
		Enabled: true,
		Pinned:  a.pinnedItems(ctx, site, trendingPinnedLimit),
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
	changed := false
	if trendingGen == gen {
		e := trendingEntryFor(site)
		changed = e.payload == nil || !reflect.DeepEqual(e.payload.Pinned, payload.Pinned) || !reflect.DeepEqual(e.payload.Windows, payload.Windows)
		e.payload, e.expiry = &payload, time.Now().Add(ttl)
	}
	trendingMu.Unlock()
	// Home draws the list into its page (trendingForHome), so a cached Home
	// holds the list it was drawn with: a new list clears the cached listings,
	// and the next visit draws it.
	if changed {
		render.CachePurgeListings()
	}
	return payload
}

// trendingForHome is the site's list for Home to draw in place, so Most read
// is in the page at its first paint. Filled in after it by the widget, the
// list pushed everything under it down on a desktop, where it is in view
// (PageSpeed's layout shift, 0.121 on johal.in). A fresh list is drawn as it
// is; a stale one too, refreshed off the request as the widget's fetch would
// have. With none yet (after a restart), Home is not held for the queries: the
// list is computed in the background, the widget fills this page in as before,
// and the purge above lets the next visit draw it. Nil leaves the widget to
// fill itself in.
func (a *App) trendingForHome(r *http.Request) *render.Trending {
	site := a.contentScope(r)
	if a.siteSettings != nil && !a.siteSettings.FeatureEnabled(r.Context(), siteSettingsScope(site), settings.KeyFeatureTrending) {
		return nil
	}
	trendingMu.Lock()
	e := trendingEntryFor(site)
	p := e.payload
	refresh := !e.computing && (p == nil || !time.Now().Before(e.expiry))
	if refresh {
		e.computing = true
	}
	trendingMu.Unlock()
	if refresh {
		go a.recomputeTrending(site)
	}
	if p == nil || p.nothingToShow() {
		return nil
	}
	return &render.Trending{Pinned: trendingDrawn(p.Pinned), Week: trendingDrawn(p.Windows["7"]), Month: trendingDrawn(p.Windows["30"])}
}

func trendingDrawn(items []trendingItem) []render.TrendingItem {
	out := make([]render.TrendingItem, len(items))
	for i, it := range items {
		out[i] = render.TrendingItem{Slug: it.Slug, Title: it.Title, Image: it.Image}
	}
	return out
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
func (a *App) trendingWarming(site string) trendingPayload {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	recent := a.recentItems(ctx, site, trendingWindowLimit)
	return trendingPayload{
		Enabled:  true,
		Pinned:   a.pinnedItems(ctx, site, trendingPinnedLimit),
		Windows:  map[string][]trendingItem{"7": recent, "30": recent},
		degraded: true,
	}
}

// pinnedSQL reads the pinned posts through idx_articles_featured.
//
// The unary pluses are not typos. Without them SQLite answers the query from
// idx_articles_pagefeed (is_page, status, created_at), walks every published
// post newest first, and reads each row to test featured, which is stored after
// content and blocks_json. On johal.in, with 234k posts and none pinned, that
// read every post's body on every trending request, the warming answer
// included: 4 s a request, and the Monitoring p95 read 5.4 s after the update
// that fixed articles. The pluses leave idx_articles_featured as the only index
// the query can use, so it reads the few rows that are pinned; the site is
// tested on those rows (domain_id is stored after content too).
const pinnedSQL = `SELECT slug, title, COALESCE(feature_image,'') FROM articles
		 WHERE featured = 1 AND +domain_id = ? AND +status = 'published' AND +is_page = 0
		 ORDER BY created_at DESC LIMIT ?`

// recentSQL reads a site's newest posts through idx_articles_feed_domain
// (site, page, status, then newest first), so it stops at limit.
const recentSQL = `SELECT slug, title, COALESCE(feature_image,'') FROM articles
		 WHERE domain_id = ? AND status = 'published' AND is_page = 0
		 ORDER BY created_at DESC LIMIT ?`

// pinnedItems returns site's pinned (featured) published posts, newest first,
// capped at limit.
func (a *App) pinnedItems(ctx context.Context, site string, limit int) []trendingItem {
	out := []trendingItem{}
	if dbpkg.DB == nil {
		return out
	}
	rows, err := dbpkg.Reader().QueryContext(ctx, pinnedSQL, site, limit)
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

// recentItems returns site's most-recent published, non-page posts as
// trending items — the fallback used when analytics has no views in the window
// yet, so the widget always renders something instead of hiding itself.
func (a *App) recentItems(ctx context.Context, site string, limit int) []trendingItem {
	out := []trendingItem{}
	if dbpkg.DB == nil {
		return out
	}
	rows, err := dbpkg.Reader().QueryContext(ctx, recentSQL, site, limit)
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

// trendingItems returns site's most-viewed posts over the trailing window via
// the analytics store. Always returns a non-nil slice so the JSON encodes "[]".
func (a *App) trendingItems(ctx context.Context, site string, days, limit int) []trendingItem {
	out := []trendingItem{}
	if a.analytics == nil {
		return out
	}
	// Prefer the SAME source the admin Analytics "Top pages" panel uses (the
	// per-pageview event log) so the public Trending list matches it exactly.
	// Fall back to the daily aggregate only if that log has no data yet.
	arts, err := a.analytics.TrendingArticlesByViews(ctx, site, days, limit)
	if err != nil || len(arts) == 0 {
		if fb, ferr := a.analytics.TrendingArticles(ctx, site, days, limit); ferr == nil {
			arts = fb
		}
	}
	for _, t := range arts {
		out = append(out, trendingItem{Slug: t.Slug, Title: t.Title, Image: t.Image, Views: t.Views})
	}
	return out
}
