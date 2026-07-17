package vayutor

// pagestats.go — OPT-IN per-page onion visit counts. Privacy-preserving by
// construction: the only thing stored is an AGGREGATE cumulative count per page
// ("<clearnet host> <path>" → total views). There is no timestamp, no IP (an
// onion service never sees one), no session, no user agent, and no ordering — so
// two views can never be correlated and a visitor can never be deanonymised.
// Visitor geolocation is deliberately impossible and therefore absent. When the
// operator hasn't opted in (KeyTorPageStats off), nothing here runs and VayuTor
// keeps its stricter "not even the path" posture.

import (
	"context"
	"sort"
	"strings"
)

const (
	// maxTrackedPages bounds the distinct pages we count, so a flood of unique
	// paths can't grow the map without limit. Once reached, existing pages keep
	// counting but brand-new paths are ignored (dropped counts are invisible in
	// the aggregate — acceptable for a popularity metric).
	maxTrackedPages = 2000
	// topPagesShown is how many rows the admin "popular pages" table shows.
	topPagesShown = 20
	// maxPageKeyLen caps a stored key's length (defensive; real slugs are short).
	maxPageKeyLen = 256
)

// pageStatsOn reports whether the operator has opted into per-page counts.
func (e *Engine) pageStatsOn() bool {
	return e.cfg.PageStats != nil && e.cfg.PageStats()
}

// IncPage records one aggregate view of a page served over an onion. No-op
// unless per-page stats are opted in. host is the clearnet host the onion maps
// to; path is the request path (never the query string — it can carry tokens).
func (e *Engine) IncPage(host, path string) {
	if e == nil || !e.pageStatsOn() {
		return
	}
	key := pageKey(host, path)
	if key == "" {
		return
	}
	e.pageMu.Lock()
	if _, ok := e.pageHits[key]; ok || len(e.pageHits) < maxTrackedPages {
		e.pageHits[key]++
	}
	e.pageMu.Unlock()
}

// pageKey builds the aggregate map key "host path", normalised and length-capped.
func pageKey(host, path string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/"
	}
	// Defensive: a path should never contain a newline/space that would corrupt
	// the "host path" encoding; collapse any whitespace to a single space.
	path = strings.Join(strings.Fields(path), " ")
	if host == "" {
		return ""
	}
	key := host + " " + path
	if len(key) > maxPageKeyLen {
		key = key[:maxPageKeyLen]
	}
	return key
}

// splitPageKey reverses pageKey into (host, path) for display.
func splitPageKey(key string) (host, path string) {
	if i := strings.IndexByte(key, ' '); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, "/"
}

// topPages returns the n most-visited pages, highest count first (ties broken by
// key for a stable order). Aggregate only — no time or identity is available to
// return even if we wanted to.
func (e *Engine) topPages(n int) []PageHit {
	e.pageMu.Lock()
	hits := make([]PageHit, 0, len(e.pageHits))
	for k, c := range e.pageHits {
		host, path := splitPageKey(k)
		hits = append(hits, PageHit{Host: host, Path: path, Count: c})
	}
	e.pageMu.Unlock()
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Count != hits[j].Count {
			return hits[i].Count > hits[j].Count
		}
		if hits[i].Host != hits[j].Host {
			return hits[i].Host < hits[j].Host
		}
		return hits[i].Path < hits[j].Path
	})
	if n > 0 && len(hits) > n {
		hits = hits[:n]
	}
	return hits
}

// ResetPageHits clears all per-page counts (operator action). The aggregate
// visit tally is unaffected.
func (e *Engine) ResetPageHits() {
	if e == nil {
		return
	}
	e.pageMu.Lock()
	e.pageHits = map[string]int64{}
	e.pageMu.Unlock()
	// Persist the cleared state immediately so a restart doesn't reload old counts.
	if e.cfg.Store != nil {
		_ = e.cfg.Store.SavePageHits(context.Background(), map[string]int64{})
	}
}
