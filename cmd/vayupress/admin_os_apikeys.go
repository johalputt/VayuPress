// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/ui"
)

// iconVCB is a shield-with-check mark — the "validated compatibility" glyph for
// the one-click Vayu Compatibility Bible link in the console header.
var iconVCB = svgIcon("M10 2.5l5.5 1.8v4.7c0 3.6-2.7 6-5.5 6.9-2.8-.9-5.5-3.3-5.5-6.9V4.3L10 2.5zM7.6 9.4l1.7 1.7 3.1-3.5")

// providerMeta describes a first-class third-party integration surfaced as its
// own card in the API Keys console. The Provider value matches a slug in the
// secrets package; HasEndpoint controls whether an endpoint/URL field is shown.
type providerMeta struct {
	Provider     string
	Title        string
	Desc         string
	SecretLabel  string
	SecretPH     string
	HasEndpoint  bool
	EndpointPH   string
	EndpointHint string
}

// knownProviders are the built-in integrations with tailored UI. "custom"
// covers anything else and is handled by the add-credential form.
var knownProviders = []providerMeta{
	{
		Provider:    secrets.ProviderIndexNow,
		Title:       "IndexNow",
		Desc:        "Instantly notify participating search engines whenever you publish or update a post. VayuPress already submits URLs automatically — add a key here to switch it on (no file upload needed; the verification file is served for you).",
		SecretLabel: "IndexNow key",
		SecretPH:    "32+ character key (letters and digits)",
	},
	{
		Provider:     secrets.ProviderOpenRouter,
		Title:        "OpenRouter",
		Desc:         "Hosted access to a wide range of AI models through a single key. Used by the writing assistant when configured.",
		SecretLabel:  "API key",
		SecretPH:     "sk-or-...",
		HasEndpoint:  true,
		EndpointPH:   "https://openrouter.ai/api/v1",
		EndpointHint: "Base URL — leave blank to use the default.",
	},
	{
		Provider:     secrets.ProviderOpenAI,
		Title:        "OpenAI",
		Desc:         "Use OpenAI's models for the writing assistant. Enter your API key; the base URL defaults to OpenAI and can point at any OpenAI-compatible gateway.",
		SecretLabel:  "API key",
		SecretPH:     "sk-...",
		HasEndpoint:  true,
		EndpointPH:   "https://api.openai.com/v1",
		EndpointHint: "Base URL — leave blank to use OpenAI's default.",
	},
	{
		Provider:     secrets.ProviderOllama,
		Title:        "Local AI (Ollama)",
		Desc:         "Connect a self-hosted model runtime so AI features run on infrastructure you control. No data leaves your server.",
		SecretLabel:  "API key (optional)",
		SecretPH:     "Leave blank if your runtime needs no key",
		HasEndpoint:  true,
		EndpointPH:   "http://localhost:11434",
		EndpointHint: "Endpoint URL of your local model runtime.",
	},
	{
		Provider:     secrets.ProviderN8N,
		Title:        "n8n automation",
		Desc:         "Trigger automation workflows by calling an n8n webhook — wire VayuPress events into hundreds of downstream apps.",
		SecretLabel:  "Webhook token / API key",
		SecretPH:     "Optional bearer token for the webhook",
		HasEndpoint:  true,
		EndpointPH:   "https://n8n.example.com/webhook/abc123",
		EndpointHint: "Webhook URL n8n exposes for the workflow.",
	},
}

// handleIndexNowKeyFile serves the IndexNow ownership-verification file at
// /.well-known/<key>.txt. Search engines fetch this URL and require the body to
// equal the key. We serve it only when the requested filename matches the
// active key (managed in the API Keys console, with env fallback), so IndexNow
// works without the operator ever uploading a static file. Anything else 404s.
func (a *App) handleIndexNowKeyFile(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimSpace(chi.URLParam(r, "file"))
	key := a.cachedIndexNowKey() // already trimmed at the source
	if key == "" || file != key+".txt" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(key))
}

// InternalAPIKey returns the live value of the auto-provisioned internal/system
// API key, for internal automation (plugins, background jobs) that needs to
// authenticate to the VayuPress API in-process. Reading it at use time means a
// rotation of the system key propagates automatically with no manual step.
func (a *App) InternalAPIKey() string {
	if a.apiKeys == nil {
		return ""
	}
	return a.apiKeys.InternalKey()
}

// handleOSAPIKeys renders the VayuOS API Keys console: VayuPress's own issued
// bearer tokens (create / rotate / revoke) and encrypted third-party service
// credentials (IndexNow, OpenRouter, Ollama, n8n, custom).
func (a *App) handleOSAPIKeys(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	var keys []apikeys.Key
	if a.apiKeys != nil {
		keys, _ = a.apiKeys.List(r.Context())
	}
	var creds []secrets.Credential
	if a.secrets != nil {
		creds, _ = a.secrets.List(r.Context())
	}

	body := osAPIKeysPage(keys, creds)

	// This page hosts the filter island (x-data="filterList"), so it opts into
	// the Alpine runtime; pageUsesAlpine keeps the decision tied to the markup.
	full := adminOSShellHead(nonce, "API Keys", "apikeys", cfg) +
		body +
		adminOSShellFoot(nonce, osAPIKeysScript, pageUsesAlpine(body))
	writeOSHTML(w, r, full)
}

// apiBaseURL returns the recommended public base URL for the REST API. When a
// dedicated API host is configured (VAYUOS_API_HOST — e.g. api.<domain>, a
// proxy-off host that machine clients reach without a bot-challenge) it is used;
// otherwise the API is advertised on the apex domain.
func apiBaseURL() string {
	host := strings.TrimSpace(config.Cfg.APIHost)
	if host == "" {
		host = config.Cfg.Domain
	}
	if host == "" || host == "localhost" {
		return "/api/v1"
	}
	return "https://" + host + "/api/v1"
}

