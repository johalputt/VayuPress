// SPDX-License-Identifier: Apache-2.0

package main

// markdown_pages.go — a post, and a site's newest posts, as Markdown for
// agents and tools (internal/mdrender): /<slug>.md and /index.md. Rendered
// from the post's own model, not from its page, and only for what a reader
// who is not signed in may read: a post behind a membership or a payment
// has no Markdown at all. Each is cached beside the post's HTML and purged
// with it (render.CachePurgePost, render.CachePurge, CachePurgeListings).

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mdrender"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

// markdownHost is the site's own host as registered, never the request's
// Host header: the Markdown is cached, so a forged Host would put another
// address in every reader's copy.
func markdownHost(r *http.Request) string {
	if d, ok := activeDomain(r); ok && d.Host != "" {
		return d.Host
	}
	return config.Cfg.Domain
}

// markdownListingSize is how many posts /index.md lists.
const markdownListingSize = 30

// errNoMarkdown is a post with no Markdown for this reader: missing, not
// published, a page, another site's, or not public.
var errNoMarkdown = errors.New("no markdown")

// postMarkdown renders slug as Markdown for a reader of site, or
// errNoMarkdown. origin is the site's own https address.
func (a *App) postMarkdown(ctx context.Context, site, slug, origin string) (string, error) {
	var (
		title, content, blocks, tagsCSV, status, owner string
		isPage                                         int
		created                                        time.Time
	)
	err := dbpkg.Reader().QueryRowContext(ctx, `SELECT title, content, COALESCE(blocks_json,''), COALESCE(tags,''), status, is_page, domain_id, created_at FROM articles WHERE slug = ?`, slug).
		Scan(&title, &content, &blocks, &tagsCSV, &status, &isPage, &owner, &created)
	if err != nil || status != "published" || isPage != 0 || owner != site {
		return "", errNoMarkdown
	}
	if a.members != nil && a.members.GetAccess(ctx, slug) != members.AccessPublic {
		return "", errNoMarkdown
	}
	var tags []string
	for _, t := range strings.Split(tagsCSV, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return mdrender.RenderPost(mdrender.Post{
		Title: title, URL: origin + "/" + slug, Published: created, Tags: tags,
		BlocksJSON: blocks, HTML: content,
	}), nil
}

// listingMarkdown renders site's newest public posts.
func (a *App) listingMarkdown(ctx context.Context, site, name, origin string) (string, error) {
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT slug, title, COALESCE(excerpt,''), created_at FROM articles
		WHERE domain_id = ? AND is_page = 0 AND status = 'published' ORDER BY created_at DESC LIMIT ?`, site, markdownListingSize*2)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var items []mdrender.Item
	for rows.Next() && len(items) < markdownListingSize {
		var slug string
		var it mdrender.Item
		if err := rows.Scan(&slug, &it.Title, &it.Excerpt, &it.Published); err != nil {
			continue
		}
		if a.members != nil && a.members.GetAccess(ctx, slug) != members.AccessPublic {
			continue
		}
		it.URL = origin + "/" + slug
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return mdrender.RenderListing(name, items), nil
}

// serveMarkdown writes md from the cache file at path, rendering and
// storing it there first when it is missing.
func serveMarkdown(w http.ResponseWriter, path string, build func() (string, error)) error {
	var body []byte
	var err error
	// A file older than the install-wide stale mark (render.CachePurgeAll)
	// is rendered again, as a page would be.
	if fi, serr := os.Stat(path); serr == nil && render.CacheEntryFresh(fi) {
		body, err = os.ReadFile(path) //nosec G304 -- path is built from a validated slug or a site id under CacheDir
	} else {
		err = os.ErrNotExist
	}
	if err != nil {
		md, rerr := build()
		if rerr != nil {
			return rerr
		}
		body = []byte(md)
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o750); mkErr == nil {
			_ = os.WriteFile(path, body, 0o640)
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/markdown; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
	return nil
}

// handlePostMarkdown serves /<slug>.md.
func (a *App) handlePostMarkdown(w http.ResponseWriter, r *http.Request, slug string) {
	if !api.IsValidSlug(slug) {
		a.handleNotFound(w, r)
		return
	}
	site, origin := a.contentScope(r), "https://"+markdownHost(r)
	path := filepath.Join(config.Cfg.CacheDir, "posts", slug+".md")
	err := serveMarkdown(w, path, func() (string, error) { return a.postMarkdown(r.Context(), site, slug, origin) })
	if err != nil {
		a.handleNotFound(w, r)
	}
}

// handleIndexMarkdown serves /index.md: the site's newest public posts.
func (a *App) handleIndexMarkdown(w http.ResponseWriter, r *http.Request) {
	site, host := a.contentScope(r), markdownHost(r)
	name := site
	if name == "" {
		name = "primary"
	}
	path := filepath.Join(config.Cfg.CacheDir, "home", "listing-"+name+".md")
	err := serveMarkdown(w, path, func() (string, error) {
		return a.listingMarkdown(r.Context(), site, host, "https://"+host)
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		a.handleNotFound(w, r)
	}
}

// mcpPostMarkdown is a post's Markdown for VayuMCP's get_post. The caller
// holds a key that may read every post, drafts and members' posts too, so
// no reader rule applies; the address is the post's site's own.
func mcpPostMarkdown(ctx context.Context, art dbpkg.Article) string {
	var blocks string
	_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COALESCE(blocks_json,'') FROM articles WHERE slug = ?`, art.Slug).Scan(&blocks)
	host := config.Cfg.Domain
	if art.DomainID != "" {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT host FROM domains WHERE id = ?`, art.DomainID).Scan(&host)
	}
	return mdrender.RenderPost(mdrender.Post{
		Title: art.Title, URL: "https://" + host + "/" + art.Slug, Published: art.CreatedAt, Tags: art.Tags,
		BlocksJSON: blocks, HTML: art.Content,
	})
}
