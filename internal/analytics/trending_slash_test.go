// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// Trending joins recorded paths to article slugs. Slugs never carry a trailing
// slash; recorded paths do, because a reader can arrive at either spelling and
// nothing collapsed them. The join used to strip the LEADING slash only, so
// "/post/" became "post/", matched no slug, and every view recorded that way was
// silently dropped — on a real install that removed the highest-traffic posts
// from the widget while leaving it looking like it worked.
//
// These tests pin both halves of the fix: the read side merges the two spellings
// for history already recorded, and the write side stops creating them.

func newSlashDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`CREATE TABLE articles(id INTEGER PRIMARY KEY, slug TEXT, title TEXT, feature_image TEXT,
		 status TEXT NOT NULL DEFAULT 'published', is_page INTEGER NOT NULL DEFAULT 0,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE analytics_pageviews(id TEXT PRIMARY KEY, session_id TEXT NOT NULL DEFAULT '',
		 url_path TEXT NOT NULL, event_type INTEGER NOT NULL DEFAULT 1,
		 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,domain_id TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE analytics_daily(day TEXT NOT NULL, domain_id TEXT NOT NULL DEFAULT '', path TEXT NOT NULL, views INTEGER NOT NULL DEFAULT 0);`,
		`INSERT INTO articles(id,slug,title,feature_image) VALUES
		 (1,'nft-experiences','NFT Experiences',''),
		 (2,'paid-email','Paid Email','');`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func addViews(t *testing.T, db *sql.DB, path string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := db.Exec(
			`INSERT INTO analytics_pageviews(id,url_path,event_type) VALUES(?,?,1)`,
			fmt.Sprintf("%s-%d", path, i), path); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTrailingSlashViewsCountTowardTrending is the regression test for the bug
// itself: the post with far more traffic must not be the one that disappears.
func TestTrailingSlashViewsCountTowardTrending(t *testing.T) {
	db := newSlashDB(t)
	addViews(t, db, "/nft-experiences/", 50) // trailing slash — used to vanish
	addViews(t, db, "/paid-email", 3)

	got, err := (&Store{db: db}).TrendingArticlesByViews(context.Background(), 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d articles, want 2 — a trailing-slash path is still invisible: %+v", len(got), got)
	}
	if got[0].Slug != "nft-experiences" {
		t.Errorf("top article = %q, want nft-experiences (50 views vs 3)", got[0].Slug)
	}
	if got[0].Views != 50 {
		t.Errorf("nft-experiences = %d views, want 50", got[0].Views)
	}
}

// TestBothSpellingsMergeIntoOneRow is the half a join-only fix would miss.
// Normalising at the join but grouping on the raw path would return the SAME
// article twice, each with part of its traffic — which reads as two posts in the
// widget and ranks both below their true position.
func TestBothSpellingsMergeIntoOneRow(t *testing.T) {
	db := newSlashDB(t)
	addViews(t, db, "/nft-experiences", 20)
	addViews(t, db, "/nft-experiences/", 30)

	got, err := (&Store{db: db}).TrendingArticlesByViews(context.Background(), 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 — the two spellings are one article: %+v", len(got), got)
	}
	if got[0].Views != 50 {
		t.Errorf("views = %d, want 50 (20 + 30 merged)", got[0].Views)
	}
}

// TestDailyAggregateAlsoNormalises — the fallback used when the event log is
// empty carries the same join, so fixing only the primary query would leave a
// young site broken in exactly the same way.
func TestDailyAggregateAlsoNormalises(t *testing.T) {
	db := newSlashDB(t)
	for _, r := range []struct {
		path  string
		views int
	}{
		{"/nft-experiences/", 40},
		{"/nft-experiences", 10},
		{"/paid-email", 5},
	} {
		if _, err := db.Exec(
			`INSERT INTO analytics_daily(day,path,views) VALUES(date('now'),?,?)`, r.path, r.views); err != nil {
			t.Fatal(err)
		}
	}
	got, err := (&Store{db: db}).TrendingArticles(context.Background(), 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d articles, want 2: %+v", len(got), got)
	}
	if got[0].Slug != "nft-experiences" || got[0].Views != 50 {
		t.Errorf("top = %q with %d views, want nft-experiences with 50", got[0].Slug, got[0].Views)
	}
}

// TestRootPathNeverMatchesAnArticle guards the edge the trim introduces:
// RTRIM("/", "/") is the empty string, and an article whose slug somehow read as
// empty would otherwise absorb every homepage view.
func TestRootPathNeverMatchesAnArticle(t *testing.T) {
	db := newSlashDB(t)
	if _, err := db.Exec(`INSERT INTO articles(id,slug,title,feature_image) VALUES(3,'','Empty','')`); err != nil {
		t.Fatal(err)
	}
	addViews(t, db, "/", 500)
	addViews(t, db, "/paid-email", 2)

	got, err := (&Store{db: db}).TrendingArticlesByViews(context.Background(), 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range got {
		if g.Slug == "" {
			t.Fatalf("homepage views were attributed to an empty slug (%d views)", g.Views)
		}
	}
	if len(got) != 1 || got[0].Slug != "paid-email" {
		t.Errorf("got %+v, want only paid-email", got)
	}
}

// TestTopPagesMergesSpellingsAndKeepsRoot covers the admin panel, which reads the
// same event log. handlers_trending.go deliberately sources Trending from this
// panel's data "so the public Trending list matches it exactly" — normalising one
// and not the other would make the two disagree. The root case is the trap: a
// plain RTRIM turns "/" into "", which would list the busiest page with no path.
func TestTopPagesMergesSpellingsAndKeepsRoot(t *testing.T) {
	db := newSlashDB(t)
	addViews(t, db, "/nft-experiences", 20)
	addViews(t, db, "/nft-experiences/", 30)
	addViews(t, db, "/", 7)

	got, err := (&Store{db: db}).TopPages(context.Background(), 7, 20)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, p := range got {
		if _, dup := seen[p.Path]; dup {
			t.Errorf("path %q listed twice — the two spellings did not merge", p.Path)
		}
		seen[p.Path] = p.Pageviews
	}
	if seen["/nft-experiences"] != 50 {
		t.Errorf("/nft-experiences = %d views, want 50 (20 + 30 merged)", seen["/nft-experiences"])
	}
	if seen["/"] != 7 {
		t.Errorf("homepage = %d views under path %q, want 7 under \"/\"", seen["/"], "/")
	}
	if _, blank := seen[""]; blank {
		t.Error(`a page was listed with an empty path — the root was trimmed away`)
	}
}

// TestNormalizePathExtendedCollapsesTrailingSlash pins the write side, so the
// event log stops storing two spellings of one page from here on.
func TestNormalizePathExtendedCollapsesTrailingSlash(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/post/", "/post"},
		{"/post", "/post"},
		{"/post//", "/post"},
		{"/a/b/", "/a/b"},
		{"/post/?utm_source=x", "/post"},
		{"/post/#frag", "/post"},
		{"/", "/"},   // root must survive — trimming it would leave ""
		{"", "/"},    // empty normalises to root, not to ""
		{"   ", "/"}, // whitespace-only likewise
	} {
		if got := normalizePathExtended(tc.in); got != tc.want {
			t.Errorf("normalizePathExtended(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