// osAPIKeysPage is the page body, apart from the request so the tests render
// exactly what an operator sees.
func osAPIKeysPage(keys []apikeys.Key, creds []secrets.Credential) string {
	return string(ui.SettingsPage("API keys", osAPIKeysState(keys),
		"Keys for your API, and the credentials VayuPress uses with other services.",
		ui.HTML(`<div id="ak-token-banner" class="card ak-token-banner" hidden>
  <div class="settings-block-title">Copy your new key now</div>
  <p class="text-sm muted">This is the only time the full key is shown. Store it somewhere safe; you won't be able to see it again.</p>
  <div class="ak-token-row">
    <input id="ak-token-value" class="input font-mono ak-token-input" type="text" readonly>
    <button type="button" class="btn btn--sm" id="ak-token-copy">Copy</button>
    <button type="button" class="btn btn--primary btn--sm" id="ak-token-done">Done</button>
  </div>
</div>
<p class="page-lead"><span id="ak-status" role="status" aria-live="polite" class="text-xs muted"></span></p>`),
		ui.Section("Your API", "Where scripts, CI and agents send their calls", ui.Rows(osAPIBaseRows()...)),
		ui.Section("Issued keys", "Each key can do only what it was granted", ui.HTML(osAPIKeysOwnSection(keys))),
		ui.Section("Third-party services", "Credentials VayuPress uses to reach other providers", ui.HTML(osAPIKeysServicesSection(creds))),
		ui.Section("Extension compatibility", "The contract an add-on must satisfy before it loads", osAPIKeysVCB()),
	))
}

// osAPIBaseRows show the API base URL operators point scripts, CI and AI
// agents at, and which host serves it. Copy avoids the literal "CDN" per the
// CSP-safe-prose rule.
func osAPIBaseRows() []ui.Row {
	host, hint := ui.State("neutral", "Main domain"), "Behind a proxy that challenges visitors? A script or agent cannot solve a bot challenge. Point a dedicated api.<your-domain> record straight at this server with the proxy off (DNS only), set VAYUOS_API_HOST=api.<your-domain> and re-run the installer: VayuPress serves a hardened, API-only host there."
	if h := strings.TrimSpace(config.Cfg.APIHost); h != "" {
		host, hint = ui.State("ok", "Dedicated host"), "Served on "+h+", set by VAYUOS_API_HOST and pointed straight at this server with the proxy off. Only /api and /health are exposed there, not the console, and VayuShield still guards it."
	}
	return []ui.Row{
		{Label: "Base URL", Hint: "For scripts, CI jobs and agents, with a key sent as Authorization: Bearer.",
			Control: ui.HTML(`<code class="space-addr" id="ak-apibase">` + html.EscapeString(apiBaseURL()) + `</code><button type="button" class="btn btn--sm" data-copy="#ak-apibase">Copy</button>`)},
		{Label: "Host", Hint: hint, Control: host},
	}
}

// apiKeyCapabilitySummary renders a compact set of capability badges for a key's
// grant set (or a single "Full access" badge for a superuser/legacy key).
func apiKeyCapabilitySummary(k apikeys.Key) string {
	if k.Scope == apikeys.ScopeInternal || k.Permissions.IsSuperuser() {
		return string(ui.State("accent", "Full access"))
	}
	caps := k.Permissions.Capabilities()
	if len(caps) == 0 {
		return string(ui.State("neutral", "No grants"))
	}
	// Collapse a section whose every action is granted to "section:*".
	out := ""
	shown := 0
	for _, c := range caps {
		if shown >= 8 {
			out += `<span class="ak-cap-more">+` + itoaSafe(len(caps)-shown) + ` more</span>`
			break
		}
		out += `<span class="ak-cap">` + html.EscapeString(c) + `</span>`
		shown++
	}
	return `<span class="ak-caps">` + out + `</span>`
}

// osAPIKeysState is the page's state beside its title.
//
// Full-access keys are named on their own, as a warning, whenever any exist. A
// superuser key can do anything the site can, and this page issues them from
// a single checkbox; an operator should see how many are live without reading
// the table. Counting only USABLE keys matters as much: a revoked or expired
// grant is not exposure, and inflating the number would make the one figure
// that should provoke a reaction easy to ignore. The system key is managed by
// VayuPress, not issued by the operator, so it is not counted at all.
func osAPIKeysState(keys []apikeys.Key) ui.HTML {
	now := time.Now().UTC()
	live, full, idle := 0, 0, 0
	for _, k := range keys {
		if k.Scope == apikeys.ScopeInternal {
			continue
		}
		if k.Revoked || !k.Active || (k.ExpiresAt != nil && !k.ExpiresAt.After(now)) {
			idle++
			continue
		}
		live++
		if k.Permissions.IsSuperuser() {
			full++
		}
	}
	st := ui.State("neutral", "No keys issued")
	if live > 0 {
		st = ui.State("ok", strconv.Itoa(live)+" key"+plural(live)+" active")
	}
	if idle > 0 {
		st += " " + ui.State("neutral", strconv.Itoa(idle)+" inactive")
	}
	if full > 0 {
		st += " " + ui.State("warn", strconv.Itoa(full)+" with full access")
	}
	return st
}

