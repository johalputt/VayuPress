package versions

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE article_versions(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		article_id TEXT NOT NULL,
		slug TEXT NOT NULL,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		tags TEXT NOT NULL DEFAULT '',
		saved_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		label TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSaveAndList(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	s := New(db)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := s.Save(ctx, "art1", "my-slug", "Title", "<p>body</p>", "go,test", ""); err != nil {
			t.Fatal(err)
		}
	}
	vs, err := s.List(ctx, "art1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 {
		t.Fatalf("want 3 versions, got %d", len(vs))
	}
	if len(vs[0].Tags) != 2 {
		t.Errorf("want 2 tags, got %v", vs[0].Tags)
	}
}

func TestGet(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	s := New(db)
	ctx := context.Background()

	id, err := s.Save(ctx, "art2", "slug2", "My Title", "<p>hello</p>", "", "pre-update")
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(ctx, id)
	if err != nil || v == nil {
		t.Fatalf("Get failed: %v", err)
	}
	if v.Title != "My Title" || v.Label != "pre-update" {
		t.Errorf("unexpected version: %+v", v)
	}
}
