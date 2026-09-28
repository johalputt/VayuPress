// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// TestPagesSurfaceRendersWithoutStores guards the Pages surface the same way the
// Theme Studio is guarded: with no settings store and no DB (worst-case startup
// state) the handler must still render the page, its New page sheet and its
// script rather than panicking on a nil dereference.
func TestPagesSurfaceRendersWithoutStores(t *testing.T) {
	a := &App{} // siteSettings + DB intentionally nil

	req := httptest.NewRequest("GET", "/os/pages", nil)
	rec := httptest.NewRecorder()

	a.handleOSPages(rec, req) // must not panic

	if rec.Code != 200 {
		t.Fatalf("Pages status = %d, want 200 (must render without stores)", rec.Code)
	}
	body := rec.Body.String()
	for want, what := range map[string]string{
		`data-page-kind="list"`:                          "the List kind",
		`<dialog class="sa-sheet" id="page-new"`:         "the New page sheet",
		`data-sheet="page-new"`:                          "a button that opens it",
		"page-compose-input":                             "the title field",
		"page-compose-template":                          "the template choice",
		"admin-os-pages.js":                              "its controller script",
		`href="/os/settings/writing">Settings › Writing`: "the way to the contact form's address",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Pages is missing %s (%q)", what, want)
		}
	}
	// The contact form's address and auto-reply are Settings › Writing's now.
	if strings.Contains(body, "contact-email") || strings.Contains(body, "contact-autoreply") {
		t.Error("the contact form's settings are still on Pages")
	}
}

// TestPageTemplateSeed verifies each known template seeds non-empty starter
// content using only sanitiser-safe tags, and that blank/unknown collapse to the
// single-space empty document article validation requires.
func TestPageTemplateSeed(t *testing.T) {
	for _, tpl := range []string{"about", "contact", "faq"} {
		got := pageTemplateSeed(tpl)
		if strings.TrimSpace(got) == "" {
			t.Errorf("template %q seeded empty content", tpl)
		}
		if !strings.Contains(got, "<h2>") {
			t.Errorf("template %q missing a heading", tpl)
		}
		if strings.Contains(got, "<form") || strings.Contains(got, "<script") {
			t.Errorf("template %q contains markup the UGC sanitiser strips", tpl)
		}
	}
	if pageTemplateSeed("blank") != " " || pageTemplateSeed("nonsense") != " " {
		t.Error("blank/unknown templates must seed a single space")
	}
}

// TestPagesAreAListWithTheirControlsInTheInspector — each page is a row that
// names its panel, one panel shows at a time, and a page is put in the menu or
// the footer from its own panel, not from the row.
func TestPagesAreAListWithTheirControlsInTheInspector(t *testing.T) {
	openMigratedDB(t)
	seedListPost(t, "about", "About", "published", 0, 1, "2026-09-27 10:00:00")
	seedListPost(t, "privacy", "Privacy", "draft", 0, 1, "2026-09-26 10:00:00")
	seedListPost(t, "a-post", "A post", "published", 0, 0, "2026-09-28 10:00:00")
	rec := httptest.NewRecorder()
	(&App{siteSettings: settings.New(dbpkg.DB)}).handleOSPages(rec, httptest.NewRequest("GET", "/os/pages", nil))
	body := rec.Body.String()
	if n := strings.Count(body, "data-list-row"); n != 2 {
		t.Errorf("%d rows, want the two pages and no post", n)
	}
	for _, slug := range []string{"about", "privacy"} {
		if !strings.Contains(body, `data-list-panel="`+slug+`"`) || !strings.Contains(body, `data-list-panel-id="`+slug+`"`) {
			t.Errorf("%s has no row naming its panel, or no panel", slug)
		}
	}
	if !strings.Contains(body, `<div data-list-panel-id="about">`) || !strings.Contains(body, `<div data-list-panel-id="privacy" hidden>`) {
		t.Error("exactly the first page's panel shows on load")
	}
	rows := body[strings.Index(body, "<tbody>"):strings.Index(body, "</tbody>")]
	if strings.Contains(rows, "data-page-nav") || strings.Contains(rows, "data-page-footer") {
		t.Error("the menu and footer controls are in the rows; they belong to the inspector")
	}
	if strings.Count(body, "data-page-nav ") != 2 || strings.Count(body, "data-page-footer ") != 2 {
		t.Error("each page's panel has its menu toggle and footer choice")
	}
}