// osAPIKeysOwnSection renders the issued-token list and the scoped-key create
// form (permission grid + expiry + rate). CSP-safe: no inline styles, all layout
// via utility/component classes.
func osAPIKeysOwnSection(keys []apikeys.Key) string {
	rows, sheets := "", ""
	for _, k := range keys {
		var status, actions string
		if k.Scope == apikeys.ScopeInternal {
			// No actions. The store refuses every lifecycle operation on the internal
			// key by design — rotating it would return a fresh unconditional
			// superuser token to the caller (audit C2) — so offering Rotate here was
			// a button that could only ever produce an error. A control that cannot
			// succeed is worse than no control: it reads as a capability, and its
			// failure reads as a bug rather than as the protection it actually is.
			status = string(ui.State("neutral", "System, auto-managed"))
			actions = `<span class="text-xs muted">Protected</span>` + string(ui.Tip("Not rotatable or revocable: VayuPress manages this key itself."))
		} else if k.Revoked {
			status = string(ui.State("neutral", "Revoked"))
			actions = `<button type="button" class="btn btn--sm" data-action="ak-delete" data-id="` + html.EscapeString(k.ID) + `">Delete</button>`
		} else if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now().UTC()) {
			status = string(ui.State("warn", "Expired"))
			actions = `<button type="button" class="btn btn--sm" data-action="ak-delete" data-id="` + html.EscapeString(k.ID) + `">Delete</button>`
		} else if !k.Active {
			status = string(ui.State("neutral", "Inactive"))
			actions = `<button type="button" class="btn btn--sm" data-action="ak-activate" data-id="` + html.EscapeString(k.ID) + `">Activate</button>
        <button type="button" class="btn btn--sm" data-action="ak-revoke" data-id="` + html.EscapeString(k.ID) + `">Revoke</button>`
		} else {
			status = string(ui.State("ok", "Active"))
			actions = `<button type="button" class="btn btn--sm" data-action="ak-rotate" data-id="` + html.EscapeString(k.ID) + `">Rotate</button>
        <button type="button" class="btn btn--sm" data-action="ak-deactivate" data-id="` + html.EscapeString(k.ID) + `">Deactivate</button>
        <button type="button" class="btn btn--sm" data-action="ak-revoke" data-id="` + html.EscapeString(k.ID) + `">Revoke</button>`
		}
		last := "Never"
		if k.LastUsedAt != nil {
			last = config.FormatSite(*k.LastUsedAt, "2006-01-02 15:04 MST")
		}
		expiry := "—"
		if k.ExpiresAt != nil {
			expiry = config.FormatSite(*k.ExpiresAt, "2006-01-02")
		}
		// A key's lifecycle controls are in its sheet, as a connector's are on
		// VayuMCP. In the row they were up to three buttons wide, and the table
		// scrolled sideways on a laptop.
		manage := actions
		if k.Scope != apikeys.ScopeInternal {
			id := "ak-key-" + k.ID
			manage = `<button type="button" class="btn btn--sm" data-sheet="` + html.EscapeString(id) + `">Manage</button>`
			sheets += string(ui.Sheet(id, k.Label, ui.HTML(`<div class="cx-details">`+
				connectorDetailRow("Key", `<code class="font-mono">`+html.EscapeString(apikeys.Mask(k.Prefix))+`</code>`)+
				connectorDetailRow("State", status)+
				connectorDetailRow("Can reach", apiKeyCapabilitySummary(k))+
				connectorDetailRow("Expires", html.EscapeString(expiry))+
				connectorDetailRow("Last used", html.EscapeString(last))+
				`</div>
<div class="ak-cred-actions">`+actions+`
  <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
</div>`)))
		}
		rows += `<tr data-filter-text="` + html.EscapeString(k.Label+" "+k.Prefix) + `">
      <td><div class="ak-key-label">` + html.EscapeString(k.Label) + `</div><code class="font-mono text-xs muted">` + html.EscapeString(apikeys.Mask(k.Prefix)) + `</code></td>
      <td>` + apiKeyCapabilitySummary(k) + `</td>
      <td class="text-xs muted">` + html.EscapeString(last) + `</td>
      <td>` + status + `</td>
      <td class="ak-row-actions">` + manage + `</td>
    </tr>`
	}
	if rows == "" {
		rows = `<tr><td colspan="5" class="text-sm muted ak-empty">No keys issued yet. Create one to authenticate API requests.</td></tr>`
	}

	// x-data="filterList" powers the live client-side filter below (ADR-0136,
	// vayu-islands.js). It is a pure enhancement: if Alpine is absent the input
	// is inert and every row stays visible, and the create/rotate/revoke flows
	// (vanilla JS) are untouched.
	create := ui.Rows(ui.Row{Icon: "key", Label: "Create a key", Hint: "Grant exactly the sections and actions you choose. The full key is shown once, when it is made.",
		Control: `<button type="button" class="btn btn--primary btn--sm" data-sheet="ak-create">Create a key</button>`})
	list := string(create) + `<div class="mt-4" x-data="filterList" data-filter-noun="keys">
  <p class="text-sm muted mb-4">Send a key as a header.` + string(ui.Tip("Send a key as the X-API-Key header or Authorization: Bearer <key>. Rotating invalidates the old value immediately; deactivating disables a key reversibly; revoking disables it permanently, keeping its audit row. The System key is managed automatically for internal use.")) + `</p>
  <div class="ak-filter"><input type="search" class="input ak-filter-input" placeholder="Filter keys by label or prefix…" x-model="q" @input="apply()" aria-label="Filter API keys"><span data-filter-status role="status" aria-live="polite" class="vp-sr-only"></span></div>
  <div class="table-wrap">
    <table class="table ak-table">
      <thead><tr><th>Label</th><th>Permissions</th><th>Last used</th><th>Status</th><th></th></tr></thead>
      <tbody>` + rows + `<tr data-filter-empty hidden><td colspan="5" class="text-sm muted ak-empty">No keys match your filter.</td></tr></tbody>
    </table>
  </div>
  <p class="field-hint mt-2">The <code>API_KEY</code> root key is not listed here.` + string(ui.Tip("A root key set through the API_KEY environment variable always remains valid as a bootstrap credential with full access.")) + `</p>
</div>
`
	return list + sheets + string(ui.Sheet("ak-create", "Create a key", osAPIKeysCreateForm()))
}

