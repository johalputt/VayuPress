package main

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
)

// iconKey is the sidebar icon for the API Keys console.
var iconKey = svgIcon("M8 11a3 3 0 100-6 3 3 0 000 6zm2.2 0l5.3 5.3M13 13l1.5 1.5M15 11l1.5 1.5")

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
	file := chi.URLParam(r, "file")
	key := a.indexNowKey()
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

	body := `<div class="page-header">
  <h1>API Keys</h1>
  <div class="page-actions">
    <a class="btn btn--sm" href="/docs/compatibility/vcb" target="_blank" rel="noopener" title="Open the Vayu Compatibility Bible">` + iconVCB + ` Compatibility (VCB)</a>
    <span id="ak-status" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<p class="text-sm muted mb-4 ak-intro">Manage the keys that authenticate calls to your VayuPress API, and store the credentials VayuPress uses to talk to third-party services. Third-party secrets are encrypted at rest with AES-256-GCM and are shown masked — they never leave your server in clear text.</p>

<div id="ak-token-banner" class="card ak-token-banner" hidden>
  <div class="settings-block-title">Copy your new key now</div>
  <p class="text-sm muted">This is the only time the full key is shown. Store it somewhere safe — you won't be able to see it again.</p>
  <div class="ak-token-row">
    <input id="ak-token-value" class="input font-mono ak-token-input" type="text" readonly>
    <button type="button" class="btn btn--sm" id="ak-token-copy">Copy</button>
    <button type="button" class="btn btn--primary btn--sm" id="ak-token-done">Done</button>
  </div>
</div>

` + osAPIBaseCard() + osAPIKeysOwnSection(keys) + osAPIKeysVCBCard() + osAPIKeysServicesSection(creds)

	// This page hosts the filter island (x-data="filterList"), so it opts into
	// the Alpine runtime; pageUsesAlpine keeps the decision tied to the markup.
	full := adminOSShellHead(nonce, "API Keys", "apikeys", cfg) +
		body +
		adminOSShellFoot(nonce, osAPIKeysScript, pageUsesAlpine(body))
	writeOSHTML(w, full)
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

// osAPIBaseCard shows the API base URL operators point scripts, CI and AI agents
// at, plus guidance on the dedicated proxy-off API host when they are behind a
// challenge-mode proxy. Copy avoids the literal "CDN" per the CSP-safe-prose rule.
func osAPIBaseCard() string {
	base := html.EscapeString(apiBaseURL())
	var hint string
	if strings.TrimSpace(config.Cfg.APIHost) != "" {
		hint = `<p class="field-hint mt-2">Served on your dedicated <code>` + html.EscapeString(config.Cfg.APIHost) + `</code> host (<code>VAYUOS_API_HOST</code>), pointed straight at the origin with the proxy <strong>off</strong>. Only <code>/api</code> and <code>/health</code> are exposed there — the admin console is not — and VayuShield still guards it.</p>`
	} else {
		hint = `<p class="field-hint mt-2">Behind a proxy that shows a bot &ldquo;challenge&rdquo; page (e.g. Cloudflare)? A script or agent can&rsquo;t solve it. Point a dedicated <code>api.&lt;your-domain&gt;</code> record straight at this server with the proxy <strong>off (&ldquo;DNS only&rdquo;)</strong>, set <code>VAYUOS_API_HOST=api.&lt;your-domain&gt;</code>, and re-run the installer — VayuPress serves a hardened, API-only host there. See the installation guide.</p>`
	}
	return `<div class="card">
  <div class="settings-block-title">Your API base URL</div>
  <p class="text-sm muted mb-4">Point scripts, CI jobs and AI agents here, sending a key you mint below as <code>Authorization: Bearer &lt;key&gt;</code>.</p>
  <div class="ak-token-row">
    <input id="ak-apibase" class="input font-mono ak-token-input" type="text" readonly value="` + base + `">
    <button type="button" class="btn btn--sm" data-copy="#ak-apibase">Copy</button>
  </div>
  ` + hint + `
</div>`
}

