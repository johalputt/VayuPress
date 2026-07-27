// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_connector.go — the VayuOS VayuMCP page (ADR-0139, Stage 2).
//
// This is the one-click front door for connecting Claude (or any MCP client) to
// this VayuPress site. It does not introduce any new backend surface: minting and
// revoking keys reuse the already-CSRF-protected API-key endpoints
// (/os/api/apikeys/create, /os/api/apikeys/revoke). The page's only job is to make
// the choice obvious — "Grant full control" (a superuser key) vs. a limited preset
// — and to hand back a ready-to-paste connector configuration for the endpoint at
// POST /mcp. All enforcement still lives in the scoped-key model; a connector is
// exactly as powerful as the key minted here, never more.

import (
	"context"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
)

// iconConnector is the sidebar/plug glyph for the VayuMCP page.
var iconConnector = svgIcon("M7 10.5V7a3 3 0 016 0v3.5M5.5 10.5h9l-.7 5A2 2 0 0111.8 17H8.2a2 2 0 01-2-1.5l-.7-5zM10 17v1.5")

// publicMCPEndpoint returns the absolute URL of this site's MCP connector
// endpoint as seen by the operator's browser — scheme + Host + /mcp. Scheme
// resolution: a terminating proxy's X-Forwarded-Proto wins (first hop); else a
// direct TLS connection is https; else (plain HTTP with no proxy — a local/dev
// run) http. Production always terminates TLS or sets X-Forwarded-Proto, so the
// advertised URL is https there and never a downgraded link. Host comes from the
// request so it is correct for whichever domain the operator is administering.
func publicMCPEndpoint(r *http.Request) string {
	scheme := "https"
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		if i := strings.IndexByte(fp, ','); i >= 0 { // first hop wins
			fp = fp[:i]
		}
		scheme = strings.TrimSpace(fp)
	} else if r.TLS == nil {
		// No proxy header and no direct TLS: likely a local/dev clearnet run.
		scheme = "http"
	}
	host := r.Host
	if host == "" {
		host = "your-domain.com"
	}
	return scheme + "://" + host + "/mcp"
}

// ── Dedicated connector host ─────────────────────────────────────────────────
//
// The endpoint above is derived from the host the OPERATOR'S BROWSER is on,
// which is almost always the apex — and the apex is the one host most likely to
// sit behind a proxy that challenges machine clients. So the page handed out the
// single URL most likely to fail, while the dedicated mcp.<domain> host that
// cannot be challenged was described only in prose, far below the copy box
// everyone actually uses.
//
// That is not a documentation problem. An MCP client is machine-to-machine: it
// has no browser and cannot answer an interactive challenge, so a proxy rule
// added months later silently kills a connector that worked the day before, and
// the request never reaches this server to be logged. When the dedicated host is
// genuinely provisioned and answering, it is strictly better in every case —
// same server, same auth, same VayuShield screening, minus the one failure mode
// nobody can diagnose from here. Advertise that one.

const mcpDedicatedTTL = 5 * time.Minute

var mcpDedicated struct {
	mu   sync.Mutex
	seen map[string]mcpDedicatedEntry
}

type mcpDedicatedEntry struct {
	checkedAt time.Time
	live      bool
	// challenged records that SOMETHING answered but it was not VayuPress —
	// almost always a proxy interstitial. Kept separate from "not live" because
	// the two need opposite advice: one host needs provisioning, the other needs
	// its proxy switched off, and telling an operator the wrong one costs an
	// evening.
	challenged bool
}

// mcpProbeFingerprint is the token requireMCPAuth puts in its WWW-Authenticate
// header (RFC 9728 resource metadata). Seeing it back is proof that THIS
// application's MCP endpoint answered, and nothing in front of it.
const mcpProbeFingerprint = "resource_metadata"

// isVayuMCPResponse reports whether a probe response came from VayuMCP itself.
//
// TWO EARLIER VERSIONS OF THIS CHECK WERE WRONG, in opposite directions, and
// both are worth recording because the same trap catches every "is it up?" test.
//
// The first accepted any completed HTTP request. But a bot challenge IS a
// completed request — 403 with an HTML body — so a proxied host was reported
// healthy, which is the exact situation the probe exists to detect.
//
// The second over-corrected: it probed with GET and treated an HTML response as
// proof of a proxy. Both halves were wrong. There is no GET handler on /mcp, so
// on this router the request falls through to r.Get("/{slug}") and renders the
// site's ordinary themed 404 — HTML, from VayuPress, on a perfectly healthy
// host. A correctly configured install was reported as blocked.
//
// The lesson is that neither status code nor content type identifies WHO
// answered. Only a response that this application alone can produce does. An
// unauthenticated POST to /mcp gets 401 plus a WWW-Authenticate header carrying
// RFC 9728 resource metadata, emitted by requireMCPAuth and by nothing else on
// the path. That is a fingerprint, not a guess.
func isVayuMCPResponse(resp *http.Response) bool {
	return resp.StatusCode == http.StatusUnauthorized &&
		strings.Contains(resp.Header.Get("Www-Authenticate"), mcpProbeFingerprint)
}

