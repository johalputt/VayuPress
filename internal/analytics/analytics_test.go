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
