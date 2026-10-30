// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	vasession "github.com/johalputt/vayupress/internal/vayuanalytics/session"
)

// A shade is the country's readers on a logarithmic scale against the
// busiest: one reader is seen beside thousands, none is unshaded.
func TestAShadeIsReadersOnALogScale(t *testing.T) {
	for _, c := range []struct{ n, max, want int }{{0, 9, 0}, {1, 1, 5}, {1, 1000, 1}, {1000, 1000, 5}, {31, 1000, 3}} {
		if got := mapLevel(c.n, c.max); got != c.want {
			t.Errorf("mapLevel(%d, %d) = %d, want %d", c.n, c.max, got, c.want)
		}
	}
}

// Audience's map shades the countries readers were in, each with its card
// and the way into its readers, and puts a dot on a placed city, where the
// projection puts it; the drawing is hidden from a screen reader, which has
// the lists. Live's map is drawn empty for its script.
func TestTheMapShowsWhereReadersAre(t *testing.T) {
	openMigratedDB(t)
	st := analytics.New(dbpkg.DB)
	st.UseVisits(vasession.NewMasterHasher([]byte("map")))
	for _, r := range []struct {
		ip  string
		geo analytics.GeoInfo
	}{
		{"198.51.100.1", analytics.GeoInfo{Country: "IN", City: "Pune", Lat: 18.5, Lon: 73.9, Located: true}},
		{"198.51.100.2", analytics.GeoInfo{Country: "IN"}},
		{"198.51.100.3", analytics.GeoInfo{Country: "US"}},
	} {
		if err := st.Collect(context.Background(), analytics.CollectRequest{URL: "/a", Hostname: "example.test", EventType: 1, Lang: "en", Geo: r.geo},
			r.ip, "Mozilla/5.0 (Windows NT 10.0) Chrome/130.0", ""); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{analytics: st}
	page := a.renderSiteAnalyticsBody(context.Background(), primaryView, "example.test", 7, "audience")
	for _, want := range []string{
		`<svg class="sa-map__svg" viewBox="0 0 1000 445" aria-hidden="true"`,
		`data-code="IN" data-name="India" data-level="5" data-n="2 visitors" data-detail="67% of visitors`,
		`data-href="/os/analytics?days=7&amp;f=country%3Ais%3AIN"`,
		`data-code="US" data-name="United States of America" data-level="3" data-n="1 visitor"`,
		`<circle class="sa-map__dot" cx="700.2" cy="174.8"`, // Pune: Equal Earth, worked by hand
		`data-name="Pune, India" data-n="1 visitor" data-href="/os/analytics?days=7&amp;f=city%3Ais%3APune"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the map has no %q", want)
		}
	}
	if strings.Contains(page, `data-code="DE" data-name="Germany" data-level`) {
		t.Error("a country no reader was in is shaded")
	}
	live := a.renderSiteAnalyticsBody(context.Background(), primaryView, "example.test", 7, "live")
	if !strings.Contains(live, `<div class="sa-map" data-map data-live-map>`) {
		t.Error("Live has no map for its script to fill")
	}
	w := httptest.NewRecorder()
	a.handleOSScopedRealtime(w, httptest.NewRequest(http.MethodGet, "/os/analytics/realtime", nil))
	if body := w.Body.String(); !strings.Contains(body, `"x": 700.2`) || !strings.Contains(body, `"located": true`) || !strings.Contains(body, `"countries": {`) {
		t.Errorf("the live panel does not place its cities or say what each country reads:\n%s", body)
	}
}
