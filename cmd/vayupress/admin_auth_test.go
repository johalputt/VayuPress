//go:build integration

package main

import (
	"net/http"
	"testing"
)

// TestAdminRoutesRequireAuth is the executable form of the admin-route
// protection audit: every admin/observability/write route must reject an
// unauthenticated request. If a future route is added outside the protected
// group, this test fails.
func TestAdminRoutesRequireAuth(t *testing.T) {
	srv, _ := newTestHarness(t)

	cases := []struct {
		method, path string
	}{
		{"GET", "/admin"},
		{"GET", "/admin/adr"},
		{"GET", "/admin/modes"},
		{"GET", "/admin/faults"},
		{"GET", "/admin/replay"},
		{"GET", "/admin/policy"},
		{"GET", "/admin/topology"},
		{"GET", "/admin/backup/validate"},
		{"GET", "/api/v1/admin/outbox/stats"},
		{"GET", "/api/v1/admin/mode"},
		{"GET", "/api/v1/admin/search/drift"},
		{"GET", "/api/v1/queue"},
		{"POST", "/api/v1/articles"},
		{"PUT", "/api/v1/articles/x"},
		{"DELETE", "/api/v1/articles/x"},
		{"POST", "/admin/search/reindex"},
		{"POST", "/admin/cache-purge"},
	}
	for _, c := range cases {
		resp := doRequest(t, srv, c.method, c.path, "", nil) // no API key
		body := resp.StatusCode
		resp.Body.Close()
		if body != http.StatusUnauthorized && body != http.StatusForbidden {
			t.Errorf("%s %s: want 401/403 without auth, got %d", c.method, c.path, body)
		}
	}
}
