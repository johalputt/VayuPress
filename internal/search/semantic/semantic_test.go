package semantic_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/ai"
	"github.com/johalputt/vayupress/internal/search/semantic"
)

func TestSemanticIndexAndSearch(t *testing.T) {
	e := ai.NewLocalEmbedder(64)
	idx := semantic.New(e)

	posts := []struct{ id, title, body string }{
		{"p1", "Go Security", "encryption key management golang"},
		{"p2", "SQLite Tips", "database wal mode performance"},
		{"p3", "Kubernetes", "container orchestration deployment"},
	}
	for _, p := range posts {
		if err := idx.Add(p.id, p.title, p.body); err != nil {
			t.Fatalf("Add %s: %v", p.id, err)
		}
	}
	if idx.Size() != 3 {
		t.Errorf("expected 3, got %d", idx.Size())
	}

	results, err := idx.Search("security encryption", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results")
	}
	// Results should be sorted by similarity desc
	if len(results) > 1 && results[0].Similarity < results[1].Similarity {
		t.Error("results not sorted by similarity desc")
	}
}
