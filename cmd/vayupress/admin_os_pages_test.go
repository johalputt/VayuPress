package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPagesSurfaceRendersWithoutStores guards the Pages surface the same way the
// Theme Studio is guarded: with no settings store and no DB (worst-case startup
// state) the handler must still render the page shell and the quick-create box
// rather than panicking on a nil dereference.
func TestPagesSurfaceRendersWithoutStores(t *testing.T) {
	a := &App{} // siteSettings + DB intentionally nil

	req := httptest.NewRequest("GET", "/os/pages", nil)
	rec := httptest.NewRecorder()

	a.handleOSPages(rec, req) // must not panic

	if rec.Code != 200 {
		t.Fatalf("Pages status = %d, want 200 (must render without stores)", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "page-compose-input") {
		t.Error("Pages surface is missing the quick-create input")
	}
	if !strings.Contains(body, "admin-os-pages.js") {
		t.Error("Pages surface is missing its controller script")
	}
	if !strings.Contains(body, "page-compose-template") {
		t.Error("Pages surface is missing the template selector")
	}
	if !strings.Contains(body, "contact-email") {
		t.Error("Pages surface is missing the contact recipient field")
	}
	if !strings.Contains(body, "contact-autoreply") {
		t.Error("Pages surface is missing the auto-reply toggle")
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
