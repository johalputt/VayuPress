// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
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

// THE HALF THAT SHIPPED MISSING, reported from the operator's own install:
// "still after update no logo appear on this page".
//
// Three of that install's sites serve hand-built bundles that carry their own
// favicon, and serveFavicon has preferred a bundle's icon over the primary's
// since the day a live install reported the opposite. The console asked only the
// settings store, so it drew a generic globe for all three while the data sat on
// disk. Nothing was misconfigured and nothing needed uploading — the question
// simply never looked where the answer was.
func TestASiteWithNoUploadStillShowsTheLogoInsideItsOwnBundle(t *testing.T) {
	a := resetSessionApp(t)
	a.siteSettings = settings.New(dbpkg.DB)
	isolateBundleRoot(t)
	ctx := context.Background()

	// Shaped like the bundles this project ACTUALLY builds, which is the whole
	// point. The first version of this test put favicon.png at the bundle root
	// and passed, while the marketing site — built by scripts/build-selfhosted-
	// site.sh from docs/site/ — declares assets/favicon-32.png and has nothing at
	// its root at all. The fixture was more convenient than reality, so it proved
	// a convention no real bundle follows.
	bundled := seedSiteWithMark(t, a, "bundled.example", nil)
	deployBundleWithFiles(t, bundled.ID, map[string]string{
		"index.html": `<!doctype html><html><head>` +
			`<link rel="icon" type="image/png" sizes="32x32" href="assets/favicon-32.png" />` +
			`</head><body>b</body></html>`,
		"assets/favicon-32.png": "\x89PNG\r\n\x1a\ndeclared-icon",
	})

	// And one that ships a root favicon without declaring it, which must still
	// be found.
	rooted := seedSiteWithMark(t, a, "rooted.example", nil)
	deployBundleWithFiles(t, rooted.ID, map[string]string{
		"index.html":  "<!doctype html><title>r</title>",
		"favicon.ico": "root-icon",
	})

	// A bundle-backed site whose bundle carries NO icon.
	bare := seedSiteWithMark(t, a, "bare.example", nil)
	deployBundleWithFiles(t, bare.ID, map[string]string{
		"index.html": "<!doctype html><title>x</title>",
	})

	if !a.siteHasOwnMark(ctx, bundled.ID) {
		t.Error("a site whose bundle DECLARES its icon via <link rel=\"icon\"> is reported as\n" +
			"having no logo. This is the shape every bundle this project builds actually\n" +
			"uses, so a root-only lookup finds nothing for a site that plainly has an icon.")
	}
	if !a.siteHasOwnMark(ctx, rooted.ID) {
		t.Error("a bundle shipping /favicon.ico without declaring it is reported as having no " +
			"logo; the conventional name must still be found")
	}
	if a.siteHasOwnMark(ctx, bare.ID) {
		t.Error("a site whose bundle carries no icon is reported as having one — its card " +
			"would render an <img> that 404s")
	}
}

// customSiteDirFor falls back to the PRIMARY's bundle directory for a blank or
// non-hex id. That is the right trade where it lives — a wrong site rather than
// an escape — and exactly the wrong one here, where it would put the operator's
// own bundle icon on a client's card.
func TestABlankOrHostileIDNeverBorrowsThePrimarysBundleIcon(t *testing.T) {
	a := resetSessionApp(t)
	a.siteSettings = settings.New(dbpkg.DB)
	isolateBundleRoot(t)
	ctx := context.Background()

	// The PRIMARY's bundle has an icon. Nothing unidentified may inherit it.
	deployBundleWithFiles(t, "", map[string]string{
		"index.html":  "<!doctype html><title>primary</title>",
		"favicon.ico": "primary-icon-bytes",
	})

	for _, id := range []string{"", "   ", "../../etc", "NOTHEX", "zzzz"} {
		if a.siteHasOwnMark(ctx, id) {
			t.Errorf("id %q was reported as having its own mark; it would be served the "+
				"operator's bundle icon", id)
		}
		if _, ok := siteBundleDir(id); ok {
			t.Errorf("id %q resolved to a bundle directory", id)
		}
	}
}

// isolateBundleRoot redirects customSiteRoot() into this test's own temp
// directory for the duration of the test.
//
// Not optional hygiene. customSiteRoot() is filepath.Dir(config.Cfg.MediaDir) +
// "/custom-site", a real data path, and the first version of the fixture below
// deployed a bundle for the PRIMARY straight into it. That left index.html on
// disk after the run, so every LATER run — including one with these changes
// stashed — saw a deployed primary bundle, and a website-mode test that refuses
// "custom" for an undeployed site began accepting it. It passed alone and failed
// in the suite, and it kept failing after the process exited, which is what
// distinguishes writing outside t.TempDir() from ordinary test pollution.
func isolateBundleRoot(t *testing.T) {
	t.Helper()
	prev := config.Cfg.MediaDir
	config.Cfg.MediaDir = filepath.Join(t.TempDir(), "media")
	t.Cleanup(func() { config.Cfg.MediaDir = prev })
}

// deployBundleWithFiles writes a live bundle for one domain id ("" = primary)
// under the isolated root.
func deployBundleWithFiles(t *testing.T, id string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(customSiteDirFor(id), "current")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
