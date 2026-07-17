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

// TestGeoSectionContinentsAndSetup: with countries but no proxy region/city data,
// Geography must still show a real continent breakdown (offline) and a precise
// setup card — and stay CSP-safe.
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
		"Continents", "North America", "Asia", "Europe", "South America",
		"vp-geo-scroll", // countries scroll container
		"vp-geo-setup",  // premium setup card
		"Add visitor location headers",
		"cf-region", "cf-ipcity",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("geo section missing %q", want)
		}
	}
}

// TestGeoSectionEmpty: with no geo data at all, still renders the setup card CSP-safe.
func TestGeoSectionEmpty(t *testing.T) {
	out := osGeoSection(nil, nil, nil)
	assertCSPSafe(t, "osGeoSection-empty", out)
	if !strings.Contains(out, "vp-geo-setup") {
		t.Error("empty geo section should show the setup card")
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
