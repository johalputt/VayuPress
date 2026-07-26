// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestFederatedAvatarNoOpenRedirect is the regression guard for CodeQL #61: the
// public /avatar/<hash> endpoint must NEVER redirect to a caller-supplied ?d= URL
// (that would be an open redirect / phishing primitive). A missing avatar returns
// 404 with no Location header, whatever ?d= claims.
func TestFederatedAvatarNoOpenRedirect(t *testing.T) {
	a := &App{} // no mail engine → the "no avatar" path, which used to honour ?d=

	for _, d := range []string{
		"https://evil.example/phish",
		"http://evil.example",
		"//evil.example",
		"/somewhere",
	} {
		req := httptest.NewRequest(http.MethodGet, "/avatar/deadbeefdeadbeefdeadbeefdeadbeef?d="+d, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("hash", "deadbeefdeadbeefdeadbeefdeadbeef")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()

		a.handleFederatedAvatar(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("d=%q: status = %d, want 404 (no redirect to a caller URL)", d, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Errorf("d=%q: must not redirect; got Location %q", d, loc)
		}
	}
}
