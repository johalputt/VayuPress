// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// TestOSPostPinFragment drives the HTMX pin endpoint against a real DB: it pins
// an unpinned post and asserts the persisted featured flag plus the returned
// fragment (Unpin button + out-of-band pinned badge).
func TestOSPostPinFragment(t *testing.T) {
	_, _ = newTestHarness(t)

	const slug = "pin-me"
	if _, err := dbpkg.DB.Exec(
		`INSERT INTO articles(id,title,slug,content,tags,status,featured,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		slug, "Pin Me", slug, "body", "", "published", 0,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	a := &App{}
	req := httptest.NewRequest(http.MethodPost, "/os/api/posts/"+slug+"/pin-fragment", strings.NewReader("pinned=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	a.handleOSPostPinFragment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, ">Unpin</button>") {
		t.Errorf("expected flipped Unpin button:\n%s", body)
	}
	if !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, "📌 Pinned") {
		t.Errorf("expected out-of-band pinned badge:\n%s", body)
	}

	var featured int
	if err := dbpkg.DB.QueryRow(`SELECT featured FROM articles WHERE slug=?`, slug).Scan(&featured); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if featured != 1 {
		t.Errorf("persisted featured = %d, want 1", featured)
	}

	// A bad pinned value is rejected before any write.
	bad := httptest.NewRecorder()
	breq := httptest.NewRequest(http.MethodPost, "/os/api/posts/"+slug+"/pin-fragment", strings.NewReader("pinned=maybe"))
	breq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	brctx := chi.NewRouteContext()
	brctx.URLParams.Add("slug", slug)
	breq = breq.WithContext(context.WithValue(breq.Context(), chi.RouteCtxKey, brctx))
	a.handleOSPostPinFragment(bad, breq)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("bad pinned = %d, want 400", bad.Code)
	}
}
