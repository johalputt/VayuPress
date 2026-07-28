// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_claudecode.go — the Claude Code connector console (/os/claudecode),
// ADR-0147.
//
// Claude has three ways in and they are genuinely different, which is why they
// now have a page instead of three accordions on a page about a protocol:
//
//   - claude.ai / Claude Desktop "Add custom connector" — needs NO key at all.
//     The Connect button lives on Claude's side; this site runs the OAuth 2.1
//     server it signs into, and the operator approves a scope on screen.
//   - Claude Desktop config file — a pasted key in claude_desktop_config.json.
//   - Claude Code CLI — a pasted key via `claude mcp add`.
//
// The one-click path is strictly the best of the three when it is available, and
// it was previously buried among instructions for clients the reader was not
// using. Leading with it here is the whole point of the split.
//
// Like /os/buzz, this page adds NO backend surface: it mints through the
// CSRF-protected API-key endpoints and shares its banner, stat strip, grant
// tiles, snippets and controller with admin_os_mcpclient.go. VayuMCP remains the
// name of the connector itself (ADR-0139) — this page is named for the client it
// configures, exactly as the Buzz page is.

import (
	"html"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/render"
)

// iconClaudeCode is a terminal prompt — the glyph for the Claude Code page.
var iconClaudeCode = svgIcon("M3 4.5h14v11H3v-11zM6 8l2.5 2L6 12M10.5 12.5h4")

// claudeKeyLabelPrefix marks the keys this page mints, so its stat strip counts
// Claude clients rather than every connector on the install.
const claudeKeyLabelPrefix = "Claude"