// apiKeyCapabilitySummary renders a compact set of capability badges for a key's
// grant set (or a single "Full access" badge for a superuser/legacy key).
func apiKeyCapabilitySummary(k apikeys.Key) string {
	if k.Scope == apikeys.ScopeInternal || k.Permissions.IsSuperuser() {
		return `<span class="badge badge--accent">Full access</span>`
	}
	caps := k.Permissions.Capabilities()
	if len(caps) == 0 {
		return `<span class="badge">No grants</span>`
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

// osAPIKeysOwnSection renders the issued-token list and the scoped-key create
// form (permission grid + expiry + rate). CSP-safe: no inline styles, all layout
// via utility/component classes.
func osAPIKeysOwnSection(keys []apikeys.Key) string {
	rows := ""
	for _, k := range keys {
		var status, actions string
		if k.Scope == apikeys.ScopeInternal {
			status = `<span class="badge">System · auto-managed</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-rotate" data-id="` + html.EscapeString(k.ID) + `">Rotate</button>`
		} else if k.Revoked {
			status = `<span class="badge">Revoked</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-delete" data-id="` + html.EscapeString(k.ID) + `">Delete</button>`
		} else if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now().UTC()) {
			status = `<span class="badge badge--warn">Expired</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-delete" data-id="` + html.EscapeString(k.ID) + `">Delete</button>`
		} else if !k.Active {
			status = `<span class="badge">Inactive</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-activate" data-id="` + html.EscapeString(k.ID) + `">Activate</button>
        <button type="button" class="btn btn--sm" data-action="ak-revoke" data-id="` + html.EscapeString(k.ID) + `">Revoke</button>`
		} else {
			status = `<span class="badge badge--ok">Active</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-rotate" data-id="` + html.EscapeString(k.ID) + `">Rotate</button>
        <button type="button" class="btn btn--sm" data-action="ak-deactivate" data-id="` + html.EscapeString(k.ID) + `">Deactivate</button>
        <button type="button" class="btn btn--sm" data-action="ak-revoke" data-id="` + html.EscapeString(k.ID) + `">Revoke</button>`
		}
		last := "Never"
		if k.LastUsedAt != nil {
			last = k.LastUsedAt.UTC().Format("2006-01-02 15:04 MST")
		}
		expiry := "—"
		if k.ExpiresAt != nil {
			expiry = k.ExpiresAt.UTC().Format("2006-01-02")
		}
		rows += `<tr data-filter-text="` + html.EscapeString(k.Label+" "+k.Prefix) + `">
      <td><div class="ak-key-label">` + html.EscapeString(k.Label) + `</div><code class="font-mono text-xs muted">` + html.EscapeString(apikeys.Mask(k.Prefix)) + `</code></td>
      <td>` + apiKeyCapabilitySummary(k) + `</td>
      <td class="text-xs muted">` + html.EscapeString(expiry) + `</td>
      <td class="text-xs muted">` + html.EscapeString(last) + `</td>
      <td>` + status + `</td>
      <td class="ak-row-actions">` + actions + `</td>
    </tr>`
	}
	if rows == "" {
		rows = `<tr><td colspan="6" class="text-sm muted ak-empty">No keys issued yet. Create one below to authenticate API requests.</td></tr>`
	}

	// x-data="filterList" powers the live client-side filter below (ADR-0136,
	// vayu-islands.js). It is a pure enhancement: if Alpine is absent the input
	// is inert and every row stays visible, and the create/rotate/revoke flows
	// (vanilla JS) are untouched.
	return `<div class="card" x-data="filterList" data-filter-noun="keys">
  <div class="settings-block-title">VayuPress API keys</div>
  <p class="text-sm muted mb-4">Issue keys for scripts, integrations, and CI. Send a key as the <code>X-API-Key</code> header or <code>Authorization: Bearer &lt;key&gt;</code>. Each key is granted <strong>only</strong> the sections and actions you check below — a key can do nothing it was not granted. Rotating invalidates the old value immediately; deactivating disables a key reversibly; revoking disables it permanently (audit row kept). The <strong>System</strong> key is auto-managed for internal use.</p>
  <div class="ak-filter"><input type="search" class="input ak-filter-input" placeholder="Filter keys by label or prefix…" x-model="q" @input="apply()" aria-label="Filter API keys"><span data-filter-status role="status" aria-live="polite" class="vp-sr-only"></span></div>
  <div class="table-wrap">
    <table class="table ak-table">
      <thead><tr><th>Label</th><th>Permissions</th><th>Expires</th><th>Last used</th><th>Status</th><th></th></tr></thead>
      <tbody>` + rows + `<tr data-filter-empty hidden><td colspan="6" class="text-sm muted ak-empty">No keys match your filter.</td></tr></tbody>
    </table>
  </div>
  <p class="field-hint mt-2">A root key set via the <code>API_KEY</code> environment variable always remains valid as a bootstrap credential (full access) and is not listed here.</p>
</div>
` + osAPIKeysCreateCard()
}

// osAPIKeysCreateCard renders the scoped-key create form: a 12×6 permission grid
// (section rows × action columns) with per-row and grand "select all" toggles,
// plus optional expiry and a per-key rate budget.
func osAPIKeysCreateCard() string {
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

	return `<div class="card">
  <div class="settings-block-title">Create a scoped key</div>
  <div class="ak-create-row">
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
    <span class="text-xs muted">The full key is shown once, immediately after creation.</span>
  </div>
</div>`
}

// osAPIKeysVCBCard renders the one-click gateway to the Vayu Compatibility
// Bible (VCB, ADR-0135) from the API Keys console: what to grant an extension,
// where the full contract lives, and how to validate a plugin/theme before
// trusting it. Every action is a same-origin link — CSP-safe, no inline style.
func osAPIKeysVCBCard() string {
	return `<div class="card">
  <div class="settings-block-title">Extension compatibility — the Vayu Compatibility Bible (VCB)</div>
  <p class="text-sm muted mb-4">Before you trust a plugin or theme, validate it against the <strong>same contract this API enforces</strong>. An extension declares the hooks, capabilities and <code>section:action</code> permissions it needs; VCB checks them and you mint a key granting <strong>only</strong> those — never more. Themes that fetch from another host, plugins that over-ask, or manifests built against a hook that doesn't exist are refused with a plain, exact reason.</p>
  <div class="ak-cred-actions">
    <a class="btn btn--primary btn--sm" href="/docs/compatibility/vcb" target="_blank" rel="noopener">` + iconVCB + ` Open the Compatibility Bible</a>
    <a class="btn btn--sm" href="/docs/compatibility/vayuapi" target="_blank" rel="noopener">API keys &amp; permissions reference</a>
    <a class="btn btn--sm" href="/docs/adr/ADR-0135-vayu-compatibility-bible" target="_blank" rel="noopener">Design record (ADR-0135)</a>
  </div>
  <p class="field-hint mt-2">Build tools can read the live contract at <code>GET /api/v1/vcb/contract</code> and check a manifest against this running host at <code>POST /api/v1/vcb/validate</code> (both need a key with <code>plugins:read</code>). The <code>vayu-compat</code> CLI runs the same checks offline for CI.</p>
</div>
`
}

// osAPIKeysServicesSection renders a card per known provider plus custom creds.
func osAPIKeysServicesSection(creds []secrets.Credential) string {
	byID := map[string]secrets.Credential{}
	customSeen := map[string]bool{}
	var custom []secrets.Credential
	firstByProvider := map[string]secrets.Credential{}
	for _, c := range creds {
		byID[c.ID] = c
		if c.Provider == secrets.ProviderCustom {
			custom = append(custom, c)
			customSeen[c.ID] = true
			continue
		}
		if _, ok := firstByProvider[c.Provider]; !ok {
			firstByProvider[c.Provider] = c
		}
	}

	cards := ""
	for _, p := range knownProviders {
		cards += osAPIKeysProviderCard(p, firstByProvider[p.Provider])
	}

	customRows := ""
	for _, c := range custom {
		customRows += osAPIKeysCustomRow(c)
	}
	if customRows == "" {
		customRows = `<p class="text-sm muted">No custom credentials yet.</p>`
	}

	return `<div class="card">
  <div class="settings-block-title">Third-party services</div>
  <p class="text-sm muted mb-4">Connect the services VayuPress integrates with. Secrets are encrypted before they touch the database and shown only as a masked hint afterwards.</p>
  ` + cards + `
</div>
<div class="card">
  <div class="settings-block-title">Custom credentials</div>
  <p class="text-sm muted mb-4">Store an API key for any other service by name. Useful for bespoke integrations and plugins.</p>
  <div class="ak-cc-form">
    <div class="field ak-field-grow">
      <label class="field-label" for="cc-label">Name</label>
      <input id="cc-label" class="input" type="text" placeholder="e.g. Sendgrid, Pushover">
    </div>
    <div class="field ak-field-grow">
      <label class="field-label" for="cc-endpoint">Endpoint (optional)</label>
      <input id="cc-endpoint" class="input" type="text" placeholder="https://…">
    </div>
    <div class="field ak-field-grow">
      <label class="field-label" for="cc-secret">Secret</label>
      <input id="cc-secret" class="input" type="password" placeholder="API key / token" autocomplete="new-password">
    </div>
    <button type="button" class="btn btn--primary" id="cc-add-btn">Add</button>
  </div>
  <div id="cc-list">` + customRows + `</div>
</div>`
}

// osAPIKeysProviderCard renders one known-provider integration card.
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

	return `<div class="settings-section ak-cred-card" data-cred-card data-provider="` + html.EscapeString(p.Provider) + `" data-id="` + dataID + `">
  <div class="ak-cred-head">
    <div>
      <div class="settings-row-label">` + html.EscapeString(p.Title) + `</div>
      <div class="text-sm muted ak-cred-desc">` + html.EscapeString(p.Desc) + `</div>
    </div>
    <label class="settings-row ak-cred-toggle"><span class="text-xs muted">Enabled</span>
      <input type="checkbox" class="toggle" data-cred-enabled` + checked + `></label>
  </div>
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

// osAPIKeysCustomRow renders one stored custom credential.
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
	return `<div class="settings-section ak-cred-card ak-cred-card--custom" data-cred-card data-provider="custom" data-id="` + id + `" data-label="` + html.EscapeString(c.Label) + `">
  <div class="ak-cred-head">
    <div class="settings-row-label">` + html.EscapeString(c.Label) + `</div>
    <label class="settings-row ak-cred-toggle"><span class="text-xs muted">Enabled</span>
      <input type="checkbox" class="toggle" data-cred-enabled` + checked + `></label>
  </div>
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
		a.apiKeyMutate(w, r, func(id string) error { return a.apiKeys.SetActive(r.Context(), id, active) })
	}
}

