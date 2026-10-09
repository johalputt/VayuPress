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
	"github.com/johalputt/vayupress/internal/ui"
)

// publicMCPEndpoint returns the absolute URL of this site's MCP connector
// endpoint as seen by the operator's browser — scheme + Host + /mcp. Scheme
// resolution: a terminating proxy's X-Forwarded-Proto wins (first hop); else a
// direct TLS connection is https; else (plain HTTP with no proxy — a local/dev
// run) http. Production always terminates TLS or sets X-Forwarded-Proto, so the
// advertised URL is https there and never a downgraded link. Host comes from the
// request so it is correct for whichever domain the operator is administering.
func publicMCPEndpoint(r *http.Request) string {
	return publicOrigin(r) + "/mcp"
}

// publicOrigin is scheme and host as the browser reached this site, resolved
// as publicMCPEndpoint describes.
func publicOrigin(r *http.Request) string {
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
	return scheme + "://" + host
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

	body := osConnectorPage(endpoint, apexEndpoint, dedicated, blockedHost, connectors)

	full := adminOSShellHead(nonce, "VayuMCP", "connector", cfg) +
		body +
		adminOSShellFoot(nonce, osConnectorScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// osConnectorPage is the page body. It takes the request-derived endpoint and
// the connectors as arguments so the tests render exactly what an operator sees.
func osConnectorPage(endpoint, apex string, dedicated bool, blockedHost string, connectors []apikeys.Key) string {
	return string(ui.SettingsPage("VayuMCP", osConnectorState(connectors),
		"Let an AI client run this site with its own tools. What it can do is decided by the key you grant.",
		ui.HTML(osConnectorIntro()),
		ui.Section("Endpoint", "The one URL every MCP client connects to", ui.Rows(mcpEndpointRows("cx-endpoint", endpoint, apex, dedicated, blockedHost)...)),
		ui.Section("Grant access", "A connector is as powerful as its key", osConnectorGrants()),
		ui.Section("Connect a client", "A granted key is filled in", osConnectorConnect(endpoint)),
		ui.Section("Connected clients", "Disconnecting one stops that client at once", ui.HTML(osConnectorManageCard(connectors))),
	))
}

// osConnectorIntro is the one-time key banner, hidden until a key is minted,
// and the status line the page script writes to.
func osConnectorIntro() string {
	return `<div id="cx-token-banner" class="card ak-token-banner" hidden>
  <div class="settings-block-title">Copy your new connector key now</div>
  <p class="text-sm muted">This is the only time the full key is shown. It has been filled into the configuration below: copy it, then keep it somewhere safe.</p>
  <div class="ak-token-row">
    <input id="cx-token-value" class="input font-mono ak-token-input" type="text" readonly>
    <button type="button" class="btn btn--sm" id="cx-token-copy">Copy key</button>
    <button type="button" class="btn btn--primary btn--sm" id="cx-token-done">Done</button>
  </div>
</div>
<p class="page-lead"><span id="cx-status" role="status" aria-live="polite" class="text-xs muted"></span></p>`
}

// osConnectorState is the page's state beside its title: live connectors,
// paused and expired ones, and how many live ones hold full control.
//
// "Connected" must mean connected. Counting paused and expired connectors as
// live would put a number beside the title that the list underneath
// contradicts, and a full-control key that is paused is not a live grant, so
// counting it in the warning would overstate exposure too. Expired is told
// apart from paused for the same reason the list does: Resume cannot bring an
// expired key back.
func osConnectorState(keys []apikeys.Key) ui.HTML {
	full, live, paused, expired := 0, 0, 0, 0
	now := time.Now()
	for _, k := range keys {
		switch {
		case k.ExpiresAt != nil && !k.ExpiresAt.After(now):
			expired++
		case !k.Active:
			paused++
		default:
			live++
			if k.Permissions.IsSuperuser() {
				full++
			}
		}
	}
	st := connectorState(live, full, paused, "connector")
	if expired > 0 {
		st += " " + ui.State("neutral", strconv.Itoa(expired)+" expired")
	}
	return st
}

// osConnectorGrants are the one-click grants. Each mints through the scoped
// key create endpoint with its capability set in data-mint.
func osConnectorGrants() ui.HTML {
	return ui.Rows(
		mcpGrantRow(true, "Full control", "Every tool, now and as the toolset grows.", "*:*", "VayuMCP (full control)", "Grant full control"),
		mcpGrantRow(false, "Author", "Write and organise posts and pages; nothing else.", "posts:read,posts:write", "VayuMCP (author)", "Grant author access"),
		mcpGrantRow(false, "Read only", "Read posts, pages and analytics; change nothing.", "posts:read,analytics:read", "VayuMCP (read-only)", "Grant read-only access"),
		ui.Row{Label: "A precise grant", Hint: "Any sections and actions you choose. Every action a client takes is audited.",
			Control: `<a class="settings-row-go" href="/os/apikeys">API keys` + ui.Icon("chev-r") + `</a>`},
	)
}

// osConnectorConnect is how a client connects: the generic, client-agnostic
// configuration, the clients with their own guided pages, and the proxy
// reference.
//
// It used to carry four blocks — claude.ai one-click, Claude Desktop, Claude Code
// CLI, and the proxy note — which made a page about a protocol spend most of its
// height on one vendor, and made a reader scroll past three sections that were
// not theirs to find the one that was. Claude and Buzz have their own pages now
// (ADR-0147); what belongs here is the shape every other MCP client needs.
//
// cfg embeds the RAW endpoint. mcpSnippet html-escapes the whole block exactly
// once; escaping the endpoint first would double-encode any HTML-special char a
// valid Host may contain.
func osConnectorConnect(endpoint string) ui.HTML {
	cfg := `{
  "mcpServers": {
    "vayupress": {
      "url": "` + endpoint + `",
      "headers": { "Authorization": "Bearer ` + keyTemplatePlaceholder + `" }
    }
  }
}`

	generic := `<p class="text-sm muted mb-3">A URL plus a header. Grant a key above and it is filled in.</p>
` + mcpSnippet("cx-cfg-generic", cfg) + `
<p class="field-hint mt-2">X-API-Key works as well as Authorization: Bearer.` + string(ui.Tip("The transport is MCP over Streamable HTTP (JSON-RPC 2.0).")) + `</p>`

	// The proxy/WAF text is reference material: essential when it bites, noise
	// on every other visit. It used to sit between the operator and the
	// connector list and dominate a page whose job is "copy an endpoint, grant a
	// key", so it opens in a sheet, findable by its own row.
	proxy := `<p class="text-sm muted">An MCP client reaches this server <strong>machine-to-machine</strong> — no browser is in the loop for the API calls — so it <strong>cannot pass a JavaScript &ldquo;challenge&rdquo; / &ldquo;Just a moment&hellip;&rdquo; page</strong>. If your site is proxied with <em>Bot&nbsp;Fight&nbsp;Mode</em>, a <em>Managed&nbsp;Challenge</em>, a custom rule, or <em>Under&nbsp;Attack</em> mode on, those requests are stopped <strong>before they reach VayuPress</strong> and Connect fails with &ldquo;couldn't register&rdquo; — the request never appears in this server's log, which is what makes it hard to diagnose.</p>
  <p class="text-sm muted">Let these exact paths <strong>bypass the challenge</strong>: <code>/mcp</code>, <code>/oauth/*</code> and <code>/.well-known/*</code>. <code>/mcp</code> matters <em>after</em> connecting too — every tool call runs over it, so a challenge there breaks the connector on first use. In Cloudflare: <em>Security → WAF → Custom rules</em>, action <strong>Skip</strong>, ticking <em>Managed rules</em>, <em>Super Bot Fight Mode</em>, <em>Rate limiting rules</em> and <em>Browser Integrity Check</em>. <strong>On the free plan</strong> you get only a handful of custom rules — if you are at the cap, append these paths to an existing Skip rule instead of adding one:</p>
  <pre class="cx-code font-mono" id="cx-waf-expr">starts_with(http.request.uri.path, "/mcp") or
starts_with(http.request.uri.path, "/oauth/") or
starts_with(http.request.uri.path, "/.well-known/")</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#cx-waf-expr">Copy expression</button>
  </div>
  <p class="field-hint mt-2">Verify with a plain <code>curl</code> of your site's <code>/health</code> endpoint: it must return JSON, <strong>not</strong> a challenge page. When curl gets through, an MCP client will too.</p>
  <p class="field-hint mt-2"><strong>Can't scope the challenge per path?</strong> (Bot&nbsp;Fight&nbsp;Mode on the free plan cannot be.) Point a dedicated <code>mcp.&lt;your-domain&gt;</code> record straight at this server with the <strong>proxy OFF (&ldquo;DNS only&rdquo;)</strong>. Your main site keeps full protection; only this host is direct, and VayuShield still guards it. VayuPress provisions the certificate and vhost itself — run <code>sudo bash scripts/setup-mcp-subdomain.sh</code>, or re-run your update once the record exists. This page then offers that host automatically.</p>`

	link := func(href, text string) ui.HTML {
		return ui.HTML(`<a class="settings-row-go" href="` + href + `">` + text + string(ui.Icon("chev-r")) + `</a>`)
	}
	return ui.Rows(
		ui.Row{Icon: "plug", Label: "Any MCP client", Hint: "A URL plus a header: the shape Cursor, Cline and custom clients use unchanged.",
			Control: `<button type="button" class="btn btn--sm" data-sheet="cx-generic">Show the config</button>`},
		ui.Row{Icon: "compass", Label: "Claude Code, Claude Desktop and claude.ai", Hint: "Guided setup. The one-click route needs no key: Claude signs in through this site's OAuth 2.1 server and you approve a scope on screen.",
			Control: link("/os/claudecode", "Claude Code")},
		ui.Row{Icon: "compass", Label: "Buzz", Hint: "Agents in a Buzz workspace, which reach this endpoint through their agent harness.",
			Control: link("/os/buzz", "Buzz")},
		ui.Row{Icon: "shield", Label: "Behind a proxy or WAF?", Hint: "The most common reason Connect fails.",
			Control: `<button type="button" class="btn btn--sm" data-sheet="cx-proxy">What to allow</button>`},
	) +
		ui.Sheet("cx-generic", "Any MCP client", ui.HTML(generic)) +
		ui.Sheet("cx-proxy", "Behind a proxy or WAF", ui.HTML(proxy))
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
		return `<p class="muted text-sm">No connectors yet. Grant a key above, then connect a client.</p>`
	}

	unused := 0
	var rows []ui.Row
	sheets := ""
	for _, k := range keys {
		id := html.EscapeString(k.ID)

		scope := "Limited"
		if k.Permissions.IsSuperuser() {
			scope = "Full control"
		}

		// Status. Expiry is checked before the active flag: an expired key is
		// already refused by the auth cache, so calling it "Paused" would invite an
		// operator to press Resume and watch nothing change.
		state, chip, icon := "active", ui.State("ok", "Active"), "plug"
		switch {
		case k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()):
			state, chip, icon = "expired", ui.State("neutral", "Expired"), "hourglass"
		case !k.Active:
			state, chip, icon = "paused", ui.State("neutral", "Paused"), "power"
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

		body := `<div class="cx-details">` + details + `</div>
  <div class="ak-cred-actions">` + toggle +
			`<button type="button" class="btn btn--sm" data-revoke="` + id + `">Disconnect</button>
    <button type="button" class="btn btn--sm btn--danger" data-cx-remove="` + id + `">Remove</button>
    <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
  </div>
  <p class="field-hint mt-2">Pause stops it at once and Resume brings back the same key. Disconnect is permanent and keeps the record for the audit log; Remove deletes the record too.</p>`

		// ui.Sheet escapes the id it is given, so it takes the raw one; the
		// opener escapes the same raw id once, or the two would not match.
		sheet := "cx-conn-" + k.ID
		rows = append(rows, ui.Row{Icon: icon, Label: k.Label, Hint: activity,
			Control: chip + ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="`+html.EscapeString(sheet)+`">Details</button>`)})
		sheets += string(ui.Sheet(sheet, k.Label, ui.HTML(body)))
	}

	hint := ""
	if unused > 0 {
		// Directly actionable: a key that has never been used is almost always a
		// leftover from a connect attempt that failed, and these accumulate
		// invisibly. Saying so turns a confusing list into a cleanup.
		n := strconv.Itoa(unused)
		word := " connectors have"
		if unused == 1 {
			word = " connector has"
		}
		hint = `<p class="text-sm muted mb-3">` + n + word + ` never made a call. That usually means a connect attempt that did not finish, and is safe to remove.</p>`
	}
	return hint + string(ui.Rows(rows...)) + sheets +
		`<p class="field-hint mt-3">Rotating a key, its expiry or its grants is on <a href="/os/apikeys">API keys</a>.</p>`
}

// osConnectorScript is the nonce-gated controller. It runs inside the shared
// bootstrap IIFE (adminOSShellFoot), so csrf() is already in scope.
const osConnectorScript = `
var cxStatus=document.getElementById('cx-status');
// A failure in a connector's sheet is said in that sheet: the page's status
// line is behind it while it is open.
function cxSet(t,isErr,from){var d=from&&from.closest('dialog');var s=(d&&d.querySelector('[data-sheet-status]'))||cxStatus;if(s){s.textContent=t;s.style.color=isErr?'var(--danger)':'var(--ok)';}}
function cxPost(url,payload){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload||{})}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
function cxCopy(text){if(navigator.clipboard){navigator.clipboard.writeText(text);}return true;}

// ── Token banner + live config fill ──
var banner=document.getElementById('cx-token-banner');
var tokenVal=document.getElementById('cx-token-value');
// Snippets carry their own template in data-tpl with a placeholder where the key
// goes, so this does not need to know what any of them say. The previous version
// rebuilt two specific blocks by id from a data-endpoint attribute, which meant
// every new snippet had to be taught to this function or silently kept its
// placeholder.
function fillConfigs(tok){
  document.querySelectorAll('[data-tpl]').forEach(function(el){
    el.textContent=el.getAttribute('data-tpl').split('` + keyTemplatePlaceholder + `').join(tok);
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
    vpConfirm({title:'Disconnect this connector permanently?',message:'Its key stops working immediately and cannot be re-enabled. To stop it temporarily, use Pause instead.',confirm:'Disconnect'},function(){
    var id=revBtn.getAttribute('data-revoke');
    revBtn.disabled=true;
    cxPost('/os/api/apikeys/revoke',{id:id}).then(function(res){
      if(res.ok){location.reload();}else{revBtn.disabled=false;cxSet(res.d.detail||'Could not disconnect',true,revBtn);}
    }).catch(function(e){revBtn.disabled=false;cxSet('Error: '+e,true,revBtn);});
    });
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
      if(res.ok){location.reload();}else{pauseBtn.disabled=false;cxSet(res.d.detail||'Could not pause',true,pauseBtn);}
    }).catch(function(e){pauseBtn.disabled=false;cxSet('Error: '+e,true,pauseBtn);});
    return;
  }
  var resumeBtn=ev.target.closest('[data-cx-resume]');
  if(resumeBtn){
    var rid=resumeBtn.getAttribute('data-cx-resume');
    resumeBtn.disabled=true;
    cxPost('/os/api/apikeys/activate',{id:rid}).then(function(res){
      if(res.ok){location.reload();}else{resumeBtn.disabled=false;cxSet(res.d.detail||'Could not resume',true,resumeBtn);}
    }).catch(function(e){resumeBtn.disabled=false;cxSet('Error: '+e,true,resumeBtn);});
    return;
  }
  var rmBtn=ev.target.closest('[data-cx-remove]');
  if(rmBtn){
    vpConfirm({title:'Remove this connector and delete its record?',message:'The client is disconnected and the entry disappears from this list. This cannot be undone.',confirm:'Remove'},function(){
    var mid=rmBtn.getAttribute('data-cx-remove');
    rmBtn.disabled=true;
    cxPost('/os/api/apikeys/delete',{id:mid}).then(function(res){
      if(res.ok){location.reload();}else{rmBtn.disabled=false;cxSet(res.d.detail||'Could not remove',true,rmBtn);}
    }).catch(function(e){rmBtn.disabled=false;cxSet('Error: '+e,true,rmBtn);});
    });
    return;
  }
});
`
