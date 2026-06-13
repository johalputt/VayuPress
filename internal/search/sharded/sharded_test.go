package sharded_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/search/sharded"
	_ "github.com/mattn/go-sqlite3"
)

func TestShardedSearch(t *testing.T) {
	dbs := make([]*sql.DB, 3)
	for i := range dbs {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		dbs[i] = db
	}

	idx, err := sharded.New(dbs)
	if err != nil {
		if strings.Contains(err.Error(), "fts5") || strings.Contains(err.Error(), "no such module") {
			t.Skip("SQLite FTS5 not available in test binary")
		}
		t.Fatalf("New: %v", err)
	}

	posts := []struct{ id, title, body string }{
		{"p1", "Go Security", "golang security hardening"},
		{"p2", "SQLite Tips", "sqlite wal mode performance"},
		{"p3", "WebAssembly Guide", "wasm sandboxing browser"},
	}
	for _, p := range posts {
		if err := idx.Index(p.id, p.title, p.body); err != nil {
			t.Fatalf("Index %s: %v", p.id, err)
		}
	}

	results, err := idx.Search("security", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'security'")
	}
}
