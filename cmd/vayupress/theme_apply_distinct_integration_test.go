//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
)

// TestApplyDesignThemeReachesPublicCSS proves the end-to-end pipeline a user
// relies on: applying a design theme must push its full component + layout CSS
// (not just colours) to the public /theme.css, and switching themes must swap
// that layout. This is the foolproof guard against the "applying a theme only
// changes fonts/colours" report.
func TestApplyDesignThemeReachesPublicCSS(t *testing.T) {
	srv, key := newTestHarness(t)
	csrf := auth.GenerateCSRFToken()

	get := func(path string) string {
		resp := doRequest(t, srv, "GET", path, "", nil)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}
	apply := func(preset string) {
		payload, _ := json.Marshal(map[string]string{"preset": preset})
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/admin/theme/apply", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: csrf})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("apply %s: %v", preset, err)
		}
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatalf("apply %s: status %d: %s", preset, resp.StatusCode, string(b))
		}
		resp.Body.Close()
	}

	// Each design theme must contribute BOTH its signature component class
	// (proves CustomCSS reached the public stylesheet) and a real-markup rule.
	cases := []struct {
		preset    string
		signature string
	}{
		{"Dispatch", ".dispatch-"},
		{"Maverick", ".maverick-"},
		{"Apex", ".apex-"},
		{"Beacon", ".beacon-"},
	}

	for _, c := range cases {
		apply(c.preset)
		css := get("/theme.css")
		if !strings.Contains(css, c.signature) {
			t.Errorf("after applying %s, /theme.css does NOT contain its component CSS %q — design CSS is not reaching the public site", c.preset, c.signature)
		}
		if !strings.Contains(css, ".vayu-post-card") || !strings.Contains(css, ".vayu-hero") {
			t.Errorf("after applying %s, /theme.css does NOT restyle the real public markup (.vayu-hero/.vayu-post-card)", c.preset)
		}
	}

	// Switching themes must actually swap the layout: after applying Maverick,
	// Dispatch's inbox component CSS must be gone.
	apply("Dispatch")
	dispatchCSS := get("/theme.css")
	apply("Maverick")
	maverickCSS := get("/theme.css")
	if dispatchCSS == maverickCSS {
		t.Fatal("/theme.css is identical for Dispatch and Maverick — theme switch is not changing layout")
	}
	if strings.Contains(maverickCSS, ".dispatch-inbox") {
		t.Error("after switching to Maverick, Dispatch's component CSS is still served — stale theme CSS")
	}
}
