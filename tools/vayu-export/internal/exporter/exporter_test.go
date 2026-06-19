package exporter

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func createTestDB(t *testing.T, articles []struct {
	id, title, slug, content, tags, createdAt, updatedAt string
}) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE articles (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		slug TEXT UNIQUE NOT NULL,
		content TEXT NOT NULL,
		tags TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	for _, a := range articles {
		_, err = db.Exec(`INSERT INTO articles VALUES (?,?,?,?,?,?,?)`,
			a.id, a.title, a.slug, a.content, a.tags, a.createdAt, a.updatedAt)
		if err != nil {
			t.Fatalf("insert %s: %v", a.slug, err)
		}
	}
	return dbPath
}

func TestExportFileStructure(t *testing.T) {
	dbPath := createTestDB(t, []struct {
		id, title, slug, content, tags, createdAt, updatedAt string
	}{
		{"1", "First Post", "first-post", "<p>Hello world</p>", "go", "2024-01-01 00:00:00", "2024-01-01 00:00:00"},
		{"2", "Second Post", "second-post", "<p>Content here</p>", "news", "2024-01-02 00:00:00", "2024-01-02 00:00:00"},
	})

	outDir := t.TempDir()
	count, err := Export(Options{
		DBPath:   dbPath,
		OutDir:   outDir,
		BaseURL:  "https://example.com",
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 articles, got %d", count)
	}

	// Check expected files.
	expectedFiles := []string{
		"index.html",
		"sitemap.xml",
		"feed.xml",
		"robots.txt",
		filepath.Join("articles", "first-post", "index.html"),
		filepath.Join("articles", "second-post", "index.html"),
	}
	for _, f := range expectedFiles {
		full := filepath.Join(outDir, f)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

func TestPagination(t *testing.T) {
	var articles []struct {
		id, title, slug, content, tags, createdAt, updatedAt string
	}
	for i := 0; i < 25; i++ {
		articles = append(articles, struct {
			id, title, slug, content, tags, createdAt, updatedAt string
		}{
			id:        fmt.Sprintf("%d", i+1),
			title:     fmt.Sprintf("Article %d", i+1),
			slug:      fmt.Sprintf("article-%d", i+1),
			content:   "<p>Content</p>",
			tags:      "",
			createdAt: "2024-01-01 00:00:00",
			updatedAt: "2024-01-01 00:00:00",
		})
	}

	dbPath := createTestDB(t, articles)
	outDir := t.TempDir()

	_, err := Export(Options{
		DBPath:   dbPath,
		OutDir:   outDir,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Expect page 1 at index.html and page 2 at page/2/index.html, page 3 at page/3/index.html
	expectedPageFiles := []string{
		"index.html",
		filepath.Join("page", "2", "index.html"),
		filepath.Join("page", "3", "index.html"),
	}
	for _, f := range expectedPageFiles {
		full := filepath.Join(outDir, f)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected pagination file %s: %v", f, err)
		}
	}
}

func TestBuildPagination(t *testing.T) {
	tests := []struct {
		page, total int
		wantPrev    string
		wantNext    string
	}{
		{1, 3, "", "/page/2/"},
		{2, 3, "/", "/page/3/"},
		{3, 3, "/page/2/", ""},
		{1, 1, "", ""},
	}
	for _, tt := range tests {
		p := buildPagination(tt.page, tt.total)
		if tt.total == 1 {
			if p != nil {
				t.Errorf("page %d/%d: expected nil pagination", tt.page, tt.total)
			}
			continue
		}
		if p.Prev != tt.wantPrev {
			t.Errorf("page %d/%d: prev = %q, want %q", tt.page, tt.total, p.Prev, tt.wantPrev)
		}
		if p.Next != tt.wantNext {
			t.Errorf("page %d/%d: next = %q, want %q", tt.page, tt.total, p.Next, tt.wantNext)
		}
	}
}

func TestExtractDescription(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{"<p>Hello world</p>", "Hello world"},
		{"<h1>Title</h1><p>Body text here.</p>", "TitleBody text here."},
		{"plain text", "plain text"},
	}
	for _, tt := range tests {
		got := extractDescription(tt.content)
		if got != tt.want {
			t.Errorf("extractDescription(%q) = %q, want %q", tt.content, got, tt.want)
		}
	}
}
