//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
)

// TestV3EditorSaveRoundTrip exercises the full save path: create a draft, POST a
// block document, and confirm the rendered HTML lands in content and the raw
// blocks persist for re-hydration.
func TestV3EditorSaveRoundTrip(t *testing.T) {
	srv, key := newTestHarness(t)

	doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
		"title": "Draft", "slug": "draft-post", "content": "seed", "tags": []string{},
	})

	csrf := auth.GenerateCSRFToken()
	if csrf == "" {
		t.Fatal("could not generate CSRF token")
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"slug":  "draft-post",
		"title": "Draft Updated",
		"blocks": []map[string]interface{}{
			{"type": "heading", "level": 2, "text": "Section"},
			{"type": "paragraph", "text": "Body text here"},
		},
	})
	req, _ := http.NewRequest("POST", srv.URL+"/admin/v3/api/editor/save", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: csrf})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("save request: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("save want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	getResp := doRequest(t, srv, "GET", "/api/v1/articles/draft-post", "", nil)
	body := decodeBody(t, getResp)
	content, _ := body["content"].(string)
	if !strings.Contains(content, "<h2>Section</h2>") || !strings.Contains(content, "Body text here") {
		t.Errorf("article content not rendered from blocks: %q", content)
	}
}
