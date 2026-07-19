package main

import (
	"database/sql"
	"io"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// newBundleTestDB creates an in-memory DB with the content columns the bundle
// export/import touches (slug UNIQUE, matching the real articles table).
func newBundleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE articles(
		id TEXT PRIMARY KEY, title TEXT, slug TEXT UNIQUE, content TEXT,
		tags TEXT, excerpt TEXT, feature_image TEXT, featured INTEGER DEFAULT 0,
		is_page INTEGER DEFAULT 0, author_id TEXT DEFAULT '',
		created_at TEXT, updated_at TEXT)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func seedArticle(t *testing.T, db *sql.DB, id, title, slug, content, tags string, isPage int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO articles(id,title,slug,content,tags,excerpt,feature_image,featured,is_page,author_id,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, title, slug, content, tags, "", "", 0, isPage, "secret-author-"+id,
		"2026-01-02T03:04:05Z", "2026-01-02T03:04:05Z"); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func countArticles(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM articles`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestBundleRoundTrip exports content from install A and imports it into a
// separate install B — the core sync/migrate path.
func TestBundleRoundTrip(t *testing.T) {
	a := newBundleTestDB(t)
	seedArticle(t, a, "1", "Hello", "hello", "<p>hi</p>", `["news","go"]`, 0)
	seedArticle(t, a, "2", "About", "about", "<p>about us</p>", `[]`, 1)

	b, err := exportBundle(a, true)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if b.Count != 2 || len(b.Posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", b.Count)
	}
	if b.Format != bundleFormat || b.Checksum == "" {
		t.Fatalf("bundle missing format/checksum: %+v", b)
	}
	// Content-only: the bundle JSON must NOT carry author identity.
	for _, p := range b.Posts {
		if p.Slug == "hello" && (len(p.Tags) != 2) {
			t.Errorf("tags not preserved: %+v", p)
		}
	}

	dst := newBundleTestDB(t)
	ins, upd, skip, err := importBundle(dst, b, "merge", false, io.Discard)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if ins != 2 || upd != 0 || skip != 0 {
		t.Fatalf("import counts wrong: ins=%d upd=%d skip=%d", ins, upd, skip)
	}
	if countArticles(t, dst) != 2 {
		t.Fatalf("expected 2 articles imported, got %d", countArticles(t, dst))
	}
	// Identity must not have crossed: author_id stays blank on the destination.
	var author string
	if err := dst.QueryRow(`SELECT COALESCE(author_id,'') FROM articles WHERE slug='hello'`).Scan(&author); err != nil {
		t.Fatal(err)
	}
	if author != "" {
		t.Errorf("author_id must NOT cross in a content bundle, got %q", author)
	}
}

// TestBundleMergeAndAddOnly covers upsert vs skip-existing semantics.
func TestBundleMergeAndAddOnly(t *testing.T) {
	src := newBundleTestDB(t)
	seedArticle(t, src, "1", "New Title", "post", "<p>new body</p>", `[]`, 0)
	b, err := exportBundle(src, true)
	if err != nil {
		t.Fatal(err)
	}

	dst := newBundleTestDB(t)
	seedArticle(t, dst, "9", "Old Title", "post", "<p>old body</p>", `[]`, 0)

	// add-only: existing slug is left untouched.
	ins, upd, skip, err := importBundle(dst, b, "add-only", false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if ins != 0 || upd != 0 || skip != 1 {
		t.Fatalf("add-only should skip existing: ins=%d upd=%d skip=%d", ins, upd, skip)
	}
	var title string
	dst.QueryRow(`SELECT title FROM articles WHERE slug='post'`).Scan(&title)
	if title != "Old Title" {
		t.Errorf("add-only must not overwrite, got %q", title)
	}

	// merge: existing slug is updated in place.
	ins, upd, skip, err = importBundle(dst, b, "merge", false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if ins != 0 || upd != 1 || skip != 0 {
		t.Fatalf("merge should update existing: ins=%d upd=%d skip=%d", ins, upd, skip)
	}
	dst.QueryRow(`SELECT title FROM articles WHERE slug='post'`).Scan(&title)
	if title != "New Title" {
		t.Errorf("merge must upsert, got %q", title)
	}
	if countArticles(t, dst) != 1 {
		t.Errorf("merge must not duplicate the slug, got %d rows", countArticles(t, dst))
	}
}

// TestBundleChecksumTamper: a modified bundle is rejected before any write.
func TestBundleChecksumTamper(t *testing.T) {
	src := newBundleTestDB(t)
	seedArticle(t, src, "1", "T", "t", "<p>x</p>", `[]`, 0)
	b, err := exportBundle(src, true)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with content after the checksum was computed.
	b.Posts[0].Content = "<p>evil</p>"
	if err := verifyBundle(b); err == nil {
		t.Fatal("verifyBundle must reject a tampered bundle")
	}
	// Wrong format is also rejected.
	bad := b
	bad.Format = "nope/9"
	if err := verifyBundle(bad); err == nil {
		t.Fatal("verifyBundle must reject an unknown format")
	}
}
