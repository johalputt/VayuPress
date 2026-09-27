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
	"net/http"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// claudeKeyLabelPrefix marks the keys this page mints, so its stat strip counts
// Claude clients rather than every connector on the install.
const claudeKeyLabelPrefix = "Claude"

// handleOSClaudeCode renders the Claude Code connector page: the routes in,
// the endpoint, the grants, and what to do when Connect fails.
func (a *App) handleOSClaudeCode(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	endpoint, apex, dedicated, blockedHost := connectorEndpoint(r)

	body := osClaudeCodePage(endpoint, apex, dedicated, blockedHost, a.liveConnectorKeys(r))

	full := adminOSShellHead(nonce, "Claude Code", "claudecode", cfg) +
		body +
		adminOSShellFoot(nonce, mcpClientScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// osClaudeCodePage is the page body. It takes the request-derived endpoint and
// the keys as arguments so the tests render exactly what an operator sees.
func osClaudeCodePage(endpoint, apex string, dedicated bool, blockedHost string, keys []apikeys.Key) string {
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
	// The one-click route leads, and only it reads Recommended: it needs no
	// key, so there is no token to leak, paste wrongly, or forget to revoke.
	connect := ui.Rows(
		ui.Row{Icon: "sparkle", Label: "One-click Connect on claude.ai", Hint: "In Claude, open Settings, Connectors, Add custom connector; paste the endpoint below and press Connect. Claude signs in through this site and you approve a scope on screen. Custom connectors on claude.ai may need a paid plan.",
			Control: ui.State("ok", "Recommended")},
		ui.Row{Icon: "keyboard", Label: "Claude Code", Hint: "Grant a key below, then run one command. The route a Claude Code agent uses, including one driven from a Buzz workspace.",
			Control: ui.State("neutral", "Needs a key") + `<button type="button" class="btn btn--sm" data-sheet="cc-cli">Show the command</button>`},
		ui.Row{Icon: "monitor", Label: "Claude Desktop", Hint: "Grant a key below, then add a block to claude_desktop_config.json and restart Claude Desktop. The one-click route stores no token on disk.",
			Control: ui.State("neutral", "Needs a key") + `<button type="button" class="btn btn--sm" data-sheet="cc-desktop">Show the config</button>`},
	)
	// Full control leads here, unlike on Buzz. The common case for this page is
	// an operator connecting their OWN assistant to their OWN site, where the
	// whole point is that it can do the work; a Buzz agent sits in a shared
	// channel and acts for several people, so its page leads with the narrow
	// grant instead. Same key model, different default, for a stated reason.
	grants := ui.Rows(
		mcpGrantRow(true, "Full control", "Every tool, now and as the toolset grows.", "*:*", claudeKeyLabelPrefix+" (full control)", "Grant full control"),
		mcpGrantRow(false, "Author", "Write and organise posts and pages; nothing else.", "posts:read,posts:write", claudeKeyLabelPrefix+" (author)", "Grant author access"),
		mcpGrantRow(false, "Read only", "Read posts, pages and analytics; change nothing.", "posts:read,analytics:read", claudeKeyLabelPrefix+" (read-only)", "Grant read-only access"),
		ui.Row{Label: "A precise grant", Hint: "Any sections and actions you choose. Every grant can be paused or revoked on VayuMCP, and every action is audited.",
			Control: `<a class="settings-row-go" href="/os/apikeys">API keys` + ui.Icon("chev-r") + `</a>`},
	)
	fails := ui.Rows(
		ui.Row{Icon: "shield", Label: "Behind a proxy or firewall?", Hint: "The most common reason Connect fails: a challenge page answers instead of this server.",
			Control: `<button type="button" class="btn btn--sm" data-sheet="cc-proxy">What to allow</button>`},
		ui.Row{Icon: "plug", Label: "Connected clients", Hint: "Pause, disconnect or remove a client. A key never used is almost always a connect attempt that did not finish, and safe to remove.",
			Control: `<a class="settings-row-go" href="/os/connector">VayuMCP` + ui.Icon("chev-r") + `</a>`},
	)
	proxy := `<p class="text-sm muted">Claude reaches this server machine to machine, with no browser to answer a challenge page. Bot Fight Mode, a Managed Challenge, a custom rule or Under Attack mode stops those requests before they reach VayuPress, so Connect fails with "couldn't register" and nothing appears in this server's log.</p>
<p class="text-sm muted mt-3">Let these paths skip the challenge. <code>/mcp</code> matters after connecting too: every tool call runs over it.</p>
<pre class="cx-code font-mono" id="cc-waf-expr">starts_with(http.request.uri.path, "/mcp") or
starts_with(http.request.uri.path, "/oauth/") or
starts_with(http.request.uri.path, "/.well-known/")</pre>
<div class="mt-3"><button type="button" class="btn btn--sm" data-copy="#cc-waf-expr">Copy the expression</button></div>
<p class="field-hint mt-3">Check with <code>curl</code> on your site's <code>/health</code>: it must return JSON, not a challenge page. When curl gets through, Claude will too. The dedicated-host route, for proxies that cannot scope a challenge per path, is on the <a href="/os/connector">VayuMCP</a> page.</p>`

	return string(ui.SettingsPage("Claude Code", mcpClientState(keys, claudeKeyLabelPrefix, "Claude client"),
		"Run this site by chat from Claude Code, Claude Desktop or claude.ai, through VayuMCP. The one-click route needs no key.",
		ui.HTML(mcpClientTokenBanner("This is the only time the full key is shown. It has been filled into the Claude Code and Desktop configurations: copy the one you need, then keep the key somewhere safe.")+
			`<p class="page-lead"><span id="`+mcpStatusID+`" role="status" aria-live="polite" class="text-xs muted"></span></p>`),
		ui.Section("Connect Claude", "The first needs no key", connect),
		ui.Section("Endpoint", "", ui.Rows(mcpEndpointRows("cc-endpoint", endpoint, apex, dedicated, blockedHost)...)),
		ui.Section("Grant access", "Only Claude Code and Desktop need a key", grants),
		ui.Section("If Connect fails", "", fails),
		ui.Sheet("cc-cli", "Claude Code", ui.HTML(`<p class="text-sm muted mb-3">Grant a key first; it is filled in here.</p>`+mcpSnippet("cc-cfg-cli", cliTpl))),
		ui.Sheet("cc-desktop", "Claude Desktop", ui.HTML(`<p class="text-sm muted mb-3">Add this to <code>claude_desktop_config.json</code> (Settings, Developer, Edit config) and restart Claude Desktop.</p>`+mcpSnippet("cc-cfg-desktop", desktopTpl))),
		ui.Sheet("cc-proxy", "Behind a proxy or firewall", ui.HTML(proxy)),
	))
}
