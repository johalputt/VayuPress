// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// TestReferrerHostDropsThePort pins the ingest half of the self-referral fix.
// A CDN in front of the site can serve on one of its alternate HTTP ports, and
// the browser then reports "johal.in:2052" as the referrer. That is the site
// itself, but it matched neither the exact-host nor the "%.domain" exclusion, so
// it was stored and displayed as an external referrer in the operator's own
// panel. A port never distinguishes one site from another for this purpose.
func TestReferrerHostDropsThePort(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://johal.in:2052/some-post", "johal.in"},
		{"https://www.johal.in:2052/", "www.johal.in"},
		{"https://johal.in/some-post", "johal.in"},
		{"http://example.com:8080/x?y=1", "example.com"},
		{"https://EXAMPLE.com:443/", "example.com"},
		{"https://[2001:db8::1]:8443/x", "2001:db8::1"},
		{"", ""},
		{"not a url", ""},
	} {
		if got := referrerHost(tc.in); got != tc.want {
			t.Errorf("referrerHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestReferrerHostExtendedDropsThePort holds the second ingest path to the same
// rule. The two extractors are written differently — one parses, one slices — so
// they are asserted separately rather than assumed to agree.
func TestReferrerHostExtendedDropsThePort(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://johal.in:2052/some-post", "johal.in"},
		{"https://www.johal.in:2052/", "www.johal.in"},
		{"https://johal.in/some-post", "johal.in"},
		{"http://example.com:8080/x?y=1", "example.com"},
		{"https://user:pass@example.com:8080/x", "example.com"},
		{"https://[2001:db8::1]:8443/x", "[2001:db8::1]"},
		{"", ""},
	} {
		if got := referrerHostExtended(tc.in); got != tc.want {
			t.Errorf("referrerHostExtended(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSelfHostPatternsCoverThePortForms pins the read half. Rows recorded before
// the ingest fix still carry ":port", so the exclusion has to catch them without
// a data migration.
//
// The domain is SET here rather than read from the environment. The first draft
// skipped when config.Cfg.Domain was empty — which it always is under `go test`
// — so the whole test was inert and passed against a mutation that emptied both
// port patterns. A test whose assertions never execute is not a weaker test, it
// is a false report of coverage.
func TestSelfHostPatternsCoverThePortForms(t *testing.T) {
	prev := config.Cfg.Domain
	t.Cleanup(func() { config.Cfg.Domain = prev })
	config.Cfg.Domain = "Johal.in" // mixed case: the helper must lower it

	host, sub, hostPort, subPort := selfHostPatterns(config.Cfg.Domain)
	if host != "johal.in" {
		t.Fatalf("host = %q, want %q (the helper must lower-case the domain)", host, "johal.in")
	}
	for _, tc := range []struct{ name, got, want string }{
		{"subdomain", sub, "%.johal.in"},
		{"host:port", hostPort, "johal.in:%"},
		{"subdomain:port", subPort, "%.johal.in:%"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s pattern = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// The patterns must actually match the strings observed in the field. SQL
	// LIKE is checked here in Go terms: '%' is a prefix/suffix wildcard.
	if !strings.HasPrefix("johal.in:2052", strings.TrimSuffix(hostPort, "%")) {
		t.Errorf("%q would not exclude the observed self-referral %q", hostPort, "johal.in:2052")
	}
	if !strings.Contains("www.johal.in:2052", strings.Trim(subPort, "%")) {
		t.Errorf("%q would not exclude the observed self-referral %q", subPort, "www.johal.in:2052")
	}
}
