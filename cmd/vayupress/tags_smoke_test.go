//go:build integration

package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSmoke_TagPages(t *testing.T) {
	srv, key := newTestHarness(t)

	mk := func(title, slug string, tags []string) {
		resp := doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
			"title": title, "slug": slug, "content": "Body for " + title, "tags": tags,
		})
		if resp.StatusCode != 202 {
			t.Fatalf("create %s: want 202, got %d", slug, resp.StatusCode)
		}
		resp.Body.Close()
	}

	mk("Go Generics", "go-generics", []string{"go", "language"})
	mk("Go Routines", "go-routines", []string{"go"})
	mk("Rust Borrow", "rust-borrow", []string{"rust"})

	get := func(path string) (int, string) {
		resp := doRequest(t, srv, "GET", path, "", nil)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}

	// Tag index lists every topic with counts.
	code, body := get("/tags")
	if code != http.StatusOK {
		t.Fatalf("/tags want 200, got %d", code)
	}
	for _, want := range []string{"#go", "#rust", "#language", "Browse by topic"} {
		if !strings.Contains(body, want) {
			t.Errorf("/tags missing %q", want)
		}
	}

	// Per-tag page lists the matching posts (and only those).
	code, body = get("/tags/go")
	if code != http.StatusOK {
		t.Fatalf("/tags/go want 200, got %d", code)
	}
	if !strings.Contains(body, "Go Generics") || !strings.Contains(body, "Go Routines") {
		t.Errorf("/tags/go missing expected posts; body len=%d", len(body))
	}
	if strings.Contains(body, "Rust Borrow") {
		t.Errorf("/tags/go leaked a rust post")
	}

	// A tag with no posts is a 404 (never an empty indexed page).
	code, _ = get("/tags/nonexistent-topic")
	if code != http.StatusNotFound {
		t.Errorf("/tags/nonexistent-topic want 404, got %d", code)
	}

	// Second request to /tags/go should hit the disk cache and still 200.
	code, _ = get("/tags/go")
	if code != http.StatusOK {
		t.Errorf("/tags/go (cached) want 200, got %d", code)
	}
}