// handleOSClaudeCode renders the Claude Code connector console.
func (a *App) handleOSClaudeCode(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	endpoint, apex, dedicated, blockedHost := connectorEndpoint(r)
	keys := a.liveConnectorKeys(r)

	body := osClaudeCodeIntro() +
		osClaudeCodeStats(endpoint, keys, dedicated, blockedHost) +
		`<div class="section-head"><span class="section-head__title">Connect Claude</span><span class="section-head__hint">Three ways in — the first needs no key at all</span></div>` +
		osClaudeCodeSetupCards(endpoint, apex, dedicated, blockedHost) +
		`<div class="section-head"><span class="section-head__title">Grant access</span><span class="section-head__hint">Only needed for the Desktop config and CLI routes</span></div>` +
		osClaudeCodeGrantCard() +
		`<div class="section-head"><span class="section-head__title">If Connect fails</span><span class="section-head__hint">Almost always something in front of this server</span></div>` +
		osClaudeCodeTroubleshootCard()

	full := adminOSShellHead(nonce, "Claude Code", "claudecode", cfg) +
		body +
		adminOSShellFoot(nonce, mcpClientScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// osClaudeCodeIntro is the page header plus the one-time key banner.
func osClaudeCodeIntro() string {
	return `<div class="page-header">
  <h1>Claude Code</h1>
  <div class="page-actions">
    <a class="btn btn--sm" href="/docs/compatibility/claude-code" target="_blank" rel="noopener">Claude docs</a>
    <span id="` + mcpStatusID + `" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<p class="text-sm muted mb-4">Connect <strong>Claude Code</strong>, <strong>Claude Desktop</strong> or <strong>claude.ai</strong> to this site and run it by chat — publish and edit posts, build pages, search content, read analytics, switch themes. It goes through <a href="/os/connector">VayuMCP</a>, the Model Context Protocol server this site already serves, so there is nothing to install on either side. <strong>The one-click route needs no key at all</strong>: Claude signs in through this site's own OAuth&nbsp;2.1 server and you approve a scope on screen.</p>

` + mcpClientTokenBanner("This is the only time the full key is shown. It has been filled into the Desktop and CLI configurations below — copy the one you need, then store the key somewhere safe. You will not be able to see it again.")
}

// osClaudeCodeStats is the at-a-glance strip, scoped to the keys this page minted.
func osClaudeCodeStats(endpoint string, keys []apikeys.Key, dedicated bool, blockedHost string) string {
	return mcpClientStats(endpoint, keys, claudeKeyLabelPrefix, "Claude clients connected", dedicated, blockedHost)
}

// osClaudeCodeSetupCards renders the three routes in, best first.
//
// The one-click accordion is the only one opened by default, and it is the only
// one whose chip reads "Recommended". That ordering is the argument: it needs no
// key, so there is no token to leak, paste wrongly, or forget to revoke.
func osClaudeCodeSetupCards(endpoint, apex string, dedicated bool, blockedHost string) string {
	e := html.EscapeString(endpoint)

	cliTpl := `claude mcp add --transport http vayupress ` + endpoint +
		` --header "Authorization: Bearer ` + keyTemplatePlaceholder + `"`
	desktopTpl := `{
  "mcpServers": {
    "vayupress": {
      "url": "` + endpoint + `",
      "headers": { "Authorization": "Bearer ` + keyTemplatePlaceholder + `" }
    }
  }
}`

	// Say WHY the offered endpoint may differ from the address in the browser
	// bar. An endpoint that silently disagrees with the site being administered
	// reads as a mistake, and an operator who "corrects" it walks into the exact
	// failure the dedicated host exists to avoid.
	hostNote := ""
	switch {
	case dedicated:
		hostNote = `<p class="text-sm muted mt-2"><span class="badge badge--ok">dedicated host</span> This install has a working <code>` +
			html.EscapeString(strings.TrimSuffix(strings.TrimPrefix(endpoint, "https://"), "/mcp")) +
			`</code> host, so that is the endpoint offered here rather than <code>` + html.EscapeString(apex) +
			`</code>. Both reach this same server with the same authentication, but the dedicated host is not proxied — so a bot challenge on your main domain can never sit in front of it.</p>`
	case blockedHost != "":
		hostNote = `<p class="text-sm muted mt-2"><span class="badge badge--warn">blocked</span> <code>` +
			html.EscapeString(blockedHost) + `</code> exists, but a request to it was answered by <strong>something in front of this server</strong> rather than by VayuPress. Until that host answers directly, the endpoint above stays on your main domain.</p>`
	}

	oneClick := `<div class="card">
  <p class="text-sm muted">This needs <strong>no key</strong>. The Connect button lives on <strong>Claude's side</strong>, not on this page — this site runs the OAuth&nbsp;2.1 server it signs into.</p>
  <p class="text-sm muted mt-2">On <strong>claude.ai</strong> or Claude Desktop open <em>Settings → Connectors → Add custom connector</em>, paste the endpoint below, and click <strong>Connect</strong>. Claude signs you in through this site and shows an <strong>Approve&nbsp;&amp;&nbsp;connect</strong> screen where you choose Full&nbsp;control, Author or Read-only.</p>
  <div class="ak-token-row">
    <input id="cc-endpoint" class="input font-mono ak-token-input" type="text" readonly value="` + e + `">
    <button type="button" class="btn btn--sm" data-copy="#cc-endpoint">Copy</button>
  </div>` + hostNote + `
  <p class="field-hint mt-2">Custom connectors on claude.ai may require a paid plan (Pro/Max/Team/Enterprise). The Desktop and CLI routes below remain for clients that use a pasted key. Technical detail: <a href="/docs/adr/ADR-0140-vayu-mcp-oauth" target="_blank" rel="noopener">ADR-0140</a>.</p>
</div>`

	cliCard := `<div class="card">
  <p class="text-sm muted">Grant a key below, then run one command. This is the route a Claude Code agent uses, including when it is driven from a <a href="/os/buzz">Buzz</a> workspace.</p>
  ` + mcpSnippet("cc-cfg-cli", cliTpl) + `
</div>`

	desktopCard := `<div class="card">
  <p class="text-sm muted">Grant a key below, then add this to <code>claude_desktop_config.json</code> (Settings → Developer → Edit config) and restart Claude Desktop:</p>
  ` + mcpSnippet("cc-cfg-desktop", desktopTpl) + `
  <p class="field-hint mt-2">Prefer the one-click route above where it is available — it stores no token on disk.</p>
</div>`

	return `<div class="mon-stack">` +
		monAcc("✨", "One-click Connect on claude.ai", "Easiest — no key to copy or store", `<span class="mon-chip mon-chip--on">● Recommended</span>`, true, oneClick) +
		monAcc("⌨️", "Claude Code (CLI)", "One command", `<span class="mon-chip mon-chip--off">○ Needs a key</span>`, false, cliCard) +
		monAcc("🖥️", "Claude Desktop (config file)", "Paste a config block", `<span class="mon-chip mon-chip--off">○ Needs a key</span>`, false, desktopCard) +
		`</div>`
}

// osClaudeCodeGrantCard renders the one-click grants.
//
// Full control leads here, unlike the Buzz page. The common case for this page is
// an operator connecting their OWN assistant to their OWN site, where the whole
// point is that it can do the work; a Buzz agent sits in a shared channel and
// acts for several people, so its page leads with the narrow grant instead. Same
// key model, different default, for a stated reason.
func osClaudeCodeGrantCard() string {
	return `<div class="card">
  <p class="text-sm muted mb-4">Only the Desktop and CLI routes need a key — the one-click route above grants its own scope on Claude's approval screen. You can pause or revoke any grant instantly from the <a href="/os/connector">VayuMCP</a> page, and every action is written to the audit log.</p>

  <div class="cx-grant-grid">
    ` + mcpGrantTile(true, "Full control", "Everything", " badge--accent",
		"A superuser key. Every current and future VayuMCP tool becomes available — posts, pages, media, analytics, settings and more as the toolset grows. This is the \"give Claude the keys to the whole site\" option.",
		"*:*", claudeKeyLabelPrefix+" (full control)", "Grant full control") + `
    ` + mcpGrantTile(false, "Author only", "Posts &amp; pages", "",
		"Let Claude write, update and organise content — create and edit posts and pages, search and list — but nothing else. A safe default for a writing assistant.",
		"posts:read,posts:write", claudeKeyLabelPrefix+" (author)", "Grant author access") + `
    ` + mcpGrantTile(false, "Read only", "Look, don't touch", "",
		"Claude can read posts and pages, search content and read analytics, but cannot change anything. Ideal for analysis, reporting and audits.",
		"posts:read,analytics:read", claudeKeyLabelPrefix+" (read-only)", "Grant read-only access") + `
  </div>

  <p class="field-hint mt-2">Need a precise grant? Build a custom scoped key on the <a href="/os/apikeys">API Keys</a> page — the connector honours it exactly.</p>
</div>`
}

// osClaudeCodeTroubleshootCard is the proxy/WAF reference: essential when it
// bites, noise on every other visit, so it is folded away behind its own title.
func osClaudeCodeTroubleshootCard() string {
	proxy := `<div class="card">
  <p class="text-sm muted">Claude reaches this server <strong>machine-to-machine</strong> — no browser is in the loop for the API calls — so it <strong>cannot pass a JavaScript &ldquo;challenge&rdquo; / &ldquo;Just a moment&hellip;&rdquo; page</strong>. If your site is proxied with <em>Bot&nbsp;Fight&nbsp;Mode</em>, a <em>Managed&nbsp;Challenge</em>, a custom rule or <em>Under&nbsp;Attack</em> mode on, those requests are stopped <strong>before they reach VayuPress</strong> and Connect fails with &ldquo;couldn't register&rdquo; — the request never appears in this server's log, which is what makes it hard to diagnose.</p>
  <p class="text-sm muted">Let these exact paths <strong>bypass the challenge</strong>: <code>/mcp</code>, <code>/oauth/*</code> and <code>/.well-known/*</code>. <code>/mcp</code> matters <em>after</em> connecting too — every tool call runs over it, so a challenge there breaks the connector on first use.</p>
  <pre class="cx-code font-mono" id="cc-waf-expr">starts_with(http.request.uri.path, "/mcp") or
starts_with(http.request.uri.path, "/oauth/") or
starts_with(http.request.uri.path, "/.well-known/")</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#cc-waf-expr">Copy expression</button>
  </div>
  <p class="field-hint mt-2">Verify with a plain <code>curl</code> of your site's <code>/health</code> endpoint: it must return JSON, <strong>not</strong> a challenge page. When curl gets through, Claude will too. Full reference, including the dedicated-host workaround for proxies that cannot scope a challenge per path, lives on the <a href="/os/connector">VayuMCP</a> page.</p>
</div>`

	manage := `<div class="card">
  <p class="text-sm muted">Every client that has connected is listed on the <a href="/os/connector">VayuMCP</a> page with what it can reach, when it last called, and controls to pause, disconnect or remove it.</p>
  <p class="field-hint mt-2">A key that has never been used is almost always a leftover from a connect attempt that did not finish — safe to remove.</p>
</div>`

	return `<div class="mon-stack">` +
		monAcc("🛡️", "Behind a proxy or WAF?", "The most common reason Connect fails", `<span class="mon-chip mon-chip--off">○ Reference</span>`, false, proxy) +
		monAcc("🔌", "Manage connected clients", "Pause, disconnect or remove a grant", `<span class="mon-chip mon-chip--off">○ On VayuMCP</span>`, false, manage) +
		`</div>`
}
