// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// The system bar's site switcher (ADR-0154, one console per site): every
// hosted site's console one click away, and inside one, the bar names that
// site rather than the install's, so an operator always sees whose site they
// are changing.

func TestTheSwitcherListsTheHostedSitesAndKnowsWhichItIsIn(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	// The install's own site is the switcher's first row, not a hosted site.
	if err := reg.EnsurePrimary(ctx, "example.com", domain.SiteBlog); err != nil {
		t.Fatal(err)
	}
	bakery, err := reg.Create(ctx, "bakery.example", domain.SiteBlog, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create(ctx, "studio.example", domain.SiteBlog, false); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Create(ctx, torSitePending+"abc.local", domain.SiteBlog, false); err != nil {
		t.Fatal(err)
	}
	a := &App{domains: reg}

	s := &osSettings{AccessLevel: accessAdmin, Route: "/os/d/" + bakery.ID + "/settings"}
	a.osSitesFor(ctx, s)
	var hosts []string
	for _, site := range s.Sites {
		hosts = append(hosts, site.Host)
	}
	if strings.Join(hosts, ",") != "bakery.example,studio.example" {
		t.Errorf("sites offered: %v, want the two hosted sites: not the install's own, nor a Tor site still minting its address", hosts)
	}
	if s.Scope == nil || s.Scope.Host != "bakery.example" {
		t.Errorf("inside bakery.example's console the scope is %+v", s.Scope)
	}

	home := &osSettings{AccessLevel: accessAdmin, Route: "/os/"}
	a.osSitesFor(ctx, home)
	if home.Scope != nil {
		t.Errorf("the install's own console is scoped to %+v", home.Scope)
	}

	// Only a session that can open the site list gets a way into each site.
	author := &osSettings{AccessLevel: accessAuthor, Route: "/os/"}
	a.osSitesFor(ctx, author)
	if len(author.Sites) != 0 {
		t.Errorf("an author is offered other sites' consoles: %+v", author.Sites)
	}
}

func TestTheBarNamesTheSiteThePageBelongsTo(t *testing.T) {
	sites := []osSite{{ID: "d1", Host: "bakery.example", Active: true}, {ID: "d2", Host: "studio.example"}}
	in := &osSettings{Sites: sites}
	in.Scope = &in.Sites[0]
	out := saBrand(in, "/os/", "Johal")
	if !strings.Contains(out, `<span class="sa-mark__site">bakery.example</span>`) {
		t.Errorf("inside bakery.example the bar does not name it:\n%s", out)
	}
	if !strings.Contains(out, `href="/os/d/d1" aria-current="page"`) {
		t.Errorf("the site the page belongs to is not marked current:\n%s", out)
	}
	if !strings.Contains(out, `href="/os/d/d2">`) || !strings.Contains(out, `>Off</span>`) {
		t.Errorf("the other site, switched off, is missing or not said to be off:\n%s", out)
	}
	if !strings.Contains(out, `href="/os/"`) || !strings.Contains(out, ">Johal<") {
		t.Errorf("the switcher offers no way back to the install's own console:\n%s", out)
	}

	alone := saBrand(&osSettings{}, "/os/", "Johal")
	if strings.Contains(alone, "sa-sites") || !strings.Contains(alone, `<span class="sa-mark__site">Johal</span>`) {
		t.Errorf("with no hosted sites the bar grows a switcher, or loses the site's name:\n%s", alone)
	}
}
