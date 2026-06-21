package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestLegacyRedirectIssues302(t *testing.T) {
	h := legacyRedirect()
	req := httptest.NewRequest(http.MethodGet, "/admin/v2/editor/hello", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/os/editor/hello" {
		t.Errorf("Location = %q, want /os/editor/hello", loc)
	}
}

func TestAdminLegacyEnabledEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE"} {
		t.Setenv("ADMIN_LEGACY", v)
		if !adminLegacyEnabled() {
			t.Errorf("ADMIN_LEGACY=%q should enable legacy", v)
		}
	}
	for _, v := range []string{"", "0", "no", "off"} {
		t.Setenv("ADMIN_LEGACY", v)
		if adminLegacyEnabled() {
			t.Errorf("ADMIN_LEGACY=%q should NOT enable legacy", v)
		}
	}
}

// The banner must carry the nonce, point at v3, name the removal release, and
// stay CSP-clean (no inline style attributes, no eval).
func TestLegacyBannerContentAndCSP(t *testing.T) {
	out := legacyDeprecationBanner("TESTNONCE")
	for _, want := range []string{
		`nonce="TESTNONCE"`,
		`href="/os"`,
		legacyRemovalRelease,
		`data-legacy-dismiss`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q", want)
		}
	}
	if strings.Contains(out, `style="`) {
		t.Error("banner contains inline style attribute (violates CSP)")
	}
	if strings.Contains(out, "eval(") || strings.Contains(out, "unsafe-eval") {
		t.Error("banner references eval")
	}
}
