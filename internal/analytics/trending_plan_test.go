// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// openTrendingDB opens a database built by the migrations, so the plans and
// results below are the ones production gets, not this package's test schema's.
func openTrendingDB(t *testing.T) {
	t.Helper()
	prev := config.Cfg.DBPath
	config.Cfg.DBPath = filepath.Join(t.TempDir(), "trending.db")
	if err := dbpkg.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dbpkg.ClosePools()
		_ = dbpkg.DB.Close()
		config.Cfg.DBPath = prev
	})
}

// TestTrendingReadsNoPostItDoesNotShow — both trending queries rank the viewed
// slugs from an index, keep the published posts among them from
// idx_articles_slug_listed, and never walk the posts. On johal.in the walk read
// every post's body: /api/trending at 6.8 s p95.
func TestTrendingReadsNoPostItDoesNotShow(t *testing.T) {
	openTrendingDB(t)
	for _, c := range []struct {
		name, sql string
		want      []string
	}{
		{"by pageviews", trendingByViewsSQL, []string{"COVERING INDEX idx_apv_trending", "COVERING INDEX idx_articles_slug_listed"}},
		{"by the daily totals", trendingDailySQL, []string{"COVERING INDEX idx_articles_slug_listed"}},
	} {
		rows, err := dbpkg.DB.Query("EXPLAIN QUERY PLAN "+c.sql, "2026-09-01", 10)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		p := strings.Join(plan, " | ")
		for _, w := range c.want {
			if !strings.Contains(p, w) {
				t.Errorf("%s does not read %q: %s", c.name, w, p)
			}
		}
		if strings.Contains(p, "idx_articles_pagefeed") || strings.Contains(p, "SCAN a") || strings.Contains(p, "SCAN l") {
			t.Errorf("%s walks the posts: %s", c.name, p)
		}
	}
}

// TestTrendingKeepsItsPlacesForPublishedPosts — the ranking is cut to the
// limit after drafts and pages are dropped, so a draft with the most views does
// not take a place, and an equal count goes to the newer post. The tied posts
// are named so that the older sorts first: grouping by slug returns them in
// that order, so only the tie-break puts the newer one ahead.
func TestTrendingKeepsItsPlacesForPublishedPosts(t *testing.T) {
	openTrendingDB(t)
	ctx := context.Background()
	for _, a := range []struct {
		slug, status, created string
		page                  int
	}{
		{"hot-draft", "draft", "2026-09-01", 0},
		{"hot-page", "published", "2026-09-01", 1},
		{"earlier", "published", "2026-01-01", 0},
		{"later", "published", "2026-06-01", 0},
		{"quiet", "published", "2026-09-01", 0},
	} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,created_at,updated_at,status,is_page) VALUES(?,?,?,'',?,?,?,?)`,
			a.slug, a.slug, a.slug, a.created, a.created, a.status, a.page); err != nil {
			t.Fatal(err)
		}
	}
	views := map[string]int{"hot-draft": 50, "hot-page": 40, "earlier": 10, "later": 10, "quiet": 5}
	now := time.Now().UTC()
	for slug, v := range views {
		if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_daily(day,path,views) VALUES(?,?,?)`, now.Format("2006-01-02"), "/"+slug, v); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < v; i++ {
			if _, err := dbpkg.DB.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,event_type,created_at) VALUES(?,'',?,1,?)`,
				fmt.Sprintf("%s-%d", slug, i), "/"+slug, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := New(dbpkg.DB)
	for _, q := range []struct {
		name string
		run  func(context.Context, int, int) ([]TrendingArticle, error)
	}{
		{"by pageviews", s.TrendingArticlesByViews},
		{"by the daily totals", s.TrendingArticles},
	} {
		for limit, want := range map[int]string{1: "later", 2: "later earlier", 3: "later earlier quiet"} {
			got, err := q.run(ctx, 7, limit)
			if err != nil {
				t.Fatalf("%s: %v", q.name, err)
			}
			var slugs []string
			for _, a := range got {
				slugs = append(slugs, a.Slug)
			}
			if strings.Join(slugs, " ") != want {
				t.Errorf("%s, limit %d: %v, want %s", q.name, limit, slugs, want)
			}
		}
	}
}
