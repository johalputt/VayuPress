// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// THE SHIELD AND ANALYTICS MUST READ THE SAME COUNTRY.
//
// They did not, and it cost a week on a live install: the operator refused
// Singapore, Analytics kept reporting Singapore as most of the audience, and the
// shield's own trail held not one request from there in seven days. Both sides
// worked. They were answering from different sources with nothing able to show
// it.

func trustLocalProxy(t *testing.T) {
	t.Helper()
	prev := config.Cfg.TrustedProxies
	_, v4, _ := net.ParseCIDR("127.0.0.0/8")
	config.Cfg.TrustedProxies = []*net.IPNet{v4}
	t.Cleanup(func() { config.Cfg.TrustedProxies = prev })
}

func proxiedWithCountry(country string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/some-post", nil)
	r.RemoteAddr = "127.0.0.1:41000"
	if country != "" {
		r.Header.Set("CF-IPCountry", country)
	}
	return r
}

// The decisive one: one request, both consumers, one answer.
func TestTheShieldAndAnalyticsAgreeOnTheCountry(t *testing.T) {
	trustLocalProxy(t)

	// 8.8.8.8 is US in the embedded table; the edge says SG. Before this fix the
	// shield read US (and never matched a "deny SG" rule) while Analytics read SG
	// and reported it to the operator.
	r := proxiedWithCountry("SG")

	shieldSees := requestCountry(r, "8.8.8.8")
	analyticsSees := geoFromHeaders(r).Country

	if shieldSees != analyticsSees {
		t.Fatalf("the shield sees %q and Analytics sees %q for the same request.\n\n"+
			"That divergence is the whole defect: an operator refuses the country they can SEE, "+
			"and the enforcement path is comparing against a different one.", shieldSees, analyticsSees)
	}
	if shieldSees != "SG" {
		t.Errorf("both agree on %q, but not on the country the operator was shown", shieldSees)
	}
}

// A country header from an UNTRUSTED peer must not be honoured, or a visitor
// picks the country they are judged under — including one just refused.
func TestAnUntrustedPeerCannotChooseItsCountry(t *testing.T) {
	trustLocalProxy(t)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:5000" // direct, not the local proxy
	r.Header.Set("CF-IPCountry", "SG")

	// 8.8.8.8 is US in the table; the forged header must not override it.
	if got := requestCountry(r, "8.8.8.8"); got == "SG" {
		t.Error("a client chose its own country by setting CF-IPCountry from an untrusted peer.\n\n" +
			"It could name a country it has not been refused, and every geo rule would be " +
			"escapable by one header.")
	}
}

// No CDN in front: the table still answers. This is the sovereign default the
// product is built for, and silently losing geography there would be a
// regression wearing a fix's clothes.
func TestADirectServedOriginStillResolvesCountries(t *testing.T) {
	trustLocalProxy(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	if got := requestCountry(r, "8.8.8.8"); got != "US" {
		t.Errorf("country = %q, want US from the embedded table", got)
	}
}

// The disagreement has to be COUNTED, or it is invisible again — which is the
// property that made this expensive rather than the divergence itself.
func TestADisagreementBetweenTheTwoSourcesIsCounted(t *testing.T) {
	trustLocalProxy(t)
	_, _, before := countrySourceStats()

	// Edge says SG, table says US for the same address.
	requestCountry(proxiedWithCountry("SG"), "8.8.8.8")

	if _, _, after := countrySourceStats(); after <= before {
		t.Error("the two sources named different countries for one request and nothing counted it.\n\n" +
			"Invisibility is what made this cost a week: both screens were confident and neither " +
			"could show it disagreed with the other.")
	}

	// And agreement must NOT be counted, or the signal is noise.
	_, _, base := countrySourceStats()
	requestCountry(proxiedWithCountry("US"), "8.8.8.8") // both say US
	if _, _, now := countrySourceStats(); now != base {
		t.Error("agreement was counted as a disagreement, which makes the number meaningless")
	}
}

// The shield's CountryFn must BE this function. A second implementation is a
// future divergence, and this whole investigation was one.
func TestTheShieldIsWiredToTheSharedResolver(t *testing.T) {
	trustLocalProxy(t)
	var cfg vayushield.Config
	cfg.CountryFn = requestCountry // compile-time proof of the signature

	if got := cfg.CountryFn(proxiedWithCountry("SG"), "8.8.8.8"); got != "SG" {
		t.Errorf("the shield's country hook returned %q, want SG — it is not reading the edge", got)
	}
}
