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
	"github.com/johalputt/vayupress/internal/settings"
)

// headersFor serves path on host for d through the real chain: the built-in
// headers, the resolved domain, then the site's own. handler may set its own.
func headersFor(a *App, d domain.Domain, host, path string, handler http.HandlerFunc) http.Header {
	if handler == nil {
		handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	}
	chain := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyDomain{}, d))
		a.siteCSPMiddleware(handler).ServeHTTP(w, r)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = host
	chain.ServeHTTP(rec, req)
	return rec.Header()
}

func setSiteHeaders(t *testing.T, a *App, id string, kv map[string]string) {
	t.Helper()
	if err := a.siteSettings.SetMany(context.Background(), settings.ForDomain(id), kv); err != nil {
		t.Fatal(err)
	}
}

var chosenHeaders = map[string]string{
	settings.KeySiteHSTS: "6m", settings.KeySiteReferrer: "same-origin",
	settings.KeySiteFrame: "sameorigin", settings.KeySiteAllowGeo: "self",
}

// A site's choices reach its public pages, framing included in the CSP.
func TestASitesChosenHeadersReachItsPages(t *testing.T) {
	a, d := shieldSitesApp(t)
	setSiteHeaders(t, a, d.ID, chosenHeaders)
	h := headersFor(a, d, "one.example", "/about", nil)
	for name, want := range map[string]string{
		"Strict-Transport-Security": "max-age=15552000",
		"Referrer-Policy":           "same-origin",
		"Permissions-Policy":        "camera=(), microphone=(), geolocation=(self), payment=()",
		"X-Frame-Options":           "SAMEORIGIN",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") || strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("the CSP does not let the site frame itself: %s", csp)
	}
	// A value that reached the store some other way than the form is not
	// sent: the header is the built-in one.
	setSiteHeaders(t, a, d.ID, map[string]string{settings.KeySiteReferrer: "unsafe-url"})
	if got := headersFor(a, d, "one.example", "/about", nil).Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("a stored value off the list was sent: Referrer-Policy %q", got)
	}
}

// Nowhere else: not the console, the API or a session page on that host, not
// the primary, and not an onion. Each answers exactly the built-in headers.
func TestASitesHeadersReachNothingButItsPublicPages(t *testing.T) {
	a, d := shieldSitesApp(t)
	builtin := func(host, path string, dom domain.Domain) http.Header {
		b := &App{}
		return headersFor(b, dom, host, path, nil)
	}
	setSiteHeaders(t, a, d.ID, chosenHeaders)
	primary, _ := a.domains.Primary(context.Background())
	setSiteHeaders(t, a, primary.ID, chosenHeaders)
	same := func(label string, got, want http.Header) {
		t.Helper()
		for _, name := range []string{"Strict-Transport-Security", "Referrer-Policy", "Permissions-Policy", "X-Frame-Options"} {
			if got.Get(name) != want.Get(name) {
				t.Errorf("%s: %s = %q, want the built-in %q", label, name, got.Get(name), want.Get(name))
			}
		}
		if g, w := got.Get("Content-Security-Policy"), want.Get("Content-Security-Policy"); !strings.Contains(g, "frame-ancestors 'none'") {
			t.Errorf("%s: framing opened (%s; built-in %s)", label, g, w)
		}
	}
	for _, p := range []string{"/os/settings", "/api/v1/articles", "/members/account", "/checkout"} {
		same(p, headersFor(a, d, "one.example", p, nil), builtin("one.example", p, d))
	}
	same("the primary", headersFor(a, primary, "example.test", "/about", nil), builtin("example.test", "/about", primary))
	onion := "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"
	got := headersFor(a, d, onion, "/about", nil)
	if got.Get("Strict-Transport-Security") != "" || got.Get("Referrer-Policy") != "no-referrer" || got.Get("X-Frame-Options") != "DENY" {
		t.Errorf("an onion took the site's headers: HSTS %q, Referrer %q, frame %q",
			got.Get("Strict-Transport-Security"), got.Get("Referrer-Policy"), got.Get("X-Frame-Options"))
	}
}

// A handler that refuses framing for its own page keeps the refusal.
func TestAPageThatRefusesFramingKeepsIt(t *testing.T) {
	a, d := shieldSitesApp(t)
	setSiteHeaders(t, a, d.ID, chosenHeaders)
	h := headersFor(a, d, "one.example", "/about", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.WriteHeader(http.StatusOK)
	})
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("a page's own DENY was opened to 'self': %s", csp)
	}
}

// Only a listed value is saved; the form shows each choice and the saved one.
func TestASitesHeaderSettingIsAChoiceNotText(t *testing.T) {
	a, d := shieldSitesApp(t)
	for k, v := range map[string]string{
		settings.KeySiteHSTS: "max-age=1; preload", settings.KeySiteReferrer: "unsafe-url",
		settings.KeySiteFrame: "allow-from https://evil.example", settings.KeySiteAllowCamera: "*",
	} {
		body, _ := json.Marshal(map[string]any{"domain_id": "c1", "settings": map[string]string{k: v}})
		if w := saveSiteShield(a, d, string(body)); w.Code != http.StatusBadRequest {
			t.Errorf("%s=%q saved with %d, want 400", k, v, w.Code)
		}
	}
	body, _ := json.Marshal(map[string]any{"domain_id": "c1", "settings": map[string]string{settings.KeySiteHSTS: "2y"}})
	if w := saveSiteShield(a, d, string(body)); w.Code != http.StatusOK {
		t.Fatalf("a listed choice answered %d", w.Code)
	}
	form := string(a.scopedShieldForm(context.Background(), d))
	if !strings.Contains(form, `<option value="2y" selected>`) || !strings.Contains(form, "until the list") {
		t.Error("the form does not show the saved choice, or HSTS's consequence")
	}
}
