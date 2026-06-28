package analytics

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE analytics_daily(day TEXT NOT NULL,path TEXT NOT NULL,views INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,path))`,
		`CREATE TABLE analytics_referrers(day TEXT NOT NULL,host TEXT NOT NULL,hits INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(day,host))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	return New(db)
}

func TestRecordAndSummary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := s.Record(ctx, "/hello", "https://news.ycombinator.com/item?id=1"); err != nil {
			t.Fatal(err)
		}
	}
	s.Record(ctx, "/world", "")

	sum, err := s.Since(ctx, 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalViews != 4 {
		t.Errorf("total = %d, want 4", sum.TotalViews)
	}
	if len(sum.TopPages) == 0 || sum.TopPages[0].Path != "/hello" || sum.TopPages[0].Views != 3 {
		t.Errorf("top page wrong: %+v", sum.TopPages)
	}
	if len(sum.Referrers) != 1 || sum.Referrers[0].Host != "news.ycombinator.com" {
		t.Errorf("referrer host wrong: %+v", sum.Referrers)
	}
}

func TestNormalizePathStripsQuery(t *testing.T) {
	if got := normalizePath("/a/b?x=1#frag"); got != "/a/b" {
		t.Errorf("normalizePath = %q, want /a/b", got)
	}
	if got := normalizePath(""); got != "/" {
		t.Errorf("empty path should normalize to /")
	}
}

func TestReferrerHostOnly(t *testing.T) {
	if got := referrerHost("https://example.com/secret/path?token=abc"); got != "example.com" {
		t.Errorf("referrerHost leaked path: %q", got)
	}
	if got := referrerHost(""); got != "" {
		t.Errorf("empty referrer should yield empty host")
	}
}

func TestPurge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Insert an old row directly.
	s.db.Exec(`INSERT INTO analytics_daily(day,path,views) VALUES('2000-01-01','/old',5)`)
	s.Record(ctx, "/new", "")
	n, err := s.Purge(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("purged %d rows, want 1", n)
	}
}

// TestTrendingArticles verifies the trending query ranks published, non-page
// articles by view total over the window, joins back the title/image from the
// path, and excludes drafts, pages, and unknown paths.
func TestTrendingArticles(t *testing.T) {
	s := newTestStore(t)
	// Minimal articles table for the join (columns the query reads).
	if _, err := s.db.Exec(`CREATE TABLE articles(
		slug TEXT PRIMARY KEY, title TEXT NOT NULL DEFAULT '',
		feature_image TEXT, status TEXT NOT NULL DEFAULT 'published',
		is_page INTEGER NOT NULL DEFAULT 0, created_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	rows := [][3]string{
		{"alpha", "Alpha", "published"}, // most views
		{"bravo", "Bravo", "published"}, // fewer views
		{"draft", "Draft", "draft"},     // excluded: not published
	}
	for i, r := range rows {
		if _, err := s.db.Exec(`INSERT INTO articles(slug,title,feature_image,status,is_page,created_at) VALUES(?,?,?,?,0,?)`,
			r[0], r[1], "/img/"+r[0]+".jpg", r[2], "2026-06-2"+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	// A page (excluded) + an orphan path with no article (excluded).
	s.db.Exec(`INSERT INTO articles(slug,title,status,is_page,created_at) VALUES('about','About','published',1,'2026-06-01')`)

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s.Record(ctx, "/alpha", "")
	}
	for i := 0; i < 2; i++ {
		s.Record(ctx, "/bravo", "")
	}
	s.Record(ctx, "/draft", "") // draft — must not appear
	s.Record(ctx, "/about", "") // page — must not appear
	s.Record(ctx, "/ghost", "") // no matching article — must not appear

	got, err := s.TrendingArticles(ctx, 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 trending posts, got %d: %+v", len(got), got)
	}
	if got[0].Slug != "alpha" || got[0].Views != 5 {
		t.Errorf("rank 1 should be alpha/5, got %+v", got[0])
	}
	if got[1].Slug != "bravo" || got[1].Views != 2 {
		t.Errorf("rank 2 should be bravo/2, got %+v", got[1])
	}
	if got[0].Title != "Alpha" || got[0].Image != "/img/alpha.jpg" {
		t.Errorf("join did not supply title/image: %+v", got[0])
	}
}

func itoa(i int) string { return string(rune('0' + i)) }
