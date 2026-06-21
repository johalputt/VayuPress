package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyToOSPathMapping(t *testing.T) {
	cases := map[string]string{
		"/admin":                   "/os",
		"/admin/v2":                "/os",
		"/admin/v2/posts":          "/os/posts",
		"/admin/v2/editor":         "/os/editor",
		"/admin/v2/editor/my-post": "/os/editor/my-post",
		"/admin/v2/seo":            "/os/seo",
		"/admin/v2/settings":       "/os/settings",
		"/admin/v3":                "/os",
		"/admin/v3/posts":          "/os/posts",
		"/admin/v3/editor/my-post": "/os/editor/my-post",
		"/admin/v3/theme":          "/os/theme",
		"/admin/v3/monitoring":     "/os/monitoring",
	}
	for in, want := range cases {
		if got := legacyToOSPath(in); got != want {
			t.Errorf("legacyToOSPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Admin v2 was removed in v1.6.0 (ADR-0069 Stage 3): legacy admin URLs now
// permanently (301) redirect into VayuOS.
func TestLegacyRedirectIssues301(t *testing.T) {
	h := legacyRedirect()
	req := httptest.NewRequest(http.MethodGet, "/admin/v2/editor/hello", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/os/editor/hello" {
		t.Errorf("Location = %q, want /os/editor/hello", loc)
	}
}
