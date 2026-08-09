// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// seo_llms.go — /llms.txt.
//
// robots.txt says what a crawler may fetch and the sitemap says which URLs
// exist. Neither says what the site is ABOUT, and a language model answering a
// question has neither the budget nor the permission to read an archive to find
// out. /llms.txt is the emerging convention for that gap (llmstxt.org): one
// small Markdown file naming the site, what it publishes, and its posts as links
// with one-line summaries.
//
// It matters because generative engines increasingly answer from a few fetched
// pages rather than a ranked list — so the choice is between being summarised
// from whatever page happened to rank, and being summarised from an index the
// author wrote.
//
// THE SWITCH IS HONOURED. When the operator has taken the site dark to search
// engines and AI crawlers, this 404s exactly like the disallow-everything
// robots.txt does. Publishing a curated invitation while robots.txt refuses
// everything would be a contradiction the operator never asked for, and the
// crawler that reads one usually reads the other.

// llmsMaxPosts bounds the file. The format is only useful while the whole thing
// fits in a context window alongside the question being asked; an unbounded list
// on a site with ten thousand posts is a file no model will read and a database
// scan on every crawl.
const llmsMaxPosts = 200

func (a *App) handleLLMsTxt(w http.ResponseWriter, r *http.Request) {
	if a.crawlersBlocked(r.Context()) {
		http.NotFound(w, r)
		return
	}

	host := a.activeHost(r)
	origin := seo.Origin(host)

	scoped := a.multiDomain(r)
	domClause, domArgs := "", []any(nil)
	if scoped {
		domClause = " AND domain_id=?"
		domArgs = []any{a.contentScope(r)}
	}

	// Same predicate the sitemap uses — published, not a page — so the two
	// artefacts can never disagree about what the site has.
	rows, err := dbpkg.Reader().Query(
		`SELECT title,slug,COALESCE(excerpt,''),created_at FROM articles
		 WHERE COALESCE(status,'published')='published' AND COALESCE(is_page,0)=0`+domClause+
			` ORDER BY created_at DESC LIMIT ?`,
		append(domArgs, llmsMaxPosts)...)
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()

	var posts []seo.LLMsPost
	for rows.Next() {
		var title, slug, excerpt string
		var created time.Time
		if err := rows.Scan(&title, &slug, &excerpt, &created); err != nil {
			continue
		}
		posts = append(posts, seo.LLMsPost{
			Title:     title,
			URL:       origin + "/" + slug,
			Summary:   excerpt,
			Published: created,
		})
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	s := render.GetActiveSettings()
	body := seo.Render(seo.LLMsDoc{
		SiteName:    s.Name,
		Origin:      origin,
		Tagline:     s.Tagline,
		Description: s.Description,
		Posts:       posts,
		Generated:   time.Now(),
	})

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write([]byte(body))
}
