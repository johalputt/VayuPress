// SPDX-License-Identifier: Apache-2.0

package pgp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWKDConditionalGET verifies the WKD handler emits a strong ETag +
// Cache-Control and honours If-None-Match with a 304 (empty body), so clients
// revalidate cheaply while a key change still busts the cache.
func TestWKDConditionalGET(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.GenerateKeypair(&PGPUser{UserID: "eve", Name: "Eve", Email: "eve@example.com"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	srv := httptest.NewServer(e.ServeWKD("example.com"))
	defer srv.Close()
	url := srv.URL + "/.well-known/openpgpkey/hu/" + wkdLocalHash("eve") + "?l=eve"

	// First fetch: 200 with an ETag and Cache-Control.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	etag := resp.Header.Get("ETag")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if etag == "" {
		t.Fatal("missing ETag on WKD response")
	}
	if resp.Header.Get("Cache-Control") == "" {
		t.Error("missing Cache-Control on WKD response")
	}

	// Conditional fetch with the ETag: 304, no body.
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("If-None-Match", etag)
	cond, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	defer cond.Body.Close()
	if cond.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", cond.StatusCode)
	}

	// A non-matching validator still returns the key.
	req2, _ := http.NewRequest(http.MethodGet, url, nil)
	req2.Header.Set("If-None-Match", `"stale"`)
	full, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("stale-validator GET: %v", err)
	}
	defer full.Body.Close()
	if full.StatusCode != http.StatusOK {
		t.Fatalf("stale-validator status = %d, want 200", full.StatusCode)
	}
}
