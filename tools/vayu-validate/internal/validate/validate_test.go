package validate

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func makeTestDB(t *testing.T, articles [][]string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE articles(
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		slug TEXT NOT NULL,
		content TEXT NOT NULL,
		tags TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i, a := range articles {
		// a: [id, title, slug, content, tags, created_at, updated_at]
		for len(a) < 7 {
			a = append(a, "")
		}
		if a[5] == "" {
			a[5] = now
		}
		if a[6] == "" {
			a[6] = now
		}
		if a[0] == "" {
			a[0] = fmt.Sprintf("id-%d", i) //nolint
		}
		_, err := db.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
			a[0], a[1], a[2], a[3], a[4], a[5], a[6])
		if err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}
	return path
}

func TestValidate_Clean(t *testing.T) {
	path := makeTestDB(t, [][]string{
		{"id1", "Hello World", "hello-world", "<p>Content</p>", "go,programming", "", ""},
	})
	r, err := Validate(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Errors > 0 || r.Warnings > 0 {
		for _, i := range r.Issues {
			t.Logf("issue: %+v", i)
		}
		t.Errorf("expected clean, got %d errors %d warnings", r.Errors, r.Warnings)
	}
}

func TestValidate_EmptyTitle(t *testing.T) {
	path := makeTestDB(t, [][]string{
		{"id1", "", "some-slug", "<p>ok</p>", "", "", ""},
	})
	r, _ := Validate(context.Background(), path)
	if r.Errors == 0 {
		t.Error("expected empty-title error")
	}
}

func TestValidate_DuplicateSlug(t *testing.T) {
	path := makeTestDB(t, [][]string{
		{"id1", "A", "dup-slug", "<p>a</p>", "", "", ""},
		{"id2", "B", "dup-slug", "<p>b</p>", "", "", ""},
	})
	r, _ := Validate(context.Background(), path)
	found := false
	for _, i := range r.Issues {
		if i.Rule == "duplicate-slug" {
			found = true
		}
	}
	if !found {
		t.Error("expected duplicate-slug error")
	}
}

func TestValidate_InvalidSlug(t *testing.T) {
	path := makeTestDB(t, [][]string{
		{"id1", "Title", "Invalid Slug!", "<p>ok</p>", "", "", ""},
	})
	r, _ := Validate(context.Background(), path)
	found := false
	for _, i := range r.Issues {
		if i.Rule == "invalid-slug" {
			found = true
		}
	}
	if !found {
		t.Error("expected invalid-slug error")
	}
}

func TestValidate_ZeroDate(t *testing.T) {
	// SQLite silently converts bad date strings to zero time (0001-01-01T00:00:00Z).
	// Validate should warn about dates before the year 2000 as suspicious.
	path := makeTestDB(t, [][]string{
		{"id1", "Title", "valid-slug", "<p>ok</p>", "", "0001-01-01T00:00:00Z", "0001-01-01T00:00:00Z"},
	})
	r, _ := Validate(context.Background(), path)
	found := false
	for _, i := range r.Issues {
		if i.Rule == "suspicious-date" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected suspicious-date warning, got issues=%+v", r.Issues)
	}
}

