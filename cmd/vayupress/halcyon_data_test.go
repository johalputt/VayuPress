// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
)

// deskPost writes a post tagged "go" with an explicit time, so the newest
// posts can be chosen to be the ones a desk must skip.
func deskPost(t *testing.T, id, status, domain string, page bool, at time.Time) {
	t.Helper()
	tx, err := dbpkg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,domain_id,is_page,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, "T "+id, id, "body", "go", status, domain, page, at, at); err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.SyncArticleTagsByIDTx(tx, id, at, []string{"go"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// A desk lists a topic's newest published posts of this site. Each seed is
// newer than the posts the desk should show, so it would take their place if
// its rule were not applied: another site's posts (enough to fill the whole
// candidate window), a draft, and a page.
func TestADeskListsOnlyThisSitesPublishedPosts(t *testing.T) {
	openMigratedDB(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		deskPost(t, fmt.Sprintf("a%d", i), "published", "a", false, t0.Add(time.Duration(i)*time.Minute))
	}
	for i := range 12 {
		deskPost(t, fmt.Sprintf("b%d", i), "published", "b", false, t0.Add(time.Hour+time.Duration(i)*time.Minute))
	}
	deskPost(t, "a-draft", "draft", "a", false, t0.Add(2*time.Hour))
	deskPost(t, "a-page", "published", "a", true, t0.Add(3*time.Hour))

	slugs := func(scope string, scoped bool) string {
		var s []string
		for _, p := range (&App{}).halcyonTopicPosts(context.Background(), "go", 3, scope, scoped) {
			s = append(s, p.Slug)
		}
		return strings.Join(s, " ")
	}
	if got, want := slugs("a", true), "a2 a1 a0"; got != want {
		t.Errorf("site a's desk = %q, want %q (no other site's post, no draft, no page)", got, want)
	}
	if got, want := slugs("", false), "b11 b10 b9"; got != want {
		t.Errorf("the install's desk = %q, want %q (no draft, no page)", got, want)
	}
}

// read records n views of slug, the way the pageview log stores them; a
// trailing-slash spelling is the same post (trendingByViewsSQL's rule).
func read(t *testing.T, slug string, n int, at time.Time) {
	t.Helper()
	for i := range n {
		path := "/" + slug
		if i%2 == 1 {
			path += "/"
		}
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,created_at) VALUES(?,?,?,1,?)`,
			fmt.Sprintf("%s-%d-%d", slug, at.Unix(), i), "s", path, at.UTC().Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
}

// A topic's "Most read" lists only its posts read in the window, most read
// first. Each seed is read more than the posts that should lead, so it would
// lead if its rule failed: a post of another topic, a draft, a page, another
// site's post, and reads older than the window.
func TestATopicsMostReadIsItsReadPostsInOrder(t *testing.T) {
	openMigratedDB(t)
	halcyonReadMemo.Lock()
	halcyonReadMemo.m = map[string]halcyonReadEntry{}
	halcyonReadMemo.Unlock()
	now := time.Now()
	t0 := now.Add(-48 * time.Hour)
	for _, id := range []string{"few", "many", "unread"} {
		deskPost(t, id, "published", "a", false, t0)
	}
	deskPost(t, "draft", "draft", "a", false, t0)
	deskPost(t, "page", "published", "a", true, t0)
	deskPost(t, "elsewhere", "published", "b", false, t0)
	read(t, "few", 2, now)
	read(t, "many", 5, now)
	read(t, "unread", 40, now.AddDate(0, 0, -(halcyonReadDays+1)))
	for _, s := range []string{"draft", "page", "elsewhere"} {
		read(t, s, 9, now)
	}
	tx, err := dbpkg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,domain_id,created_at,updated_at) VALUES('other','T other','other','body','rust','published','a',?,?)`, t0, t0); err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.SyncArticleTagsByIDTx(tx, "other", t0, []string{"rust"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	read(t, "other", 9, now)

	slugs := func(scope string, scoped bool) string {
		var s []string
		for _, p := range (&App{}).halcyonTopicMostRead(context.Background(), "Go", 10, scope, scoped) {
			s = append(s, p.Slug)
		}
		return strings.Join(s, " ")
	}
	if got, want := slugs("a", true), "many few"; got != want {
		t.Errorf("site a's most read = %q, want %q", got, want)
	}
	halcyonReadMemo.Lock()
	halcyonReadMemo.m = map[string]halcyonReadEntry{}
	halcyonReadMemo.Unlock()
	if got, want := slugs("", false), "elsewhere many few"; got != want {
		t.Errorf("the install's most read = %q, want %q", got, want)
	}
}

// The ranking groups the window's pageviews inside idx_apv_trending, as the
// Trending widget does, and reaches each read post's tag rows by the post:
// reaching them by tag walked the whole topic once per read path. Global and
// per-domain, since both are served.
func TestATopicsMostReadIsBoundedByTheWindowNotTheTopic(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []struct {
		name string
		sql  string
		args []any
	}{
		{"global", halcyonTopicReadSQL, []any{"2026-09-01", "go", 200}},
		{"one domain", halcyonTopicReadSQL + ` AND +t.domain_id = ?`, []any{"2026-09-01", "go", "d1", 200}},
	} {
		got := queryPlan(t, q.sql+` ORDER BY pv.v DESC, a.created_at DESC LIMIT ?`, q.args...)
		if !strings.Contains(got, "COVERING INDEX idx_apv_trending (event_type=? AND created_at>?)") {
			t.Errorf("%s: the window's views are not grouped inside idx_apv_trending: %s", q.name, got)
		}
		if !strings.Contains(got, "SEARCH t USING INDEX idx_article_tags_article (article_id=?)") || strings.Contains(got, "tag_norm=?") {
			t.Errorf("%s: a read post's tag rows are not reached by the post: %s", q.name, got)
		}
	}
}

// A topic's search results take the topic page's order; newest first when no
// reads are known.
func TestTopicHitsTakeTheTopicsOrder(t *testing.T) {
	d := func(n int) time.Time { return time.Date(2026, 1, n, 0, 0, 0, 0, time.UTC) }
	hits := []render.SearchHit{{Slug: "old", CreatedAt: d(1)}, {Slug: "new", CreatedAt: d(3)}, {Slug: "mid", CreatedAt: d(2)}}
	(&App{}).orderTopicHits(context.Background(), hits, false)
	var got []string
	for _, h := range hits {
		got = append(got, h.Slug)
	}
	if strings.Join(got, " ") != "new mid old" {
		t.Errorf("newest order = %v", got)
	}
	if !hasTopic([]string{" DevOps "}, "devops") || hasTopic([]string{"devops-tools"}, "devops") {
		t.Error("a topic matches its tag case- and space-insensitively, and only that tag")
	}
	if !hasTopic([]string{"Machine Learning"}, "machine-learning") {
		t.Error("a post's tag as written is not matched to the topic's key")
	}
}
