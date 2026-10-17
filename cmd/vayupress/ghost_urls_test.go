// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// Ghost's address shapes, one case per rule, through the 404 handler: the
// redirects a migrated catalogue's inbound links need, and the requests that
// must still 404 — a draft, an unknown slug, a non-GET, and a protocol-relative
// path that would redirect off the site.
func TestGhostShapedAddressesRedirect(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []string{
		`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at,status) VALUES('g1','Live','live-post','<p>x</p>','',datetime('now'),datetime('now'),'published')`,
		`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at,status) VALUES('g2','Draft','draft-post','<p>x</p>','',datetime('now'),datetime('now'),'draft')`,
	} {
		if _, err := dbpkg.WDB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{}
	for _, c := range []struct {
		method, path, want string // want "" = still 404
	}{
		{"GET", "/live-post/", "/live-post"},
		{"HEAD", "/live-post/", "/live-post"},
		{"GET", "/live-post/?ref=old", "/live-post?ref=old"},
		{"GET", "/tag/go/", "/tags/go"},
		{"GET", "/tag/go", "/tags/go"},
		{"GET", "/tags/go/", "/tags/go"},
		{"GET", "/tag/a%20b/", "/tags/a%20b"},
		{"GET", "/draft-post/", ""},
		{"GET", "/no-such-post/", ""},
		{"POST", "/live-post/", ""},
		{"GET", "//evil.example/", ""},
		{"GET", "/\\evil.example/", ""},
		{"GET", "/tag/%2F%2Fevil.example/", ""},
		{"GET", "/tag/", ""},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(c.method, "/", nil)
		// Set as the server parses a request line, so "//evil.example/" stays a
		// path rather than becoming a host.
		u, err := url.ParseRequestURI(c.path)
		if err != nil {
			t.Fatal(err)
		}
		req.URL = u
		a.handleNotFound(rec, req)
		if c.want == "" {
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d (Location %q), want 404", c.method, c.path, rec.Code, rec.Header().Get("Location"))
			}
			continue
		}
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != c.want {
			t.Errorf("%s %s = %d → %q, want 301 → %q", c.method, c.path, rec.Code, rec.Header().Get("Location"), c.want)
		}
	}
}
