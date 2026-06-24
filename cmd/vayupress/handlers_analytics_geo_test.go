package main

import (
	"net/http/httptest"
	"testing"
)

func TestGeoFromHeaders(t *testing.T) {
	t.Parallel()

	t.Run("cloudflare", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/api/v1/analytics/collect", nil)
		r.Header.Set("CF-IPCountry", "us") // lowercase -> normalised to US
		r.Header.Set("CF-IPCity", "Austin")
		g := geoFromHeaders(r)
		if g.Country != "US" {
			t.Fatalf("country: got %q want US", g.Country)
		}
		if g.City != "Austin" {
			t.Fatalf("city: got %q want Austin", g.City)
		}
	})

	t.Run("generic x-geo", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("X-Geo-Country", "IN")
		r.Header.Set("X-Geo-Region", "MH")
		g := geoFromHeaders(r)
		if g.Country != "IN" || g.Region != "MH" {
			t.Fatalf("got %+v", g)
		}
	})

	t.Run("placeholder and invalid dropped", func(t *testing.T) {
		for _, code := range []string{"XX", "T1", "USA", ""} {
			r := httptest.NewRequest("POST", "/", nil)
			if code != "" {
				r.Header.Set("CF-IPCountry", code)
			}
			if g := geoFromHeaders(r); g.Country != "" {
				t.Fatalf("code %q should be dropped, got %q", code, g.Country)
			}
		}
	})

	t.Run("no headers", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", nil)
		g := geoFromHeaders(r)
		if g.Country != "" || g.City != "" || g.Region != "" {
			t.Fatalf("expected empty geo, got %+v", g)
		}
	})
}
