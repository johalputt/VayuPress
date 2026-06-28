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
	tags, err := a.articles.ListTags(r.Context())
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	infos := make([]render.TagInfo, 0, len(tags))
	for _, t := range tags {
		if strings.TrimSpace(t.Tag) == "" {
			continue
		}
		infos = append(infos, render.TagInfo{Name: t.Tag, Count: t.Count})
	}
	// Most-used topics first; ties broken alphabetically for a stable, scannable list.
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Count != infos[j].Count {
			return infos[i].Count > infos[j].Count
		}
		return strings.ToLower(infos[i].Name) < strings.ToLower(infos[j].Name)
	})

	var totalPosts int
	dbpkg.Reader().QueryRow(`SELECT COUNT(1) FROM articles WHERE status='published'`).Scan(&totalPosts)

	html, err := render.RenderTagIndex(config.Cfg.Domain, Version, infos, totalPosts)
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	fmt.Fprint(w, html)
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

	cacheRel, cacheable := render.TagPageCacheRel(tag)
	if cacheable {
		cachePath := filepath.Join(config.Cfg.CacheDir, filepath.FromSlash(cacheRel))
		if fi, err := os.Stat(cachePath); err == nil && render.CacheEntryFresh(fi) { //nosec G703 -- cacheRel sanitised by TagPageCacheRel (rejects unsafe path components); tag length-bounded; path confined to CacheDir
			atomic.AddInt64(&metrics.MetricCacheHits, 1)
			http.ServeFile(w, r, cachePath) //nosec G703 -- path confined to CacheDir; tag sanitised by TagPageCacheRel
			return
		}
	}
	atomic.AddInt64(&metrics.MetricCacheMisses, 1)

	articles, total := a.articlesByTag(r.Context(), tag, 200)
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
// insensitive), most recent first, plus the precise total match count. The SQL
// LIKE filter is only a coarse pre-filter — a substring match would wrongly pair
// "go" with "golang" — so each candidate is re-checked token-by-token in Go, the
// same precise-matching strategy used by relatedArticles. At most `max` articles
// are materialised for the page; total still reflects every match.
func (a *App) articlesByTag(ctx context.Context, tag string, max int) ([]render.HomeArticle, int) {
	if dbpkg.DB == nil {
		return nil, 0
	}
	// Escape LIKE metacharacters so a tag value can never act as a wildcard.
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(tag)
	like := "%" + esc + "%"
	q := `SELECT title,slug,content,tags,created_at FROM articles WHERE tags LIKE ? ESCAPE '\' AND status='published' ORDER BY created_at DESC LIMIT 5000`
	rows, err := dbpkg.Reader().QueryContext(ctx, q, like)
	if err != nil {
		return nil, 0
	}
	defer rows.Close()

	var out []render.HomeArticle
	total := 0
	author := render.GetActiveSettings().Author
	for rows.Next() {
		var ha render.HomeArticle
		var content, tagsCSV string
		if err := rows.Scan(&ha.Title, &ha.Slug, &content, &tagsCSV, &ha.CreatedAt); err != nil {
			continue
		}
		matched := false
		for _, t := range api.SplitTags(tagsCSV) {
			if strings.EqualFold(strings.TrimSpace(t), tag) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		total++
		if len(out) < max {
			ha.Tags = api.SplitTags(tagsCSV)
			ha.Excerpt = excerptFromHTML(content, 160)
			ha.Image = seo.ExtractFirstImage(content)
			ha.Author = author
			out = append(out, ha)
		}
	}
	_ = rows.Err()
	return out, total
}
