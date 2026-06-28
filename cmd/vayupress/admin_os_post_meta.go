package main

// admin_os_post_meta.go — per-post publishing options (the editor's "Post
// settings" panel). These fields live in dedicated articles columns (migration
// 045) and are written through a direct, synchronous column update — the same
// side-car pattern used for blocks_json — so the authoritative article write
// pipeline (content/title/tags via the article service queue) is untouched and
// the two never contend (they target disjoint columns).
//
// Every field is optional: a blank value means "fall back to the derived
// default" at render time, so existing posts look identical until an operator
// sets something explicitly. Values are stored verbatim (escaping happens at
// the render barrier) and the public surfaces are emitted through html/template,
// so none of these strings can break out of their attribute/JSON context.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
)

// PostMeta is the per-post publishing-options document. JSON tags match the
// editor's panel model (static/js/admin-os-editor.js) so it round-trips through
// the save payload and the hydration script tag unchanged.
type PostMeta struct {
	Excerpt            string `json:"excerpt"`
	FeatureImage       string `json:"featureImage"`
	MetaTitle          string `json:"metaTitle"`
	MetaDescription    string `json:"metaDescription"`
	CanonicalURL       string `json:"canonicalURL"`
	OGTitle            string `json:"ogTitle"`
	OGDescription      string `json:"ogDescription"`
	OGImage            string `json:"ogImage"`
	TwitterTitle       string `json:"twitterTitle"`
	TwitterDescription string `json:"twitterDescription"`
	TwitterImage       string `json:"twitterImage"`
	Featured           bool   `json:"featured"`
	IsPage             bool   `json:"isPage"`
}

// loadPostMeta reads the publishing-options columns for a slug. A missing row or
// a pre-045 database yields a zero-value PostMeta (all derived defaults apply).
func loadPostMeta(ctx context.Context, slug string) PostMeta {
	var m PostMeta
	if dbpkg.DB == nil {
		return m
	}
	var featured, isPage int
	_ = dbpkg.DB.QueryRowContext(ctx, `SELECT
		COALESCE(excerpt,''), COALESCE(feature_image,''),
		COALESCE(meta_title,''), COALESCE(meta_description,''), COALESCE(canonical_url,''),
		COALESCE(og_title,''), COALESCE(og_description,''), COALESCE(og_image,''),
		COALESCE(twitter_title,''), COALESCE(twitter_description,''), COALESCE(twitter_image,''),
		COALESCE(featured,0), COALESCE(is_page,0)
		FROM articles WHERE slug = ?`, slug).Scan(
		&m.Excerpt, &m.FeatureImage,
		&m.MetaTitle, &m.MetaDescription, &m.CanonicalURL,
		&m.OGTitle, &m.OGDescription, &m.OGImage,
		&m.TwitterTitle, &m.TwitterDescription, &m.TwitterImage,
		&featured, &isPage,
	)
	m.Featured = featured != 0
	m.IsPage = isPage != 0
	return m
}

// savePostMeta writes the publishing-options columns for a slug. Strings are
// trimmed; booleans become 0/1. It is a single synchronous UPDATE on disjoint
// columns, so it never races the queued content/title/tags write.
func savePostMeta(ctx context.Context, slug string, m PostMeta) error {
	if dbpkg.DB == nil {
		return nil
	}
	featured, isPage := 0, 0
	if m.Featured {
		featured = 1
	}
	if m.IsPage {
		isPage = 1
	}
	_, err := dbpkg.WDB.Exec(`UPDATE articles SET
		excerpt=?, feature_image=?,
		meta_title=?, meta_description=?, canonical_url=?,
		og_title=?, og_description=?, og_image=?,
		twitter_title=?, twitter_description=?, twitter_image=?,
		featured=?, is_page=?
		WHERE slug=?`,
		strings.TrimSpace(m.Excerpt), strings.TrimSpace(m.FeatureImage),
		strings.TrimSpace(m.MetaTitle), strings.TrimSpace(m.MetaDescription), strings.TrimSpace(m.CanonicalURL),
		strings.TrimSpace(m.OGTitle), strings.TrimSpace(m.OGDescription), strings.TrimSpace(m.OGImage),
		strings.TrimSpace(m.TwitterTitle), strings.TrimSpace(m.TwitterDescription), strings.TrimSpace(m.TwitterImage),
		featured, isPage, slug)
	if err == nil {
		// A pin/unpin (featured) change must surface promptly in the public
		// Trending & pinned widget, so drop its memoised payload.
		invalidateTrendingCache()
	}
	return err
}

// setPublishDate overrides an article's publish date (created_at). The editor
// exposes this as the post's "publish date"; the article template and feeds use
// created_at as the canonical published time. An empty/zero time is ignored.
func setPublishDate(ctx context.Context, slug string, t time.Time) error {
	if dbpkg.DB == nil || t.IsZero() {
		return nil
	}
	_, err := dbpkg.WDB.Exec(`UPDATE articles SET created_at=? WHERE slug=?`, t.UTC(), slug)
	return err
}

// handleOSEditorSlug renames an existing post's URL slug. It is synchronous and
// self-contained so it never races the queued content writer: it validates and
// uniquifies the target, moves the row (and its blocks side-car travels with it
// since slug is the row key), then purges the public caches for both the old and
// new URLs so links update immediately.
func (a *App) handleOSEditorSlug(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug    string `json:"slug"`
		NewSlug string `json:"newSlug"`
	}
	if err := readJSONDirect(r, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	old := strings.TrimSpace(body.Slug)
	next := migrateSlugify(strings.TrimSpace(body.NewSlug))
	if old == "" || next == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input", "slug and a non-empty newSlug are required", "")
		return
	}
	if next == old {
		writeJSON(w, r, http.StatusOK, map[string]string{"slug": old})
		return
	}
	// Fetch the current row (also confirms it exists) and its tags for purge.
	art, err := a.articles.Get(r.Context(), old)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No article with that slug", "")
		return
	}
	// Uniquify against collisions, preserving the operator's intent.
	base := next
	for i := 2; i <= 99; i++ {
		if _, e := a.articles.Get(r.Context(), next); e != nil {
			break // available
		}
		next = base + "-" + strconv.Itoa(i)
	}
	if _, err := dbpkg.WDB.Exec(`UPDATE articles SET slug=?, updated_at=? WHERE slug=?`, next, time.Now().UTC(), old); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "update-error", err.Error(), "")
		return
	}
	// Purge both URLs (article page, home, tag pages, sitemap, feed).
	render.CachePurge(old, art.Tags, generateSitemap, generateRSS, generateRobots)
	render.CachePurge(next, art.Tags, generateSitemap, generateRSS, generateRobots)
	writeJSON(w, r, http.StatusOK, map[string]string{"slug": next})
}
