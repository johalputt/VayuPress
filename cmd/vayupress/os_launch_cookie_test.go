// SPDX-License-Identifier: Apache-2.0

package main

// os_launch_cookie_test.go — an installed console on Android stays signed in.
//
// Reported from a Samsung phone, Edge and Brave, "Remember me" ticked: after the
// phone was switched off, the installed console opened on the login page.
// Chromium on Android treats an installed app's cold launch as a cross-site
// top-level navigation (Sec-Fetch-Site: cross-site) and withholds the
// SameSite=Strict session cookie from that request. The session was valid; the
// browser simply did not send it once.
//
// The launch marker (Lax) is sent on that launch, and the console answers with a
// same-origin reload that carries the Strict cookie. These tests pin both halves
// and the narrowness: one address, GET navigations, cross-site only — so the
// reload itself can never bounce again, and nothing else loses F-6's Strict
// posture.

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
)

func setCookies(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		out[c.Name] = c
	}
	return out
}

func TestTheLaunchMarkerTravelsWithTheSessionAndCarriesNothing(t *testing.T) {
	for _, remember := range []bool{true, false} {
		rec := httptest.NewRecorder()
		auth.SetSessionCookieRemember(rec, "secret-session-token", remember)
		c := setCookies(rec)
		s, m := c[auth.SessionCookie], c[auth.LaunchMarkerCookie]
		if s == nil || m == nil {
			t.Fatalf("remember=%v: cookies set = %v, want the session and its launch marker", remember, c)
		}
		if s.SameSite != http.SameSiteStrictMode {
			t.Errorf("the session cookie is no longer Strict (audit F-6): %v", s.SameSite)
		}
		if m.SameSite != http.SameSiteLaxMode {
			t.Errorf("the launch marker must be Lax, or it is withheld from the very launch it exists for: %v", m.SameSite)
		}
		if m.Value != "1" || strings.Contains(m.Value, "secret") {
			t.Errorf("the launch marker carries %q — it must be a fixed value that grants nothing", m.Value)
		}
		if !m.HttpOnly || !m.Secure {
			t.Errorf("launch marker HttpOnly=%v Secure=%v", m.HttpOnly, m.Secure)
		}
		if m.MaxAge != s.MaxAge {
			t.Errorf("remember=%v: marker Max-Age %d, session %d — the marker must live exactly as long", remember, m.MaxAge, s.MaxAge)
		}
	}

	rec := httptest.NewRecorder()
	auth.ClearSessionCookie(rec)
	c := setCookies(rec)
	for _, name := range []string{auth.SessionCookie, auth.LaunchMarkerCookie} {
		if c[name] == nil || c[name].MaxAge >= 0 {
			t.Errorf("sign-out did not expire %s — a signed-out phone would keep bouncing its launch", name)
		}
	}
}

// consoleGate is the production order for a console page: core middleware (CSP
// and its nonce), then the session gate.
func consoleGate() http.Handler {
	a := &App{}
	r := chi.NewRouter()
	r.Use(coreMiddleware()...)
	// Every method, so the POST seed below reaches the gate instead of chi's 405.
	r.With(a.requireSessionOrAPIKey).HandleFunc("/os/*", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // reached only when signed in
	})
	return r
}

func launchRequest(mut func(*http.Request)) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "https://johal.in/os/", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: auth.LaunchMarkerCookie, Value: "1"})
	if mut != nil {
		mut(req)
	}
	return req
}

var scriptNonce = regexp.MustCompile(`<script nonce="([^"]+)">location\.replace\(location\.href\);</script>`)

func TestAColdLaunchWithoutItsStrictCookieReloadsSameOrigin(t *testing.T) {
	rec := httptest.NewRecorder()
	consoleGate().ServeHTTP(rec, launchRequest(nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("launch → %d %s, want the reload page; the phone is shown the login page instead", rec.Code, rec.Header().Get("Location"))
	}
	m := scriptNonce.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no same-origin reload in the page: %s", rec.Body.String())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if m[1] == "" || !strings.Contains(csp, "'nonce-"+m[1]+"'") {
		t.Fatalf("the reload script's nonce %q is not in the page's CSP (%s) — the browser refuses to run it", m[1], csp)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the reload page is cacheable — a cached copy would bounce forever")
	}
}

// Each seed differs from the launch in exactly one way, and must get the normal
// signed-out answer. The first is the one that matters most: the reload itself.
func TestTheLaunchBounceIsThisNarrow(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*http.Request)
	}{
		{"the same-origin reload itself (no loop)", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-origin") }},
		{"a browser that never signed in", func(r *http.Request) {
			r.Header.Del("Cookie")
		}},
		{"a marker with another value", func(r *http.Request) {
			r.Header.Del("Cookie")
			r.AddCookie(&http.Cookie{Name: auth.LaunchMarkerCookie, Value: "0"})
		}},
		{"a session cookie that was sent (expired)", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "expired"})
		}},
		{"any other console page", func(r *http.Request) { r.URL.Path = "/os/posts" }},
		{"a subresource, not a navigation", func(r *http.Request) { r.Header.Set("Sec-Fetch-Mode", "no-cors") }},
		{"a typed address or bookmark (Strict is sent)", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "none") }},
		{"a POST", func(r *http.Request) { r.Method = http.MethodPost }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			consoleGate().ServeHTTP(rec, launchRequest(tc.mut))
			if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "location.replace") {
				t.Fatalf("→ %d with the reload page; the bounce must answer the cold launch and nothing else", rec.Code)
			}
		})
	}
}
