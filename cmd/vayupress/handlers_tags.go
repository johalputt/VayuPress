// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// handleTagIndex renders the public topic index (/tags): every distinct tag with
// its published-post count, sorted by frequency. Drafts never contribute.
//
// The counts come from tagIndexCounts, at most tagIndexTTL old, never from a
// count per request: counting reads every tag link, which on johal.in is 4.6
// million rows.
func (a *App) handleTagIndex(w http.ResponseWriter, r *http.Request) {
	key, count := "", a.tagIndexGlobal
	if a.multiDomain(r) {
		// VayuDomains Stage 2c: count only the active domain's published posts, so
		// each domain's topic index reflects exactly what its tag pages will serve.
		scope := a.contentScope(r)
		key = "d:" + scope
		count = func(ctx context.Context) ([]render.TagInfo, int, error) {
			infos, total := a.tagIndexScoped(ctx, scope)
			return infos, total, nil
		}
	}
	idx, ok := tagIndexCounts(w, r, key, count)
	if !ok {
		return
	}

	html, err := render.RenderTagIndex(config.Cfg.Domain, Version, idx.infos, idx.total)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	fmt.Fprint(w, html)
}

// tagIndexTTL is how old the topic index's counts may be before they are
// counted again. It matches the page's own max-age, so a new tag shows within
// the time a browser would have held the page anyway.
const tagIndexTTL = 5 * time.Minute

type tagIndexEntry struct {
	infos []render.TagInfo // sorted; shared by every request, never modified
	total int
	at    time.Time
}

// tagIndexMemo holds the counts per scope ("" for a single-domain install).
var tagIndexMemo = struct {
	sync.Mutex
	m          map[string]tagIndexEntry
	refreshing map[string]bool
}{m: map[string]tagIndexEntry{}, refreshing: map[string]bool{}}

// tagIndexCounts returns the counts for key. Held counts are served at once;
// once they are older than tagIndexTTL, one background count per key replaces
// them while the old ones keep serving. Only the first request for a key counts
// on the request path, and it takes a render slot to do so. Without a slot it
// has already answered (503) and returns false; on a failed count it has
// answered 500.
func tagIndexCounts(w http.ResponseWriter, r *http.Request, key string,
	count func(context.Context) ([]render.TagInfo, int, error)) (tagIndexEntry, bool) {
	tagIndexMemo.Lock()
	e, have := tagIndexMemo.m[key]
	if have {
		if time.Since(e.at) >= tagIndexTTL && !tagIndexMemo.refreshing[key] {
			tagIndexMemo.refreshing[key] = true
			go refreshTagIndex(key, count)
		}
		tagIndexMemo.Unlock()
		return e, true
	}
	tagIndexMemo.Unlock()

	release, ok := admitColdRender(w, r)
	if !ok {
		return e, false
	}
	defer release()
	infos, total, err := count(r.Context())
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return e, false
	}
	return storeTagIndex(key, infos, total), true
}

// refreshTagIndex counts again off the request path. On failure the old counts
// stay, and the next request after the TTL tries again.
func refreshTagIndex(key string, count func(context.Context) ([]render.TagInfo, int, error)) {
	defer func() {
		tagIndexMemo.Lock()
		delete(tagIndexMemo.refreshing, key)
		tagIndexMemo.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	infos, total, err := count(ctx)
	if err != nil {
		logging.LogWarn("render", "topic index count failed; the previous counts stay: "+err.Error())
		return
	}
	storeTagIndex(key, infos, total)
}

func storeTagIndex(key string, infos []render.TagInfo, total int) tagIndexEntry {
	// Most-used topics first; ties broken alphabetically for a stable, scannable list.
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Count != infos[j].Count {
			return infos[i].Count > infos[j].Count
		}
		return strings.ToLower(infos[i].Name) < strings.ToLower(infos[j].Name)
	})
	e := tagIndexEntry{infos: infos, total: total, at: time.Now()}
	tagIndexMemo.Lock()
	tagIndexMemo.m[key] = e
	tagIndexMemo.Unlock()
	return e
}

