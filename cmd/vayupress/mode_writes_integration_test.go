// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/mode"
)

// Every console route that writes a post or page answers 503 writes-refused in
// read-only mode, before it writes anything. One request per route, so a route
// left unwrapped is named; and the post is read back unchanged at the end.
func TestTheConsoleRefusesPostWritesInReadOnly(t *testing.T) {
	srv, key := newTestHarness(t)
	doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
		"title": "Kept", "slug": "kept-post", "content": "<p>kept</p>", "tags": []string{},
	})

	mode.Global.ForceTransition(mode.ModeReadOnly, "test")
	t.Cleanup(mode.Global.Reset)

	csrf := auth.GenerateCSRFToken("")
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/os/api/editor/save", `{"slug":"kept-post","title":"Changed","blocks":[{"type":"paragraph","text":"changed"}]}`},
		{"POST", "/os/api/editor/slug", `{"slug":"kept-post","new":"moved-post"}`},
		{"POST", "/os/api/editor/import", `{"slug":"kept-post","content":"changed"}`},
		{"POST", "/os/api/editor/versions/kept-post/1/restore", `{}`},
		{"POST", "/os/api/posts/quick-create", `{"title":"New"}`},
		{"POST", "/os/api/pages/quick-create", `{"title":"New page"}`},
		{"POST", "/os/api/posts/status", `{"slug":"kept-post","status":"draft"}`},
		{"POST", "/os/api/posts/bulk", `{"action":"delete","slugs":["kept-post"]}`},
		{"POST", "/os/api/posts/kept-post/status-fragment", ``},
		{"POST", "/os/api/posts/kept-post/pin-fragment", ``},
		{"POST", "/os/api/posts/kept-post/text-fragment", ``},
		{"DELETE", "/os/api/posts/kept-post", ``},
	} {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, bytes.NewReader([]byte(c.body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: csrf})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(b), "writes-refused") {
			t.Errorf("%s %s in read-only = %d %s, want 503 writes-refused", c.method, c.path, resp.StatusCode, b)
		}
	}

	body := decodeBody(t, doRequest(t, srv, "GET", "/api/v1/articles/kept-post", "", nil))
	if body["title"] != "Kept" || !strings.Contains(body["content"].(string), "kept") {
		t.Errorf("the post changed in read-only mode: %v", body)
	}
}