func (a *App) handleOSAPIKeyRotate(w http.ResponseWriter, r *http.Request) {
	if a.apiKeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", "API key store not initialised", "")
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
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"token": raw})
}

func (a *App) handleOSAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	a.apiKeyMutate(w, r, func(id string) error {
		if err := a.apiKeys.Revoke(r.Context(), id); err != nil {
			return err
		}
		a.revokeOAuthRefreshForKey(r, id)
		return nil
	})
}

func (a *App) handleOSAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	a.apiKeyMutate(w, r, func(id string) error {
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

// apiKeyMutate is the shared revoke/delete helper.
func (a *App) apiKeyMutate(w http.ResponseWriter, r *http.Request, fn func(id string) error) {
	if a.apiKeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", "API key store not initialised", "")
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
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleOSCredentialSave(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secrets store not initialised", "")
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
	id, err := a.secrets.Upsert(r.Context(), body.Provider, body.Label, body.Endpoint, body.Secret, body.Enabled, false)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "secrets-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "id": id})
}

func (a *App) handleOSCredentialReveal(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secrets store not initialised", "")
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
	writeJSON(w, r, http.StatusOK, map[string]string{"secret": secret})
}

func (a *App) handleOSCredentialDelete(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secrets store not initialised", "")
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
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// osAPIKeysScript is the nonce-gated page controller for the API Keys console.
// It runs inside the shared bootstrap IIFE (see adminOSShellFoot), so csrf() is
// already in scope.
const osAPIKeysScript = `
var akStatus=document.getElementById('ak-status');
function akSet(t,isErr){if(akStatus){akStatus.textContent=t;akStatus.style.color=isErr?'var(--color-danger,#ef4444)':'var(--color-success,#22c55e)';}}
function jpost(url,payload){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload||{})}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}

// ── Token reveal banner (shown once after create/rotate) ──
var banner=document.getElementById('ak-token-banner');
var tokenVal=document.getElementById('ak-token-value');
var copyBtn=document.getElementById('ak-token-copy');
var doneBtn=document.getElementById('ak-token-done');
function showToken(tok){if(!banner)return;tokenVal.value=tok;banner.hidden=false;banner.scrollIntoView({behavior:'smooth',block:'start'});}
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
  if(caps.length===0){akSet('Grant at least one permission (or tick Full access)',true);return;}
  createBtn.disabled=true;akSet('Creating…',false);
  jpost('/os/api/apikeys/create',{label:label,capabilities:caps,expires_at:expiry,rate_per_min:rate}).then(function(res){
    createBtn.disabled=false;
    if(res.ok){showToken(res.d.token);akSet('Key created',false);}else{akSet(res.d.detail||res.d.title||'Error',true);}
  }).catch(function(e){createBtn.disabled=false;akSet('Error: '+e,true);});
});

document.addEventListener('click',function(ev){
  var b=ev.target.closest('[data-action]');if(!b)return;
  var act=b.getAttribute('data-action');var id=b.getAttribute('data-id');
  if(act==='ak-rotate'){
    if(!confirm('Rotate this key? The current value stops working immediately.'))return;
    b.disabled=true;jpost('/os/api/apikeys/rotate',{id:id}).then(function(res){b.disabled=false;if(res.ok){showToken(res.d.token);akSet('Key rotated',false);}else{akSet(res.d.detail||'Error',true);}});
  }else if(act==='ak-activate'){
    b.disabled=true;jpost('/os/api/apikeys/activate',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true);}});
  }else if(act==='ak-deactivate'){
    if(!confirm('Deactivate this key? It stops authenticating until you re-activate it.'))return;
    b.disabled=true;jpost('/os/api/apikeys/deactivate',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true);}});
  }else if(act==='ak-revoke'){
    if(!confirm('Revoke this key? It can no longer authenticate.'))return;
    b.disabled=true;jpost('/os/api/apikeys/revoke',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true);}});
  }else if(act==='ak-delete'){
    if(!confirm('Delete this key permanently?'))return;
    b.disabled=true;jpost('/os/api/apikeys/delete',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true);}});
  }else if(act==='cred-save'){
    saveCred(b);
  }else if(act==='cred-reveal'){
    revealCred(b,id);
  }else if(act==='cred-delete'){
    if(!confirm('Delete this credential? The stored secret is erased.'))return;
    b.disabled=true;jpost('/os/api/credentials/delete',{id:id}).then(function(res){if(res.ok){location.reload();}else{b.disabled=false;akSet(res.d.detail||'Error',true);}});
  }
});

function cardOf(el){return el.closest('[data-cred-card]');}
function cardStatus(card,t,isErr){var s=card.querySelector('[data-cred-status]');if(s){s.textContent=t;s.style.color=isErr?'var(--color-danger,#ef4444)':'var(--color-success,#22c55e)';}}

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
  if(!label.trim()){akSet('Give the credential a name',true);return;}
  ccAdd.disabled=true;akSet('Saving…',false);
  jpost('/os/api/credentials/save',{provider:'custom',label:label,endpoint:ep,secret:sec,enabled:true}).then(function(res){
    ccAdd.disabled=false;if(res.ok){location.reload();}else{akSet(res.d.detail||res.d.title||'Error',true);}
  }).catch(function(e){ccAdd.disabled=false;akSet('Error: '+e,true);});
});
`