// osAPIKeysCreateForm is the scoped-key create form, shown in a sheet: a 12×6
// permission grid (section rows × action columns) with per-row and grand
// "select all" toggles, plus optional expiry and a per-key rate budget. It is
// the largest control on the page and needed only while issuing a key, so it
// does not stand between the operator and the key list they came to read.
func osAPIKeysCreateForm() ui.HTML {
	// Column header.
	head := `<th scope="col" class="ak-grid-section">Section</th><th scope="col" class="ak-grid-all">All</th>`
	for _, act := range apikeys.AllActions {
		head += `<th scope="col">` + html.EscapeString(string(act)) + `</th>`
	}

	body := ""
	for _, sec := range apikeys.AllSections {
		s := html.EscapeString(string(sec))
		cells := `<th scope="row" class="ak-grid-section">` + s + `</th>` +
			`<td><input type="checkbox" class="ak-perm-all" data-section="` + s + `" aria-label="All ` + s + ` actions"></td>`
		for _, act := range apikeys.AllActions {
			a := html.EscapeString(string(act))
			cells += `<td><input type="checkbox" class="ak-perm" data-section="` + s + `" data-action="` + a + `" aria-label="` + s + `:` + a + `"></td>`
		}
		body += `<tr>` + cells + `</tr>`
	}

	return ui.HTML(`<div class="ak-create-row">
    <div class="field ak-field-grow">
      <label class="field-label" for="ak-new-label">Label</label>
      <input id="ak-new-label" class="input" type="text" placeholder="e.g. Theme builder, Zapier, CI">
    </div>
    <div class="field">
      <label class="field-label" for="ak-new-expiry">Expires (optional)</label>
      <input id="ak-new-expiry" class="input" type="datetime-local">
    </div>
    <div class="field ak-field-narrow">
      <label class="field-label" for="ak-new-rate">Rate / min</label>
      <input id="ak-new-rate" class="input" type="number" min="0" step="1" placeholder="600">
    </div>
  </div>
  <div class="ak-grid-toolbar">
    <span class="field-label">Permissions</span>
    <label class="ak-superuser"><input type="checkbox" id="ak-perm-super"> <span>Full access (all sections &amp; actions)</span></label>
  </div>
  <div class="table-wrap">
    <table class="table ak-grid" id="ak-perm-grid">
      <thead><tr>` + head + `</tr></thead>
      <tbody>` + body + `</tbody>
    </table>
  </div>
  <div class="ak-create-actions">
    <button type="button" class="btn btn--primary" id="ak-create-btn">Create key</button>
    <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
  </div>`)
}

// osAPIKeysVCB is the gateway to the Vayu Compatibility Bible (VCB, ADR-0135):
// what to grant an extension, where the full contract lives, and how to
// validate a plugin or theme before trusting it. Every action is a same-origin
// link.
func osAPIKeysVCB() ui.HTML {
	return ui.Rows(ui.Row{Icon: "book", Label: "Vayu Compatibility Bible", Hint: "Validate a plugin or theme against the contract this API enforces, then grant it only what it declares.",
		Control: `<button type="button" class="btn btn--sm" data-sheet="ak-vcb">How it works</button><a class="settings-row-go" href="/docs/compatibility/vcb" target="_blank" rel="noopener">Open` + ui.Icon("chev-r") + `</a>`}) +
		ui.Sheet("ak-vcb", "Vayu Compatibility Bible", ui.HTML(`<p class="text-sm muted mb-4">Before you trust a plugin or theme, validate it against the <strong>same contract this API enforces</strong>. An extension declares the hooks, capabilities and <code>section:action</code> permissions it needs; VCB checks them and you mint a key granting <strong>only</strong> those, never more. Themes that fetch from another host, plugins that over-ask, or manifests built against a hook that doesn't exist are refused with a plain, exact reason.</p>
<div class="ak-cred-actions">
  <a class="btn btn--primary btn--sm" href="/docs/compatibility/vcb" target="_blank" rel="noopener">`+iconVCB+` Open the Compatibility Bible</a>
  <a class="btn btn--sm" href="/docs/compatibility/vayuapi" target="_blank" rel="noopener">API keys &amp; permissions reference</a>
  <a class="btn btn--sm" href="/docs/adr/ADR-0135-vayu-compatibility-bible" target="_blank" rel="noopener">Design record (ADR-0135)</a>
</div>
<p class="field-hint mt-2">Build tools can read the live contract at <code>GET /api/v1/vcb/contract</code> and check a manifest against this running host at <code>POST /api/v1/vcb/validate</code> (both need a key with <code>plugins:read</code>). The <code>vayu-compat</code> CLI runs the same checks offline for CI.</p>`))
}

// osAPIKeysServicesSection lists each known provider and every custom
// credential as a row with its state; the fields that set one up open in a
// sheet.
func osAPIKeysServicesSection(creds []secrets.Credential) string {
	var custom []secrets.Credential
	firstByProvider := map[string]secrets.Credential{}
	for _, c := range creds {
		if c.Provider == secrets.ProviderCustom {
			custom = append(custom, c)
			continue
		}
		if _, ok := firstByProvider[c.Provider]; !ok {
			firstByProvider[c.Provider] = c
		}
	}

	var rows []ui.Row
	sheets := ""
	for _, p := range knownProviders {
		c := firstByProvider[p.Provider]
		id := "ak-cred-" + p.Provider
		rows = append(rows, ui.Row{Label: p.Title, Hint: firstSentence(p.Desc),
			Control: credState(c) + ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="`+html.EscapeString(id)+`">`+credVerb(c)+`</button>`)})
		sheets += string(ui.Sheet(id, p.Title, ui.HTML(osAPIKeysProviderCard(p, c))))
	}
	for _, c := range custom {
		id := "ak-cc-" + c.ID
		hint := "No endpoint"
		if c.Endpoint != "" {
			hint = c.Endpoint
		}
		rows = append(rows, ui.Row{Label: c.Label, Hint: hint,
			Control: credState(c) + ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="`+html.EscapeString(id)+`">Edit</button>`)})
		sheets += string(ui.Sheet(id, c.Label, ui.HTML(osAPIKeysCustomRow(c))))
	}
	rows = append(rows, ui.Row{Icon: "plus", Label: "Another service", Hint: "Any other service, by name.",
		Control: `<button type="button" class="btn btn--sm" data-sheet="ak-cc-new">Add a credential</button>`})
	sheets += string(ui.Sheet("ak-cc-new", "Add a credential", ui.HTML(`<div class="field">
  <label class="field-label" for="cc-label">Name</label>
  <input id="cc-label" class="input" type="text" placeholder="e.g. Sendgrid, Pushover">
</div>
<div class="field">
  <label class="field-label" for="cc-endpoint">Endpoint (optional)</label>
  <input id="cc-endpoint" class="input" type="text" placeholder="https://…">
</div>
<div class="field">
  <label class="field-label" for="cc-secret">Secret</label>
  <input id="cc-secret" class="input" type="password" placeholder="API key / token" autocomplete="new-password">
</div>
<div class="ak-cred-actions">
  <button type="button" class="btn btn--primary btn--sm" id="cc-add-btn">Add</button>
  <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
</div>`)))

	return `<p class="text-sm muted mb-3">Secrets are encrypted before they are stored and shown only masked afterwards.` +
		string(ui.Tip("Encrypted at rest with AES-256-GCM; they never leave your server in clear text. Every issue, rotate, revoke and reveal is written to the audit log.")) + `</p>` +
		string(ui.Rows(rows...)) + sheets
}

