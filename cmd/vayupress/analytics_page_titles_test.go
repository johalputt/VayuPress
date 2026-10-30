// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// A listed page is named as its post is, on the site the list is about: a
// post's title, a path that is no post kept as it is, and another site's post
// never lending its title (a slug is the install's, so a path one site has no
// post at may be another site's post). Platform's lists, every site added
// together, take a post's title from whichever site has it.
func TestListedPagesAreNamedByTheirPostOnTheirSite(t *testing.T) {
	openMigratedDB(t)
	for _, q := range []string{
		`INSERT INTO articles(id,slug,title,content,status,domain_id,created_at,updated_at) VALUES('a1','hello','Hello, primary','x','published','',datetime('now'),datetime('now'))`,
		`INSERT INTO articles(id,slug,title,content,status,domain_id,created_at,updated_at) VALUES('a3','only-one','Only on site one','x','published','s1',datetime('now'),datetime('now'))`,
	} {
		if _, err := dbpkg.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{}
	pages := []analytics.PageStat{{Path: "/hello"}, {Path: "/"}, {Path: "/tag/go"}, {Path: "/only-one"}}
	for _, tc := range []struct {
		v    analyticsView
		want map[string]string
	}{
		{primaryView, map[string]string{"/hello": "Hello, primary", "/": "/", "/tag/go": "/tag/go", "/only-one": "/only-one"}},
		{hostedView(domain.Domain{ID: "s1"}), map[string]string{"/hello": "/hello", "/": "/", "/tag/go": "/tag/go", "/only-one": "Only on site one"}},
		{platformView, map[string]string{"/hello": "Hello, primary", "/": "/", "/tag/go": "/tag/go", "/only-one": "Only on site one"}},
	} {
		got := a.pageTitles(context.Background(), tc.v, pages)
		for path, want := range tc.want {
			if got[path] != want {
				t.Errorf("%s: %s is named %q, want %q", tc.v.base, path, got[path], want)
			}
		}
	}
}
