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
	"html"
	"net/http"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/render"
)

// iconBuzz is a hive cell — the sidebar/hub glyph for the Buzz connector.
var iconBuzz = svgIcon("M10 2.6l6 3.45v6.9l-6 3.45-6-3.45v-6.9l6-3.45zM10 7.4a2.6 2.6 0 100 5.2 2.6 2.6 0 000-5.2z")

// buzzKeyLabelPrefix marks the keys this page mints, so the stat strip can count
// "agents granted access from here" without confusing them with keys granted to
// Claude or anything else. It is a label convention, not a permission: the key
// model does not know or care what a key is for.
const buzzKeyLabelPrefix = "Buzz agent"

// handleOSBuzz renders the Buzz connector console.
func (a *App) handleOSBuzz(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	endpoint, _, dedicated, blockedHost := connectorEndpoint(r)
	keys := a.liveConnectorKeys(r)

	body := osBuzzIntro() +
		osBuzzStats(endpoint, keys, dedicated, blockedHost) +
		`<div class="section-head"><span class="section-head__title">Connect Buzz to this site</span><span class="section-head__hint">Four steps — grant a key, point the agent here, run it, check it</span></div>` +
		osBuzzSetupCards(endpoint) +
		`<div class="section-head"><span class="section-head__title">Grant access</span><span class="section-head__hint">A Buzz agent is exactly as powerful as the key you give it</span></div>` +
		osBuzzGrantCard() +
		`<div class="section-head"><span class="section-head__title">About Buzz</span><span class="section-head__hint">What it is, and what this connector does not do</span></div>` +
		osBuzzAboutCard()

	full := adminOSShellHead(nonce, "Buzz", "buzz", cfg) +
		body +
		adminOSShellFoot(nonce, mcpClientScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// osBuzzIntro is the page header plus the one-time key banner.
func osBuzzIntro() string {
	return `<div class="page-header">
  <h1>Buzz</h1>
  <div class="page-actions">
    <a class="btn btn--sm" href="/docs/compatibility/buzz" target="_blank" rel="noopener">Buzz docs</a>
    <span id="` + mcpStatusID + `" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<p class="text-sm muted mb-4"><strong>Buzz</strong> is an open-source workspace where people and AI agents work together, built on the <strong>Nostr</strong> protocol — every member, human or agent, holds their own cryptographic keypair, so an agent signs its own work instead of borrowing someone's login. Its agents run <strong>Claude Code, Goose and Codex</strong>, and all three speak <strong>Model Context Protocol</strong>. This site already serves its whole toolset over MCP through <a href="/os/connector">VayuMCP</a> — so a Buzz agent can publish posts, build pages, search content and read analytics here <strong>without anything extra being installed on either side</strong>. Grant a key below and point the agent at your endpoint.</p>

` + mcpClientTokenBanner("This is the only time the full key is shown. It has been filled into the configurations below — copy the one your agent uses, then store the key somewhere safe. You will not be able to see it again.")
}

// osBuzzStats is the at-a-glance strip, scoped to the keys this page minted.
func osBuzzStats(endpoint string, keys []apikeys.Key, dedicated bool, blockedHost string) string {
	return mcpClientStats(endpoint, keys, buzzKeyLabelPrefix, "Buzz agents connected", dedicated, blockedHost)
}

// osBuzzSetupCards is the four-step walkthrough.
func osBuzzSetupCards(endpoint string) string {
	e := html.EscapeString(endpoint)

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

	step1 := `<div class="card">
  <p class="text-sm muted">Use <strong>Grant access</strong> further down this page to mint a key for the agent. Start with <strong>Author</strong> unless you have a reason not to — it lets the agent write and organise content but nothing else, and you can grant a wider key later without disconnecting anything.</p>
  <p class="field-hint mt-2">Each Buzz agent should get its own key. That way the audit log tells you which agent did what, and revoking one does not disconnect the others.</p>
</div>`

	step2 := `<div class="card">
  <p class="text-sm muted">Your endpoint is below. Buzz agents are ordinary MCP clients, so they connect the same way any other does — the URL plus an <code>Authorization: Bearer</code> header.</p>
  <div class="ak-token-row">
    <input id="bz-endpoint" class="input font-mono ak-token-input" type="text" readonly value="` + e + `">
    <button type="button" class="btn btn--sm" data-copy="#bz-endpoint">Copy</button>
  </div>
  <p class="text-sm muted mt-2"><strong>Claude Code</strong> — the agent Buzz drives most directly. Run this where the agent runs:</p>
  ` + mcpSnippet("bz-cfg-cli", cliTpl) + `
  <p class="text-sm muted mt-2"><strong>Goose, Codex, or any other MCP client</strong> — add a remote MCP server with the same URL and header. Most take this shape:</p>
  ` + mcpSnippet("bz-cfg-json", jsonTpl) + `
</div>`

	step3 := `<div class="card">
  <p class="text-sm muted">Start the agent from your Buzz workspace as you normally would. Buzz bridges it into the channel through its agent harness, and the tools this site exposes become available to it there — no extra bridge, relay or plugin in between.</p>
  <p class="field-hint mt-2">The agent reaches this server directly over HTTPS. Buzz relays your team's messages; it does not proxy these tool calls, so your content never travels through the workspace to get here.</p>
</div>`

	step4 := `<div class="card">
  <p class="text-sm muted">Ask the agent to list your most recent posts. If it answers with real titles from this site, the connector is live.</p>
  <p class="text-sm muted mt-2">If it cannot connect, the cause is almost always in front of this server rather than in it — a proxy or firewall challenging a client that has no browser to answer with. The <a href="/os/connector">VayuMCP</a> page has the paths to exempt and a dedicated-host workaround.</p>
  <p class="field-hint mt-2">Every call an agent makes is written to the audit log, and any grant can be paused or revoked from <a href="/os/connector">VayuMCP</a> at any time.</p>
</div>`

	return `<div class="mon-stack">` +
		monAcc("🔑", "Step 1 · Grant a key", "Choose how much the agent may do", `<span class="mon-chip mon-chip--on">● Start here</span>`, true, step1) +
		monAcc("🔗", "Step 2 · Point the agent at this site", "Copy the endpoint and config", `<span class="mon-chip mon-chip--off">○ Copy &amp; paste</span>`, false, step2) +
		monAcc("🐝", "Step 3 · Run the agent in Buzz", "It joins the channel with these tools", `<span class="mon-chip mon-chip--off">○ In Buzz</span>`, false, step3) +
		monAcc("✅", "Step 4 · Verify", "One question proves it works", `<span class="mon-chip mon-chip--off">○ Check</span>`, false, step4) +
		`</div>`
}

// osBuzzGrantCard renders the one-click grants.
//
// Author leads, and full control is not the primary button. The VayuMCP page
// leads with full control because its common case is an operator connecting
// their own assistant; a Buzz workspace is a team, and an agent in a shared
// channel acts for more than one person — so the safe grant should be the one
// that looks like the default.
func osBuzzGrantCard() string {
	return `<div class="card">
  <p class="text-sm muted mb-4">Pick how much of this site the Buzz agent may reach. You can pause or revoke any grant instantly from the <a href="/os/connector">VayuMCP</a> page, and every action an agent takes is written to the audit log.</p>

  <div class="cx-grant-grid">
    ` + mcpGrantTile(true, "Author", "Posts &amp; pages", "",
		"The agent can write, update and organise content — create and edit posts and pages, search and list — but nothing else. The right default for an agent that drafts and publishes alongside your team.",
		"posts:read,posts:write", buzzKeyLabelPrefix+" (author)", "Grant author access") + `
    ` + mcpGrantTile(false, "Read only", "Look, don't touch", "",
		"The agent can read posts and pages, search content and read analytics, but cannot change anything. Ideal for an agent that reports into a channel or answers questions about the site.",
		"posts:read,analytics:read", buzzKeyLabelPrefix+" (read-only)", "Grant read-only access") + `
    ` + mcpGrantTile(false, "Full control", "Everything", " badge--accent",
		"A superuser key — every current and future tool, including settings and infrastructure. Grant this only to an agent you would trust with your own login, and prefer one of the narrower grants above where it will do.",
		"*:*", buzzKeyLabelPrefix+" (full control)", "Grant full control") + `
  </div>

  <p class="field-hint mt-2">Need a precise grant? Build a custom scoped key on the <a href="/os/apikeys">API Keys</a> page — the connector honours it exactly.</p>
</div>`
}

// osBuzzAboutCard is reference material: what Buzz is, and an honest statement of
// what this connector does and does not do.
//
// The maturity note is deliberate. Buzz is young, and an operator deciding
// whether to move their team onto it deserves that from the page offering the
// integration rather than from a failed rollout.
func osBuzzAboutCard() string {
	about := `<div class="card">
  <p class="text-sm muted">Buzz is an open-source (Apache 2.0) workspace from Block that combines team chat, Git hosting and agent coordination on top of the Nostr protocol. Its distinguishing idea is identity: every participant, human or agent, holds a keypair that belongs to them rather than to the platform, so an agent's history and signatures travel with it.</p>
  <p class="text-sm muted mt-2">A workspace runs through a single relay that your team can host itself — Buzz calls this organisational sovereignty: self-host the relay and you hold the record. It is not a peer-to-peer mesh, and the distinction matters when you are deciding where your team's messages live.</p>
  <p class="field-hint mt-2">Buzz is early software and moving quickly. Its mobile client cannot yet create an identity on its own — it pairs with an existing desktop install — so set up on desktop first.</p>
</div>`

	scope := `<div class="card">
  <p class="text-sm muted">This connector runs in <strong>one direction</strong>: a Buzz agent reaches into this site and uses its tools. That is what makes it free of new moving parts — it rides the MCP endpoint this site already serves, so there is no extra service to run, no key material to store here, and nothing new leaving your server.</p>
  <p class="text-sm muted mt-2">It does <strong>not</strong> post from this site into a Buzz channel. That is the opposite direction and a genuinely different feature: it would mean signing Nostr events, holding a relay credential, and opening an outbound path that has to stay closed in a Tor Space. Keeping the two apart is deliberate — see <a href="/docs/adr/ADR-0146-buzz-connector" target="_blank" rel="noopener">ADR-0146</a>.</p>
</div>`

	return `<div class="mon-stack">` +
		monAcc("📖", "What Buzz is", "Open-source workspace on Nostr, by Block", `<span class="mon-chip mon-chip--off">○ Reference</span>`, false, about) +
		monAcc("🧭", "What this connector does", "Agents in — not posts out", `<span class="mon-chip mon-chip--off">○ Scope</span>`, false, scope) +
		`</div>`
}