// credState is a stored credential's state as a dot and a word. A provider
// with no record has never been set up, which is different from one the
// operator switched off.
func credState(c secrets.Credential) ui.HTML {
	switch {
	case c.ID == "":
		return ui.State("neutral", "Not set up")
	case !c.Enabled:
		return ui.State("neutral", "Off")
	}
	return ui.State("ok", "On")
}

// firstSentence is a description's first sentence, for a row's hint; the
// whole description is in the sheet the row opens.
func firstSentence(s string) string {
	first, _ := ui.FirstSentence(s)
	return first
}

// credVerb names the row's button for what it will do.
func credVerb(c secrets.Credential) string {
	if c.ID == "" {
		return "Set up"
	}
	return "Edit"
}

// osAPIKeysProviderCard is one known provider's fields, shown in its sheet.
func osAPIKeysProviderCard(p providerMeta, c secrets.Credential) string {
	endpointField := ""
	if p.HasEndpoint {
		hint := ""
		if p.EndpointHint != "" {
			hint = `<span class="field-hint">` + html.EscapeString(p.EndpointHint) + `</span>`
		}
		endpointField = `<div class="field">
    <label class="field-label">Endpoint</label>
    <input class="input" type="text" data-cred-endpoint value="` + html.EscapeString(c.Endpoint) + `" placeholder="` + html.EscapeString(p.EndpointPH) + `">
    ` + hint + `
  </div>`
	}
	hintLine := "No key stored."
	if c.HasSecret {
		hintLine = "Stored key: " + c.Hint
	}
	checked := " checked"
	if c.ID != "" && !c.Enabled {
		checked = ""
	}
	dataID := ""
	revealDel := ""
	if c.ID != "" {
		dataID = html.EscapeString(c.ID)
		revealDel = `<button type="button" class="btn btn--sm" data-action="cred-reveal" data-id="` + dataID + `">Reveal</button>
    <button type="button" class="btn btn--sm" data-action="cred-delete" data-id="` + dataID + `">Delete</button>`
	}

	return `<div data-cred-card data-provider="` + html.EscapeString(p.Provider) + `" data-id="` + dataID + `">
  <p class="text-sm muted mb-4">` + html.EscapeString(p.Desc) + `</p>
  <label class="settings-row ak-cred-toggle"><span class="text-sm">Enabled</span>
    <input type="checkbox" class="toggle" role="switch" data-cred-enabled` + checked + `></label>
  ` + endpointField + `
  <div class="field">
    <label class="field-label">` + html.EscapeString(p.SecretLabel) + `</label>
    <input class="input font-mono" type="password" data-cred-secret placeholder="` + html.EscapeString(p.SecretPH) + `" autocomplete="new-password">
    <span class="field-hint" data-cred-hint>` + html.EscapeString(hintLine) + `</span>
  </div>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--primary btn--sm" data-action="cred-save" data-provider="` + html.EscapeString(p.Provider) + `" data-label="` + html.EscapeString(p.Title) + `">Save</button>
    ` + revealDel + `
    <span class="text-xs muted" data-cred-status role="status" aria-live="polite"></span>
  </div>
</div>`
}

// osAPIKeysCustomRow is one stored custom credential's fields, shown in its
// sheet.
func osAPIKeysCustomRow(c secrets.Credential) string {
	hintLine := "No key stored."
	if c.HasSecret {
		hintLine = "Stored key: " + c.Hint
	}
	checked := " checked"
	if !c.Enabled {
		checked = ""
	}
	id := html.EscapeString(c.ID)
	return `<div data-cred-card data-provider="custom" data-id="` + id + `" data-label="` + html.EscapeString(c.Label) + `">
  <label class="settings-row ak-cred-toggle"><span class="text-sm">Enabled</span>
    <input type="checkbox" class="toggle" role="switch" data-cred-enabled` + checked + `></label>
  <div class="field">
    <label class="field-label">Endpoint</label>
    <input class="input" type="text" data-cred-endpoint value="` + html.EscapeString(c.Endpoint) + `" placeholder="https://…">
  </div>
  <div class="field">
    <label class="field-label">Secret</label>
    <input class="input font-mono" type="password" data-cred-secret placeholder="Leave blank to keep current" autocomplete="new-password">
    <span class="field-hint" data-cred-hint>` + html.EscapeString(hintLine) + `</span>
  </div>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--primary btn--sm" data-action="cred-save" data-provider="custom" data-label="` + html.EscapeString(c.Label) + `">Save</button>
    <button type="button" class="btn btn--sm" data-action="cred-reveal" data-id="` + id + `">Reveal</button>
    <button type="button" class="btn btn--sm" data-action="cred-delete" data-id="` + id + `">Delete</button>
    <span class="text-xs muted" data-cred-status role="status" aria-live="polite"></span>
  </div>
</div>`
}

// ── JSON action handlers ──────────────────────────────────────────────────────

