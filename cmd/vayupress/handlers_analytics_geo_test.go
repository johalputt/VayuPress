// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// trustLoopbackProxy installs the shipped default trusted-proxy set for the
// duration of a test: a local reverse proxy on loopback, which is what fronts
// an ordinary install.
func trustLoopbackProxy(t *testing.T) {
	t.Helper()
	prev := config.Cfg.TrustedProxies
	_, v4, _ := net.ParseCIDR("127.0.0.0/8")
	_, v6, _ := net.ParseCIDR("::1/128")
	config.Cfg.TrustedProxies = []*net.IPNet{v4, v6}
	t.Cleanup(func() { config.Cfg.TrustedProxies = prev })
}

// proxiedRequest is a request as it arrives from the local reverse proxy.
func proxiedRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	r := httptest.NewRequest("POST", path, nil)
	r.RemoteAddr = "127.0.0.1:52001"
	return r
}

func TestGeoFromHeaders(t *testing.T) {
	trustLoopbackProxy(t)

	t.Run("cloudflare", func(t *testing.T) {
		r := proxiedRequest(t, "/api/v1/analytics/collect")
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
		r := proxiedRequest(t, "/")
		r.Header.Set("X-Geo-Country", "IN")
		r.Header.Set("X-Geo-Region", "MH")
		g := geoFromHeaders(r)
		if g.Country != "IN" || g.Region != "MH" {
			t.Fatalf("got %+v", g)
		}
	})

	t.Run("placeholder and invalid dropped", func(t *testing.T) {
		for _, code := range []string{"XX", "T1", "USA", ""} {
			r := proxiedRequest(t, "/")
			if code != "" {
				r.Header.Set("CF-IPCountry", code)
			}
			if g := geoFromHeaders(r); g.Country != "" {
				t.Fatalf("code %q should be dropped, got %q", code, g.Country)
			}
		}
	})

	t.Run("no headers", func(t *testing.T) {
		r := proxiedRequest(t, "/")
		g := geoFromHeaders(r)
		if g.Country != "" || g.City != "" || g.Region != "" {
			t.Fatalf("expected empty geo, got %+v", g)
		}
	})
}

// A COUNTRY IS A CLAIM BY A PROXY ABOUT SOMEBODY ELSE.
//
// These headers were read from whoever connected. The endpoint that consumes
// them — POST /api/v1/analytics/collect — is public, unauthenticated, and on
// VayuShield's bypass list, so nothing else stood in the way either: a client
// could file its beacons under any country and the operator's audience report
// would show them there.
//
// That is worse than a wrong chart. An operator who refuses a country and then
// reads it at the top of their audience goes looking for a fault in the shield,
// which is a real place to lose an afternoon.
func TestACountryHeaderFromAnUntrustedPeerIsRefused(t *testing.T) {
	trustLoopbackProxy(t)

	// A direct connection from an arbitrary address — a bot posting beacons
	// straight at the origin, which is reachable whether or not a CDN fronts the
	// hostname.
	r := httptest.NewRequest("POST", "/api/v1/analytics/collect", nil)
	r.RemoteAddr = "198.51.100.9:41000"
	r.Header.Set("CF-IPCountry", "SG")
	r.Header.Set("CF-IPCity", "Singapore")
	r.Header.Set("CF-Region", "Central")

	g := geoFromHeaders(r)
	if g.Country == "SG" {
		t.Error("a client chose its own country by setting CF-IPCountry.\n\n" +
			"Whatever it declares lands in the audience report, so the report can be made to " +
			"show traffic from a country the operator has refused — which reads as the shield " +
			"failing rather than as a forged header.")
	}
	if g.City != "" || g.Region != "" {
		t.Errorf("city/region were taken from an untrusted peer: %+v", g)
	}
}

// The other half: every vendor header, not just Cloudflare's. A gate that
// covers one name and leaves six others reachable is not a gate — and picking
// the one being tested is exactly how that survives review.
func TestEveryVendorCountryHeaderIsGatedNotJustCloudflares(t *testing.T) {
	trustLoopbackProxy(t)

	for _, h := range []string{
		"CF-IPCountry", "CloudFront-Viewer-Country", "X-Vercel-IP-Country",
		"Fastly-Geo-Country", "X-Geo-Country", "X-Country", "X-AppEngine-Country",
	} {
		r := httptest.NewRequest("POST", "/api/v1/analytics/collect", nil)
		r.RemoteAddr = "203.0.113.44:9000"
		r.Header.Set(h, "SG")
		if got := geoFromHeaders(r).Country; got == "SG" {
			t.Errorf("%s was honoured from an untrusted peer (got %q)", h, got)
		}
	}
}

// And the fix must not cost an ordinary install its geography. The peer on a
// normal deployment is the local reverse proxy; if that stopped being trusted,
// every operator's audience report would go blank at the next update — a
// silent, total data loss dressed as a security fix.
func TestTheLocalReverseProxyStillSuppliesTheVisitorsCountry(t *testing.T) {
	trustLoopbackProxy(t)

	r := proxiedRequest(t, "/api/v1/analytics/collect")
	r.Header.Set("CF-IPCountry", "SG")
	if got := geoFromHeaders(r).Country; got != "SG" {
		t.Errorf("country from the local proxy = %q, want SG.\n\n"+
			"This is the shape every real install has, and the country its CDN reports is the "+
			"only accurate one available at the origin.", got)
	}
}