// looksLikeProxyInterstitial reports whether a response came from something in
// front of the origin rather than the origin itself.
//
// Deliberately NARROW. Content type is not a signal here: VayuPress serves HTML
// for any unmatched path, so treating HTML as a proxy signature mislabels a
// healthy install. Only signals a proxy uniquely produces qualify — Cloudflare
// naming its own mitigation, or the refuse/throttle codes this endpoint never
// returns for an unauthenticated call (it answers 401).
//
// Under-claiming is the right failure here: a host that is not positively
// identified is simply not advertised, and a wrong "blocked" badge sends an
// operator to change DNS that was already correct.
func looksLikeProxyInterstitial(resp *http.Response) bool {
	if resp.Header.Get("Cf-Mitigated") != "" {
		return true
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	}
	return false
}

// dedicatedMCPHost returns "mcp.<host>" when that host is actually provisioned
// and answering over TLS, else "".
//
// It PROBES rather than infers. A DNS record pointing here proves nothing about
// whether the certificate was ever issued, and advertising an endpoint whose TLS
// fails would trade one broken connector for another.
//
// "Live" means THIS SERVER answered — see isVayuMCPResponse. A response arriving is
// not enough on its own, because the failure being detected produces a perfectly
// valid response of its own.
func dedicatedMCPHost(ctx context.Context, adminHost string) string {
	// A Tor Space must make no clearnet call, and a .onion has no proxy in front
	// of it to work around, so there is nothing to gain and a leak to lose.
	if safefetch.ClearnetBlocked() {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(adminHost))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// Already on the dedicated host, or nothing usable to build one from.
	if host == "" || strings.HasPrefix(host, "mcp.") || strings.Count(host, ".") < 1 {
		return ""
	}
	if net.ParseIP(host) != nil || host == "localhost" {
		return ""
	}
	cand := "mcp." + host

	mcpDedicated.mu.Lock()
	if mcpDedicated.seen == nil {
		mcpDedicated.seen = map[string]mcpDedicatedEntry{}
	}
	if e, ok := mcpDedicated.seen[cand]; ok && time.Since(e.checkedAt) < mcpDedicatedTTL {
		mcpDedicated.mu.Unlock()
		if e.live {
			return cand
		}
		return ""
	}
	mcpDedicated.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	live, challenged := false, false
	// SafeTransport, not the default client: this is a server-side outbound call
	// to an operator-supplied name, so it goes through the same SSRF guard as
	// every other one — private and reserved destinations refused, the validated
	// IP pinned at dial time — and honours the Tor-Space kill switch. Redirects
	// are never followed: a redirect off this host proves nothing about this host,
	// and following one would hand an operator-controlled name a second hop.
	client := &http.Client{
		Transport: safefetch.SafeTransport(safefetch.TransportOptions{DialTimeout: 2 * time.Second}),
		Timeout:   3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// The REAL path and the REAL method. A skip rule that exempts /health but not
	// /mcp would make a /health probe report success for an endpoint that is still
	// challenged, and a GET on /mcp does not reach the MCP handler at all — it
	// falls through to the article route. Only an unauthenticated POST exercises
	// the path a client actually uses.
	//
	// No credentials are sent and no side effect is possible: requireMCPAuth
	// rejects the request before any tool handler runs.
	if req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+cand+"/mcp", strings.NewReader("{}")); err == nil {
		req.Header.Set("Content-Type", "application/json")
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close()
			switch {
			case isVayuMCPResponse(resp):
				live = true
			case looksLikeProxyInterstitial(resp):
				challenged = true
			}
		}
	}

	mcpDedicated.mu.Lock()
	mcpDedicated.seen[cand] = mcpDedicatedEntry{checkedAt: time.Now(), live: live, challenged: challenged}
	mcpDedicated.mu.Unlock()
	if live {
		return cand
	}
	return ""
}

// dedicatedMCPChallenged reports whether the last probe of mcp.<host> was
// answered by something other than VayuPress. Read from cache only — it never
// probes, so rendering the warning cannot cost a second round trip.
func dedicatedMCPChallenged(adminHost string) (host string, challenged bool) {
	h := strings.ToLower(strings.TrimSpace(adminHost))
	if x, _, err := net.SplitHostPort(h); err == nil {
		h = x
	}
	if h == "" || strings.HasPrefix(h, "mcp.") {
		return "", false
	}
	cand := "mcp." + h
	mcpDedicated.mu.Lock()
	defer mcpDedicated.mu.Unlock()
	e, ok := mcpDedicated.seen[cand]
	return cand, ok && e.challenged
}

