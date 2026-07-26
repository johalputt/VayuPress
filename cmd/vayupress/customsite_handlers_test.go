// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBlogBaseForModeCustom: the custom-website mode keeps the blog at /blog.
func TestBlogBaseForModeCustom(t *testing.T) {
	if got := blogBaseForMode("custom"); got != "/blog" {
		t.Errorf("blogBaseForMode(custom) = %q, want /blog", got)
	}
}

// TestCustomBuildGuideHasHardRules ensures the AI build guide states the rules
// the deploy validator actually enforces, so operators/AI produce valid bundles.
func TestCustomBuildGuideHasHardRules(t *testing.T) {
	g := customBuildGuide("example.com")
	for _, want := range []string{
		"index.html",  // required entry point
		"Relative",    // relative paths only
		"50 MiB",      // total size cap
		"25 MiB",      // per-file cap
		"/blog",       // reserved blog path
		"example.com", // domain interpolated
		".php",        // disallowed example called out
	} {
		if !strings.Contains(g, want) {
			t.Errorf("build guide missing %q", want)
		}
	}
}

// TestCustomGuideHandlerServesMarkdown checks the download endpoint returns the
// guide as a Markdown attachment.
func TestCustomGuideHandlerServesMarkdown(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "/os/api/website/custom-guide", nil)
	rec := httptest.NewRecorder()
	a.handleOSWebsiteCustomGuide(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("content-type = %q, want text/markdown", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("content-disposition = %q, want attachment", cd)
	}
	if !strings.Contains(rec.Body.String(), "index.html") {
		t.Error("guide body missing core instruction")
	}
}

// TestNotFoundSafeWithoutSettings guards the custom-site 404 fallback: with no
// settings store (custom mode inactive) handleNotFound must still render a plain
// 404 and never panic.
func TestNotFoundSafeWithoutSettings(t *testing.T) {
	a := &App{} // siteSettings nil → customSiteActive false
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	a.handleNotFound(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
