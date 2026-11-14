// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

// The stylesheet a Halcyon page links is kept a year only when asked for by
// the version it is: a page drawn before the site's custom CSS changed asks
// for the old one, and what it gets must not outlive a minute.
func TestTheSiteStylesheetIsKeptAYearOnlyAtItsOwnVersion(t *testing.T) {
	prev := render.GetActiveSettings()
	t.Cleanup(func() { render.SetActiveSettings(prev) })
	render.SetActiveSettings(render.SiteSettings{Name: "Acme", CustomCSS: ".h-mark{color:red}"})
	_, ver := render.HalcyonSiteCSS(render.GetActiveSettings())
	a := &App{}

	for _, c := range []struct {
		v, cache string
	}{
		{ver, "public, immutable, max-age=31536000"},
		{"0000000000", "public, max-age=60"},
	} {
		w := httptest.NewRecorder()
		a.handleHalcyonSiteCSS(w, httptest.NewRequest("GET", "/static/css/halcyon-site.css?v="+c.v, nil))
		if got := w.Header().Get("Cache-Control"); got != c.cache {
			t.Errorf("asked for v=%s: Cache-Control %q, want %q", c.v, got, c.cache)
		}
		if got := w.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
			t.Errorf("asked for v=%s: Content-Type %q", c.v, got)
		}
		if !strings.HasSuffix(w.Body.String(), ".h-mark{color:red}") {
			t.Errorf("asked for v=%s: the site's custom CSS is not the stylesheet's end", c.v)
		}
	}
}
