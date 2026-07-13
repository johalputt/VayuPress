package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/search"
)

// search_domain.go — VayuDomains Stage 2c: per-domain search results.
//
// The VayuFind index is domain-blind (one shared index), so rather than fork the
// engine we over-fetch and post-filter by ownership only when a secondary domain
// is registered. A single-domain install calls the engine exactly as before with
// no extra query — byte-identical.

// searchScoped runs a query and, when a secondary domain is active, keeps only the
// hits owned by the active domain (domain_id = contentScope). It over-fetches from
// the domain-blind engine so the post-filter still fills the requested page, then
// truncates to limit. On the single-domain / primary path it is a straight
// pass-through.
func (a *App) searchScoped(ctx context.Context, r *http.Request, q string, limit int) (search.Result, error) {
	if !a.multiDomain(r) {
		return a.search.Search(ctx, q, limit)
	}
	fetch := limit * 5
	if fetch < 100 {
		fetch = 100
	}
	if fetch > 500 {
		fetch = 500
	}
	res, err := a.search.Search(ctx, q, fetch)
	if err != nil {
		return res, err
	}
	res.Hits = filterHitsByDomain(ctx, res.Hits, a.contentScope(r), limit)
	return res, nil
}

// filterHitsByDomain keeps, in rank order, only the hits whose slug is owned by
// the given domain scope, up to limit. Ownership for the whole batch is resolved
// in a single indexed lookup so scoping adds one query, not one per hit.
func filterHitsByDomain(ctx context.Context, hits []search.Hit, scope string, limit int) []search.Hit {
	if len(hits) == 0 {
		return hits
	}
	slugs := make([]string, 0, len(hits))
	for _, h := range hits {
		slugs = append(slugs, h.Slug)
	}
	allowed := domainSlugSet(ctx, scope, slugs)
	// Bound the pre-allocation by the number of hits (the true ceiling on the
	// output) rather than the caller's limit, which is request-derived: the
	// filtered result can never exceed len(hits), so this both avoids
	// over-allocating and keeps a large/negative limit from sizing the slice.
	out := make([]search.Hit, 0, max(0, min(limit, len(hits))))
	for _, h := range hits {
		if _, ok := allowed[h.Slug]; ok {
			out = append(out, h)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// clientIndexView mirrors the search engine's client-index JSON so the per-domain
// serve path can filter it without the engine exposing its internal shape. Field
// tags must track internal/search clientIndex/clientPost.
type clientIndexView struct {
	V     string           `json:"v"`
	N     int              `json:"n"`
	Posts []clientPostView `json:"posts"`
}

type clientPostView struct {
	T string   `json:"t"`
	U string   `json:"u"`
	E string   `json:"e"`
	G []string `json:"g,omitempty"`
	D int64    `json:"d"`
}

type domIndexEntry struct {
	base    string // engine snapshot version this filtered payload was built from
	payload []byte
}

var (
	domIndexMu    sync.Mutex
	domIndexCache = map[string]domIndexEntry{} // scope -> last filtered client index
)

// scopedSearchIndex filters the engine's global client index down to the posts
// owned by the active domain, so a secondary domain's instant-search modal never
// autocompletes another domain's titles. The result is memoised per scope and
// rebuilt only when the engine's snapshot version changes (a content mutation).
// The returned version differs per domain so browsers/CDNs cache each separately.
// It fails open to the global payload on any parse error — search must not break.
func (a *App) scopedSearchIndex(ctx context.Context, scope, baseVersion string, payload []byte) ([]byte, string) {
	etag := baseVersion + "-d" + domCacheDir(scope)

	domIndexMu.Lock()
	if c, ok := domIndexCache[scope]; ok && c.base == baseVersion {
		out := c.payload
		domIndexMu.Unlock()
		return out, etag
	}
	domIndexMu.Unlock()

	var idx clientIndexView
	if err := json.Unmarshal(payload, &idx); err != nil {
		return payload, baseVersion
	}
	owned := publishedSlugSet(ctx, scope)
	kept := make([]clientPostView, 0, len(idx.Posts))
	for _, p := range idx.Posts {
		if _, ok := owned[p.U]; ok {
			kept = append(kept, p)
		}
	}
	idx.Posts = kept
	idx.N = len(owned) // total published for this domain drives the "full archive" hint
	idx.V = etag
	out, err := json.Marshal(idx)
	if err != nil {
		return payload, baseVersion
	}

	domIndexMu.Lock()
	domIndexCache[scope] = domIndexEntry{base: baseVersion, payload: out}
	domIndexMu.Unlock()
	return out, etag
}

// purgeDomainSearchIndex drops every memoised per-domain client index. Called
// when a post is reassigned between domains: that changes no search-indexed field,
// so the engine snapshot version is unchanged and the memo would otherwise serve a
// stale per-domain slice.
func purgeDomainSearchIndex() {
	domIndexMu.Lock()
	domIndexCache = map[string]domIndexEntry{}
	domIndexMu.Unlock()
}

// publishedSlugSet returns every published slug owned by the given domain scope,
// as a membership set. Bounded by the domain's own post count (not the corpus).
func publishedSlugSet(ctx context.Context, scope string) map[string]struct{} {
	out := make(map[string]struct{})
	rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT slug FROM articles WHERE domain_id=? AND COALESCE(status,'published')='published'`, scope)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out[s] = struct{}{}
		}
	}
	_ = rows.Err()
	return out
}

// domainSlugSet returns the subset of slugs that belong to the given domain scope,
// as a set for O(1) membership. One parameterised IN query; empty input is a
// no-op.
func domainSlugSet(ctx context.Context, scope string, slugs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(slugs))
	if len(slugs) == 0 {
		return out
	}
	ph := make([]string, len(slugs))
	args := make([]any, 0, len(slugs)+1)
	args = append(args, scope)
	for i, s := range slugs {
		ph[i] = "?"
		args = append(args, s)
	}
	q := `SELECT slug FROM articles WHERE domain_id=? AND slug IN (` + strings.Join(ph, ",") + `)`
	rows, err := dbpkg.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			out[s] = struct{}{}
		}
	}
	_ = rows.Err()
	return out
}
