//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/comments"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/metrics"
)

// TestOSCommentModerateFragment drives the HTMX moderation endpoint against a
// real DB: it approves a pending comment and asserts the persisted status plus
// the returned fragment (action buttons without Approve, out-of-band approved
// pill, and out-of-band pending/approved counts).
func TestOSCommentModerateFragment(t *testing.T) {
	_, _ = newTestHarness(t) // initialises config, DB and the CSRF secret

	store := comments.New(dbpkg.DB)
	const id = "abcdef0123456789abcdef01"
	if _, err := dbpkg.DB.Exec(
		`INSERT INTO comments(id,article_id,author,email,body,status) VALUES(?,?,?,?,?,?)`,
		id, "art-1", "Reader", "r@example.com", "nice post", "pending",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	before := atomic.LoadInt64(&metrics.MetricCommentsModerated)
	a := &App{commentStore: store}
	req := httptest.NewRequest(http.MethodPost, "/os/api/comments/"+id+"/status-fragment", strings.NewReader("status=approved"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	a.handleOSCommentModerateFragment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, ">Approve</button>") {
		t.Errorf("approved comment must not re-offer Approve:\n%s", body)
	}
	for _, want := range []string{
		`data-status="approved"`, `hx-swap-oob="true"`,
		`id="cpill-` + id + `"`, `id="cc-pending"`, `id="cc-approved"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fragment missing %q in:\n%s", want, body)
		}
	}

	var st string
	if err := dbpkg.DB.QueryRow(`SELECT status FROM comments WHERE id=?`, id).Scan(&st); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if st != "approved" {
		t.Errorf("persisted status = %q, want approved", st)
	}
	if atomic.LoadInt64(&metrics.MetricCommentsModerated) <= before {
		t.Error("MetricCommentsModerated did not increment")
	}

	// Invalid status is rejected before any store call.
	bad := httptest.NewRecorder()
	breq := httptest.NewRequest(http.MethodPost, "/os/api/comments/"+id+"/status-fragment", strings.NewReader("status=bogus"))
	breq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	brctx := chi.NewRouteContext()
	brctx.URLParams.Add("id", id)
	breq = breq.WithContext(context.WithValue(breq.Context(), chi.RouteCtxKey, brctx))
	a.handleOSCommentModerateFragment(bad, breq)
	if bad.Code != http.StatusBadRequest {
		t.Errorf("bad status = %d, want 400", bad.Code)
	}

	// A malicious id (non-hex) is rejected before any reflection (XSS guard).
	xss := httptest.NewRecorder()
	xreq := httptest.NewRequest(http.MethodPost, "/os/api/comments/x/status-fragment", strings.NewReader("status=approved"))
	xreq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	xrctx := chi.NewRouteContext()
	xrctx.URLParams.Add("id", `abc"><script>alert(1)</script>`)
	xreq = xreq.WithContext(context.WithValue(xreq.Context(), chi.RouteCtxKey, xrctx))
	a.handleOSCommentModerateFragment(xss, xreq)
	if xss.Code != http.StatusBadRequest {
		t.Errorf("malicious id got %d, want 400", xss.Code)
	}
	if strings.Contains(xss.Body.String(), "<script>") {
		t.Errorf("response reflected the malicious id: %s", xss.Body.String())
	}
}