// tagIndexGlobal counts the topic index of a single-domain install.
func (a *App) tagIndexGlobal(ctx context.Context) ([]render.TagInfo, int, error) {
	tags, err := a.articles.ListTags(ctx)
	if err != nil {
		return nil, 0, err
	}
	infos := make([]render.TagInfo, 0, len(tags))
	for _, t := range tags {
		if strings.TrimSpace(t.Tag) == "" {
			continue
		}
		infos = append(infos, render.TagInfo{Name: t.Tag, Count: t.Count})
	}
	var total int
	dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE status='published'`).Scan(&total)
	return infos, total, nil
}

// tagIndexScoped computes the topic index for a single domain (Stage 2c): each
// tag's published-post count and the domain's total, filtered by domain_id. It
// counts only published rows so a tag never appears here that its (published-
// only) tag page would 404 on. Runs solely on the multi-domain path; the primary
// single-domain path keeps the original global ListTags for byte-identity.
func (a *App) tagIndexScoped(ctx context.Context, scope string) ([]render.TagInfo, int) {
	infos := make([]render.TagInfo, 0, 32)
	rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT t.tag, COUNT(1) FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE a.status='published' AND a.domain_id=? GROUP BY t.tag`,
		scope,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var n int
			if err := rows.Scan(&name, &n); err != nil {
				continue
			}
			if strings.TrimSpace(name) == "" {
				continue
			}
			infos = append(infos, render.TagInfo{Name: name, Count: n})
		}
		_ = rows.Err()
	}

	var totalPosts int
	dbpkg.Reader().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM articles WHERE status='published' AND domain_id=?`, scope,
	).Scan(&totalPosts)
	return infos, totalPosts
}

// handleTagPage renders a single tag's listing page (/tags/{tag}). It serves a
// cached copy when present and regenerates on miss, mirroring handleHome. The
// per-tag cache file (tags/<tag>.html) is invalidated automatically by CachePurge
// whenever an article carrying that tag is created, updated, or deleted. A tag
// with no published posts is treated as not-found so empty pages are never indexed.
func (a *App) handleTagPage(w http.ResponseWriter, r *http.Request) {
	tag := strings.TrimSpace(chi.URLParam(r, "tag"))
	if tag == "" || len(tag) > 100 {
		a.handleNotFound(w, r)
		return
	}

	// VayuDomains Stage 2c: scope the tag listing to the active domain, but only
	// when a secondary domain is registered — a single-domain install is
	// byte-identical (same query, same cache path).
	scope := ""
	scoped := a.multiDomain(r)
	if scoped {
		scope = a.contentScope(r)
	}

	cacheRel, cacheable := render.TagPageCacheRel(tag)
	if cacheable && scope != "" {
		cacheRel = "d_" + domCacheDir(scope) + "/" + cacheRel
	}
	if cacheable {
		cachePath := filepath.Join(config.Cfg.CacheDir, filepath.FromSlash(cacheRel))
		if fi, err := os.Stat(cachePath); err == nil && render.CacheEntryFresh(fi) { //nosec G703 -- cacheRel sanitised by TagPageCacheRel (rejects unsafe path components) + domCacheDir; tag length-bounded; path confined to CacheDir
			atomic.AddInt64(&metrics.MetricCacheHits, 1)
			http.ServeFile(w, r, cachePath) //nosec G703 -- path confined to CacheDir; tag sanitised by TagPageCacheRel
			return
		}
	}
	atomic.AddInt64(&metrics.MetricCacheMisses, 1)
	release, ok := admitColdRender(w, r)
	if !ok {
		return
	}
	defer release()

	articles, total := a.articlesByTag(r.Context(), tag, 200, scope, scoped)
	if total == 0 {
		// No published post carries this tag — indistinguishable from a bad URL.
		a.handleNotFound(w, r)
		return
	}

	html, err := render.RenderTagPage(config.Cfg.Domain, Version, tag, articles, total)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	if cacheable {
		render.CacheWrite(cacheRel, html) //nolint:errcheck
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// articlesByTag returns the published articles tagged exactly with tag (case-
// insensitive), most recent first, plus the precise total match count. Membership
// is resolved through the indexed article_tags join table (migration 048): the
// tag_norm index turns this into a point lookup plus a primary-key join, so it
// stays fast at 1M+ posts instead of full-scanning the articles table with a
// `tags LIKE '%..%'` predicate. At most `max` articles are materialised for the
// page; total still reflects every published match.
func (a *App) articlesByTag(ctx context.Context, tag string, max int, scope string, scoped bool) ([]render.HomeArticle, int) {
	if dbpkg.DB == nil {
		return nil, 0
	}
	norm := strings.ToLower(strings.TrimSpace(tag))
	if norm == "" {
		return nil, 0
	}

	// Optional per-domain scope (Stage 2c): empty clause on a single-domain install.
	domClause := ""
	var domArg []any
	if scoped {
		domClause = " AND a.domain_id=?"
		domArg = []any{scope}
	}

	var total int
	dbpkg.Reader().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm=? AND a.status='published'`+domClause,
		append([]any{norm}, domArg...)...,
	).Scan(&total)
	if total == 0 {
		return nil, 0
	}

	q := `SELECT a.title,a.slug,a.content,a.tags,a.created_at FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm=? AND a.status='published'` + domClause + ` ORDER BY t.created_at DESC LIMIT ?`
	rows, err := dbpkg.Reader().QueryContext(ctx, q, append(append([]any{norm}, domArg...), max)...)
	if err != nil {
		return nil, total
	}
	defer rows.Close()

	var out []render.HomeArticle
	author := render.GetActiveSettings().Author
	for rows.Next() {
		var ha render.HomeArticle
		var content, tagsCSV string
		if err := rows.Scan(&ha.Title, &ha.Slug, &content, &tagsCSV, &ha.CreatedAt); err != nil {
			continue
		}
		ha.Tags = api.SplitTags(tagsCSV)
		ha.Excerpt = excerptFromHTML(content, 160)
		ha.Image = seo.ExtractFirstImage(content)
		ha.Author = author
		out = append(out, ha)
	}
	_ = rows.Err()
	return out, total
}
