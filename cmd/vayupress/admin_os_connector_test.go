// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// TestConnectorManageCard proves each connector renders as its own panel with
// the facts that tell two clients apart, and the controls to govern them.
func TestConnectorManageCard(t *testing.T) {
	limited := apikeys.NewPermissions()
	limited.Grant(apikeys.SectionPosts, apikeys.ActionWrite)
	used := time.Now().Add(-2 * time.Hour)

	full := apikeys.Key{
		ID: "k-full", Label: "Claude (full control)", Prefix: "vp_full00",
		Scope: apikeys.ScopeExternal, Permissions: apikeys.Superuser(),
		Active: true, CreatedAt: time.Now().Add(-72 * time.Hour),
		LastUsedAt: &used, UseCount: 412,
	}
	paused := apikeys.Key{
		ID: "k-lim", Label: "Cline", Prefix: "vp_lim000",
		Scope: apikeys.ScopeExternal, Permissions: limited,
		Active: false, CreatedAt: time.Now().Add(-48 * time.Hour),
	}

	out := osConnectorManageCard([]apikeys.Key{full, paused})
	assertCSPSafe(t, "osConnectorManageCard", out)

	// Every governing control must be present, for the right key.
	for _, want := range []string{
		`data-cx-pause="k-full"`,  // an active connector can be paused
		`data-cx-resume="k-lim"`,  // a paused one can be resumed
		`data-revoke="k-full"`,    // permanent disconnect
		`data-cx-remove="k-full"`, // delete the record
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manage card missing control %q", want)
		}
	}
	// A paused key must NOT also offer Pause, or the panel contradicts itself.
	if strings.Contains(out, `data-cx-pause="k-lim"`) {
		t.Error("a paused connector is still offering Pause")
	}
	if strings.Contains(out, `data-cx-resume="k-full"`) {
		t.Error("an active connector is offering Resume")
	}

	// The usage facts are the whole point: without them every row looks alike and
	// an operator cannot tell which connector is which client.
	if !strings.Contains(out, "412") {
		t.Error("call count is not shown — it is the main way to tell two connectors apart")
	}
	if !strings.Contains(out, "Never used") {
		t.Error("a connector that never made a call must say so; those are the removable leftovers")
	}
	if !strings.Contains(out, "Full control") || !strings.Contains(out, "Limited") {
		t.Error("access level must be labelled per connector")
	}
	if !strings.Contains(out, "Paused") {
		t.Error("a paused connector must be visibly paused")
	}
	// The masked prefix may be shown; the secret never can be.
	if strings.Contains(out, "vp_full00") && !strings.Contains(out, apikeys.Mask("vp_full00")) {
		t.Error("an unmasked key prefix is rendered")
	}

	// Empty state is friendly, not blank.
	empty := osConnectorManageCard(nil)
	if !strings.Contains(empty, "No connectors yet") {
		t.Error("empty manage card should show a helpful empty state")
	}
}

