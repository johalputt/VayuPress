package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func reqWithChiParam(target, key, val string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestStaticFontServed proves the self-hosted Space Grotesk woff2 is served
// same-origin with the right headers, is a valid woff2, and that only
// allowlisted filenames are ever served (defence against arbitrary reads).
func TestStaticFontServed(t *testing.T) {
	a := &App{}

	rec := httptest.NewRecorder()
	a.handleStaticFont(rec, reqWithChiParam("/static/fonts/space-grotesk-latin-700.woff2", "file", "space-grotesk-latin-700.woff2"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (font not embedded/served?)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "font/woff2" {
		t.Errorf("content-type = %q, want font/woff2", ct)
	}
	body := rec.Body.Bytes()
	if len(body) < 5000 || string(body[:4]) != "wOF2" {
		t.Errorf("served body is not a valid woff2 (len=%d, magic=%q)", len(body), string(body[:min(4, len(body))]))
	}

	for _, bad := range []string{"evil.woff2", "../../etc/passwd", "space-grotesk-latin-800.woff2"} {
		rec := httptest.NewRecorder()
		a.handleStaticFont(rec, reqWithChiParam("/static/fonts/x", "file", bad))
		if rec.Code != http.StatusNotFound {
			t.Errorf("non-allowlisted %q served with %d, want 404", bad, rec.Code)
		}
	}
}
