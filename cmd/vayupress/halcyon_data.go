// SPDX-License-Identifier: Apache-2.0

package main

// halcyon_data.go — the data Halcyon shows that the shared templates never
// asked for: the largest topics with their counts, a front-page desk's newest
// posts, and related reading ranked by the topics it shares.
//
// None of it is counted on a request. Topic counts come from the same memo the
// topic index (/tags) keeps, refreshed off the request path; while it is cold
// the sections it feeds are left out, and the pages that left them out are
// marked stale once the first count lands. On a multi-domain install every
// figure is the active domain's, exactly as the topic index scopes them.

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/tagrules"
)

// halcyonScope is the topic memo key and count for the request's domain,
// matching handleTagIndex.
func (a *App) halcyonScope(r *http.Request) (key string, scope string, scoped bool, count func(context.Context) ([]render.TagInfo, int, error)) {
	if !a.multiDomain(r) {
		return "", "", false, a.tagIndexGlobal
	}
	scope = a.contentScope(r)
	return "d:" + scope, scope, true, func(ctx context.Context) ([]render.TagInfo, int, error) {
		infos, total := a.tagIndexScoped(ctx, scope)
		return infos, total, nil
	}
}

// halcyonTopics returns the topic counts held for key, largest first, and the
// number of posts they are counted over. A cold or stale memo starts one
// background count and returns what it holds (nothing, when cold).
func (a *App) halcyonTopics(key string, count func(context.Context) ([]render.TagInfo, int, error)) ([]render.TagInfo, int) {
	tagIndexMemo.Lock()
	e, have := tagIndexMemo.m[key]
	start := !tagIndexMemo.refreshing[key] && (!have || time.Since(e.at) >= tagIndexTTL)
	if start {
		tagIndexMemo.refreshing[key] = true
	}
	tagIndexMemo.Unlock()
	if start {
		go func() {
			refreshTagIndex(key, count)
			// Home and the topic pages rendered while the memo was cold went out
			// without their topic sections; drop them so the next visit rebuilds
			// them. Only those: a post rendered in that window keeps its related
			// reading until its own next render, rather than every cached post
			// on the site re-rendering after each restart.
			if !have {
				tagIndexMemo.Lock()
				_, filled := tagIndexMemo.m[key]
				tagIndexMemo.Unlock()
				if _, on := render.Halcyon(); filled && on {
					render.CachePurgeListings()
				}
			}
		}()
	}
	return e.infos, e.total
}

// halcyonDeskPostsSQL reads a topic's newest published posts, for the whole
// install or one site, from idx_article_tags_live alone (migration 106), as the
// topic page does. Filtering publication and site in the index, not after it,
// means drafts or another site's posts cannot fill the window and leave a desk
// short.
const (
	halcyonDeskPostsSQL       = `SELECT article_id FROM article_tags WHERE tag_norm=? AND live=1 ORDER BY created_at DESC LIMIT ?`
	halcyonDeskPostsScopedSQL = `SELECT article_id FROM article_tags WHERE tag_norm=? AND live=1 AND domain_id=? ORDER BY created_at DESC LIMIT ?`
)

// halcyonDesks builds the front page's three topic desks: the largest topics
// that are not ubiquitous, each with its three newest published posts.
func (a *App) halcyonDesks(ctx context.Context, topics []render.TagInfo, total int, scope string, scoped bool) []render.TopicDesk {
	var desks []render.TopicDesk
	for _, t := range topics {
		if len(desks) == 3 {
			break
		}
		if render.Ubiquitous(t.Count, total) {
			continue
		}
		posts := a.halcyonTopicPosts(ctx, tagrules.Key(t.Name), 3, scope, scoped)
		if len(posts) == 0 {
			continue
		}
		desks = append(desks, render.TopicDesk{Name: t.Name, Count: t.Count, Posts: posts})
	}
	return desks
}

func (a *App) halcyonTopicPosts(ctx context.Context, norm string, n int, scope string, scoped bool) []render.HomeArticle {
	q, args := halcyonDeskPostsSQL, []any{norm, n * 4}
	if scoped {
		q, args = halcyonDeskPostsScopedSQL, []any{norm, scope, n * 4}
	}
	rows, err := dbpkg.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Err()
	rows.Close()
	if len(ids) == 0 {
		return nil
	}
	// A page can carry a topic too, and its tag row cannot say so; a desk
	// lists posts only.
	in, pargs := placeholders(ids)
	ok := map[string]bool{}
	if rs, err := dbpkg.Reader().QueryContext(ctx, `SELECT id FROM articles WHERE id IN (`+in+`) AND is_page=0`, pargs...); err == nil {
		for rs.Next() {
			var id string
			if rs.Scan(&id) == nil {
				ok[id] = true
			}
		}
		_ = rs.Err()
		rs.Close()
	}
	var keep []string
	for _, id := range ids {
		if ok[id] {
			keep = append(keep, id)
			if len(keep) == n {
				break
			}
		}
	}
	return listingCards(ctx, keep)
}

// halcyonRelated ranks a post's related candidates for Halcyon: by topics
// shared, ignoring ubiquitous ones, newest first on a tie, three at most.
func (a *App) halcyonRelated(r *http.Request, slug string, tags []string) []render.RelatedArticle {
	key, _, _, count := a.halcyonScope(r)
	topics, total := a.halcyonTopics(key, count)
	counts := make(map[string]int, len(topics))
	for _, t := range topics {
		counts[tagrules.Key(t.Name)] = t.Count
	}
	ubiquitous := func(tag string) bool { return render.Ubiquitous(counts[tag], total) }
	// relatedArticles reads four times its limit from each tag: six gives
	// twenty-four candidates a tag, ample for the three Halcyon keeps.
	return render.RankRelated(a.relatedArticles(r.Context(), slug, tags, 6), ubiquitous, 3)
}

