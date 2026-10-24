// SPDX-License-Identifier: Apache-2.0

package vayushield

import (
	"net/http/httptest"
	"testing"
)

// A path is exempt from the shield only when it is under an exempt prefix,
// or feed-shaped, both as it arrived and as path.Clean reads it: as it
// arrived alone, "/api/../private.html" skipped every gate and was served
// as /private.html by a handler that cleans its path.
func TestAnExemptionHoldsForBothReadingsOfAPath(t *testing.T) {
	m := New(Config{Enabled: true, BypassPrefixes: []string{"/api", "/os"}})
	for p, want := range map[string]bool{
		"/api/v1/posts": true, "/api": true, "/os/": true, "/feed": true, "/feed/": true, "/sitemap.xml": true,
		"/api/../private.html": false, "/os/../private.html": false, "/feed/../private.html": false,
		"/x/../api/v1/posts": false, "/private.html": false,
	} {
		if got := m.isBypassed(httptest.NewRequest("GET", "http://site.test"+p, nil)); got != want {
			t.Errorf("%s exempt %v, want %v", p, got, want)
		}
	}
}
