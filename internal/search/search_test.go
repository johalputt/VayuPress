package search

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func mustIndex(t *testing.T, s Service, id, title, slug, content string, tags []string) {
	t.Helper()
	if err := s.Index(context.Background(), id, title, slug, content, tags, int64(len(id))); err != nil {
		t.Fatalf("Index(%s): %v", id, err)
	}
}

func TestBuiltinSearchRankingAndAND(t *testing.T) {
	SetEnabled(true)
	s := NewService(nil)
	mustIndex(t, s, "1", "Go concurrency patterns", "go-concurrency", "Channels and goroutines in depth.", []string{"go", "concurrency"})
	mustIndex(t, s, "2", "A gentle intro", "gentle-intro", "We mention go briefly in passing.", []string{"beginners"})
	mustIndex(t, s, "3", "Rust ownership", "rust-ownership", "Borrow checker explained.", []string{"rust"})

	res, err := s.Search(context.Background(), "go", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("expected 2 hits for 'go', got %d", len(res.Hits))
	}
	// Title/tag match must rank above an excerpt-only mention.
	if res.Hits[0].Slug != "go-concurrency" {
		t.Errorf("expected title/tag match first, got %q", res.Hits[0].Slug)
	}

	// AND semantics: every term must match somewhere.
	res, _ = s.Search(context.Background(), "go rust", 10)
	if len(res.Hits) != 0 {
		t.Errorf("expected 0 hits for 'go rust' (no doc has both), got %d", len(res.Hits))
	}

	res, _ = s.Search(context.Background(), "concurrency patterns", 10)
	if len(res.Hits) != 1 || res.Hits[0].Slug != "go-concurrency" {
		t.Errorf("expected the concurrency post for a 2-term query, got %+v", res.Hits)
	}
}

func TestBuiltinSnapshotVersioningAndIncremental(t *testing.T) {
	SetEnabled(true)
	s := NewService(nil)
	mustIndex(t, s, "1", "First", "first", "hello world", nil)
	p1, v1 := s.Snapshot()
	if v1 == "" || v1 == "off" {
		t.Fatalf("expected a real version, got %q", v1)
	}
	var idx clientIndex
	if err := json.Unmarshal(p1, &idx); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if len(idx.Posts) != 1 || idx.Posts[0].U != "first" {
		t.Fatalf("unexpected snapshot posts: %+v", idx.Posts)
	}

	// A snapshot must be memoised (same version) until a mutation invalidates it.
	_, v1b := s.Snapshot()
	if v1b != v1 {
		t.Errorf("snapshot version changed without a mutation: %q -> %q", v1, v1b)
	}

	// Incremental add must change the version (and content).
	mustIndex(t, s, "2", "Second", "second", "another", nil)
	p2, v2 := s.Snapshot()
	if v2 == v1 {
		t.Errorf("snapshot version did not change after an incremental add")
	}
	if !strings.Contains(string(p2), `"second"`) {
		t.Errorf("new post missing from snapshot")
	}

	// Delete must remove it again.
	if err := s.Delete(context.Background(), "2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	p3, _ := s.Snapshot()
	if strings.Contains(string(p3), `"second"`) {
		t.Errorf("deleted post still present in snapshot")
	}
}

func TestBuiltinSearchDisabled(t *testing.T) {
	s := NewService(nil)
	mustIndex(t, s, "1", "Hello", "hello", "world", nil)
	SetEnabled(false)
	defer SetEnabled(true)

	res, _ := s.Search(context.Background(), "hello", 10)
	if len(res.Hits) != 0 {
		t.Errorf("disabled search must return no hits, got %d", len(res.Hits))
	}
	if _, v := s.Snapshot(); v != "off" {
		t.Errorf("disabled snapshot version should be 'off', got %q", v)
	}
}
