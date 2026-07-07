package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestAdminOSWebsiteJSRouteServed guards the design-picker regression: the
// Website studio references /os/static/js/admin-os-website.js, but its serve
// route was never registered, so the script 404'd — no click handlers attached,
// design cards could not be selected, fields never hydrated, and Save did
// nothing. This asserts the route is registered and serves the studio script.
func TestAdminOSWebsiteJSRouteServed(t *testing.T) {
	a := &App{}
	r := chi.NewRouter()
	a.registerAdminOSUIRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/os/static/js/admin-os-website.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /os/static/js/admin-os-website.js = %d, want 200 (serve route not registered?)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("content-type = %q, want application/javascript", ct)
	}
	// It must be the studio script (the picker/hydration logic keys off this).
	if !strings.Contains(rec.Body.String(), "data-biz-template") {
		t.Error("served body is not the Website studio script")
	}
}
