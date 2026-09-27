// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_buzz.go — the Buzz connector console (/os/buzz), ADR-0146.
//
// Buzz is Block's open-source workspace built on Nostr, where AI agents are
// members with their own cryptographic identity rather than bots wearing a
// human's credentials. Its agents run through an ACP harness that bridges them
// into workspace channels over MCP tools, and the agents it ships with — Claude
// Code, Goose, Codex — all speak MCP.
//
// That is the whole reason this page is thin. VayuPress already exposes its
// entire toolset over MCP at /mcp with an OAuth 2.1 server in front of it
// (VayuMCP, ADR-0139/0140). A Buzz agent is therefore already a supported
// client; nothing new has to be spoken, signed or dialled for one to publish a
// post or read analytics here. What was missing was not a protocol, it was an
// operator being told that this works and shown the four steps.
//
// So this page mints a scoped key and hands back a ready-to-paste agent
// configuration. It adds NO new backend surface: minting and revoking reuse the
// CSRF-protected API-key endpoints, exactly as /os/connector does, and a Buzz
// agent is precisely as powerful as the key granted here — never more. The
// banner, stat strip, grant tiles, snippets and controller all come from
// admin_os_mcpclient.go, shared with the Claude Code page.
//
// What this page deliberately is NOT: an outbound Nostr client. Publishing
// events INTO a Buzz relay would mean secp256k1 Schnorr signing, a relay
// credential in the keystore, and a new egress path to hold behind the Tor
// kill-switch. That is a different feature with a different risk profile, and
// ADR-0146 records why it was separated rather than bundled.

