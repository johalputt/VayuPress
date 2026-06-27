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
}
