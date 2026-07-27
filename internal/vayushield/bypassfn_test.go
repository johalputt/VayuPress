// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBypassFnIsConsulted covers the dynamic bypass hook. It exists for machine
// endpoints whose path is not a fixed prefix — the IndexNow verification file is
// named after the operator's rotatable key — and those callers can no more solve
// a browser challenge than an MCP client can. If isBypassed stopped consulting
// it, the file would be challenged and every IndexNow submission silently voided.
func TestBypassFnIsConsulted(t *testing.T) {
	m := &Manager{cfg: Config{
		BypassPrefixes: []string{"/os"},
		BypassFn: func(r *http.Request) bool {
			return r.URL.Path == "/dynamic-key.txt"
		},
	}}

	if !m.isBypassed(httptest.NewRequest(http.MethodGet, "/dynamic-key.txt", nil)) {
		t.Error("BypassFn returned true for this path but isBypassed did not honour it")
	}
	if m.isBypassed(httptest.NewRequest(http.MethodGet, "/other-key.txt", nil)) {
		t.Error("isBypassed must not bypass a path BypassFn rejected")
	}
	// The static prefixes must keep working alongside the hook.
	if !m.isBypassed(httptest.NewRequest(http.MethodGet, "/os/posts", nil)) {
		t.Error("BypassPrefixes must still apply when BypassFn is set")
	}
}

// TestBypassFnNilIsSafe: the hook is optional, so the zero Config must not panic
// and must not change any existing decision.
func TestBypassFnNilIsSafe(t *testing.T) {
	m := &Manager{cfg: Config{BypassPrefixes: []string{"/api"}}}
	if m.isBypassed(httptest.NewRequest(http.MethodGet, "/some-post", nil)) {
		t.Error("a normal page must stay shielded when BypassFn is nil")
	}
	if !m.isBypassed(httptest.NewRequest(http.MethodGet, "/api/posts", nil)) {
		t.Error("static prefixes must still work when BypassFn is nil")
	}
	if !m.isBypassed(httptest.NewRequest(http.MethodGet, "/robots.txt", nil)) {
		t.Error("feed-like paths must still be bypassed when BypassFn is nil")
	}
}
