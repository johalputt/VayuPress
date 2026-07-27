// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
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

	epCard := osConnectorEndpointCard(endpoint, endpoint, false, "")
	assertCSPSafe(t, "osConnectorEndpointCard", epCard)
	if !strings.Contains(epCard, endpoint) {
		t.Errorf("endpoint card must show the endpoint URL %q", endpoint)
	}

	// The dedicated-host variant is the one an operator sees when mcp.<domain> is
	// provisioned, and it names both URLs — so it is the copy most at risk of
	// drifting outside the CSP contract.
	dedi := osConnectorEndpointCard("https://mcp.example.com/mcp", endpoint, true, "")
	assertCSPSafe(t, "osConnectorEndpointCard/dedicated", dedi)
	if !strings.Contains(dedi, "https://mcp.example.com/mcp") {
		t.Error("dedicated endpoint card must offer the dedicated URL")
	}
	if !strings.Contains(dedi, endpoint) {
		t.Error("dedicated endpoint card must explain which URL it replaced, or the difference reads as a bug")
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

// TestDedicatedMCPHostRefusesUnusableBases pins the cases where no dedicated
// host can exist, so the probe never fires for them.
//
// Each guard is load-bearing. A Tor Space must make no clearnet call at all, an
// IP literal or localhost has no subdomain to build, and a page already served on
// mcp.<domain> would otherwise probe mcp.mcp.<domain>. Without these the page
// would spend three seconds on a lookup that cannot succeed, on every render.
func TestDedicatedMCPHostRefusesUnusableBases(t *testing.T) {
	for _, host := range []string{
		"",                // nothing to build from
		"localhost",       // no public subdomain
		"localhost:8080",  // ditto, with a port
		"127.0.0.1",       // IP literal
		"5.189.133.235",   // ditto, public
		"mcp.example.com", // already the dedicated host
		"single",          // not a domain at all
	} {
		if got := dedicatedMCPHost(context.Background(), host); got != "" {
			t.Errorf("dedicatedMCPHost(%q) = %q, want \"\" (no probe should be attempted)", host, got)
		}
	}
}

// TestConnectorEndpointFallsBackToTheRequestHost — with no dedicated host
// reachable, the advertised endpoint must stay exactly what it has always been.
// Changing the URL an operator pastes into their client is not a safe default;
// it is only correct when the replacement is known to answer.
func TestConnectorEndpointFallsBackToTheRequestHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/os/connector", nil)
	req.Host = "localhost:8080"
	endpoint, apex, dedicated, _ := connectorEndpoint(req)
	if dedicated {
		t.Error("claimed a dedicated host for a localhost admin session")
	}
	if endpoint != apex {
		t.Errorf("endpoint %q != apex %q with no dedicated host", endpoint, apex)
	}
	if !strings.HasSuffix(endpoint, "/mcp") {
		t.Errorf("endpoint %q does not end in /mcp", endpoint)
	}
}

// TestProbeRejectsAProxyInterstitial is the regression test for a flaw that
// defeated this feature's entire purpose in its first release.
//
// The probe exists to avoid advertising an endpoint that a proxy will challenge.
// Its first version treated ANY completed HTTP response as proof of life — but a
// bot challenge IS a completed response: 403, HTML body, served by the proxy. So
// a proxied host would have been marked live and the page would have offered,
// with a confident green badge, exactly the URL that cannot work.
//
// "The request completed" and "the right server answered" are different
// questions. Only the second one is worth anything here.
func TestProbeRejectsAProxyInterstitial(t *testing.T) {
	challenge := func(h http.Header, code int) *http.Response {
		return &http.Response{StatusCode: code, Header: h}
	}

	// Cloudflare says so outright.
	cf := http.Header{}
	cf.Set("Cf-Mitigated", "challenge")
	cf.Set("Content-Type", "text/html; charset=UTF-8")
	if !looksLikeProxyInterstitial(challenge(cf, http.StatusForbidden)) {
		t.Error("a cf-mitigated challenge was not recognised as a proxy interstitial")
	}
	// A proxy that does not announce itself still returns HTML where a machine
	// endpoint never would.
	html := http.Header{}
	html.Set("Content-Type", "text/html")
	if !looksLikeProxyInterstitial(challenge(html, http.StatusOK)) {
		t.Error("an HTML 200 on a machine endpoint was not treated as an interstitial")
	}
	// The block/throttle codes a machine client can never work through.
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		if !looksLikeProxyInterstitial(challenge(http.Header{}, code)) {
			t.Errorf("HTTP %d was not treated as unusable for a machine client", code)
		}
	}

	// The two statuses that DO prove VayuPress answered must not be mistaken for
	// an interstitial, or the probe would never accept a healthy host.
	for code := range mcpProbeAccepts {
		if looksLikeProxyInterstitial(challenge(http.Header{}, code)) {
			t.Errorf("HTTP %d proves the app answered but was rejected as an interstitial", code)
		}
	}
	if !mcpProbeAccepts[http.StatusUnauthorized] || !mcpProbeAccepts[http.StatusMethodNotAllowed] {
		t.Error("the accept set must contain 401 (auth middleware) and 405 (POST-only route)")
	}
	// A bare 200 is NOT proof: /mcp is POST-only, so this server cannot produce
	// one for a GET. Something else did.
	if mcpProbeAccepts[http.StatusOK] {
		t.Error("200 accepted on a POST-only route — that answer did not come from this server")
	}
}

// TestBlockedHostIsReportedDistinctlyFromMissing — "not set up" and "set up but
// proxied" need opposite actions, so the page must not collapse them into one
// message. Getting this wrong sends an operator to provision a host that already
// exists, which is exactly the wrong hour of work.
func TestBlockedHostIsReportedDistinctlyFromMissing(t *testing.T) {
	apexOnly := osConnectorEndpointCard("https://example.com/mcp", "https://example.com/mcp", false, "")
	blocked := osConnectorEndpointCard("https://example.com/mcp", "https://example.com/mcp", false, "mcp.example.com")
	assertCSPSafe(t, "osConnectorEndpointCard/blocked", blocked)

	if !strings.Contains(blocked, "mcp.example.com") {
		t.Error("the blocked notice must name the host that is being blocked")
	}
	if !strings.Contains(blocked, "DNS only") {
		t.Error("the blocked notice must say what to change; naming a fault without the fix is half a message")
	}
	if blocked == apexOnly {
		t.Error("a blocked dedicated host renders identically to having none — the two need opposite actions")
	}
}
