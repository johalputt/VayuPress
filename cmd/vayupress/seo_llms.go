// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
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

// llmsOpeningChars is how much of a post with no excerpt is read to summarise
// it: more than a summary needs, so markup and a diagram before the first
// paragraph still leave one.
const llmsOpeningChars = 4000

// llmsMaxPinned and llmsMaxTopics keep the two short sections short: they
// are what to read first, and a long list is not that.
const (
	llmsMaxPinned = 10
	llmsMaxTopics = 12
)

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
	posts, err := llmsPosts(r.Context(), origin,
		`WHERE COALESCE(status,'published')='published' AND COALESCE(is_page,0)=0`+domClause+` ORDER BY created_at DESC LIMIT ?`,
		append(domArgs, llmsMaxPosts))
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	// The trending widget's predicate (pinnedSQL), with its unary pluses so
	// the featured index is the one read.
	pinned, _ := llmsPosts(r.Context(), origin,
		`WHERE featured = 1 AND +status = 'published' AND +is_page = 0`+strings.Replace(domClause, "domain_id", "+domain_id", 1)+` ORDER BY created_at DESC LIMIT ?`,
		append(domArgs, llmsMaxPinned))

	// Topics come from the index Halcyon's pages read (a memo, refreshed in
	// the background), so this costs no count of its own; an install that
	// serves no articles has no index to read.
	var topics []seo.LLMsLink
	if a.articles != nil {
		key, _, _, count := a.halcyonScope(r)
		all, total := a.halcyonTopics(key, count)
		for _, t := range render.OfferedTopics(all, total, llmsMaxTopics) {
			topics = append(topics, seo.LLMsLink{Name: render.TopicName(t.Name), URL: origin + render.TopicPath(t.Name), Note: strconv.Itoa(t.Count) + " posts"})
		}
	}

	s := render.GetActiveSettings()
	body := seo.Render(seo.LLMsDoc{
		SiteName:    s.Name,
		Origin:      origin,
		Tagline:     s.Tagline,
		Description: s.Description,
		Author:      s.Author,
		Pinned:      pinned,
		Topics:      topics,
		Posts:       posts,
		Generated:   time.Now(),
	})

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Robots-Tag", "noindex")
	_, _ = w.Write([]byte(body))
}

// llmsPosts reads the posts that tail selects (a WHERE … LIMIT ? over
// articles), each with a one-line summary: its excerpt, or, where it has none
// (every post the API writes), the opening of its prose, as its meta
// description reads. Only the opening is read, so a summary costs the same for
// a post of any length; a post whose opening is all markup (an imported post's
// own <style> runs for pages) is read whole, once, rather than summarised by
// nothing.
func llmsPosts(ctx context.Context, origin, tail string, args []any) ([]seo.LLMsPost, error) {
	rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT title,slug,COALESCE(excerpt,''),CASE WHEN COALESCE(excerpt,'')='' THEN substr(content,1,?) ELSE '' END,created_at FROM articles `+tail,
		append([]any{llmsOpeningChars}, args...)...)
	if err != nil {
		return nil, err
	}
	var posts []seo.LLMsPost
	var bare []int
	for rows.Next() {
		var title, slug, excerpt, opening string
		var created time.Time
		if err := rows.Scan(&title, &slug, &excerpt, &opening, &created); err != nil {
			continue
		}
		if excerpt == "" {
			if excerpt = seo.ExtractDescription(opening); excerpt == "" {
				bare = append(bare, len(posts))
			}
		}
		posts = append(posts, seo.LLMsPost{Title: title, URL: origin + "/" + slug, Summary: excerpt, Published: created})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, i := range bare {
		slug := strings.TrimPrefix(posts[i].URL, origin+"/")
		var content string
		if dbpkg.Reader().QueryRowContext(ctx, `SELECT content FROM articles WHERE slug=?`, slug).Scan(&content) == nil {
			posts[i].Summary = seo.ExtractDescription(content)
		}
	}
	return posts, nil
}