import (
	"net/http"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// buzzKeyLabelPrefix marks the keys this page mints, so the stat strip can count
// "agents granted access from here" without confusing them with keys granted to
// Claude or anything else. It is a label convention, not a permission: the key
// model does not know or care what a key is for.
const buzzKeyLabelPrefix = "Buzz agent"

// handleOSBuzz renders the Buzz connector page: four steps in, the endpoint,
// the grants, and an honest account of what Buzz is and what this does not do.
func (a *App) handleOSBuzz(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	endpoint, apex, dedicated, blockedHost := connectorEndpoint(r)

	body := osBuzzPage(endpoint, apex, dedicated, blockedHost, a.liveConnectorKeys(r))

	full := adminOSShellHead(nonce, "Buzz", "buzz", cfg) +
		body +
		adminOSShellFoot(nonce, mcpClientScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// osBuzzPage is the page body. It takes the request-derived endpoint and the
// keys as arguments so the tests render exactly what an operator sees.
func osBuzzPage(endpoint, apex string, dedicated bool, blockedHost string, keys []apikeys.Key) string {
	cliTpl := `claude mcp add --transport http vayupress ` + endpoint +
		` --header "Authorization: Bearer ` + keyTemplatePlaceholder + `"`
	jsonTpl := `{
  "mcpServers": {
    "vayupress": {
      "url": "` + endpoint + `",
      "headers": { "Authorization": "Bearer ` + keyTemplatePlaceholder + `" }
    }
  }
}`
	steps := ui.Rows(
		ui.Row{Icon: "key", Label: "1. Grant a key", Hint: "One key per agent, starting with Author, so the audit log tells agents apart and revoking one leaves the others connected.",
			Control: ui.State("accent", "Start here")},
		ui.Row{Icon: "link", Label: "2. Point the agent at this site", Hint: "The endpoint below, then the config for the agent's client: Claude Code, Goose, Codex or any MCP client.",
			Control: `<button type="button" class="btn btn--sm" data-sheet="bz-config">Show the config</button>`},
		ui.Row{Icon: "talk", Label: "3. Run the agent in Buzz", Hint: "It reaches this server directly over HTTPS. Buzz relays your team's messages but not these tool calls, so your content never travels through the workspace."},
		ui.Row{Icon: "check-c", Label: "4. Check it", Hint: "Ask the agent for your latest posts. Real titles from this site mean the connector is live; if it cannot connect, VayuMCP has the paths a proxy must let through."},
	)
	// Author leads, and full control is not the primary button. Claude Code
	// leads with full control because its common case is an operator
	// connecting their own assistant; a Buzz workspace is a team, and an agent
	// in a shared channel acts for more than one person, so the safe grant is
	// the one that looks like the default.
	grants := ui.Rows(
		mcpGrantRow(true, "Author", "Write and organise posts and pages; nothing else.", "posts:read,posts:write", buzzKeyLabelPrefix+" (author)", "Grant author access"),
		mcpGrantRow(false, "Read only", "Read posts, pages and analytics; change nothing.", "posts:read,analytics:read", buzzKeyLabelPrefix+" (read-only)", "Grant read-only access"),
		mcpGrantRow(false, "Full control", "Every tool, settings included. Only for an agent you would trust with your own login.", "*:*", buzzKeyLabelPrefix+" (full control)", "Grant full control"),
		ui.Row{Label: "A precise grant", Hint: "Any sections and actions you choose. Every grant can be paused or revoked on VayuMCP, and every action is audited.",
			Control: `<a class="settings-row-go" href="/os/apikeys">API keys` + ui.Icon("chev-r") + `</a>`},
	)
	// The maturity note in the reference is deliberate: Buzz is young, and an
	// operator deciding whether to move a team onto it deserves that from the
	// page offering the integration rather than from a failed rollout.
	reference := ui.Rows(
		ui.Row{Icon: "book", Label: "What Buzz is", Hint: "An open-source workspace from Block on the Nostr protocol, where people and agents each hold their own keypair.",
			Control: `<button type="button" class="btn btn--sm" data-sheet="bz-about">Read</button>`},
		ui.Row{Icon: "compass", Label: "What this connector does", Hint: "Agents reach into this site. It does not post from this site into a Buzz channel.",
			Control: `<button type="button" class="btn btn--sm" data-sheet="bz-scope">Read</button>`},
	)
	config := `<p class="text-sm muted mb-3"><strong>Claude Code</strong>, where the agent runs. Grant a key first; it is filled in.</p>` + mcpSnippet("bz-cfg-cli", cliTpl) +
		`<p class="text-sm muted mt-4 mb-3"><strong>Goose, Codex or any other MCP client</strong>:</p>` + mcpSnippet("bz-cfg-json", jsonTpl)

	return string(ui.SettingsPage("Buzz", mcpClientState(keys, buzzKeyLabelPrefix, "Buzz agent"),
		"Let an agent in a Buzz workspace publish, build pages and read analytics here, through VayuMCP.",
		ui.HTML(mcpClientTokenBanner("This is the only time the full key is shown. It has been filled into the configurations: copy the one your agent uses, then keep the key somewhere safe.")+
			`<p class="page-lead"><span id="`+mcpStatusID+`" role="status" aria-live="polite" class="text-xs muted"></span></p>`),
		ui.Section("Connect an agent", "Four steps", steps),
		ui.Section("Endpoint", "", ui.Rows(mcpEndpointRows("bz-endpoint", endpoint, apex, dedicated, blockedHost)...)),
		ui.Section("Grant access", "An agent is as powerful as its key", grants),
		ui.Section("About Buzz", "", reference),
		ui.Sheet("bz-config", "Point the agent at this site", ui.HTML(config)),
		ui.Sheet("bz-about", "What Buzz is", ui.HTML(`  <p class="text-sm muted">Buzz is an open-source (Apache 2.0) workspace from Block that combines team chat, Git hosting and agent coordination on top of the Nostr protocol. Its distinguishing idea is identity: every participant, human or agent, holds a keypair that belongs to them rather than to the platform, so an agent's history and signatures travel with it.</p>
  <p class="text-sm muted mt-2">A workspace runs through a single relay that your team can host itself — Buzz calls this organisational sovereignty: self-host the relay and you hold the record. It is not a peer-to-peer mesh, and the distinction matters when you are deciding where your team's messages live.</p>
  <p class="field-hint mt-2">Buzz is early software and moving quickly. Its mobile client cannot yet create an identity on its own — it pairs with an existing desktop install — so set up on desktop first.</p>
`)),
		ui.Sheet("bz-scope", "What this connector does", ui.HTML(`  <p class="text-sm muted">This connector runs in <strong>one direction</strong>: a Buzz agent reaches into this site and uses its tools. That is what makes it free of new moving parts — it rides the MCP endpoint this site already serves, so there is no extra service to run, no key material to store here, and nothing new leaving your server.</p>
  <p class="text-sm muted mt-2">It does <strong>not</strong> post from this site into a Buzz channel. That is the opposite direction and a genuinely different feature: it would mean signing Nostr events, holding a relay credential, and opening an outbound path that has to stay closed in a Tor Space. Keeping the two apart is deliberate — see <a href="/docs/adr/ADR-0146-buzz-connector" target="_blank" rel="noopener">ADR-0146</a>.</p>
`)),
	))
}