// connectorEndpoint returns the URL the page should advertise, preferring a
// provisioned dedicated host, plus the plain request-derived one for comparison.
// blockedHost names a dedicated host that exists but is answered by a proxy, so
// the page can say which of the two very different problems this install has.
func connectorEndpoint(r *http.Request) (endpoint, apex string, dedicated bool, blockedHost string) {
	apex = publicMCPEndpoint(r)
	if h := dedicatedMCPHost(r.Context(), r.Host); h != "" {
		return "https://" + h + "/mcp", apex, true, ""
	}
	if h, challenged := dedicatedMCPChallenged(r.Host); challenged {
		return apex, apex, false, h
	}
	return apex, apex, false, ""
}

// handleOSConnector renders the VayuMCP page.
func (a *App) handleOSConnector(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	endpoint, apexEndpoint, dedicated, blockedHost := connectorEndpoint(r)

	// Existing external keys, so the operator can see and revoke connectors they
	// already granted without leaving the page. The internal/system key and
	// revoked keys are filtered out — this list is "live connectors you granted".
	var connectors []apikeys.Key
	if a.apiKeys != nil {
		all, _ := a.apiKeys.List(r.Context())
		for _, k := range all {
			if k.Scope == apikeys.ScopeInternal || k.Revoked {
				continue
			}
			connectors = append(connectors, k)
		}
	}

	body := osConnectorIntro() +
		osConnectorStats(endpoint, connectors, dedicated, blockedHost) +
		`<div class="section-head"><span class="section-head__title">Your connector endpoint</span><span class="section-head__hint">The single URL every MCP client connects to</span></div>` +
		osConnectorEndpointCard(endpoint, apexEndpoint, dedicated, blockedHost) +
		`<div class="section-head"><span class="section-head__title">Grant access</span><span class="section-head__hint">A connector is exactly as powerful as the key you give it</span></div>` +
		osConnectorGrantCard() +
		osConnectorConnectCard(endpoint) +
		`<div class="section-head"><span class="section-head__title">Active connectors</span><span class="section-head__hint">Revoking one disconnects that client immediately</span></div>` +
		osConnectorManageCard(connectors)

	full := adminOSShellHead(nonce, "VayuMCP", "connector", cfg) +
		body +
		adminOSShellFoot(nonce, osConnectorScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

func osConnectorIntro() string {
	return `<div class="page-header">
  <h1>VayuMCP</h1>
  <div class="page-actions">
    <a class="btn btn--sm" href="/docs/compatibility/mcp" target="_blank" rel="noopener">Connector docs</a>
    <span id="cx-status" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<p class="text-sm muted mb-4"><strong>VayuMCP</strong> is a built-in <strong>Model Context Protocol</strong> connector: it lets an AI client connect directly to this site and run it with a native set of tools — publish posts, build pages, read analytics, search content, and more. <strong>Claude and Claude Code connect in one click</strong>, but claude.ai is just one client — <strong>any MCP client works the same way</strong> (Claude Desktop, Claude Code, Cursor, or anything that speaks MCP over HTTP + OAuth). What a client can do is decided <strong>entirely by the key you grant below</strong>: a full-control key lets it run the whole site; a limited key exposes only what you allow. It is simply your API, spoken in MCP.</p>

<div id="cx-token-banner" class="card ak-token-banner" hidden>
  <div class="settings-block-title">Copy your new connector key now</div>
  <p class="text-sm muted">This is the only time the full key is shown. Paste it into the connector configuration below (it has been filled in for you), then store it somewhere safe — you won't be able to see it again.</p>
  <div class="ak-token-row">
    <input id="cx-token-value" class="input font-mono ak-token-input" type="text" readonly>
    <button type="button" class="btn btn--sm" id="cx-token-copy">Copy key</button>
    <button type="button" class="btn btn--primary btn--sm" id="cx-token-done">Done</button>
  </div>
</div>`
}

// osConnectorStats is the at-a-glance strip, matching the Monetization and
// Domains & DNS pages: what is granted, and whether the endpoint is on the host
// that cannot be challenged.
//
// Full-control keys get their own tile deliberately. A superuser key can run the
// whole site, and this page hands them out in one click — an operator should be
// able to see how many exist without reading down a table.
func osConnectorStats(endpoint string, keys []apikeys.Key, dedicated bool, blockedHost string) string {
	// "Active" must mean active. Counting paused and expired connectors here would
	// put a number on the page that the panels underneath contradict — and a
	// full-control key that is paused is not a live grant, so folding it into the
	// warning count would overstate exposure too.
	full, live, idle := 0, 0, 0
	now := time.Now()
	for _, k := range keys {
		usable := k.Active && (k.ExpiresAt == nil || k.ExpiresAt.After(now))
		if usable {
			live++
			if k.Permissions.IsSuperuser() {
				full++
			}
		} else {
			idle++
		}
	}
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/mcp")

	hostLabel, hostTone := "Main domain", ""
	switch {
	case dedicated:
		hostLabel = "Dedicated host"
	case blockedHost != "":
		hostLabel, hostTone = "Dedicated host blocked", "warn"
	}
	fullTone := ""
	if full > 0 {
		fullTone = "warn"
	}
	tile := func(value, label, tone string) string {
		cls := "stat-card"
		if tone != "" {
			cls += " stat-card--" + tone
		}
		return `<div class="` + cls + `"><div class="stat-card__label">` + html.EscapeString(label) +
			`</div><div class="stat-card__value">` + html.EscapeString(value) + `</div></div>`
	}
	activeLabel := "Active connectors"
	if idle > 0 {
		activeLabel += " · " + strconv.Itoa(idle) + " paused"
	}
	return `<div class="stat-grid">` +
		tile(strconv.Itoa(live), activeLabel, "") +
		tile(strconv.Itoa(full), "Full-control keys", fullTone) +
		tile(host, "Serving on", "") +
		tile(hostLabel, "Endpoint host", hostTone) +
		`</div>`
}

func osConnectorEndpointCard(endpoint, apex string, dedicated bool, blockedHost string) string {
	e := html.EscapeString(endpoint)
	note := ""
	if dedicated {
		// Say WHY this differs from the address in the browser bar. An endpoint
		// that silently disagrees with the site you are administering reads as a
		// mistake, and an operator who "corrects" it back to the apex walks
		// straight into the failure this is here to avoid.
		note = `<p class="text-sm muted mb-4"><span class="badge badge--ok">dedicated host</span> This install has a working <code>` +
			html.EscapeString(strings.TrimSuffix(strings.TrimPrefix(endpoint, "https://"), "/mcp")) +
			`</code> host, so that is the endpoint offered above rather than <code>` + html.EscapeString(apex) +
			`</code>. Both reach this same server with the same authentication, but the dedicated host is not proxied — so a bot challenge or firewall rule on your main domain can never sit in front of it. An MCP client has no browser and cannot answer a challenge, and when one appears the request never reaches this server to be logged, which makes it very hard to diagnose from here.</p>`
	} else if blockedHost != "" {
		// The diagnosis an operator otherwise has to reach with curl and a header
		// dump: the dedicated host EXISTS, and something in front of it answered
		// instead of this server. Naming that distinctly matters — "not set up" and
		// "set up but proxied" need opposite actions.
		note = `<p class="text-sm muted mb-4"><span class="badge badge--warn">blocked</span> <code>` +
			html.EscapeString(blockedHost) + `</code> exists, but a request to it was answered by <strong>something in front of this server</strong> — a bot challenge or firewall interstitial, not VayuPress. A machine client cannot answer one, so that host is unusable until it is switched to <strong>DNS only</strong> (the grey cloud in Cloudflare) at your DNS provider. Nothing needs changing here; the endpoint above stays on your main domain until the dedicated host answers directly.</p>`
	} else {
		note = `<p class="text-sm muted mb-4">If your domain sits behind a proxy or firewall that can challenge visitors, this endpoint can stop working without warning — an MCP client has no browser and cannot answer a challenge. A dedicated <code>mcp.&lt;your-domain&gt;</code> host with the proxy switched off avoids that permanently; see the note below. Once it is pointed and provisioned, this page offers it here automatically.</p>`
	}
	return `<div class="card">
  <div class="settings-block-title">Your connector endpoint</div>
  <p class="text-sm muted mb-4">This is the single URL an MCP client (Claude, Claude Code, or any other) connects to. It is served by VayuPress itself — no extra service, no extra port. Requests are authenticated with the key you grant below.</p>
  ` + note + `
  <div class="ak-token-row">
    <input id="cx-endpoint" class="input font-mono ak-token-input" type="text" readonly value="` + e + `">
    <button type="button" class="btn btn--sm" data-copy="#cx-endpoint">Copy</button>
  </div>
</div>`
}

// osConnectorGrantCard renders the two one-click choices: full control (a
// superuser key) or a limited preset. Both mint through the existing scoped-key
// create endpoint; the preset buttons carry their capability set in data-caps.
func osConnectorGrantCard() string {
	return `<div class="card">
  <p class="text-sm muted mb-4">Pick how much control to hand over to the connected client. You can revoke any grant instantly below, and every action the client takes is written to the audit log.</p>

  <div class="cx-grant-grid">
    <div class="cx-grant cx-grant--full">
      <div class="cx-grant-head">
        <span class="settings-row-label">Full control</span>
        <span class="badge badge--accent">Everything</span>
      </div>
      <p class="text-sm muted">A superuser key. Every current and future VayuMCP tool becomes available — posts, pages, media, analytics, and more as the toolset grows. This is the "give the client the keys to the whole site" option.</p>
      <button type="button" class="btn btn--primary" data-mint="*:*" data-label="VayuMCP (full control)">Grant full control</button>
    </div>

    <div class="cx-grant">
      <div class="cx-grant-head">
        <span class="settings-row-label">Author only</span>
        <span class="badge">Posts &amp; pages</span>
      </div>
      <p class="text-sm muted">Let Claude write, update, and organise content — create and edit posts and pages, search, and list — but nothing else. A safe default for a writing assistant.</p>
      <button type="button" class="btn" data-mint="posts:read,posts:write" data-label="VayuMCP (author)">Grant author access</button>
    </div>

    <div class="cx-grant">
      <div class="cx-grant-head">
        <span class="settings-row-label">Read only</span>
        <span class="badge">Look, don't touch</span>
      </div>
      <p class="text-sm muted">Claude can read posts and pages, search content, and read analytics — but cannot change anything. Ideal for analysis, reporting, and audits.</p>
      <button type="button" class="btn" data-mint="posts:read,analytics:read" data-label="VayuMCP (read-only)">Grant read-only access</button>
    </div>
  </div>

  <p class="field-hint mt-2">Need a precise grant? Build a custom scoped key on the <a href="/os/apikeys">API Keys</a> page — the connector honours it exactly.</p>
</div>`
}

// osConnectorConnectCard shows the ready-to-paste configuration for Claude
// Desktop and the Claude Code CLI. The key placeholder is replaced live once a
// key is minted above (see the page script), so the operator gets a genuinely
// copy-paste result.
func osConnectorConnectCard(endpoint string) string {
	e := html.EscapeString(endpoint)
	// The template carries a visible placeholder; JS swaps in the real key after
	// a mint. data-endpoint lets the script rebuild the block with the live token.
	// cfg embeds the RAW endpoint: the whole block is html-escaped exactly once
	// when rendered into the <pre> below (matching the CLI path). Escaping e here
	// would double-encode any HTML-special char a valid Host may contain.
	cfg := `{
  "mcpServers": {
    "vayupress": {
      "url": "` + endpoint + `",
      "headers": { "Authorization": "Bearer YOUR_KEY_HERE" }
    }
  }
}`
	cli := `claude mcp add --transport http vayupress ` + endpoint + ` --header "Authorization: Bearer YOUR_KEY_HERE"`

	oneClick := `<div class="card">
  <p class="text-sm muted">This needs <strong>no key at all</strong>. The Connect button lives on <strong>Claude's side</strong>, not on this page — this site runs the OAuth&nbsp;2.1 server it signs into. On <strong>claude.ai</strong> (or Claude Desktop) open <em>Settings → Connectors → Add custom connector</em>, paste your connector endpoint <code>` + e + `</code>, and click <strong>Connect</strong>. Claude signs you in through this site and shows an <strong>Approve&nbsp;&amp;&nbsp;connect</strong> screen where you choose Full&nbsp;control / Author / Read-only.</p>
  <p class="field-hint mt-2">Custom connectors on claude.ai may require a paid plan (Pro/Max/Team/Enterprise). The Desktop and CLI options remain for clients that use a pasted key. Technical detail: <a href="/docs/adr/ADR-0140-vayu-mcp-oauth" target="_blank" rel="noopener">ADR-0140</a>.</p>
</div>`

	desktop := `<div class="card">
  <p class="text-sm muted">Add this to your <code>claude_desktop_config.json</code> (Settings → Developer → Edit config), then restart Claude Desktop:</p>
  <pre class="cx-code font-mono" id="cx-cfg-desktop" data-endpoint="` + e + `">` + html.EscapeString(cfg) + `</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#cx-cfg-desktop">Copy config</button>
  </div>
</div>`

	cliCard := `<div class="card">
  <p class="text-sm muted">One command:</p>
  <pre class="cx-code font-mono" id="cx-cfg-cli" data-endpoint="` + html.EscapeString(endpoint) + `">` + html.EscapeString(cli) + `</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#cx-cfg-cli">Copy command</button>
  </div>
</div>`

	// The proxy/WAF section is reference material: essential when it bites, noise
	// on every other visit. It used to run to five dense paragraphs sitting between
	// the operator and the connector list, so it dominated a page whose actual job
	// is "copy an endpoint, grant a key". Folded away, findable by its own title.
	proxy := `<div class="card">
  <p class="text-sm muted">An MCP client reaches this server <strong>machine-to-machine</strong> — no browser is in the loop for the API calls — so it <strong>cannot pass a JavaScript &ldquo;challenge&rdquo; / &ldquo;Just a moment&hellip;&rdquo; page</strong>. If your site is proxied with <em>Bot&nbsp;Fight&nbsp;Mode</em>, a <em>Managed&nbsp;Challenge</em>, a custom rule, or <em>Under&nbsp;Attack</em> mode on, those requests are stopped <strong>before they reach VayuPress</strong> and Connect fails with &ldquo;couldn't register&rdquo; — the request never appears in this server's log, which is what makes it hard to diagnose.</p>
  <p class="text-sm muted">Let these exact paths <strong>bypass the challenge</strong>: <code>/mcp</code>, <code>/oauth/*</code> and <code>/.well-known/*</code>. <code>/mcp</code> matters <em>after</em> connecting too — every tool call runs over it, so a challenge there breaks the connector on first use. In Cloudflare: <em>Security → WAF → Custom rules</em>, action <strong>Skip</strong>, ticking <em>Managed rules</em>, <em>Super Bot Fight Mode</em>, <em>Rate limiting rules</em> and <em>Browser Integrity Check</em>. <strong>On the free plan</strong> you get only a handful of custom rules — if you are at the cap, append these paths to an existing Skip rule instead of adding one:</p>
  <pre class="cx-code font-mono" id="cx-waf-expr">starts_with(http.request.uri.path, "/mcp") or
starts_with(http.request.uri.path, "/oauth/") or
starts_with(http.request.uri.path, "/.well-known/")</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#cx-waf-expr">Copy expression</button>
  </div>
  <p class="field-hint mt-2">Verify with a plain <code>curl</code> of your site's <code>/health</code> endpoint: it must return JSON, <strong>not</strong> a challenge page. When curl gets through, an MCP client will too.</p>
  <p class="field-hint mt-2"><strong>Can't scope the challenge per path?</strong> (Bot&nbsp;Fight&nbsp;Mode on the free plan cannot be.) Point a dedicated <code>mcp.&lt;your-domain&gt;</code> record straight at this server with the <strong>proxy OFF (&ldquo;DNS only&rdquo;)</strong>. Your main site keeps full protection; only this host is direct, and VayuShield still guards it. VayuPress provisions the certificate and vhost itself — run <code>sudo bash scripts/setup-mcp-subdomain.sh</code>, or re-run your update once the record exists. This page then offers that host automatically.</p>
</div>`

	return `<div class="section-head"><span class="section-head__title">Connect a client</span><span class="section-head__hint">A granted key is filled into these automatically — pick your client</span></div>
<div class="mon-stack">` +
		monAcc("✨", "One-click Connect on claude.ai", "Easiest — no key to copy", `<span class="mon-chip mon-chip--on">● Recommended</span>`, true, oneClick) +
		monAcc("🖥️", "Claude Desktop", "Paste a config block", `<span class="mon-chip mon-chip--off">○ Needs a key</span>`, false, desktop) +
		monAcc("⌨️", "Claude Code (CLI)", "One command", `<span class="mon-chip mon-chip--off">○ Needs a key</span>`, false, cliCard) +
		monAcc("🛡️", "Behind a proxy or WAF?", "The most common reason Connect fails", `<span class="mon-chip mon-chip--off">○ Reference</span>`, false, proxy) +
		`</div>`
}

// osConnectorManageCard lists the operator's live (non-revoked, external) keys so
// a connector can be revoked here. It intentionally mirrors a subset of the API
// Keys console; the full grid lives on /os/apikeys.
// relTimeAgo renders a timestamp as a short human interval.
func relTimeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	case d < 30*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d ago"
	}
	return t.UTC().Format("2 Jan 2006")
}

