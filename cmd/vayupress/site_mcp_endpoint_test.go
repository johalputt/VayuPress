// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSiteAdvertisesPathEndpointNotBareSubdomain guards a real drift caught in
// review: the marketing site advertised VayuMCP's address as
// "mcp.yourdomain.com" while every other surface said "<your-domain>/mcp".
//
// Both halves of that were wrong. The endpoint is ALWAYS a /mcp path —
// connectorEndpoint() returns publicMCPEndpoint() (host + "/mcp") by default and
// "https://mcp.<host>/mcp" when a dedicated host is provisioned, so even the
// subdomain form keeps the path. And the subdomain is OPTIONAL: it exists only
// for installs whose proxy cannot skip a bot challenge on the /mcp path, and the
// DNS record ships turned off. Naming it as the product's address told a reader
// they needed a subdomain they almost certainly do not.
//
// Nothing was functionally broken — no config anywhere carried a bare host — but
// the front page of the project contradicted its own documentation, which is how
// an operator ends up provisioning DNS they did not need.
//
// This pins the site copy specifically. It deliberately does NOT police bare
// "mcp.<domain>" everywhere: a DNS-record table is a list of hostnames, and
// "mcp.example.com" is exactly right there.
func TestSiteAdvertisesPathEndpointNotBareSubdomain(t *testing.T) {
	// What vayupress.com says: its sections and pages (docs/site) and the
	// words its templates draw (internal/docsite).
	var files []string
	for _, g := range []string{"../../docs/site/home/*.md", "../../docs/site/pages/*.md", "../../internal/docsite/templates/*.html", "../../internal/docsite/build.go"} {
		m, err := filepath.Glob(g)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 4 {
		t.Fatalf("found %d files of the site's copy; this guard has stopped reading the site", len(files))
	}
	// An address on mcp.<something>, and whether /mcp follows it.
	addr := regexp.MustCompile(`\bmcp\.[a-z0-9<>-]+(?:\.[a-z0-9<>-]+)+(/mcp)?`)
	bare := func(body string) []string {
		var out []string
		for _, m := range addr.FindAllStringSubmatch(body, -1) {
			if m[1] == "" {
				out = append(out, m[0])
			}
		}
		return out
	}
	if got := bare("reach it at mcp.yourdomain.com today"); len(got) != 1 {
		t.Fatalf("the matcher misses a bare subdomain (%v); this guard would pass anything", got)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range bare(string(b)) {
			t.Errorf("%s names %q as VayuMCP's address; the endpoint is always a /mcp path, and the mcp. subdomain is optional, shipped off", f, got)
		}
	}
}

// TestConnectorEndpointAlwaysCarriesPath is the claim the site copy has to match,
// asserted against the code that actually builds the URL: both the apex and the
// dedicated-host form end in /mcp.
func TestConnectorEndpointAlwaysCarriesPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/os/connector", nil)
	req.Host = "blog.example.com"
	endpoint, apex, _, _ := connectorEndpoint(req)

	for name, got := range map[string]string{"endpoint": endpoint, "apex": apex} {
		if !strings.HasSuffix(got, "/mcp") {
			t.Errorf("%s = %q, must end in /mcp", name, got)
		}
	}
}
