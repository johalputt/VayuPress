// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
)

// TestConnectorCardsCSPSafe renders every connector-page fragment and asserts
// they carry no inline style / unsafe-eval / external asset host (the VayuOS CSP
// contract), and that the one-click grant choices and connect snippets are all
// present.
func TestConnectorCardsCSPSafe(t *testing.T) {
	endpoint := "https://blog.example.com/mcp"

	intro := osConnectorIntro()
	assertCSPSafe(t, "osConnectorIntro", intro)

	epCard := osConnectorEndpointCard(endpoint)
	assertCSPSafe(t, "osConnectorEndpointCard", epCard)
	if !strings.Contains(epCard, endpoint) {
		t.Errorf("endpoint card must show the endpoint URL %q", endpoint)
	}

	grant := osConnectorGrantCard()
	assertCSPSafe(t, "osConnectorGrantCard", grant)
	// The headline one-click choice mints a superuser ("*:*") key.
	if !strings.Contains(grant, `data-mint="*:*"`) {
		t.Error("grant card missing the full-control (*:*) one-click button")
	}
	// The limited presets must never silently include a wildcard.
	for _, preset := range []string{`data-mint="posts:read,posts:write"`, `data-mint="posts:read,analytics:read"`} {
		if !strings.Contains(grant, preset) {
			t.Errorf("grant card missing limited preset %q", preset)
		}
	}
	if !strings.Contains(grant, `href="/os/apikeys"`) {
		t.Error("grant card should link to the API Keys page for custom grants")
	}

	connect := osConnectorConnectCard(endpoint)
	assertCSPSafe(t, "osConnectorConnectCard", connect)
	for _, want := range []string{"mcpServers", endpoint, "claude mcp add", "Authorization"} {
		if !strings.Contains(connect, want) {
			t.Errorf("connect card missing %q", want)
		}
	}
}

// TestConnectorEndpointNotDoubleEscaped guards the fix for the review finding:
// a Host with an HTML-special char must be encoded exactly once in the Claude
// Desktop config, identically to the CLI snippet — never double-encoded.
func TestConnectorEndpointNotDoubleEscaped(t *testing.T) {
	out := osConnectorConnectCard("https://a&b.example.com/mcp")
	if strings.Contains(out, "&amp;amp;") {
		t.Error("endpoint is double HTML-escaped in the connect card (expected single encoding)")
	}
	if !strings.Contains(out, "https://a&amp;b.example.com/mcp") {
		t.Error("endpoint should appear single-encoded (&amp;) in the rendered snippet")
	}
}

// TestConnectorManageCard proves the live-connectors table renders a revoke
// control per key and labels full-control vs. limited access correctly.
func TestConnectorManageCard(t *testing.T) {
	full := apikeys.Key{ID: "k-full", Label: "Claude (full control)", Prefix: "vp_full00", Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser()}
	limited := apikeys.NewPermissions()
	limited.Grant(apikeys.SectionPosts, apikeys.ActionWrite)
	lim := apikeys.Key{ID: "k-lim", Label: "Claude (author)", Prefix: "vp_lim000", Scope: apikeys.ScopeExternal, Permissions: limited}

	out := osConnectorManageCard([]apikeys.Key{full, lim})
	assertCSPSafe(t, "osConnectorManageCard", out)

	if !strings.Contains(out, `data-revoke="k-full"`) || !strings.Contains(out, `data-revoke="k-lim"`) {
		t.Error("manage card must offer a revoke control for every listed key")
	}
	if !strings.Contains(out, "Full control") {
		t.Error("a superuser key must be labelled 'Full control'")
	}
	if !strings.Contains(out, "Limited") {
		t.Error("a scoped key must be labelled 'Limited'")
	}

	// Empty state is friendly, not blank.
	empty := osConnectorManageCard(nil)
	if !strings.Contains(empty, "No active connector keys") {
		t.Error("empty manage card should show a helpful empty state")
	}
}

// TestPublicMCPEndpoint proves scheme resolution: proxy header wins, then direct
// TLS is https, and a plain-HTTP dev request degrades to http — never guessing
// https for a clearnet run.
func TestPublicMCPEndpoint(t *testing.T) {
	// Direct TLS → https.
	tls := httptest.NewRequest(http.MethodGet, "https://a.example.com/os/connector", nil)
	if got := publicMCPEndpoint(tls); got != "https://a.example.com/mcp" {
		t.Errorf("direct TLS: got %q", got)
	}
	// Behind a TLS-terminating proxy (request itself is plain HTTP) → https.
	proxied := httptest.NewRequest(http.MethodGet, "http://b.example.com/os/connector", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if got := publicMCPEndpoint(proxied); got != "https://b.example.com/mcp" {
		t.Errorf("proxied https: got %q", got)
	}
	// First forwarded hop wins when the header lists several.
	multi := httptest.NewRequest(http.MethodGet, "http://c.example.com/os/connector", nil)
	multi.Header.Set("X-Forwarded-Proto", "https, http")
	if got := publicMCPEndpoint(multi); got != "https://c.example.com/mcp" {
		t.Errorf("multi-hop XFP: got %q", got)
	}
	// Plain clearnet dev run (no TLS, no proxy header) → http, not a guessed https.
	dev := httptest.NewRequest(http.MethodGet, "http://localhost:8080/os/connector", nil)
	if got := publicMCPEndpoint(dev); got != "http://localhost:8080/mcp" {
		t.Errorf("clearnet dev: got %q", got)
	}
}