// connectorDetailRow is one label/value line in a connector's detail panel.
func connectorDetailRow(label, value string) string {
	return `<div class="cx-detail"><span class="cx-detail__k">` + html.EscapeString(label) +
		`</span><span class="cx-detail__v">` + value + `</span></div>`
}

// osConnectorManageCard lists every granted connector as its own expandable
// panel: what it is, what it can reach, when it last did, and the controls to
// pause, disconnect or remove it.
//
// It replaces a three-column table showing label, access and a Revoke button.
// That table could not answer the question an operator actually has — "which of
// these is Claude and which is Cline?" — because every row looked the same and
// the only distinguishing data (last used, call count) was recorded but never
// displayed. With several clients connected, and a leftover key for every failed
// connect attempt, the list became unreadable exactly when it mattered.
//
// Pausing is a real control, not a label: the auth cache loads keys WHERE
// revoked=0 AND active=1, and SetActive invalidates that cache, so a paused
// connector stops authenticating at once and resumes with the same secret. That
// is the difference between this and Revoke, which is permanent.
func osConnectorManageCard(keys []apikeys.Key) string {
	if len(keys) == 0 {
		return `<div class="card"><div class="empty-state">No connectors yet. Grant a key above, then connect a client.</div></div>`
	}

	unused := 0
	panels := ""
	for _, k := range keys {
		id := html.EscapeString(k.ID)

		scope := "Limited"
		if k.Permissions.IsSuperuser() {
			scope = "Full control"
		}

		// Status. Expiry is checked before the active flag: an expired key is
		// already refused by the auth cache, so calling it "Paused" would invite an
		// operator to press Resume and watch nothing change.
		state, chip, icon := "active", `<span class="mon-chip mon-chip--on">● Active</span>`, "🔌"
		switch {
		case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
			state, chip, icon = "expired", `<span class="mon-chip mon-chip--off">○ Expired</span>`, "⌛"
		case !k.Active:
			state, chip, icon = "paused", `<span class="mon-chip mon-chip--off">⏸ Paused</span>`, "⏸️"
		}

		// The subtitle carries the one fact that tells two connectors apart.
		activity := "Never used"
		if k.LastUsedAt != nil {
			activity = "Last used " + relTimeAgo(*k.LastUsedAt)
		} else {
			unused++
		}
		if k.UseCount > 0 {
			activity += " · " + strconv.FormatInt(k.UseCount, 10) + " calls"
		}

		details := connectorDetailRow("Key", `<code class="font-mono">`+html.EscapeString(apikeys.Mask(k.Prefix))+`</code>`) +
			connectorDetailRow("Access", html.EscapeString(scope)) +
			connectorDetailRow("Granted", html.EscapeString(relTimeAgo(k.CreatedAt))) +
			connectorDetailRow("Last used", html.EscapeString(func() string {
				if k.LastUsedAt == nil {
					return "never"
				}
				return relTimeAgo(*k.LastUsedAt)
			}())) +
			connectorDetailRow("Calls", strconv.FormatInt(k.UseCount, 10))
		if k.ExpiresAt != nil {
			details += connectorDetailRow("Expires", html.EscapeString(k.ExpiresAt.UTC().Format("2 Jan 2006 15:04")+" UTC"))
		}
		if k.RatePerMin > 0 {
			details += connectorDetailRow("Rate limit", strconv.Itoa(k.RatePerMin)+"/min")
		}
		if caps := k.Permissions.Capabilities(); len(caps) > 0 && !k.Permissions.IsSuperuser() {
			chips := ""
			for _, c := range caps {
				chips += `<code class="font-mono text-xs cx-cap">` + html.EscapeString(c) + `</code> `
			}
			details += connectorDetailRow("Can reach", chips)
		}

		// Pause/Resume is offered only where it can do something. An expired key
		// cannot be resumed by flipping a flag, so offering the button would be a
		// control that lies.
		toggle := ""
		switch state {
		case "active":
			toggle = `<button type="button" class="btn btn--sm" data-cx-pause="` + id + `">Pause</button>`
		case "paused":
			toggle = `<button type="button" class="btn btn--sm" data-cx-resume="` + id + `">Resume</button>`
		}

		body := `<div class="card">
  <div class="cx-details">` + details + `</div>
  <div class="ak-cred-actions">` + toggle +
			`<button type="button" class="btn btn--sm" data-revoke="` + id + `">Disconnect</button>
    <button type="button" class="btn btn--sm btn--danger" data-cx-remove="` + id + `">Remove</button>
  </div>
  <p class="field-hint mt-2"><strong>Pause</strong> stops this connector immediately and can be undone — the same key works again on Resume. <strong>Disconnect</strong> is permanent: the key stops working for good and the record is kept for the audit log. <strong>Remove</strong> also deletes the record.</p>
</div>`

		panels += monAcc(icon, html.EscapeString(k.Label), html.EscapeString(activity), chip, false, body)
	}

	hint := `<p class="text-sm muted mb-4">Every client that has connected, and what it can reach. Each action is written to the audit log.</p>`
	if unused > 0 {
		// Directly actionable: a key that has never been used is almost always a
		// leftover from a connect attempt that failed, and these accumulate
		// invisibly. Saying so turns a confusing list into a cleanup.
		n := strconv.Itoa(unused)
		word := " connectors have"
		if unused == 1 {
			word = " connector has"
		}
		hint += `<p class="text-sm muted mb-4"><span class="mon-chip mon-chip--off">○ ` + n + ` never used</span> ` +
			n + word + ` never made a call. That usually means a connect attempt that did not finish — safe to remove.</p>`
	}
	return `<div class="card">` + hint + `<div class="mon-stack">` + panels + `</div>
  <p class="field-hint mt-2">Rotating a key, changing its expiry or editing per-section grants lives on the <a href="/os/apikeys">API Keys</a> page.</p>
</div>`
}

