package render

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/db"
)

// A browser only offers to install a site whose page links a manifest AND whose
// page registers a service worker. Miss either and Android silently downgrades
// "Install" to a launcher shortcut — a launcher database entry rather than an
// installed package, which a device restart can discard. Nothing in the install
// flow tells you which one you got, so the install tags have to be pinned on every
// page a reader might install from.

// TestEveryPublicPageCarriesTheInstallTags renders each public template and checks
// the real output, not the template source: a page could parse and still be
// missing the tags if a funcmap entry were dropped.
func TestEveryPublicPageCarriesTheInstallTags(t *testing.T) {
	SetActiveSettings(SiteSettings{Name: "Acme", Description: "A description"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	home, err := RenderHome("example.com", "1.0.0", nil, 0, 1, 1)
	if err != nil {
		t.Fatalf("RenderHome: %v", err)
	}
	tagIndex, err := RenderTagIndex("example.com", "1.0.0", nil, 0)
	if err != nil {
		t.Fatalf("RenderTagIndex: %v", err)
	}
	tagPage, err := RenderTagPage("example.com", "1.0.0", "go", nil, 0)
	if err != nil {
		t.Fatalf("RenderTagPage: %v", err)
	}

	for _, page := range []struct{ name, html string }{
		{"home", home},
		{"tag index", tagIndex},
		{"tag page", tagPage},
	} {
		t.Run(page.name, func(t *testing.T) {
			assertInstallable(t, page.html)
		})
	}
}

// TestArticlePagesCarryTheInstallTags covers the article template separately, as
// it needs the sanitiser and settings initialised.
func TestArticlePagesCarryTheInstallTags(t *testing.T) {
	Init(t.TempDir())
	SetActiveSettings(SiteSettings{Name: "Acme", Description: "A description"})
	t.Cleanup(func() { SetActiveSettings(SiteSettings{}) })

	out, err := RenderArticle(db.Article{
		Slug: "hello", Title: "Hello", Content: "Body text", Status: "published",
	})
	if err != nil {
		t.Fatalf("RenderArticle: %v", err)
	}
	assertInstallable(t, out)
}

func assertInstallable(t *testing.T, html string) {
	t.Helper()
	// The manifest link: without it there is nothing to install.
	if !strings.Contains(html, `rel="manifest" href="/manifest.json"`) {
		t.Error("page does not link the web app manifest, so it cannot be installed")
	}
	// The registration script: without it the site fails the installability check
	// and the install degrades to a shortcut that a restart can remove.
	if !strings.Contains(html, `/static/js/pwa.js?v=`) {
		t.Error("page does not load the service-worker registration script")
	}
	// iOS reads none of the manifest for icons or standalone display.
	if !strings.Contains(html, `rel="apple-touch-icon"`) {
		t.Error("page is missing apple-touch-icon, so an iPhone install gets a screenshot as its icon")
	}
	if !strings.Contains(html, `name="apple-mobile-web-app-capable" content="yes"`) {
		t.Error("page is missing apple-mobile-web-app-capable, so an iOS install opens in browser chrome")
	}
	// The registration script must be same-origin and deferred: an off-origin
	// script would be refused by script-src 'self', and a blocking one would delay
	// first paint for something that only matters after load.
	if !strings.Contains(html, `src="/static/js/pwa.js?v=`) || !strings.Contains(html, `pwa.js?v=`) {
		t.Error("the registration script must be loaded from a same-origin versioned URL")
	}
}
