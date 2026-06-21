package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV2ToV3PathMapping(t *testing.T) {
	cases := map[string]string{
		"/admin":                   "/admin/v3",
		"/admin/v2":                "/admin/v3",
		"/admin/v2/posts":          "/admin/v3/posts",
		"/admin/v2/editor":         "/admin/v3/editor",
		"/admin/v2/editor/my-post": "/admin/v3/editor/my-post",
		"/admin/v2/seo":            "/admin/v3/seo",
		"/admin/v2/settings":       "/admin/v3/settings",
	}
	for in, want := range cases {
		if got := v2ToV3Path(in); got != want {
			t.Errorf("v2ToV3Path(%q) = %q, want %q", in, got, want)
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
	if loc := rec.Header().Get("Location"); loc != "/admin/v3/editor/hello" {
		t.Errorf("Location = %q, want /admin/v3/editor/hello", loc)
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
		`href="/admin/v3"`,
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
