// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

func TestAPIKeyMayAccessDomain(t *testing.T) {
	scoped := httptest.NewRequest("GET", "/api/x", nil)
	scoped = RequestWithKeyInfo(scoped, apikeys.KeyInfo{ID: "k1", DomainID: "dom_a"})

	global := httptest.NewRequest("GET", "/api/x", nil)
	global = RequestWithKeyInfo(global, apikeys.KeyInfo{ID: "k2"})

	session := httptest.NewRequest("GET", "/api/x", nil) // no KeyInfo stamped

	cases := []struct {
		name     string
		r        *http.Request
		domainID string
		want     bool
	}{
		{"scoped key on its own domain", scoped, "dom_a", true},
		{"scoped key on another domain", scoped, "dom_b", false},
		{"global key anywhere", global, "dom_b", true},
		{"session operator anywhere", session, "dom_z", true},
	}
	for _, tc := range cases {
		if got := APIKeyMayAccessDomain(tc.r, tc.domainID); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
