//go:build integration

package main

import (
	"fmt"
	"net/http"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// insertDraftArticle writes a draft row directly into the DB, bypassing the
// queue. This simulates an article that was created as a draft via the editor.
func insertDraftArticle(t *testing.T, slug string) {
	t.Helper()
	_, err := dbpkg.DB.Exec(
		`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		slug, "Draft Article", slug, "secret draft content", "", "draft",
	)
	if err != nil {
		t.Fatalf("insert draft: %v", err)
	}
}

// TestDraftNotLeakedViaArticleAPI asserts that GET /api/v1/articles/{slug}
// returns 404 for a draft when called without an API key.
func TestDraftNotLeakedViaArticleAPI(t *testing.T) {
	srv, apiKey := newTestHarness(t)
	slug := "secret-draft"
	insertDraftArticle(t, slug)

	// Anonymous request must get 404.
	resp := doRequest(t, srv, "GET", "/api/v1/articles/"+slug, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("anonymous: expected 404 for draft, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Authenticated operator request must get 200.
	resp = doRequest(t, srv, "GET", "/api/v1/articles/"+slug, apiKey, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("operator: expected 200 for draft, got %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	if body["status"] != "draft" {
		t.Errorf("expected status=draft in response, got %v", body["status"])
	}
}

// TestDraftNotLeakedViaListAPI asserts that GET /api/v1/articles does not
// include draft articles for anonymous callers.
func TestDraftNotLeakedViaListAPI(t *testing.T) {
	srv, _ := newTestHarness(t)
	insertDraftArticle(t, "draft-in-list")

	resp := doRequest(t, srv, "GET", "/api/v1/articles", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list articles: unexpected status %d", resp.StatusCode)
	}
	body := decodeBody(t, resp)
	articles, _ := body["articles"].([]interface{})
	for _, a := range articles {
		m, _ := a.(map[string]interface{})
		if fmt.Sprint(m["slug"]) == "draft-in-list" {
			t.Error("draft article appeared in public list response")
		}
	}
}

// TestDraftNotLeakedViaCommentAPI asserts that submitting a comment to a
// draft article slug is rejected (article not found).
func TestDraftNotLeakedViaCommentAPI(t *testing.T) {
	srv, _ := newTestHarness(t)
	insertDraftArticle(t, "draft-comment-target")

	payload := map[string]string{
		"author":  "eve",
		"email":   "eve@example.com",
		"comment": "I can see this draft!",
	}
	resp := doRequest(t, srv, "POST", "/api/v1/comments/draft-comment-target", "", payload)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted {
		t.Errorf("comment on draft should be rejected, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
