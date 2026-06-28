package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// articleTagsTestDB returns a single-connection in-memory DB with the articles
// and article_tags schema, swapped in as the package writer for the test.
func articleTagsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })

	old, oldR := DB, RDB
	DB = d
	RDB = nil // force Reader() to fall back to the single shared connection
	t.Cleanup(func() { DB, RDB = old, oldR })

	stmts := []string{
		`CREATE TABLE articles(id TEXT PRIMARY KEY,title TEXT NOT NULL,slug TEXT UNIQUE NOT NULL,content TEXT NOT NULL,tags TEXT DEFAULT '',created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL,status TEXT NOT NULL DEFAULT 'published')`,
		`CREATE TABLE article_tags(article_id TEXT NOT NULL, tag TEXT NOT NULL, tag_norm TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX idx_article_tags_norm_created ON article_tags(tag_norm, created_at DESC, article_id)`,
		`CREATE INDEX idx_article_tags_article ON article_tags(article_id)`,
		`CREATE INDEX idx_article_tags_tag ON article_tags(tag)`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return d
}

func insertArticleRow(t *testing.T, d *sql.DB, id, slug, tagsCSV string, created time.Time) {
	t.Helper()
	if _, err := d.Exec(
		`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at,status) VALUES(?,?,?,?,?,?,?, 'published')`,
		id, "T-"+id, slug, "c", tagsCSV, created, created); err != nil {
		t.Fatalf("insert article: %v", err)
	}
}

func tagRowCount(t *testing.T, d *sql.DB, articleID string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(1) FROM article_tags WHERE article_id=?`, articleID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNormaliseTags(t *testing.T) {
	got := normaliseTags([]string{" Go ", "go", "", "Web", "WEB", "rust"})
	want := []string{"Go", "Web", "rust"} // first-seen casing, case-insensitive dedupe, blanks dropped
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestSyncArticleTagsLifecycle(t *testing.T) {
	d := articleTagsTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Insert by id.
	insertArticleRow(t, d, "a1", "post-1", "Go,Web", now)
	if err := RunInTx(ctx, d, func(tx *sql.Tx) error {
		return SyncArticleTagsByIDTx(tx, "a1", now, []string{"Go", "Web"})
	}); err != nil {
		t.Fatalf("sync by id: %v", err)
	}
	if got := tagRowCount(t, d, "a1"); got != 2 {
		t.Fatalf("after insert: got %d tag rows, want 2", got)
	}

	// Update by slug replaces membership (drop Web, add rust).
	if _, err := d.Exec(`UPDATE articles SET tags=? WHERE slug=?`, "Go,rust", "post-1"); err != nil {
		t.Fatal(err)
	}
	if err := RunInTx(ctx, d, func(tx *sql.Tx) error {
		return SyncArticleTagsBySlugTx(tx, "post-1", []string{"Go", "rust"})
	}); err != nil {
		t.Fatalf("sync by slug: %v", err)
	}
	var hasWeb int
	d.QueryRow(`SELECT COUNT(1) FROM article_tags WHERE article_id='a1' AND tag_norm='web'`).Scan(&hasWeb)
	if hasWeb != 0 {
		t.Errorf("update should have removed the web tag")
	}
	if got := tagRowCount(t, d, "a1"); got != 2 {
		t.Errorf("after update: got %d tag rows, want 2", got)
	}

	// Delete by slug clears membership.
	if err := RunInTx(ctx, d, func(tx *sql.Tx) error {
		return DeleteArticleTagsBySlugTx(tx, "post-1")
	}); err != nil {
		t.Fatalf("delete by slug: %v", err)
	}
	if got := tagRowCount(t, d, "a1"); got != 0 {
		t.Errorf("after delete: got %d tag rows, want 0", got)
	}
}

func TestArticleTagsBackfill(t *testing.T) {
	d := articleTagsTestDB(t)
	now := time.Now().UTC()

	// Seed articles directly (as a bulk import would), leaving article_tags empty.
	insertArticleRow(t, d, "b1", "p1", "go,web", now.Add(-3*time.Minute))
	insertArticleRow(t, d, "b2", "p2", "go", now.Add(-2*time.Minute))
	insertArticleRow(t, d, "b3", "p3", "", now.Add(-time.Minute)) // no tags

	if !articleTagsBackfillNeeded() {
		t.Fatal("backfill should be needed before it runs")
	}

	done := make(chan struct{})
	if err := backfillArticleTags(done); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if articleTagsBackfillNeeded() {
		t.Error("backfill should not be needed after completion")
	}
	if got := tagRowCount(t, d, "b1"); got != 2 {
		t.Errorf("b1: got %d tag rows, want 2", got)
	}
	if got := tagRowCount(t, d, "b2"); got != 1 {
		t.Errorf("b2: got %d tag rows, want 1", got)
	}
	if got := tagRowCount(t, d, "b3"); got != 0 {
		t.Errorf("b3 (untagged): got %d tag rows, want 0", got)
	}

	// Running again is a no-op (resumable + idempotent) and must not duplicate rows.
	if err := backfillArticleTags(done); err != nil {
		t.Fatalf("backfill rerun: %v", err)
	}
	var total int
	d.QueryRow(`SELECT COUNT(1) FROM article_tags`).Scan(&total)
	if total != 3 {
		t.Errorf("after rerun: got %d total tag rows, want 3", total)
	}
}

func TestTagCountsIndexed(t *testing.T) {
	d := articleTagsTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	insertArticleRow(t, d, "c1", "c1", "go,web", now)
	insertArticleRow(t, d, "c2", "c2", "go", now)
	for _, id := range []struct {
		id   string
		tags []string
	}{{"c1", []string{"go", "web"}}, {"c2", []string{"go"}}} {
		if err := RunInTx(ctx, d, func(tx *sql.Tx) error {
			return SyncArticleTagsByIDTx(tx, id.id, now, id.tags)
		}); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewArticleRepo(d)
	counts, err := repo.TagCounts(ctx)
	if err != nil {
		t.Fatalf("TagCounts: %v", err)
	}
	if counts["go"] != 2 {
		t.Errorf("go count = %d, want 2", counts["go"])
	}
	if counts["web"] != 1 {
		t.Errorf("web count = %d, want 1", counts["web"])
	}
}
