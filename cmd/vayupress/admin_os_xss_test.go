// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// postFragReq builds a POST request carrying a chi {slug} param and a form body.
func postFragReq(slug, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/os/api/posts/x/"+"frag", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", slug)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestPostFragmentsRejectMaliciousSlug verifies the HTMX post fragment endpoints
// reject a slug that isn't a well-formed slug BEFORE any DB access or reflection,
// closing the reflected-XSS vector (the slug is otherwise echoed into the
// returned fragment).
func TestPostFragmentsRejectMaliciousSlug(t *testing.T) {
	a := &App{}
	bad := `x"><img src=x onerror=alert(1)>`
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		body string
	}{
		{"status", a.handleOSPostToggleFragment, "status=draft"},
		{"pin", a.handleOSPostPinFragment, "pinned=1"},
	} {
		rec := httptest.NewRecorder()
		tc.h(rec, postFragReq(bad, tc.body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: malicious slug got %d, want 400", tc.name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<img") {
			t.Errorf("%s: response reflected the malicious slug: %s", tc.name, rec.Body.String())
		}
	}
}

// TestCommentStateNeverReflectsTheStatus — a comment's state is drawn from a
// fixed word per known status, so nothing of a status that reaches it is echoed
// (reflected-XSS regression guard), and an unknown one claims no known state.
func TestCommentStateNeverReflectsTheStatus(t *testing.T) {
	out := osCommentState("abc123", `x"><script>alert(1)</script>`, "c", true)
	if strings.Contains(out, "script") || strings.Contains(out, `x"`) {
		t.Errorf("osCommentState reflected a raw status: %s", out)
	}
	for _, claim := range []string{"Approved", "Waiting", "Rejected", "Spam"} {
		if strings.Contains(out, claim) {
			t.Errorf("an unknown status reads as %q: %s", claim, out)
		}
	}
	if canonicalCommentStatus("bogus") != "" || canonicalCommentStatus("approved") != "approved" {
		t.Error("canonicalCommentStatus must map only the known enum")
	}
}