func (a *App) handleOSAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if a.apiKeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", "API key store not initialised", "")
		return
	}
	var body struct {
		Label        string   `json:"label"`
		Capabilities []string `json:"capabilities"` // "section:action" tokens; ["*:*"] = full access
		ExpiresAt    string   `json:"expires_at"`   // RFC3339 / datetime-local; empty = never
		RatePerMin   int      `json:"rate_per_min"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "API key"
	}

	// Build the grant set from the checked capabilities, dropping any unknown
	// token (fail-closed). An empty set is a valid deny-all key.
	perms := apikeys.NewPermissions()
	for _, c := range body.Capabilities {
		if sec, act, ok := apikeys.ParseCapability(strings.TrimSpace(c)); ok {
			perms.Grant(sec, act)
		}
	}

	// Defense-in-depth: a key-authenticated caller can never mint a key more
	// powerful than itself (the "a key is never more powerful than its grant"
	// invariant that the whole VayuMCP/connector story rests on). Session-
	// authenticated admins carry no KeyInfo — they reach this handler through
	// console RBAC (which already gated them to admin level for /os/apikeys and
	// /os/connector) — so the interactive UI, including the one-click "Grant full
	// control", is unaffected. Only a scoped API key trying to escalate is blocked.
	if ki, ok := auth.KeyInfoFromContext(r.Context()); ok && !ki.IsSuperuser() {
		if !ki.Perms.Covers(perms) {
			writeAPIError(w, r, http.StatusForbidden, "grant-exceeds-key",
				"an API key cannot mint a key with capabilities it does not itself hold", "/docs/compatibility/vayuapi")
			return
		}
	}

	// Optional hard expiry. Accept both the browser datetime-local shape
	// (2006-01-02T15:04) and full RFC3339; reject a past time.
	var expiresAt *time.Time
	if s := strings.TrimSpace(body.ExpiresAt); s != "" {
		t, err := parseAPIKeyExpiry(s)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-expiry", "Could not read the expiry date/time.", "")
			return
		}
		if !t.After(time.Now()) {
			writeAPIError(w, r, http.StatusBadRequest, "past-expiry", "The expiry must be in the future.", "")
			return
		}
		expiresAt = &t
	}

	rate := body.RatePerMin
	if rate < 0 {
		rate = 0
	}

	owner := currentUserIDOf(r) // per-user ownership; admins can still manage all
	key, raw, err := a.apiKeys.CreateWithPermissions(r.Context(), owner, label, perms, expiresAt, rate)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "apikeys-error", err.Error(), "")
		return
	}
	// Record WHAT was granted, not just that a key appeared. A full-access key is
	// the most consequential thing this console issues, and "a key was created"
	// without its scope is not enough to answer the question afterwards.
	grant := strings.Join(perms.Capabilities(), ",")
	if perms.IsSuperuser() {
		grant = "FULL-ACCESS"
	}
	dbpkg.AuditLog("apikey.create", dbpkg.AuditActor(r), key.ID, "label="+label+" grant="+grant)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": key.ID, "token": raw})
}

// parseAPIKeyExpiry accepts a browser datetime-local value or RFC3339 and returns
// a UTC time. datetime-local carries no zone, so it is read in the server's local
// zone (the operator's own clock) then normalised to UTC for storage.
func parseAPIKeyExpiry(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, errBadExpiry
}

var errBadExpiry = &apiKeyError{"unparseable expiry"}

type apiKeyError struct{ s string }

func (e *apiKeyError) Error() string { return e.s }

// handleOSAPIKeySetActive activates or deactivates a key without rotating it
// (reversible enable/disable, distinct from terminal revocation).
func (a *App) handleOSAPIKeySetActive(active bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		action := "deactivate"
		if active {
			action = "activate"
		}
		a.apiKeyMutate(w, r, action, func(id string) error { return a.apiKeys.SetActive(r.Context(), id, active) })
	}
}

func (a *App) handleOSAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	if a.apiKeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", "API key store not initialised", "")
		return
	}
	if !a.keyLifecycleAuthorized(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "rotating an API key requires an administrator or a superuser key", "")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		id = strings.TrimSpace(body.ID)
	}
	raw, err := a.apiKeys.Rotate(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "apikeys-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("apikey.rotate", dbpkg.AuditActor(r), id, "")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"token": raw})
}

func (a *App) handleOSAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	a.apiKeyMutate(w, r, "revoke", func(id string) error {
		if err := a.apiKeys.Revoke(r.Context(), id); err != nil {
			return err
		}
		a.revokeOAuthRefreshForKey(r, id)
		return nil
	})
}

func (a *App) handleOSAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	a.apiKeyMutate(w, r, "delete", func(id string) error {
		if err := a.apiKeys.Delete(r.Context(), id); err != nil {
			return err
		}
		a.revokeOAuthRefreshForKey(r, id)
		return nil
	})
}

// revokeOAuthRefreshForKey drops any OAuth refresh tokens bound to a key when it
// is revoked or deleted, so a revoked connector cannot mint a fresh access token
// by rotating through /oauth/token (ADR-0140). Best-effort — a cleanup failure
// must not block the revoke, and the access token (the key itself) is already dead.
func (a *App) revokeOAuthRefreshForKey(r *http.Request, id string) {
	if a.oauth != nil {
		_ = a.oauth.RevokeRefreshForKey(r.Context(), id)
	}
}

// apiKeyMutate is the shared revoke/delete/activate helper.
// apiKeyMutate runs one key-lifecycle change and RECORDS IT.
//
// Nothing on this page used to be audit-logged, while creating a post or
// applying a theme was. That is the wrong way round: issuing a full-access key,
// rotating one, or reviving a deactivated key are the highest-consequence
// actions in the console — each one hands out or restores the ability to act as
// the site — and they left no trace at all. An operator investigating "how did
// this key come to exist" had nothing to read.
//
// action names the operation so the log distinguishes a deactivate from a
// revoke; the key id is the target. No secret is ever recorded.
func (a *App) apiKeyMutate(w http.ResponseWriter, r *http.Request, action string, fn func(id string) error) {
	if a.apiKeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", "API key store not initialised", "")
		return
	}
	if !a.keyLifecycleAuthorized(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "modifying an API key requires an administrator or a superuser key", "")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	id := strings.TrimSpace(body.ID)
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if err := fn(id); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "apikeys-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("apikey."+action, dbpkg.AuditActor(r), id, "")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleOSCredentialSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	var body struct {
		Provider string `json:"provider"`
		Label    string `json:"label"`
		Endpoint string `json:"endpoint"`
		Secret   string `json:"secret"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	// The backup passphrase is set on the Backups page, which says to keep the
	// old one for the backups it sealed. Saved here under another label it
	// would become the newest and replace it with no word said.
	if strings.EqualFold(strings.TrimSpace(body.Provider), secrets.ProviderVayuKeep) {
		writeAPIError(w, r, http.StatusBadRequest, "secrets-error", "The backup passphrase is set on the Backups page.", "")
		return
	}
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secrets store not initialised", "")
		return
	}
	id, err := a.secrets.Upsert(r.Context(), body.Provider, body.Label, body.Endpoint, body.Secret, body.Enabled, false)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "secrets-error", err.Error(), "")
		return
	}
	// Provider and enabled state only — never the secret or the endpoint, which
	// can itself carry a token in the path.
	enabled := "disabled"
	if body.Enabled {
		enabled = "enabled"
	}
	// Drop the cached IndexNow key unconditionally: a save may have set, rotated
	// or disabled it, and the key file must reflect that on the very next request
	// — a stale key means search engines read the wrong value and every
	// submission is silently voided until the cache expires.
	a.invalidateIndexNowKey()
	dbpkg.AuditLog("credential.save", dbpkg.AuditActor(r), strings.TrimSpace(body.Provider), enabled)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "id": id})
}

