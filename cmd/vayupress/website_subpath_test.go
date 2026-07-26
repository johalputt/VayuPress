// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/render"
)

// TestBlogBaseForMode maps site modes to the blog base path.
func TestBlogBaseForMode(t *testing.T) {
	if got := blogBaseForMode("business_subpath"); got != "/blog" {
		t.Errorf("business_subpath -> %q, want /blog", got)
	}
	for _, m := range []string{"", "blog", "business", "unknown"} {
		if got := blogBaseForMode(m); got != "/" {
			t.Errorf("%q -> %q, want /", m, got)
		}
	}
}

// reqWithPage builds a request carrying a chi {page} route param.
func reqWithPage(method, target, page string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("page", page)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestHomePagedRedirectsToBlogInSubpath proves that in business_subpath mode the
// legacy /page/N URL 301-redirects to the canonical /blog/page/N.
func TestHomePagedRedirectsToBlogInSubpath(t *testing.T) {
	render.SetBlogBase("/blog")
	defer render.SetBlogBase("/")

	a := &App{}
	rec := httptest.NewRecorder()
	a.handleHomePaged(rec, reqWithPage(http.MethodGet, "/page/3", "3"))

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/blog/page/3" {
		t.Errorf("Location = %q, want /blog/page/3", loc)
	}
}

// TestBlogPagedInactiveWhenNotSubpath proves /blog/page/N is not a blog surface
// outside business_subpath mode (it 404s rather than rendering the feed).
func TestBlogPagedInactiveWhenNotSubpath(t *testing.T) {
	render.SetBlogBase("/") // default (blog-at-root / subdomain modes)

	a := &App{}
	rec := httptest.NewRecorder()
	a.handleBlogPaged(rec, reqWithPage(http.MethodGet, "/blog/page/2", "2"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when not in business_subpath mode", rec.Code)
	}
}

// TestBlogPagedPage1RedirectsToBlog proves /blog/page/1 canonicalises to /blog.
func TestBlogPagedPage1RedirectsToBlog(t *testing.T) {
	render.SetBlogBase("/blog")
	defer render.SetBlogBase("/")

	a := &App{}
	rec := httptest.NewRecorder()
	a.handleBlogPaged(rec, reqWithPage(http.MethodGet, "/blog/page/1", "1"))

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/blog" {
		t.Errorf("Location = %q, want /blog", loc)
	}
}