// warmHalcyonTopics starts the first topic count at boot while Halcyon is the
// active theme, so the first visitors are not the ones who find it cold.
func (a *App) warmHalcyonTopics() {
	if _, on := render.Halcyon(); !on || a.articles == nil {
		return
	}
	logging.LogInfo("render", "Halcyon: counting topics for the front page")
	a.halcyonTopics("", a.tagIndexGlobal)
}

// halcyonTopicReadSQL ranks a topic's published posts by their pageviews over
// a window, in the shape trendingByViewsSQL already runs for the Trending
// widget: views are grouped per path inside idx_apv_trending's date range
// first, and only the paths that were read are joined to their post and its
// tag row. A topic's unread posts are never touched, so the cost follows the
// window's traffic, not the topic's size.
//
// The unary + on the tag columns is load-bearing. Without it the planner
// reaches a read post's tag rows through idx_article_tags_live by tag, which
// walks every post of the topic once per read path: for "tutorial" on
// johal.in, 234k rows for each of some twenty thousand paths. With it, a
// post's few tag rows are found by idx_article_tags_article and tested.
const halcyonTopicReadSQL = `SELECT a.id FROM (
	SELECT RTRIM(SUBSTR(url_path, 2), '/') AS slug, COUNT(1) AS v
	FROM analytics_pageviews
	WHERE event_type = 1 AND created_at >= ? AND url_path LIKE '/%'
	GROUP BY RTRIM(SUBSTR(url_path, 2), '/')
) pv
CROSS JOIN articles a ON a.slug = pv.slug
CROSS JOIN article_tags t ON t.article_id = a.id
WHERE pv.slug <> '' AND a.is_page = 0 AND +t.tag_norm = ? AND +t.live = 1`

// halcyonReadDays is the window "Most read" counts over: the Trending widget's
// longer window, so the two never disagree about what was read.
const halcyonReadDays = 30

// halcyonReadTTL is how long a topic's ranking is held. Reads move slowly; a
// ranking ten minutes old is still true, and holding it means a crawler
// walking every topic's "Most read" runs the grouping once per topic per ten
// minutes, not once per request.
const halcyonReadTTL = 10 * time.Minute

// halcyonReadMemoCap bounds the held rankings. Past it the memo is emptied
// rather than evicted one by one: a request for any of the topics' many
// spellings cannot grow memory without limit, and a cleared memo only costs
// each topic its next query.
const halcyonReadMemoCap = 512

var halcyonReadMemo = struct {
	sync.Mutex
	m map[string]halcyonReadEntry
}{m: map[string]halcyonReadEntry{}}

type halcyonReadEntry struct {
	ids []string
	at  time.Time
}

// halcyonTopicMostRead returns a topic's posts read in the last
// halcyonReadDays days, most read first (newest first on a tie), at most max.
// A post nobody read in the window is not listed: "Most read" never pads
// itself with unread posts.
func (a *App) halcyonTopicMostRead(ctx context.Context, tag string, max int, scope string, scoped bool) []render.HomeArticle {
	norm := tagrules.Key(tag)
	if dbpkg.DB == nil || norm == "" {
		return nil
	}
	key := scope + "\x00" + norm
	halcyonReadMemo.Lock()
	e, ok := halcyonReadMemo.m[key]
	halcyonReadMemo.Unlock()
	if !ok || time.Since(e.at) >= halcyonReadTTL {
		q, args := halcyonTopicReadSQL, []any{time.Now().UTC().AddDate(0, 0, -(halcyonReadDays - 1)).Format("2006-01-02"), norm}
		if scoped {
			q += ` AND +t.domain_id = ?`
			args = append(args, scope)
		}
		rows, err := dbpkg.Reader().QueryContext(ctx, q+` ORDER BY pv.v DESC, a.created_at DESC LIMIT ?`, append(args, max)...)
		if err != nil {
			return nil
		}
		e = halcyonReadEntry{at: time.Now()}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				e.ids = append(e.ids, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil
		}
		halcyonReadMemo.Lock()
		if len(halcyonReadMemo.m) >= halcyonReadMemoCap {
			halcyonReadMemo.m = map[string]halcyonReadEntry{}
		}
		halcyonReadMemo.m[key] = e
		halcyonReadMemo.Unlock()
	}
	return listingCards(ctx, e.ids)
}

// hasTopic reports whether a post's tags include topic, as the topic page
// matches them: by key (tagrules.Key), so "Machine Learning" is in
// machine-learning.
func hasTopic(tags []string, topic string) bool {
	want := tagrules.Key(topic)
	if want == "" {
		return false
	}
	for _, t := range tags {
		if tagrules.Key(t) == want {
			return true
		}
	}
	return false
}

// orderTopicHits puts a topic's search results in the topic page's order:
// newest first, or most read over halcyonReadDays (newest first on a tie, and
// after every read post when nothing was recorded for them).
func (a *App) orderTopicHits(ctx context.Context, hits []render.SearchHit, mostRead bool) {
	views := map[string]int64{}
	if mostRead && a.analytics != nil && len(hits) > 0 {
		slugs := make([]string, len(hits))
		for i, h := range hits {
			slugs[i] = h.Slug
		}
		if m, err := a.analytics.ViewsBySlug(ctx, halcyonReadDays, slugs); err == nil {
			views = m
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if vi, vj := views[hits[i].Slug], views[hits[j].Slug]; vi != vj {
			return vi > vj
		}
		return hits[i].CreatedAt.After(hits[j].CreatedAt)
	})
}