func (a *App) handleOSCredentialReveal(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secrets store not initialised", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	secret, err := a.secrets.Reveal(r.Context(), strings.TrimSpace(body.ID))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "secrets-error", err.Error(), "")
		return
	}
	// A reveal returns a third-party secret in plaintext. It is the single most
	// sensitive READ this console offers, and it recorded nothing — so a leaked
	// provider key could never be traced to the moment it was displayed. The
	// secret itself is of course never written to the log.
	dbpkg.AuditLog("credential.reveal", dbpkg.AuditActor(r), strings.TrimSpace(body.ID), "")
	writeJSON(w, r, http.StatusOK, map[string]string{"secret": secret})
}

func (a *App) handleOSCredentialDelete(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secrets store not initialised", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := a.secrets.Delete(r.Context(), strings.TrimSpace(body.ID)); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "secrets-error", err.Error(), "")
		return
	}
	// The delete carries only an opaque id, so we cannot tell whether it was the
	// IndexNow credential — invalidate regardless. One extra resolve is far
	// cheaper than continuing to serve a key file for a credential that is gone.
	a.invalidateIndexNowKey()
	dbpkg.AuditLog("credential.delete", dbpkg.AuditActor(r), strings.TrimSpace(body.ID), "")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// osAPIKeysScript is the nonce-gated page controller for the API Keys console.
