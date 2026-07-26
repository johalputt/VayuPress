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
	"sync/atomic"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// handleTagIndex renders the public topic index (/tags): every distinct tag with
// its published-post count, sorted by frequency. It is rendered live rather than
// disk-cached so a newly introduced tag appears at once; a short Cache-Control
// still lets a CDN or browser hold it briefly. Drafts never contribute.
func (a *App) handleTagIndex(w http.ResponseWriter, r *http.Request) {
	var infos []render.TagInfo
	var totalPosts int

	if a.multiDomain(r) {
		// VayuDomains Stage 2c: count only the active domain's published posts, so
		// each domain's topic index reflects exactly what its tag pages will serve.
		infos, totalPosts = a.tagIndexScoped(r.Context(), a.contentScope(r))
	} else {
		tags, err := a.articles.ListTags(r.Context())
		if err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}
		infos = make([]render.TagInfo, 0, len(tags))
		for _, t := range tags {
			if strings.TrimSpace(t.Tag) == "" {
				continue
			}
			infos = append(infos, render.TagInfo{Name: t.Tag, Count: t.Count})
		}
		dbpkg.Reader().QueryRow(`SELECT COUNT(1) FROM articles WHERE status='published'`).Scan(&totalPosts)
	}

	// Most-used topics first; ties broken alphabetically for a stable, scannable list.
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Count != infos[j].Count {
			return infos[i].Count > infos[j].Count
		}
		return strings.ToLower(infos[i].Name) < strings.ToLower(infos[j].Name)
	})

	html, err := render.RenderTagIndex(config.Cfg.Domain, Version, infos, totalPosts)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	fmt.Fprint(w, html)
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
