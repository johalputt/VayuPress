package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// docsRouter wires the public docs routes onto a bare App, mirroring routes.go.
func docsRouter() http.Handler {
	a := &App{}
	r := chi.NewRouter()
	r.Get("/docs", a.handleDocs)
	r.Get("/docs/*", a.handleDocs)
	return r
}

func getDocs(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	docsRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestDocsIndexLists pins that /docs renders the index with the shell, the
// embedded stylesheet (CSP-safe, no inline styles), and links to real docs.
func TestDocsIndexLists(t *testing.T) {
	rec := getDocs(t, "/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("/docs status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"VayuPress Documentation",
		`<link rel="stylesheet" href="/static/css/docs.css`,
		`href="/docs/OPERATIONS"`, // a real top-level guide
		"Architecture Decisions",  // the ADR section
		`href="/docs/adr"`,        // ADR registry link
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/docs index missing %q", want)
		}
	}
	// No inline <style>/<script> — the page must stay within style-src 'self'.
	if strings.Contains(body, "<style") || strings.Contains(strings.ToLower(body), "<script") {
		t.Errorf("/docs must not emit inline style/script (CSP)")
	}
}

// TestDocsRendersGuide renders a real embedded guide and an ADR.
func TestDocsRendersGuide(t *testing.T) {
	rec := getDocs(t, "/docs/OPERATIONS")
	if rec.Code != http.StatusOK {
		t.Fatalf("/docs/OPERATIONS status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<article class="doc-prose">`) {
		t.Errorf("guide should render inside the prose article")
	}

	adr := getDocs(t, "/docs/adr/ADR-0001-sqlite-first")
	if adr.Code != http.StatusOK {
		t.Fatalf("/docs/adr/ADR-0001 status = %d, want 200", adr.Code)
	}
	if !strings.Contains(adr.Body.String(), `<article class="doc-prose">`) {
		t.Errorf("ADR should render inside the prose article")
	}

	list := getDocs(t, "/docs/adr")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "ADR-0001") {
		t.Errorf("/docs/adr should list the ADRs (status %d)", list.Code)
	}
}

// TestDocsLegacyAnchorAlias pins that a legacy API-error anchor path (no file of
// its own) resolves to its covering guide instead of 404-ing, so error-hint
// links never dead-end.
func TestDocsLegacyAnchorAlias(t *testing.T) {
	for _, p := range []string{"/docs/operations/replay", "/docs/api/errors", "/docs/governance/budgets"} {
		rec := getDocs(t, p)
		if rec.Code != http.StatusOK {
			t.Errorf("%s should resolve to a guide (200), got %d", p, rec.Code)
		}
	}
}

// TestDocsNotFoundAndTraversal pins a real 404 for unknown docs and that a
// traversal attempt cannot escape the embedded tree.
func TestDocsNotFoundAndTraversal(t *testing.T) {
	if rec := getDocs(t, "/docs/does-not-exist-xyz"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown doc should 404, got %d", rec.Code)
	}
	// ../ is cleaned away; nothing outside the doc set can ever resolve.
	rec := getDocs(t, "/docs/../../etc/passwd")
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "root:") {
		t.Fatalf("path traversal leaked a file")
	}
}
