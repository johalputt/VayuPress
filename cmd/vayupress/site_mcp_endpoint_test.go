// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
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
	b, err := os.ReadFile("../../docs/site/assets/app.js")
	if err != nil {
		t.Fatalf("read site app.js: %v", err)
	}
	src := string(b)

	// Every host: field on the site that mentions mcp must carry the path.
	hostRe := regexp.MustCompile(`host:'([^']*mcp[^']*)'`)
	found := 0
	for _, m := range hostRe.FindAllStringSubmatch(src, -1) {
		got := m[1]
		found++
		if !strings.Contains(got, "/mcp") {
			t.Errorf("site advertises %q as the VayuMCP address; the endpoint is always a /mcp path "+
				"(and the mcp. subdomain is optional, shipped off)", got)
		}
		if strings.HasPrefix(got, "mcp.") {
			t.Errorf("site leads with the optional dedicated host %q; the default is <your-domain>/mcp", got)
		}
	}
	if found == 0 {
		t.Fatal("no VayuMCP host: field found on the site — this guard has stopped guarding anything")
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
