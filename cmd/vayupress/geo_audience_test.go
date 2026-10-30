// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/analytics"
)

func TestContinentName(t *testing.T) {
	cases := map[string]string{
		"US": "North America",
		"us": "North America",
		"IN": "Asia",
		"ID": "Asia",
		"DE": "Europe",
		"BR": "South America",
		"NG": "Africa",
		"AU": "Oceania",
		"ZZ": "", // unknown
		"":   "",
	}
	for code, want := range cases {
		if got := continentName(code); got != want {
			t.Errorf("continentName(%q) = %q, want %q", code, got, want)
		}
	}
}

// With countries and no region or city, the section shows the countries and
// the continents they make up (no proxy needed), then says once what is
// missing; how to get it is in How this works, not a box on the page.
func TestGeoSectionContinentsAndSetup(t *testing.T) {
	countries := []analytics.AudienceStat{
		{Label: "US", Count: 120},
		{Label: "IN", Count: 80},
		{Label: "DE", Count: 40},
		{Label: "BR", Count: 20},
	}
	out := osGeoSection(countries, nil, nil)
	assertCSPSafe(t, "osGeoSection", out)
	for _, want := range []string{
		">Countries<", ">Continents<", "North America", "Asia", "Europe", "South America",
		`<p class="table-empty">No region or city recorded yet.</p>`,
		"Add visitor location headers", "cf-region", "cf-ipcity",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("geo section missing %q", want)
		}
	}
	if strings.Contains(out, "vp-geo-setup") || strings.Contains(out, "<ol") {
		t.Error("the setup is a box of numbered steps again")
	}
}

// With no location at all, the section says so in one line, then how a
// location is recorded; no empty country and continent lists.
func TestGeoSectionEmpty(t *testing.T) {
	out := osGeoSection(nil, nil, nil)
	assertCSPSafe(t, "osGeoSection-empty", out)
	if !strings.HasPrefix(out, `<p class="table-empty">No location recorded yet.</p>`) || !strings.Contains(out, "cf-region") {
		t.Errorf("empty geo section = %s", out)
	}
	if strings.Contains(out, "Countries") || strings.Count(out, "table-empty") != 1 {
		t.Errorf("empty geo section says it more than once: %s", out)
	}
}

// TestGeoSectionWithRegions: when proxy headers supply regions/cities, they render
// as bar lists (not the setup card).
func TestGeoSectionWithRegions(t *testing.T) {
	countries := []analytics.AudienceStat{{Label: "US", Count: 100}}
	regions := []analytics.AudienceStat{{Label: "California", Count: 60}, {Label: "Texas", Count: 40}}
	cities := []analytics.AudienceStat{{Label: "San Jose", Count: 30}}
	out := osGeoSection(countries, regions, cities)
	assertCSPSafe(t, "osGeoSection-regions", out)
	for _, want := range []string{"California", "Texas", "San Jose"} {
		if !strings.Contains(out, want) {
			t.Errorf("geo section missing region/city %q", want)
		}
	}
}
