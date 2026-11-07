// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConnectorEndpointAlwaysCarriesPath: every page that names VayuMCP's
// address has to match the code that builds it, where both the apex and the
// dedicated-host form end in /mcp; the mcp. subdomain is optional and ships off.
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
