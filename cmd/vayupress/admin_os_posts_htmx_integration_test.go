// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// TestOSPostToggleFragmentFlipsStatus drives the HTMX publish/unpublish endpoint
// end-to-end against a real SQLite DB: it flips a published post to draft and
// asserts both the persisted status and the returned fragment (flipped button +
// out-of-band Draft pill). Toggling to draft avoids the publish-only IndexNow
// ping, keeping the test hermetic.
func TestOSPostToggleFragmentFlipsStatus(t *testing.T) {
	_, _ = newTestHarness(t) // initialises config, DB and the CSRF secret

	const slug = "toggle-me"
	if _, err := dbpkg.DB.Exec(
		`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		slug, "Toggle Me", slug, "body", "", "published",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	a := &App{}
	form := url.Values{"status": {"draft"}}
	req := httptest.NewRequest(http.MethodPost, "/os/api/posts/"+slug+"/status-fragment", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	a.handleOSPostToggleFragment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `>Publish</button>`) {
		t.Errorf("expected flipped button to read Publish, got:\n%s", body)
	}
	if !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, "Draft") {
		t.Errorf("expected out-of-band Draft pill, got:\n%s", body)
	}

	var st string
	if err := dbpkg.DB.QueryRow(`SELECT status FROM articles WHERE slug=?`, slug).Scan(&st); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if st != "draft" {
		t.Errorf("persisted status = %q, want draft", st)
	}

	// A bad status is rejected before any DB write.
	bad := httptest.NewRecorder()
	breq := httptest.NewRequest(http.MethodPost, "/os/api/posts/"+slug+"/status-fragment", strings.NewReader("status=bogus"))
	breq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	brctx := chi.NewRouteContext()
	brctx.URLParams.Add("slug", slug)
	breq = breq.WithContext(context.WithValue(breq.Context(), chi.RouteCtxKey, brctx))
	a.handleOSPostToggleFragment(bad, breq)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("bad status = %d, want 400", bad.Code)
	}
}
