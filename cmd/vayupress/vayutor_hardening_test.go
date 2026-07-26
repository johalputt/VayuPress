// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSecurityHeadersOnionAware verifies the onion hardening in
// securityHeadersMiddleware: a .onion request omits HSTS (meaningless over a
// plain-HTTP, self-authenticating address) and uses a no-referrer policy, while
// a clearnet request keeps HSTS and the standard referrer policy.
func TestSecurityHeadersOnionAware(t *testing.T) {
	h := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name       string
		host       string
		wantHSTS   bool
		wantRefPol string
	}{
		{"clearnet", "johal.in", true, "strict-origin-when-cross-origin"},
		{"onion", "f7ar4p3dbdezrdgj4zsaaolmxmvddq3rsmryf6zztltlgocliomn6qad.onion", false, "no-referrer"},
		{"onion with port", "abc.onion:80", false, "no-referrer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+c.host+"/some/post", nil)
			req.Host = c.host
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			gotHSTS := rec.Header().Get("Strict-Transport-Security") != ""
			if gotHSTS != c.wantHSTS {
				t.Errorf("HSTS present = %v, want %v", gotHSTS, c.wantHSTS)
			}
			if got := rec.Header().Get("Referrer-Policy"); got != c.wantRefPol {
				t.Errorf("Referrer-Policy = %q, want %q", got, c.wantRefPol)
			}
			// CSP is always set (and is onion-safe: no upgrade-insecure-requests).
			if rec.Header().Get("Content-Security-Policy") == "" && rec.Header().Get("Content-Security-Policy-Report-Only") == "" {
				t.Error("expected a CSP header on every response")
			}
		})
	}
}

func TestIsOnionHost(t *testing.T) {
	cases := map[string]bool{
		"abc.onion":        true,
		"ABC.ONION":        true,
		"abc.onion:80":     true,
		"johal.in":         false,
		"sub.johal.in":     false,
		"notonion.example": false,
	}
	for host, want := range cases {
		if got := isOnionHost(host); got != want {
			t.Errorf("isOnionHost(%q) = %v, want %v", host, got, want)
		}
	}
}
