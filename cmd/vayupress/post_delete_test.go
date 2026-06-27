package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestPostDeleteRequiresSlug proves the delete handler rejects an empty slug
// with a 400 rather than attempting a nil-DB query.
func TestPostDeleteRequiresSlug(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest("DELETE", "/os/api/posts/", nil)
	// Empty slug URL param.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	a.handleOSPostDelete(rec, req)

	if rec.Code != 400 {
		t.Fatalf("empty slug delete = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "slug is required") {
		t.Errorf("expected slug-required error, got: %s", rec.Body.String())
	}
}
