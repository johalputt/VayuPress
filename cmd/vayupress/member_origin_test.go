// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
)

// A reader's cookie is SameSite=Lax: another site's page cannot use it, but a
// page on another origin of the SAME site can, and a hosted domain such as
// shop.example.com, whose owner uploads its own scripts, is same-site with
// example.com. These writes take no CSRF token, so the browser's Origin is
// the check: a write from another host is refused for the reason that it came
// from another host; one from the site itself, or with no Origin (an app, not
// a browser), goes through.
func TestAMemberWriteFromAnotherOriginIsRefused(t *testing.T) {
	openMigratedDB(t)
	a := appWithMailAccounts(t)
	a.members = members.New(dbpkg.DB)
	m, err := a.members.UpsertScoped(t.Context(), "", "reader@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.members.CreateSession(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	a.registerRoutes(router, t.TempDir())

	send := func(method, path, origin string, i int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader("name=Mallory"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.AddCookie(&http.Cookie{Name: memberCookie, Value: token})
		req.RemoteAddr = "198.51.100." + string(rune('1'+i%9)) + ":4000"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	// Behind a proxy that sends its upstream as the Host, the host the browser
	// asked for comes as X-Forwarded-Host, and the site's own write is its own.
	proxied := httptest.NewRequest("POST", "http://127.0.0.1:8080/members/account", strings.NewReader("name=Ada"))
	proxied.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxied.Header.Set("Origin", "http://localhost:8088")
	proxied.Header.Set("X-Forwarded-Host", "localhost:8088")
	proxied.AddCookie(&http.Cookie{Name: memberCookie, Value: token})
	prec := httptest.NewRecorder()
	router.ServeHTTP(prec, proxied)
	if strings.Contains(prec.Body.String(), "cross-origin") {
		t.Error("the site's own write through a proxy was refused as cross-origin")
	}
	routes := [][2]string{
		{"POST", "/members/account"}, {"POST", "/members/logout"},
		{"POST", "/api/v1/members/avatar"}, {"POST", "/api/v1/members/avatar/choose"},
		{"POST", "/api/v1/members/mailbox/claim"}, {"POST", "/api/v1/members/mailbox/premium/checkout"},
		{"POST", "/api/v1/members/mailbox/premium/activate"}, {"POST", "/api/v1/members/ads"},
		{"POST", "/api/v1/members/totp/begin"}, {"POST", "/api/v1/members/totp/verify"}, {"POST", "/api/v1/members/totp/disable"},
		{"POST", "/api/v1/articles/a-post/comments"}, {"PATCH", "/api/v1/articles/a-post/comments/1"}, {"DELETE", "/api/v1/articles/a-post/comments/1"},
		{"POST", "/checkout"}, {"POST", "/checkout/post/a-post"},
	}
	// The write that started this: a reader renamed from another origin.
	send("POST", "/members/account", "https://shop.example.com", 0)
	if got, _ := a.members.Get(t.Context(), "reader@example.com"); got == nil || got.Name == "Mallory" {
		t.Error("another origin renamed the reader")
	}
	for i, rt := range routes {
		rec := send(rt[0], rt[1], "https://shop.example.com", i)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cross-origin") {
			t.Errorf("%s %s from another origin: %d %.80q, want 403 cross-origin", rt[0], rt[1], rec.Code, rec.Body.String())
		}
		for _, origin := range []string{"http://example.com", ""} {
			if rec := send(rt[0], rt[1], origin, i); strings.Contains(rec.Body.String(), "cross-origin") {
				t.Errorf("%s %s with Origin %q was refused as cross-origin", rt[0], rt[1], origin)
			}
		}
	}
}
