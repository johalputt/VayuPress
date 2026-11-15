// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

const testPage = "<!doctype html><html><head><title>t</title></head><body><p>page</p></body></html>"

func storeSnippets(t *testing.T, a *App, sc settings.Scope, head, foot string) {
	t.Helper()
	if err := a.siteSettings.SetMany(context.Background(), sc, map[string]string{settings.KeySnippetHead: head, settings.KeySnippetFoot: foot}); err != nil {
		t.Fatal(err)
	}
}

// servedPage runs a request through the real header chain to a handler that
// writes page with the given status and headers.
func servedPage(a *App, path string, d *domain.Domain, status int, hdr map[string]string, page string) *httptest.ResponseRecorder {
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(page))
	})
	h := securityHeadersMiddleware(a.siteCSPMiddleware(final))
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if d != nil {
		req = req.WithContext(context.WithValue(req.Context(), ctxKeyDomain{}, *d))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var htmlType = map[string]string{"Content-Type": "text/html; charset=utf-8"}

func TestASitesCodeReachesItsPublicPagesAndNoStrictOne(t *testing.T) {
	a := servicesApp(t)
	storeSnippets(t, a, settings.ForPrimary(), "<script>alert(1)</script>", `<div id="chat"></div>`)

	rec := servedPage(a, "/a-post", nil, http.StatusOK, htmlType, testPage)
	body := rec.Body.String()
	if want := "<title>t</title><script>alert(1)</script></head>"; !strings.Contains(body, want) {
		t.Errorf("the head snippet is not at the end of <head>: %s", body)
	}
	if want := `<p>page</p><div id="chat"></div></body>`; !strings.Contains(body, want) {
		t.Errorf("the footer snippet is not at the end of <body>: %s", body)
	}
	// The site's policy is strict, and the snippet's script still runs: by
	// its hash, which is in the page's policy and nothing wider.
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'sha256-bhHHL3z2vDgxUt0W3dWQOrprscmda2Y5pLsLg4GF+pI='") {
		t.Errorf("the page's policy does not allow the snippet's script: %s", csp)
	}
	// One seed per kind of strict path: the console, a member page.
	for _, p := range []string{"/os/posts", "/members/login"} {
		rec := servedPage(a, p, nil, http.StatusOK, htmlType, testPage)
		if strings.Contains(rec.Body.String(), "alert(1)") || strings.Contains(rec.Header().Get("Content-Security-Policy"), "sha256-bhHH") {
			t.Errorf("%s carries a session and got the site's code", p)
		}
	}
}

func TestASitesCodeIsItsOwn(t *testing.T) {
	a := servicesApp(t)
	hosted := domain.Domain{ID: "d1", Host: "shop.example.com"}
	storeSnippets(t, a, settings.ForDomain(hosted.ID), "<script>alert(1)</script>", "")
	if rec := servedPage(a, "/", nil, http.StatusOK, htmlType, testPage); strings.Contains(rec.Body.String(), "alert(1)") {
		t.Error("a hosted site's code reached the primary's pages")
	}
	if rec := servedPage(a, "/", &hosted, http.StatusOK, htmlType, testPage); !strings.Contains(rec.Body.String(), "alert(1)") {
		t.Error("a hosted site's code is missing from its own pages")
	}
}

// Only the site's own successful HTML page is held and changed: not a
// challenge VayuShield answers with, not JSON, not a 304, not a body already
// encoded.
func TestOnlyASitesOwnPagesCarryItsCode(t *testing.T) {
	a := servicesApp(t)
	storeSnippets(t, a, settings.ForPrimary(), "", `<div id="chat"></div>`)
	for name, c := range map[string]struct {
		status int
		hdr    map[string]string
	}{
		"a challenge":      {http.StatusOK, map[string]string{"Content-Type": "text/html", "X-VayuShield": "challenge"}},
		"JSON":             {http.StatusOK, map[string]string{"Content-Type": "application/json"}},
		"a missing page":   {http.StatusNotFound, htmlType},
		"an encoded body":  {http.StatusOK, map[string]string{"Content-Type": "text/html", "Content-Encoding": "gzip"}},
		"a page unchanged": {http.StatusNotModified, htmlType},
	} {
		if rec := servedPage(a, "/", nil, c.status, c.hdr, testPage); strings.Contains(rec.Body.String(), "chat") {
			t.Errorf("%s got the site's code", name)
		}
	}
}

func TestSavingASitesCodeReadsItAsAPageWill(t *testing.T) {
	a := servicesApp(t)
	admin := &users.User{ID: "u1", Email: "a@example.com", Role: users.RoleAdmin}
	post := func(u *users.User, head, foot string) (*httptest.ResponseRecorder, map[string]any) {
		b, _ := json.Marshal(map[string]string{"head": head, "foot": foot})
		rec := httptest.NewRecorder()
		a.handleOSSnippetsSave(rec, withUser(httptest.NewRequest(http.MethodPost, "/os/api/website/snippets", strings.NewReader(string(b))), u))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec, out
	}
	if rec, _ := post(&users.User{ID: "u2", Role: users.RoleAuthor}, "<script>alert(1)</script>", ""); rec.Code != http.StatusForbidden {
		t.Errorf("an author saved a site's code: %d", rec.Code)
	}
	rec, _ := post(admin, `<button onclick="x()">x</button>`, "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "inline handler") {
		t.Errorf("an inline handler was saved: %d %s", rec.Code, rec.Body.String())
	}
	if got := a.siteSettings.Get(context.Background(), settings.ForPrimary(), settings.KeySnippetHead); got != "" {
		t.Errorf("a refused snippet was stored: %q", got)
	}
	// Saved, and the answer says the script from outside will not run until
	// its service is on.
	rec, out := post(admin, `<script async src="https://www.googletagmanager.com/gtag/js?id=G-X"></script>`, "")
	if rec.Code != http.StatusOK || !strings.Contains(out["detail"].(string), "https://www.googletagmanager.com is not allowed") {
		t.Errorf("an origin the site does not allow was not named: %d %v", rec.Code, out)
	}
	storePolicy(t, a, settings.ForPrimary(), render.SitePolicy{Mode: render.PolicyCustom, Services: []string{"google-analytics"}})
	if _, out := post(admin, `<script async src="https://www.googletagmanager.com/gtag/js?id=G-X"></script>`, ""); strings.Contains(out["detail"].(string), "not allowed") {
		t.Errorf("an origin the site allows was named as not allowed: %v", out)
	}
}