// osConnectorScript is the nonce-gated controller. It runs inside the shared
// bootstrap IIFE (adminOSShellFoot), so csrf() is already in scope.
const osConnectorScript = `
var cxStatus=document.getElementById('cx-status');
function cxSet(t,isErr){if(cxStatus){cxStatus.textContent=t;cxStatus.style.color=isErr?'var(--color-danger,#ef4444)':'var(--color-success,#22c55e)';}}
function cxPost(url,payload){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload||{})}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
function cxCopy(text){if(navigator.clipboard){navigator.clipboard.writeText(text);}return true;}

// ── Token banner + live config fill ──
var banner=document.getElementById('cx-token-banner');
var tokenVal=document.getElementById('cx-token-value');
function fillConfigs(tok){
  document.querySelectorAll('[data-endpoint]').forEach(function(el){
    var ep=el.getAttribute('data-endpoint');
    if(el.id==='cx-cfg-desktop'){
      el.textContent='{\n  "mcpServers": {\n    "vayupress": {\n      "url": "'+ep+'",\n      "headers": { "Authorization": "Bearer '+tok+'" }\n    }\n  }\n}';
    }else if(el.id==='cx-cfg-cli'){
      el.textContent='claude mcp add --transport http vayupress '+ep+' --header "Authorization: Bearer '+tok+'"';
    }
  });
}
function showToken(tok){
  if(tokenVal){tokenVal.value=tok;}
  if(banner){banner.hidden=false;banner.scrollIntoView({behavior:'smooth',block:'start'});}
  fillConfigs(tok);
}
var copyBtn=document.getElementById('cx-token-copy');
if(copyBtn)copyBtn.addEventListener('click',function(){if(tokenVal){tokenVal.select();cxCopy(tokenVal.value);cxSet('Key copied',false);}});
var doneBtn=document.getElementById('cx-token-done');
if(doneBtn)doneBtn.addEventListener('click',function(){location.reload();});

// ── Grant (mint) + copy + revoke via event delegation ──
document.addEventListener('click',function(ev){
  var mintBtn=ev.target.closest('[data-mint]');
  if(mintBtn){
    var caps=mintBtn.getAttribute('data-mint').split(',');
    var label=mintBtn.getAttribute('data-label')||'VayuMCP';
    mintBtn.disabled=true;cxSet('Creating key…',false);
    cxPost('/os/api/apikeys/create',{label:label,capabilities:caps}).then(function(res){
      mintBtn.disabled=false;
      if(res.ok&&res.d.token){showToken(res.d.token);cxSet('Key granted — paste it into your client below',false);}
      else{cxSet(res.d.detail||res.d.title||'Could not create key',true);}
    }).catch(function(e){mintBtn.disabled=false;cxSet('Error: '+e,true);});
    return;
  }
  var copyBtn2=ev.target.closest('[data-copy]');
  if(copyBtn2){
    var sel=copyBtn2.getAttribute('data-copy');var el=document.querySelector(sel);
    if(el){var text=el.value!==undefined?el.value:el.textContent;cxCopy(text);cxSet('Copied',false);}
    return;
  }
  var revBtn=ev.target.closest('[data-revoke]');
  if(revBtn){
    if(!confirm('Disconnect this connector permanently? Its key stops working immediately and cannot be re-enabled. To stop it temporarily, use Pause instead.'))return;
    var id=revBtn.getAttribute('data-revoke');
    revBtn.disabled=true;
    cxPost('/os/api/apikeys/revoke',{id:id}).then(function(res){
      if(res.ok){location.reload();}else{revBtn.disabled=false;cxSet(res.d.detail||'Could not disconnect',true);}
    }).catch(function(e){revBtn.disabled=false;cxSet('Error: '+e,true);});
    return;
  }
  // Pause / Resume. Reversible, so no confirm on the way in -- the cost of an
  // accidental pause is one click back, and a prompt on a safe action trains
  // people to click through the prompts that matter.
  var pauseBtn=ev.target.closest('[data-cx-pause]');
  if(pauseBtn){
    var pid=pauseBtn.getAttribute('data-cx-pause');
    pauseBtn.disabled=true;
    cxPost('/os/api/apikeys/deactivate',{id:pid}).then(function(res){
      if(res.ok){location.reload();}else{pauseBtn.disabled=false;cxSet(res.d.detail||'Could not pause',true);}
    }).catch(function(e){pauseBtn.disabled=false;cxSet('Error: '+e,true);});
    return;
  }
  var resumeBtn=ev.target.closest('[data-cx-resume]');
  if(resumeBtn){
    var rid=resumeBtn.getAttribute('data-cx-resume');
    resumeBtn.disabled=true;
    cxPost('/os/api/apikeys/activate',{id:rid}).then(function(res){
      if(res.ok){location.reload();}else{resumeBtn.disabled=false;cxSet(res.d.detail||'Could not resume',true);}
    }).catch(function(e){resumeBtn.disabled=false;cxSet('Error: '+e,true);});
    return;
  }
  var rmBtn=ev.target.closest('[data-cx-remove]');
  if(rmBtn){
    if(!confirm('Remove this connector and delete its record? The client is disconnected and the entry disappears from this list. This cannot be undone.'))return;
    var mid=rmBtn.getAttribute('data-cx-remove');
    rmBtn.disabled=true;
    cxPost('/os/api/apikeys/delete',{id:mid}).then(function(res){
      if(res.ok){location.reload();}else{rmBtn.disabled=false;cxSet(res.d.detail||'Could not remove',true);}
    }).catch(function(e){rmBtn.disabled=false;cxSet('Error: '+e,true);});
    return;
  }
});
`
