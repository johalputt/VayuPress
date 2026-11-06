// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// hostedBlogApp is an install whose own blog is at "/", hosting a website
// with its blog at /blog and a site that is all blog.
func hostedBlogApp(t *testing.T) (a *App, shop, blog domain.Domain) {
	t.Helper()
	openMigratedDB(t)
	render.SetBlogBase("/")
	a = &App{siteSettings: settings.New(dbpkg.DB), domains: domain.New(dbpkg.DB, dbpkg.RDB)}
	ctx := context.Background()
	mk := func(host, mode string) domain.Domain {
		d, err := a.domains.Create(ctx, host, domain.SiteBlog, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.domains.SetSite(ctx, d.ID, domain.SiteConfig{Mode: mode}); err != nil {
			t.Fatal(err)
		}
		got, err := a.domains.Resolve(ctx, host)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	return a, mk("shop.example.com", "business_subpath"), mk("notes.example.com", "blog")
}

func requestFor(path string, d domain.Domain) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	return r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d))
}

// Each site's blog lives where its own website mode puts it, whatever the
// install's own blog does: a website with its blog at /blog was read with the
// install's "/", so its /blog was taken for a post and answered Not found.
func TestEachHostedSitesBlogIsWhereItsOwnModePutsIt(t *testing.T) {
	a, shop, blog := hostedBlogApp(t)
	if got := a.blogBaseFor(requestFor("/blog", shop)); got != "/blog" {
		t.Errorf("a website with its blog at /blog: base %q", got)
	}
	if got := a.blogBaseFor(requestFor("/", blog)); got != "/" {
		t.Errorf("a site that is all blog: base %q", got)
	}
	s, ok := a.brandForRequest(requestFor("/blog", shop))
	if !ok || s.BlogBase != "/blog" {
		t.Errorf("the site's settings carry base %q (%v), want /blog", s.BlogBase, ok)
	}
}

// A site that has not named itself is called by its own address, and its Not
// found page is drawn in its own name: both had the install's.
func TestAnUnnamedHostedSiteIsCalledByItsAddress(t *testing.T) {
	a, _, blog := hostedBlogApp(t)
	prev := render.GetActiveSettings()
	render.SetActiveSettings(render.SiteSettings{Name: "The Install"})
	t.Cleanup(func() { render.SetActiveSettings(prev) })

	s, ok := a.brandForRequest(requestFor("/", blog))
	if !ok || s.Name != "notes.example.com" {
		t.Errorf("an unnamed site is called %q (%v), want its address", s.Name, ok)
	}
	rec := httptest.NewRecorder()
	a.handleNotFound(rec, requestFor("/no-such-post", blog))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if page := rec.Body.String(); strings.Contains(page, "The Install") || !strings.Contains(page, "notes.example.com") {
		t.Errorf("the site's Not found page is not in its own name:\n%.400s", page)
	}
}