// It runs inside the shared bootstrap IIFE (see adminOSShellFoot), so csrf() is
// already in scope.
const osAPIKeysScript = `
var akStatus=document.getElementById('ak-status');
// A message about something done in a sheet is said in that sheet: the page's
// own status line is behind the sheet while it is open.
function akSet(t,isErr,from){var d=from&&from.closest('dialog');var s=(d&&d.querySelector('[data-sheet-status]'))||akStatus;if(s){s.textContent=t;s.style.color=isErr?'var(--danger)':'var(--ok)';}}
function jpost(url,payload){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload||{})}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}

// ── Token reveal banner (shown once after create/rotate) ──
var banner=document.getElementById('ak-token-banner');
var tokenVal=document.getElementById('ak-token-value');
var copyBtn=document.getElementById('ak-token-copy');
var doneBtn=document.getElementById('ak-token-done');
// The key is shown once, so the sheet it was made in closes first: left open,
// it would cover the only copy of the key.
function showToken(tok){if(!banner)return;var d=banner.ownerDocument.querySelector('dialog.sa-sheet[open]');if(d)d.close();tokenVal.value=tok;banner.hidden=false;banner.scrollIntoView({behavior:'smooth',block:'start'});}
if(copyBtn)copyBtn.addEventListener('click',function(){tokenVal.select();try{document.execCommand('copy');}catch(e){}if(navigator.clipboard)navigator.clipboard.writeText(tokenVal.value);akSet('Copied to clipboard',false);});
if(doneBtn)doneBtn.addEventListener('click',function(){location.reload();});

// ── Permission grid wiring (row "all", grand superuser) ──
var grid=document.getElementById('ak-perm-grid');
var superBox=document.getElementById('ak-perm-super');
function permBoxes(){return grid?grid.querySelectorAll('.ak-perm'):[];}
function rowBox(section){return grid?grid.querySelector('.ak-perm-all[data-section="'+section+'"]'):null;}
function rowCells(section){return grid?grid.querySelectorAll('.ak-perm[data-section="'+section+'"]'):[];}
function syncRow(section){var r=rowBox(section);if(!r)return;var cells=rowCells(section),all=cells.length>0;cells.forEach(function(c){if(!c.checked)all=false;});r.checked=all;}
if(grid)grid.addEventListener('change',function(ev){
  var t=ev.target;
  if(t.classList.contains('ak-perm-all')){var sec=t.getAttribute('data-section');rowCells(sec).forEach(function(c){c.checked=t.checked;});}
  else if(t.classList.contains('ak-perm')){syncRow(t.getAttribute('data-section'));}
  if(superBox&&superBox.checked&&!(t===superBox)){/* editing individual boxes leaves superuser as-is */}
});
if(superBox)superBox.addEventListener('change',function(){
  // Full access dims the grid — grants are then implicit.
  if(grid)grid.classList.toggle('ak-grid--disabled',superBox.checked);
});
function collectCapabilities(){
  if(superBox&&superBox.checked)return['*:*'];
  var caps=[];permBoxes().forEach(function(c){if(c.checked)caps.push(c.getAttribute('data-section')+':'+c.getAttribute('data-action'));});
  return caps;
}

// ── Create / rotate / activate / deactivate / revoke / delete own keys ──
var createBtn=document.getElementById('ak-create-btn');
if(createBtn)createBtn.addEventListener('click',function(){
  var label=(document.getElementById('ak-new-label')||{}).value||'';
  var expiry=(document.getElementById('ak-new-expiry')||{}).value||'';
  var rateRaw=(document.getElementById('ak-new-rate')||{}).value||'';
  var rate=parseInt(rateRaw,10);if(isNaN(rate)||rate<0)rate=0;
  var caps=collectCapabilities();
  if(caps.length===0){akSet('Grant at least one permission (or tick Full access)',true,createBtn);return;}
  createBtn.disabled=true;akSet('Creating…',false,createBtn);
  jpost('/os/api/apikeys/create',{label:label,capabilities:caps,expires_at:expiry,rate_per_min:rate}).then(function(res){
    createBtn.disabled=false;
    if(res.ok){showToken(res.d.token);akSet('Key created',false);}else{akSet(res.d.detail||res.d.title||'Error',true,createBtn);}
  }).catch(function(e){createBtn.disabled=false;akSet('Error: '+e,true,createBtn);});
});

document.addEventListener('click',function(ev){
  var cp=ev.target.closest('[data-copy]');
  if(cp){var el=document.querySelector(cp.getAttribute('data-copy'));if(el){var v=el.value!==undefined?el.value:el.textContent;if(navigator.clipboard)navigator.clipboard.writeText(v).then(function(){akSet('Copied',false);},function(){akSet('Copy failed; select it instead',true);});}return;}
  var b=ev.target.closest('[data-action]');if(!b)return;
  var act=b.getAttribute('data-action');var id=b.getAttribute('data-id');
  if(act==='ak-rotate'){
    vpConfirm({title:'Rotate this key?',message:'The current value stops working immediately.',confirm:'Rotate'},function(){
    b.disabled=true;jpost('/os/api/apikeys/rotate',{id:id}).then(function(res){b.disabled=false;if(res.ok){showToken(res.d.token);akSet('Key rotated',false);}else{akSet(res.d.detail||'Error',true,b);}});
    });
  }else if(act==='ak-activate'){
    b.disabled=true;jpost('/os/api/apikeys/activate',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true,b);}});
  }else if(act==='ak-deactivate'){
    vpConfirm({title:'Deactivate this key?',message:'It stops authenticating until you re-activate it.',confirm:'Deactivate'},function(){
    b.disabled=true;jpost('/os/api/apikeys/deactivate',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true,b);}});
    });
  }else if(act==='ak-revoke'){
    vpConfirm({title:'Revoke this key?',message:'It can no longer authenticate.',confirm:'Revoke'},function(){
    b.disabled=true;jpost('/os/api/apikeys/revoke',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true,b);}});
    });
  }else if(act==='ak-delete'){
    vpConfirm({title:'Delete this key permanently?',confirm:'Delete'},function(){
    b.disabled=true;jpost('/os/api/apikeys/delete',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true,b);}});
    });
  }else if(act==='cred-save'){
    saveCred(b);
  }else if(act==='cred-reveal'){
    revealCred(b,id);
  }else if(act==='cred-delete'){
    vpConfirm({title:'Delete this credential?',message:'The stored secret is erased.',confirm:'Delete'},function(){
    b.disabled=true;jpost('/os/api/credentials/delete',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;cardStatus(cardOf(b),res.d.detail||'Error',true);}});
    });
  }
});

function cardOf(el){return el.closest('[data-cred-card]');}
function cardStatus(card,t,isErr){var s=card.querySelector('[data-cred-status]');if(s){s.textContent=t;s.style.color=isErr?'var(--danger)':'var(--ok)';}}

function saveCred(btn){
  var card=cardOf(btn);if(!card)return;
  var provider=btn.getAttribute('data-provider');
  var label=btn.getAttribute('data-label')||'';
  var ep=card.querySelector('[data-cred-endpoint]');
  var sec=card.querySelector('[data-cred-secret]');
  var en=card.querySelector('[data-cred-enabled]');
  var payload={provider:provider,label:label,endpoint:ep?ep.value:'',secret:sec?sec.value:'',enabled:en?en.checked:true};
  btn.disabled=true;cardStatus(card,'Saving…',false);
  jpost('/os/api/credentials/save',payload).then(function(res){
    btn.disabled=false;
    if(res.ok){cardStatus(card,'Saved',false);if(sec)sec.value='';setTimeout(function(){location.reload();},600);}
    else{cardStatus(card,res.d.detail||res.d.title||'Error',true);}
  }).catch(function(e){btn.disabled=false;cardStatus(card,'Error: '+e,true);});
}

function revealCred(btn,id){
  var card=cardOf(btn);if(!card)return;
  var sec=card.querySelector('[data-cred-secret]');if(!sec)return;
  btn.disabled=true;
  jpost('/os/api/credentials/reveal',{id:id}).then(function(res){
    btn.disabled=false;
    if(res.ok){sec.type='text';sec.value=res.d.secret;btn.textContent='Hide';btn.setAttribute('data-action','noop');}
    else{cardStatus(card,res.d.detail||'Error',true);}
  }).catch(function(e){btn.disabled=false;cardStatus(card,'Error: '+e,true);});
}

// ── Add a custom credential ──
var ccAdd=document.getElementById('cc-add-btn');
if(ccAdd)ccAdd.addEventListener('click',function(){
  var label=(document.getElementById('cc-label')||{}).value||'';
  var ep=(document.getElementById('cc-endpoint')||{}).value||'';
  var sec=(document.getElementById('cc-secret')||{}).value||'';
  if(!label.trim()){akSet('Give the credential a name',true,ccAdd);return;}
  ccAdd.disabled=true;akSet('Saving…',false,ccAdd);
  jpost('/os/api/credentials/save',{provider:'custom',label:label,endpoint:ep,secret:sec,enabled:true}).then(function(res){
    ccAdd.disabled=false;if(res.ok){location.reload();}else{akSet(res.d.detail||res.d.title||'Error',true,ccAdd);}
  }).catch(function(e){ccAdd.disabled=false;akSet('Error: '+e,true,ccAdd);});
});
`
