// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/settings"
)

// Every website wears its OWN logo.
//
// The Optimize hub drew the same globe on every card under a heading that reads
// "Edit branding, content & theme per site". It had no choice: there was nowhere
// to put a per-site logo. The one upload in the product wrote
// settings.ForPrimary() at every mount — including the one reached from a hosted
// domain's Theme Studio, whose control is labelled "Logo & favicon" and whose
// preview was the bare /favicon.ico. An operator who uploaded a client's logo
// there rebranded their own install for every domain on the box, and nothing
// said so.
//
// Two properties matter, and the second is the one worth being strict about:
//
//	a site with its own mark shows it, and
//	a site WITHOUT one never shows somebody else's.
//
// The second is why the fallback is a neutral globe rather than the primary's
// mark. A client's hostname above the studio's logo is not a cosmetic
// imperfection; it is the panel asserting something false about whose site it is.

func seedSiteWithMark(t *testing.T, a *App, host string, mark []byte) domain.Domain {
	t.Helper()
	ctx := context.Background()
	reg := domain.New(dbpkg.DB, dbpkg.RDB)
	d, err := reg.Create(ctx, host, domain.SiteBlog, false)
	if err != nil {
		t.Fatalf("create %s: %v", host, err)
	}
	if len(mark) > 0 {
		if err := a.siteSettings.SetMany(ctx, settings.ForDomain(d.ID), map[string]string{
			settings.KeyBrandFavicon:     b64(mark),
			settings.KeyBrandFaviconType: "image/png",
		}); err != nil {
			t.Fatalf("store mark for %s: %v", host, err)
		}
	}
	return d
}

// A site's stored mark is reported for that site and for no other.
func TestOnlyTheSiteThatUploadedAMarkIsReportedAsHavingOne(t *testing.T) {
	a := resetSessionApp(t)
	a.siteSettings = settings.New(dbpkg.DB)
	ctx := context.Background()

	withLogo := seedSiteWithMark(t, a, "haslogo.example", []byte("\x89PNG\r\n\x1a\nlogo-bytes"))
	without := seedSiteWithMark(t, a, "nologo.example", nil)

	// And the operator's own install has a mark, which is the thing that must
	// NOT leak onto a client's card.
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyBrandFavicon:     b64([]byte("\x89PNG\r\n\x1a\nprimary-bytes")),
		settings.KeyBrandFaviconType: "image/png",
	}); err != nil {
		t.Fatalf("store primary mark: %v", err)
	}

	if !a.hasBrandMark(ctx, settings.ForDomain(withLogo.ID)) {
		t.Error("a site that uploaded its own logo is reported as having none, so its card " +
			"falls back to the generic globe")
	}
	if a.hasBrandMark(ctx, settings.ForDomain(without.ID)) {
		t.Error("a site that never uploaded a logo is reported as having one.\n\n" +
			"Its card would render an <img> for a mark that does not exist — a broken image " +
			"where a clean fallback belongs.")
	}

	// The decisive one: the primary's mark must not answer for a hosted domain.
	if b, _, ok := a.brandMark(ctx, settings.ForDomain(without.ID)); ok {
		t.Errorf("a hosted domain with no mark of its own was served %d bytes.\n\n"+
			"If that is the operator's install-wide logo, the panel is putting one business's "+
			"brand on another's site.", len(b))
	}
}

// settings.ForDomain("") is deliberately an INVALID scope, not the primary,
// because "" is the primary's sentinel id everywhere else. A logo reader that
// resolved it to the primary would hand every unidentified caller the
// operator's mark — which the settings package's own comment records as the
// shape of two earlier defects.
func TestABlankDomainIDResolvesToNoMarkRatherThanTheOperatorsOwn(t *testing.T) {
	a := resetSessionApp(t)
	a.siteSettings = settings.New(dbpkg.DB)
	ctx := context.Background()
	if err := a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{
		settings.KeyBrandFavicon:     b64([]byte("\x89PNG\r\n\x1a\nprimary-bytes")),
		settings.KeyBrandFaviconType: "image/png",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := a.brandMark(ctx, settings.ForDomain("")); ok {
		t.Error("a blank domain id was resolved to the primary's mark")
	}
	if a.hasBrandMark(ctx, settings.ForDomain("")) {
		t.Error("a blank domain id reports a mark")
	}
}

// The page itself: one site with a logo, one without, rendered together.
func TestTheOptimizePageDrawsEachSitesOwnMarkAndAGlobeForTheRest(t *testing.T) {
	withLogo := optimizeSite{ID: "aaaaaaaaaaaaaaaaaaaaaaaa", Host: "haslogo.example", Label: "Business site", HasMark: true}
	without := optimizeSite{ID: "bbbbbbbbbbbbbbbbbbbbbbbb", Host: "nologo.example", Label: "Business site"}

	got := osOptimizeGrid(accessAdmin, []optimizeSite{withLogo, without})

	if !strings.Contains(got, `src="/os/d/`+withLogo.ID+`/branding/mark"`) {
		t.Errorf("the site with its own logo does not render it.\n\npage:\n%s", got)
	}
	// The card without a mark must NOT point at a mark route — an <img> whose
	// source 404s is a broken-image glyph, which looks like a bug rather than
	// like a site that simply has no logo yet.
	if strings.Contains(got, `src="/os/d/`+without.ID+`/branding/mark"`) {
		t.Error("a site with no logo still renders an <img> for one, which resolves to 404 " +
			"and shows a broken image")
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