// TestExpiredConnectorIsNotOfferedResume — an expired key is already refused by
// the auth cache, so a Resume button would be a control that does nothing. The
// operator would press it, see no change, and lose trust in the whole panel.
func TestExpiredConnectorIsNotOfferedResume(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	expired := apikeys.Key{
		ID: "k-exp", Label: "Old client", Prefix: "vp_exp000",
		Scope: apikeys.ScopeExternal, Permissions: apikeys.NewPermissions(),
		Active: true, CreatedAt: time.Now().Add(-96 * time.Hour), ExpiresAt: &past,
	}
	out := osConnectorManageCard([]apikeys.Key{expired})
	if strings.Contains(out, `data-cx-resume="k-exp"`) || strings.Contains(out, `data-cx-pause="k-exp"`) {
		t.Error("an expired connector offers a pause/resume control that cannot change anything")
	}
	if !strings.Contains(out, "Expired") {
		t.Error("an expired connector must be labelled expired, not paused")
	}
	// It must still be removable, or a dead entry can never be cleared.
	if !strings.Contains(out, `data-cx-remove="k-exp"`) {
		t.Error("an expired connector cannot be removed")
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

// TestProbeIdentifiesWhoAnswered is the regression test for two opposite
// mistakes in this check, both shipped.
//
// The first accepted any completed HTTP request as proof of life. A bot
// challenge IS a completed request — 403 with an HTML body — so a proxied host
// was reported healthy, the exact situation the probe exists to detect.
//
// The second over-corrected: it probed with GET and treated an HTML response as
// a proxy signature. There is no GET handler on /mcp, so the request falls
// through to r.Get("/{slug}") and renders the site's ordinary themed 404 — HTML,
// from VayuPress, on a perfectly healthy host. A correct install was reported as
// blocked, and the operator was told to change DNS that was already right.
//
// Neither status code nor content type says WHO answered. Only a response this
// application alone can produce does.
func TestProbeIdentifiesWhoAnswered(t *testing.T) {
	mk := func(code int, hdr map[string]string) *http.Response {
		h := http.Header{}
		for k, v := range hdr {
			h.Set(k, v)
		}
		return &http.Response{StatusCode: code, Header: h}
	}

	// The fingerprint: 401 + RFC 9728 resource metadata, from requireMCPAuth.
	ok := mk(http.StatusUnauthorized, map[string]string{
		"WWW-Authenticate": `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`,
	})
	if !isVayuMCPResponse(ok) {
		t.Error("the real VayuMCP 401 was not recognised — a healthy host would never be advertised")
	}
	if looksLikeProxyInterstitial(ok) {
		t.Error("the real VayuMCP 401 was mislabelled as a proxy interstitial")
	}

	// The regression: this site's themed 404 for an unmatched path. HTML, from
	// VayuPress, on a healthy host. It is not the MCP endpoint, but it is
	// emphatically NOT evidence of a proxy.
	themed404 := mk(http.StatusNotFound, map[string]string{"Content-Type": "text/html; charset=utf-8"})
	if isVayuMCPResponse(themed404) {
		t.Error("a themed 404 was accepted as the MCP endpoint")
	}
	if looksLikeProxyInterstitial(themed404) {
		t.Error("this site's own HTML 404 was reported as a proxy blocking the host — the false positive")
	}

	// A bare 401 without the fingerprint proves nothing: anything can return 401.
	if isVayuMCPResponse(mk(http.StatusUnauthorized, nil)) {
		t.Error("a 401 with no resource_metadata was accepted; that is not a fingerprint")
	}

	// Genuine proxy signals.
	cf := mk(http.StatusForbidden, map[string]string{"Cf-Mitigated": "challenge", "Content-Type": "text/html"})
	if isVayuMCPResponse(cf) || !looksLikeProxyInterstitial(cf) {
		t.Error("a cf-mitigated challenge was not recognised as a proxy interstitial")
	}
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		if !looksLikeProxyInterstitial(mk(code, nil)) {
			t.Errorf("HTTP %d was not treated as unusable for a machine client", code)
		}
	}
	// A 200 is not proof either: the endpoint answers 401 when unauthenticated.
	if isVayuMCPResponse(mk(http.StatusOK, nil)) {
		t.Error("a bare 200 was accepted; the endpoint cannot produce one for an unauthenticated call")
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

// TestConnectorStatsSurfaceFullControlKeys — this page mints superuser keys in
// one click, and a superuser key can run the entire site. How many exist is the
// one number an operator should not have to read down a table to find.
func TestConnectorStatsSurfaceFullControlKeys(t *testing.T) {
	limited := apikeys.NewPermissions()
	limited.Grant(apikeys.SectionPosts, apikeys.ActionWrite)
	keys := []apikeys.Key{
		{ID: "a", Permissions: apikeys.Superuser(), Active: true},
		{ID: "b", Permissions: limited, Active: true},
		{ID: "c", Permissions: apikeys.Superuser(), Active: true},
	}

	out := osConnectorStats("https://mcp.example.com/mcp", keys, true, "")
	assertCSPSafe(t, "osConnectorStats", out)
	if !strings.Contains(out, "Full-control keys") {
		t.Error("the stat strip does not surface how many superuser keys are live")
	}
	// Two superuser keys, and the tile must be marked for attention.
	if !strings.Contains(out, "stat-card--warn") {
		t.Error("full-control keys exist but nothing marks the tile as needing attention")
	}
	if !strings.Contains(out, "mcp.example.com") {
		t.Error("the strip must name the host actually being served")
	}
	if !strings.Contains(out, "Dedicated host") {
		t.Error("a dedicated endpoint host is not reflected in the strip")
	}

	// A blocked dedicated host is a warning state, distinct from serving on the
	// main domain by choice.
	blocked := osConnectorStats("https://example.com/mcp", nil, false, "mcp.example.com")
	if !strings.Contains(blocked, "blocked") && !strings.Contains(blocked, "Dedicated host blocked") {
		t.Error("a blocked dedicated host is not surfaced in the stat strip")
	}
	// No keys at all: nothing should be flagged.
	clean := osConnectorStats("https://example.com/mcp", nil, false, "")
	if strings.Contains(clean, "stat-card--warn") {
		t.Error("an install with no keys and no dedicated host is showing a warning")
	}
}

// TestStatStripCountsOnlyUsableConnectors — a paused connector is not active,
// and a paused full-control key is not a live grant. Counting either would put a
// number in the strip that the panels below it contradict.
func TestStatStripCountsOnlyUsableConnectors(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	keys := []apikeys.Key{
		{ID: "live", Permissions: apikeys.Superuser(), Active: true},
		{ID: "paused", Permissions: apikeys.Superuser(), Active: false},
		{ID: "expired", Permissions: apikeys.Superuser(), Active: true, ExpiresAt: &past},
	}
	out := osConnectorStats("https://example.com/mcp", keys, false, "")
	if !strings.Contains(out, ">1<") {
		t.Error("the strip does not report exactly one active connector out of three rows")
	}
	if !strings.Contains(out, "paused") {
		t.Error("connectors that are not counted as active should be accounted for, not silently dropped")
	}
}
