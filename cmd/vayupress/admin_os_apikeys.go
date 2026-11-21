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
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
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

// providersIn are the built-in integrations with tailored UI, described in
// lang. "custom" covers anything else and is handled by the add-credential
// form. A product's name, a key's shape and an address are the same in every
// language.
func providersIn(lang string) []providerMeta {
	apiKey := i18n.T(lang, "API key")
	return []providerMeta{
		{
			Provider:    secrets.ProviderIndexNow,
			Title:       "IndexNow",
			Desc:        i18n.T(lang, "Instantly notify participating search engines whenever you publish or update a post. VayuPress already submits URLs automatically: add a key here to switch it on (no file upload needed; the verification file is served for you)."),
			SecretLabel: i18n.T(lang, "IndexNow key"),
			SecretPH:    i18n.T(lang, "32+ character key (letters and digits)"),
		},
		{
			Provider:     secrets.ProviderOpenRouter,
			Title:        "OpenRouter",
			Desc:         i18n.T(lang, "Hosted access to a wide range of AI models through a single key. Used by the writing assistant when configured."),
			SecretLabel:  apiKey,
			SecretPH:     "sk-or-...",
			HasEndpoint:  true,
			EndpointPH:   "https://openrouter.ai/api/v1",
			EndpointHint: i18n.T(lang, "Base URL: leave blank to use the default."),
		},
		{
			Provider:     secrets.ProviderOpenAI,
			Title:        "OpenAI",
			Desc:         i18n.T(lang, "Use OpenAI's models for the writing assistant. Enter your API key; the base URL defaults to OpenAI and can point at any OpenAI-compatible gateway."),
			SecretLabel:  apiKey,
			SecretPH:     "sk-...",
			HasEndpoint:  true,
			EndpointPH:   "https://api.openai.com/v1",
			EndpointHint: i18n.T(lang, "Base URL: leave blank to use OpenAI's default."),
		},
		{
			Provider:    secrets.ProviderYouTube,
			Title:       "YouTube Data API",
			Desc:        i18n.T(lang, "Lets a channel followed on Content › YouTube bring its whole back catalogue, not only its newest videos, and post premieres and streams before they start. Without a key, channels are followed from their public feeds. Create a key in Google Cloud with the YouTube Data API v3 enabled; it is kept sealed and only ever sent to YouTube."),
			SecretLabel: apiKey,
			SecretPH:    "AIza...",
		},
		{
			Provider:    secrets.ProviderPixabay,
			Title:       "Pixabay",
			Desc:        i18n.T(lang, "Gives posts with no card image one from Pixabay: the picture a post already links to, or the first photo found for its title, copied into the media library in the background. Switch it on under Tools and plugins. The key is kept sealed and only ever sent to Pixabay; readers fetch nothing from Pixabay."),
			SecretLabel: apiKey,
			SecretPH:    i18n.T(lang, "From pixabay.com/api/docs"),
		},
		{
			Provider:     secrets.ProviderOllama,
			Title:        i18n.T(lang, "Local AI (Ollama)"),
			Desc:         i18n.T(lang, "Connect a self-hosted model runtime so AI features run on infrastructure you control. No data leaves your server."),
			SecretLabel:  i18n.T(lang, "API key (optional)"),
			SecretPH:     i18n.T(lang, "Leave blank if your runtime needs no key"),
			HasEndpoint:  true,
			EndpointPH:   "http://localhost:11434",
			EndpointHint: i18n.T(lang, "Endpoint URL of your local model runtime."),
		},
		{
			Provider:     secrets.ProviderN8N,
			Title:        i18n.T(lang, "n8n automation"),
			Desc:         i18n.T(lang, "Trigger automation workflows by calling an n8n webhook: wire VayuPress events into hundreds of downstream apps."),
			SecretLabel:  i18n.T(lang, "Webhook token / API key"),
			SecretPH:     i18n.T(lang, "Optional bearer token for the webhook"),
			HasEndpoint:  true,
			EndpointPH:   "https://n8n.example.com/webhook/abc123",
			EndpointHint: i18n.T(lang, "Webhook URL n8n exposes for the workflow."),
		},
	}
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
	lang := i18n.Lang(r.Context())

	var keys []apikeys.Key
	if a.apiKeys != nil {
		keys, _ = a.apiKeys.List(r.Context())
	}
	var creds []secrets.Credential
	if a.secrets != nil {
		creds, _ = a.secrets.List(r.Context())
	}

	body := osAPIKeysPage(lang, keys, creds, a.apiDefaultRatePerMin(r.Context())) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-apikeys.js?v=` + assetVer("js/admin-os-apikeys.js") + `"></script>`

	// This page hosts the filter island (x-data="filterList"), so it opts into
	// the Alpine runtime; pageUsesAlpine keeps the decision tied to the markup.
	full := adminOSShellHead(nonce, i18n.T(lang, "API keys"), "apikeys", cfg) +
		body +
		adminOSShellFoot(nonce, "", pageUsesAlpine(body))
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

// osAPIKeysPage is the page body, in lang, apart from the request so the
// tests render exactly what an operator sees.
func osAPIKeysPage(lang string, keys []apikeys.Key, creds []secrets.Credential, defaultRate int) string {
	return string(ui.SettingsPage(i18n.T(lang, "API keys"), osAPIKeysState(lang, keys),
		i18n.T(lang, "Keys for your API, and the credentials VayuPress uses with other services."),
		ui.HTML(`<div id="ak-token-banner" class="card ak-token-banner" hidden>
  <div class="settings-block-title">`+esc(i18n.T(lang, "Copy your new key now"))+`</div>
  <p class="text-sm muted">`+esc(i18n.T(lang, "This is the only time the full key is shown. Store it somewhere safe; you won't be able to see it again."))+`</p>
  <div class="ak-token-row">
    <input id="ak-token-value" class="input font-mono ak-token-input" type="text" readonly aria-label="`+esc(i18n.T(lang, "Your new key"))+`">
    <button type="button" class="btn btn--sm" id="ak-token-copy">`+esc(i18n.T(lang, "Copy"))+`</button>
    <button type="button" class="btn btn--primary btn--sm" id="ak-token-done">`+esc(i18n.T(lang, "Done"))+`</button>
  </div>
</div>
<p class="page-lead"><span id="ak-status" role="status" aria-live="polite" class="text-xs muted"></span></p>`),
		ui.Section(i18n.T(lang, "Your API"), i18n.T(lang, "Where scripts, CI and agents send their calls"), ui.Rows(osAPIBaseRows(lang)...)),
		ui.Section(i18n.T(lang, "Limits"), i18n.T(lang, "Requests a minute for each key, through the API and MCP"), ui.Rows(ui.Row{
			Label: i18n.T(lang, "Each key, unless it sets its own"),
			Hint: i18n.T(lang, "Now {rate}. Empty is the built-in {n} a minute; Unlimited counts no key's calls. Sign-in attempts and public pages keep their own limits, which this does not lift.",
				"rate", rateLabelIn(lang, defaultRate), "n", apiKeyBuiltInRatePerMin()),
			Control: rateControl(lang, "ak-rate-default", "", func() int {
				if defaultRate == apiKeyBuiltInRatePerMin() {
					return 0
				}
				return defaultRate
			}())})),
		ui.Section(i18n.T(lang, "Issued keys"), i18n.T(lang, "Each key can do only what it was granted"), ui.HTML(osAPIKeysOwnSection(lang, keys))),
		ui.Section(i18n.T(lang, "Third-party services"), i18n.T(lang, "Credentials VayuPress uses to reach other providers"), ui.HTML(osAPIKeysServicesSection(lang, creds))),
		ui.Section(i18n.T(lang, "Extension compatibility"), i18n.T(lang, "The contract an add-on must satisfy before it loads"), osAPIKeysVCB(lang)),
	))
}

// osAPIBaseRows show the API base URL operators point scripts, CI and AI
// agents at, and which host serves it, in lang. Copy avoids the literal "CDN"
// per the CSP-safe-prose rule.
func osAPIBaseRows(lang string) []ui.Row {
	host := ui.State("neutral", i18n.T(lang, "Main domain"))
	hint := i18n.T(lang, "Behind a proxy that challenges visitors? A script or agent cannot solve a bot challenge. Point a dedicated {record} record straight at this server with the proxy off (DNS only), set {setting} and re-run the installer: VayuPress serves a hardened, API-only host there.",
		"record", "api.example.com", "setting", "VAYUOS_API_HOST=api.example.com")
	if h := strings.TrimSpace(config.Cfg.APIHost); h != "" {
		host = ui.State("ok", i18n.T(lang, "Dedicated host"))
		hint = i18n.T(lang, "Served on {host}, set by {setting} and pointed straight at this server with the proxy off. Only {paths} are exposed there, not the console, and VayuShield still guards it.",
			"host", h, "setting", "VAYUOS_API_HOST", "paths", "/api, /health")
	}
	return []ui.Row{
		{Label: i18n.T(lang, "Base URL"), Hint: i18n.T(lang, "For scripts, CI jobs and agents, with a key sent as {header}.", "header", "Authorization: Bearer"),
			Control: ui.HTML(`<code class="space-addr" id="ak-apibase">` + html.EscapeString(apiBaseURL()) + `</code><button type="button" class="btn btn--sm" data-copy="#ak-apibase">` + esc(i18n.T(lang, "Copy")) + `</button>`)},
		{Label: i18n.T(lang, "Host"), Hint: hint, Control: host},
	}
}

// apiKeyCapabilitySummary renders a compact set of capability badges for a key's
// grant set (or a single "Full access" badge for a superuser/legacy key). A
// capability is its section:action token, the same in every language.
func apiKeyCapabilitySummary(lang string, k apikeys.Key) string {
	if k.Scope == apikeys.ScopeInternal || k.Permissions.IsSuperuser() {
		return string(ui.State("accent", i18n.T(lang, "Full access")))
	}
	caps := k.Permissions.Capabilities()
	if len(caps) == 0 {
		return string(ui.State("neutral", i18n.T(lang, "No grants")))
	}
	out := ""
	shown := 0
	for _, c := range caps {
		if shown >= 8 {
			out += `<span class="ak-cap-more">` + esc(i18n.T(lang, "+{n} more", "n", len(caps)-shown)) + `</span>`
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
func osAPIKeysState(lang string, keys []apikeys.Key) ui.HTML {
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
	st := ui.State("neutral", i18n.T(lang, "No keys issued"))
	if live > 0 {
		st = ui.State("ok", i18n.T(lang, "{n, plural, one {# key active} other {# keys active}}", "n", live))
	}
	if idle > 0 {
		st += " " + ui.State("neutral", i18n.T(lang, "{n} inactive", "n", idle))
	}
	if full > 0 {
		st += " " + ui.State("warn", i18n.T(lang, "{n} with full access", "n", full))
	}
	return st
}

// osAPIKeysOwnSection renders the issued-token list and the scoped-key create
// form (permission grid + expiry + rate). CSP-safe: no inline styles, all layout
// via utility/component classes.
func osAPIKeysOwnSection(lang string, keys []apikeys.Key) string {
	closeLabel := i18n.T(lang, "Close")
	button := func(action, id, label string) string {
		return `<button type="button" class="btn btn--sm" data-action="` + action + `" data-id="` + html.EscapeString(id) + `">` + esc(label) + `</button>`
	}
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
			status = string(ui.State("neutral", i18n.T(lang, "System, auto-managed")))
			actions = `<span class="text-xs muted">` + esc(i18n.T(lang, "Protected")) + `</span>` + string(ui.Tip(i18n.T(lang, "Not rotatable or revocable: VayuPress manages this key itself.")))
		} else if k.Revoked {
			status = string(ui.State("neutral", i18n.T(lang, "Revoked")))
			actions = button("ak-delete", k.ID, i18n.T(lang, "Delete"))
		} else if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now().UTC()) {
			status = string(ui.State("warn", i18n.T(lang, "Expired")))
			actions = button("ak-delete", k.ID, i18n.T(lang, "Delete"))
		} else if !k.Active {
			status = string(ui.State("neutral", i18n.T(lang, "Inactive")))
			actions = button("ak-activate", k.ID, i18n.T(lang, "Activate")) + "\n        " + button("ak-revoke", k.ID, i18n.T(lang, "Revoke"))
		} else {
			status = string(ui.State("ok", i18n.T(lang, "Active")))
			actions = button("ak-rotate", k.ID, i18n.T(lang, "Rotate")) + "\n        " + button("ak-deactivate", k.ID, i18n.T(lang, "Deactivate")) +
				"\n        " + button("ak-revoke", k.ID, i18n.T(lang, "Revoke"))
		}
		last := i18n.T(lang, "Never")
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
			manage = `<button type="button" class="btn btn--sm" data-sheet="` + html.EscapeString(id) + `">` + esc(i18n.T(lang, "Manage")) + `</button>`
			sheets += string(ui.SheetClosing(id, k.Label, closeLabel, ui.HTML(`<div class="cx-details">`+
				connectorDetailRow(i18n.T(lang, "Key"), `<code class="font-mono">`+html.EscapeString(apikeys.Mask(k.Prefix))+`</code>`)+
				connectorDetailRow(i18n.T(lang, "State"), status)+
				connectorDetailRow(i18n.T(lang, "Can reach"), apiKeyCapabilitySummary(lang, k))+
				connectorDetailRow(i18n.T(lang, "Expires"), html.EscapeString(expiry))+
				connectorDetailRow(i18n.T(lang, "Requests a minute"), string(rateControl(lang, "ak-rate", k.ID, k.RatePerMin)))+
				connectorDetailRow(i18n.T(lang, "Last used"), html.EscapeString(last))+
				`</div>
<div class="ak-cred-actions">`+actions+`
  <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
</div>`)))
		}
		rows += `<tr data-filter-text="` + html.EscapeString(k.Label+" "+k.Prefix) + `">
      <td><div class="ak-key-label" translate="no">` + html.EscapeString(k.Label) + `</div><code class="font-mono text-xs muted">` + html.EscapeString(apikeys.Mask(k.Prefix)) + `</code></td>
      <td>` + apiKeyCapabilitySummary(lang, k) + `</td>
      <td class="text-xs muted">` + html.EscapeString(last) + `</td>
      <td>` + status + `</td>
      <td class="ak-row-actions">` + manage + `</td>
    </tr>`
	}
	if rows == "" {
		rows = `<tr><td colspan="5" class="text-sm muted ak-empty">` + esc(i18n.T(lang, "No keys issued yet. Create one to authenticate API requests.")) + `</td></tr>`
	}

	// x-data="filterList" powers the live client-side filter below (ADR-0136,
	// vayu-islands.js). It is a pure enhancement: if Alpine is absent the input
	// is inert and every row stays visible, and the create/rotate/revoke flows
	// (vanilla JS) are untouched. The island says how many matched in the
	// sentences the page gives it (data-filter-none, data-filter-some).
	createKey := i18n.T(lang, "Create a key")
	create := ui.Rows(ui.Row{Icon: "key", Label: createKey, Hint: i18n.T(lang, "Grant exactly the sections and actions you choose. The full key is shown once, when it is made."),
		Control: ui.HTML(`<button type="button" class="btn btn--primary btn--sm" data-sheet="ak-create">` + esc(createKey) + `</button>`)})
	list := string(create) + `<div class="mt-4" x-data="filterList" data-filter-none="` + esc(i18n.T(lang, "No keys match your filter.")) +
		`" data-filter-some="` + esc(i18n.T(lang, "Keys matching your filter: {n}", "n", "{n}")) + `">
  <p class="text-sm muted mb-4">` + esc(i18n.T(lang, "Send a key as a header.")) + string(ui.Tip(i18n.T(lang, "Send a key as the {header} header or {bearer}. Rotating invalidates the old value immediately; deactivating disables a key reversibly; revoking disables it permanently, keeping its audit row. The System key is managed automatically for internal use.",
		"header", "X-API-Key", "bearer", "Authorization: Bearer <key>"))) + `</p>
  <div class="ak-filter"><input type="search" class="input ak-filter-input" placeholder="` + esc(i18n.T(lang, "Filter keys by label or prefix…")) + `" x-model="q" @input="apply()" aria-label="` + esc(i18n.T(lang, "Filter API keys")) + `"><span data-filter-status role="status" aria-live="polite" class="vp-sr-only"></span></div>
  <div class="table-wrap">
    <table class="table ak-table">
      <thead><tr><th>` + esc(i18n.T(lang, "Label")) + `</th><th>` + esc(i18n.T(lang, "Permissions")) + `</th><th>` + esc(i18n.T(lang, "Last used")) + `</th><th>` + esc(i18n.T(lang, "Status")) + `</th><th></th></tr></thead>
      <tbody>` + rows + `<tr data-filter-empty hidden><td colspan="5" class="text-sm muted ak-empty">` + esc(i18n.T(lang, "No keys match your filter.")) + `</td></tr></tbody>
    </table>
  </div>
  <p class="field-hint mt-2">` + escFill(i18n.T(lang, "The {key} root key is not listed here.", "key", htmlMark(0)), `<code>`+akRootKeyVar+`</code>`) +
		string(ui.Tip(i18n.T(lang, "A root key set through the {variable} environment variable always remains valid as a bootstrap credential with full access.", "variable", akRootKeyVar))) + `</p>
</div>
`
	return list + sheets + string(ui.SheetClosing("ak-create", createKey, closeLabel, osAPIKeysCreateForm(lang)))
}

// The names and code the page shows, the same in every language: the
// variable that sets the root key, the contract's name, a capability's form,
// and the contract's endpoints, grant and command-line tool.
const (
	akRootKeyVar  = "API_KEY"
	akBibleName   = "Vayu Compatibility Bible"
	akCapToken    = "section:action"
	akVCBContract = "GET /api/v1/vcb/contract"
	akVCBValidate = "POST /api/v1/vcb/validate"
	akVCBGrant    = "plugins:read"
	akVCBCLI      = "vayu-compat"
)

// rateControl edits a budget, in lang: empty for the default, a number, or
// Unlimited. action names the data-action the page's script sends it with;
// id is the key's, or "" for the default.
func rateControl(lang, action, id string, rate int) ui.HTML {
	n, unl := "", ""
	switch {
	case rate == apikeys.Unlimited:
		unl = " checked"
	case rate > 0:
		n = strconv.Itoa(rate)
	}
	return ui.HTML(`<span class="ak-cred-actions" data-ak-rate><input class="input ak-field-narrow" type="number" min="1" max="` + strconv.Itoa(apikeys.MaxRatePerMin) +
		`" step="1" placeholder="` + esc(i18n.T(lang, "Default")) + `" value="` + n + `" aria-label="` + esc(i18n.T(lang, "Requests a minute")) + `"><label><input type="checkbox"` + unl + `> ` + esc(i18n.T(lang, "Unlimited")) + `</label>` +
		`<button type="button" class="btn btn--sm" data-action="` + action + `" data-id="` + html.EscapeString(id) + `">` + esc(i18n.T(lang, "Save")) + `</button></span>`)
}

// osAPIKeysCreateForm is the scoped-key create form, shown in a sheet: a 12×6
// permission grid (section rows × action columns) with per-row and grand
// "select all" toggles, plus optional expiry and a per-key rate budget. It is
// the largest control on the page and needed only while issuing a key, so it
// does not stand between the operator and the key list they came to read.
// The grid's sections and actions are the capability tokens a key is granted
// and a manifest declares, so they are shown as they are written.
func osAPIKeysCreateForm(lang string) ui.HTML {
	head := `<th scope="col" class="ak-grid-section">` + esc(i18n.T(lang, "Section")) + `</th><th scope="col" class="ak-grid-all">` + esc(i18n.T(lang, "All")) + `</th>`
	for _, act := range apikeys.AllActions {
		head += `<th scope="col" translate="no">` + html.EscapeString(string(act)) + `</th>`
	}

	body := ""
	for _, sec := range apikeys.AllSections {
		s := html.EscapeString(string(sec))
		cells := `<th scope="row" class="ak-grid-section" translate="no">` + s + `</th>` +
			`<td><input type="checkbox" class="ak-perm-all" data-section="` + s + `" aria-label="` + esc(i18n.T(lang, "Every {section} action", "section", string(sec))) + `"></td>`
		for _, act := range apikeys.AllActions {
			a := html.EscapeString(string(act))
			cells += `<td><input type="checkbox" class="ak-perm" data-section="` + s + `" data-action="` + a + `" aria-label="` + s + `:` + a + `"></td>`
		}
		body += `<tr>` + cells + `</tr>`
	}

	return ui.HTML(`<div class="ak-create-row">
    <div class="field ak-field-grow">
      <label class="field-label" for="ak-new-label">` + esc(i18n.T(lang, "Label")) + `</label>
      <input id="ak-new-label" class="input" type="text" placeholder="` + esc(i18n.T(lang, "e.g. Theme builder, Zapier, CI")) + `">
    </div>
    <div class="field">
      <label class="field-label" for="ak-new-expiry">` + esc(i18n.T(lang, "Expires (optional)")) + `</label>
      <input id="ak-new-expiry" class="input" type="datetime-local">
    </div>
    <div class="field ak-field-narrow">
      <label class="field-label" for="ak-new-rate">` + esc(i18n.T(lang, "Requests a minute")) + `</label>
      <input id="ak-new-rate" class="input" type="number" min="1" max="100000" step="1" placeholder="` + esc(i18n.T(lang, "Default")) + `">
      <label class="text-xs"><input type="checkbox" id="ak-new-rate-unl"> ` + esc(i18n.T(lang, "Unlimited")) + `</label>
    </div>
  </div>
  <div class="ak-grid-toolbar">
    <span class="field-label">` + esc(i18n.T(lang, "Permissions")) + `</span>
    <label class="ak-superuser"><input type="checkbox" id="ak-perm-super"> <span>` + esc(i18n.T(lang, "Full access (all sections and actions)")) + `</span></label>
  </div>
  <div class="table-wrap">
    <table class="table ak-grid" id="ak-perm-grid">
      <thead><tr>` + head + `</tr></thead>
      <tbody>` + body + `</tbody>
    </table>
  </div>
  <div class="ak-create-actions">
    <button type="button" class="btn btn--primary" id="ak-create-btn">` + esc(i18n.T(lang, "Create key")) + `</button>
    <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
  </div>`)
}

// osAPIKeysVCB is the gateway to the Vayu Compatibility Bible (VCB, ADR-0135):
// what to grant an extension, where the full contract lives, and how to
// validate a plugin or theme before trusting it. Every action is a same-origin
// link. The documents it opens are in English.
func osAPIKeysVCB(lang string) ui.HTML {
	bible := akBibleName
	return ui.Rows(ui.Row{Icon: "book", Label: bible, Hint: i18n.T(lang, "Validate a plugin or theme against the contract this API enforces, then grant it only what it declares."),
		Control: ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="ak-vcb">` + esc(i18n.T(lang, "How it works")) + `</button><a class="settings-row-go" href="/docs/compatibility/vcb" target="_blank" rel="noopener">` + esc(i18n.T(lang, "Open")) + string(ui.Icon("chev-r")) + `</a>`)}) +
		ui.SheetClosing("ak-vcb", bible, i18n.T(lang, "Close"), ui.HTML(`<p class="text-sm muted mb-4">`+
			escFill(i18n.T(lang, "Before you trust a plugin or theme, validate it against the {contract}. An extension declares the hooks, capabilities and {token} permissions it needs; VCB checks them and you mint a key granting {only} those, never more. Themes that fetch from another host, plugins that over-ask, or manifests built against a hook that doesn't exist are refused with a plain, exact reason.",
				"contract", htmlMark(0), "token", htmlMark(1), "only", htmlMark(2)),
				keepStrong(i18n.T(lang, "same contract this API enforces")), `<code>`+akCapToken+`</code>`, keepStrong(i18n.T(lang, "only")))+`</p>
<div class="ak-cred-actions">
  <a class="btn btn--primary btn--sm" href="/docs/compatibility/vcb" target="_blank" rel="noopener">`+iconVCB+` `+esc(i18n.T(lang, "Open the Compatibility Bible"))+`</a>
  <a class="btn btn--sm" href="/docs/compatibility/vayuapi" target="_blank" rel="noopener">`+esc(i18n.T(lang, "API keys and permissions reference"))+`</a>
  <a class="btn btn--sm" href="/docs/adr/ADR-0135-vayu-compatibility-bible" target="_blank" rel="noopener">`+esc(i18n.T(lang, "Design record ({adr})", "adr", "ADR-0135"))+`</a>
</div>
<p class="field-hint mt-2">`+escFill(i18n.T(lang, "Build tools can read the live contract at {contract} and check a manifest against this running host at {validate} (both need a key with {grant}). The {cli} CLI runs the same checks offline for CI.",
			"contract", htmlMark(0), "validate", htmlMark(1), "grant", htmlMark(2), "cli", htmlMark(3)),
			`<code>`+akVCBContract+`</code>`, `<code>`+akVCBValidate+`</code>`, `<code>`+akVCBGrant+`</code>`, `<code>`+akVCBCLI+`</code>`)+`</p>`))
}

// osAPIKeysServicesSection lists each known provider and every custom
// credential as a row with its state, in lang; the fields that set one up open
// in a sheet.
func osAPIKeysServicesSection(lang string, creds []secrets.Credential) string {
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
	// A provider's credential is stored under its English name, whoever saved it.
	stored := map[string]string{}
	for _, p := range providersIn("en") {
		stored[p.Provider] = p.Title
	}

	closeLabel := i18n.T(lang, "Close")
	var rows []ui.Row
	sheets := ""
	for _, p := range providersIn(lang) {
		c := firstByProvider[p.Provider]
		id := "ak-cred-" + p.Provider
		rows = append(rows, ui.Row{Label: p.Title, Hint: firstSentence(p.Desc),
			Control: credState(lang, c) + ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="`+html.EscapeString(id)+`">`+esc(credVerb(lang, c))+`</button>`)})
		sheets += string(ui.SheetClosing(id, p.Title, closeLabel, ui.HTML(osAPIKeysProviderCard(lang, p, stored[p.Provider], c))))
	}
	for _, c := range custom {
		id := "ak-cc-" + c.ID
		hint := i18n.T(lang, "No endpoint")
		if c.Endpoint != "" {
			hint = c.Endpoint
		}
		rows = append(rows, ui.Row{Label: c.Label, Hint: hint,
			Control: credState(lang, c) + ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="`+html.EscapeString(id)+`">`+esc(i18n.T(lang, "Edit"))+`</button>`)})
		sheets += string(ui.SheetClosing(id, c.Label, closeLabel, ui.HTML(osAPIKeysCustomRow(lang, c))))
	}
	addCred := i18n.T(lang, "Add a credential")
	rows = append(rows, ui.Row{Icon: "plus", Label: i18n.T(lang, "Another service"), Hint: i18n.T(lang, "Any other service, by name."),
		Control: ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="ak-cc-new">` + esc(addCred) + `</button>`)})
	sheets += string(ui.SheetClosing("ak-cc-new", addCred, closeLabel, ui.HTML(`<div class="field">
  <label class="field-label" for="cc-label">`+esc(i18n.T(lang, "Name"))+`</label>
  <input id="cc-label" class="input" type="text" placeholder="`+esc(i18n.T(lang, "e.g. Sendgrid, Pushover"))+`">
</div>
<div class="field">
  <label class="field-label" for="cc-endpoint">`+esc(i18n.T(lang, "Endpoint (optional)"))+`</label>
  <input id="cc-endpoint" class="input" type="text" placeholder="https://…">
</div>
<div class="field">
  <label class="field-label" for="cc-secret">`+esc(i18n.T(lang, "Secret"))+`</label>
  <input id="cc-secret" class="input" type="password" placeholder="`+esc(i18n.T(lang, "API key / token"))+`" autocomplete="new-password">
</div>
<div class="ak-cred-actions">
  <button type="button" class="btn btn--primary btn--sm" id="cc-add-btn">`+esc(i18n.T(lang, "Add"))+`</button>
  <span class="text-xs muted" data-sheet-status role="status" aria-live="polite"></span>
</div>`)))

	return `<p class="text-sm muted mb-3">` + esc(i18n.T(lang, "Secrets are encrypted before they are stored and shown only masked afterwards.")) +
		string(ui.Tip(i18n.T(lang, "Encrypted at rest with AES-256-GCM; they never leave your server in clear text. Every issue, rotate, revoke and reveal is written to the audit log."))) + `</p>` +
		string(ui.Rows(rows...)) + sheets
}

// credState is a stored credential's state as a dot and a word, in lang. A
// provider with no record has never been set up, which is different from one
// the operator switched off.
func credState(lang string, c secrets.Credential) ui.HTML {
	switch {
	case c.ID == "":
		return ui.State("neutral", i18n.T(lang, "Not set up"))
	case !c.Enabled:
		return ui.State("neutral", i18n.T(lang, "Off"))
	}
	return ui.State("ok", i18n.T(lang, "On"))
}

// firstSentence is a description's first sentence, for a row's hint; the
// whole description is in the sheet the row opens.
func firstSentence(s string) string {
	first, _ := ui.FirstSentence(s)
	return first
}

// credVerb names the row's button for what it will do, in lang.
func credVerb(lang string, c secrets.Credential) string {
	if c.ID == "" {
		return i18n.T(lang, "Set up")
	}
	return i18n.T(lang, "Edit")
}

// credHint says, in lang, whether a credential holds a secret, and its
// masked end.
func credHint(lang string, c secrets.Credential) string {
	if c.HasSecret {
		return i18n.T(lang, "Stored key: {hint}", "hint", c.Hint)
	}
	return i18n.T(lang, "No key stored.")
}

// osAPIKeysProviderCard is one known provider's fields, shown in its sheet,
// in lang. storedLabel is the provider's English name, which its credential
// is saved under.
func osAPIKeysProviderCard(lang string, p providerMeta, storedLabel string, c secrets.Credential) string {
	endpointField := ""
	if p.HasEndpoint {
		hint := ""
		if p.EndpointHint != "" {
			hint = `<span class="field-hint">` + html.EscapeString(p.EndpointHint) + `</span>`
		}
		endpointField = `<div class="field">
    <label class="field-label">` + esc(i18n.T(lang, "Endpoint")) + `</label>
    <input class="input" type="text" data-cred-endpoint value="` + html.EscapeString(c.Endpoint) + `" placeholder="` + html.EscapeString(p.EndpointPH) + `">
    ` + hint + `
  </div>`
	}
	checked := " checked"
	if c.ID != "" && !c.Enabled {
		checked = ""
	}
	dataID := ""
	revealDel := ""
	if c.ID != "" {
		dataID = html.EscapeString(c.ID)
		revealDel = `<button type="button" class="btn btn--sm" data-action="cred-reveal" data-id="` + dataID + `">` + esc(i18n.T(lang, "Reveal")) + `</button>
    <button type="button" class="btn btn--sm" data-action="cred-delete" data-id="` + dataID + `">` + esc(i18n.T(lang, "Delete")) + `</button>`
	}

	return `<div data-cred-card data-provider="` + html.EscapeString(p.Provider) + `" data-id="` + dataID + `">
  <p class="text-sm muted mb-4">` + html.EscapeString(p.Desc) + `</p>
  <label class="settings-row ak-cred-toggle"><span class="text-sm">` + esc(i18n.T(lang, "Enabled")) + `</span>
    <input type="checkbox" class="toggle" role="switch" data-cred-enabled` + checked + `></label>
  ` + endpointField + `
  <div class="field">
    <label class="field-label">` + html.EscapeString(p.SecretLabel) + `</label>
    <input class="input font-mono" type="password" data-cred-secret placeholder="` + html.EscapeString(p.SecretPH) + `" autocomplete="new-password">
    <span class="field-hint" data-cred-hint>` + html.EscapeString(credHint(lang, c)) + `</span>
  </div>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--primary btn--sm" data-action="cred-save" data-provider="` + html.EscapeString(p.Provider) + `" data-label="` + html.EscapeString(storedLabel) + `">` + esc(i18n.T(lang, "Save")) + `</button>
    ` + revealDel + `
    <span class="text-xs muted" data-cred-status role="status" aria-live="polite"></span>
  </div>
</div>`
}

// osAPIKeysCustomRow is one stored custom credential's fields, shown in its
// sheet, in lang.
func osAPIKeysCustomRow(lang string, c secrets.Credential) string {
	checked := " checked"
	if !c.Enabled {
		checked = ""
	}
	id := html.EscapeString(c.ID)
	return `<div data-cred-card data-provider="custom" data-id="` + id + `" data-label="` + html.EscapeString(c.Label) + `">
  <label class="settings-row ak-cred-toggle"><span class="text-sm">` + esc(i18n.T(lang, "Enabled")) + `</span>
    <input type="checkbox" class="toggle" role="switch" data-cred-enabled` + checked + `></label>
  <div class="field">
    <label class="field-label">` + esc(i18n.T(lang, "Endpoint")) + `</label>
    <input class="input" type="text" data-cred-endpoint value="` + html.EscapeString(c.Endpoint) + `" placeholder="https://…">
  </div>
  <div class="field">
    <label class="field-label">` + esc(i18n.T(lang, "Secret")) + `</label>
    <input class="input font-mono" type="password" data-cred-secret placeholder="` + esc(i18n.T(lang, "Leave blank to keep current")) + `" autocomplete="new-password">
    <span class="field-hint" data-cred-hint>` + html.EscapeString(credHint(lang, c)) + `</span>
  </div>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--primary btn--sm" data-action="cred-save" data-provider="custom" data-label="` + html.EscapeString(c.Label) + `">` + esc(i18n.T(lang, "Save")) + `</button>
    <button type="button" class="btn btn--sm" data-action="cred-reveal" data-id="` + id + `">` + esc(i18n.T(lang, "Reveal")) + `</button>
    <button type="button" class="btn btn--sm" data-action="cred-delete" data-id="` + id + `">` + esc(i18n.T(lang, "Delete")) + `</button>
    <span class="text-xs muted" data-cred-status role="status" aria-live="polite"></span>
  </div>
</div>`
}

// ── JSON action handlers ──────────────────────────────────────────────────────

func (a *App) handleOSAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if a.apiKeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", i18n.T(i18n.Lang(r.Context()), "API keys are not available yet: their store is not ready."), "")
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
				i18n.T(i18n.Lang(r.Context()), "An API key cannot make a key with capabilities it does not hold itself."), "/docs/compatibility/vayuapi")
			return
		}
	}

	// Optional hard expiry. Accept both the browser datetime-local shape
	// (2006-01-02T15:04) and full RFC3339; reject a past time.
	var expiresAt *time.Time
	if s := strings.TrimSpace(body.ExpiresAt); s != "" {
		t, err := parseAPIKeyExpiry(s)
		if err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-expiry", i18n.T(i18n.Lang(r.Context()), "The expiry date and time could not be read."), "")
			return
		}
		if !t.After(time.Now()) {
			writeAPIError(w, r, http.StatusBadRequest, "past-expiry", i18n.T(i18n.Lang(r.Context()), "The expiry must be in the future."), "")
			return
		}
		expiresAt = &t
	}

	rate := body.RatePerMin
	if !apikeys.ValidRatePerMin(rate) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-rate", apikeys.ErrBadRate.Error(), "")
		return
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", i18n.T(i18n.Lang(r.Context()), "API keys are not available yet: their store is not ready."), "")
		return
	}
	if !a.keyLifecycleAuthorized(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator or a full-access key can rotate a key."), "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", i18n.T(i18n.Lang(r.Context()), "API keys are not available yet: their store is not ready."), "")
		return
	}
	if !a.keyLifecycleAuthorized(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator or a full-access key can change a key."), "")
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

// handleOSAPIKeyRate sets one key's requests a minute: 0 for the default,
// apikeys.Unlimited, or a number (apikeys.ValidRatePerMin). Recorded, since
// lifting a key's budget is a decision the audit log should be able to show.
func (a *App) handleOSAPIKeyRate(w http.ResponseWriter, r *http.Request) {
	if a.apiKeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "apikeys-error", i18n.T(i18n.Lang(r.Context()), "API keys are not available yet: their store is not ready."), "")
		return
	}
	if !a.keyLifecycleAuthorized(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator or a full-access key can change a key's limit."), "")
		return
	}
	var body struct {
		ID   string `json:"id"`
		Rate int    `json:"rate_per_min"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", i18n.T(i18n.Lang(r.Context()), "The limit could not be read."), "")
		return
	}
	id := strings.TrimSpace(body.ID)
	if err := a.apiKeys.SetRatePerMin(r.Context(), id, body.Rate); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "apikeys-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("apikey.rate", dbpkg.AuditActor(r), id, "rate_per_min="+strconv.Itoa(body.Rate))
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSAPIRateDefault sets the requests a minute of every key that sets
// none: 0 for the built-in budget, apikeys.Unlimited, or a number.
func (a *App) handleOSAPIRateDefault(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", i18n.T(i18n.Lang(r.Context()), "Settings cannot be saved yet: their storage is not ready."), "")
		return
	}
	if !a.keyLifecycleAuthorized(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator or a full-access key can change the keys' default limit."), "")
		return
	}
	var body struct {
		Rate int `json:"rate_per_min"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !apikeys.ValidRatePerMin(body.Rate) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-rate", apikeys.ErrBadRate.Error(), "")
		return
	}
	v := ""
	switch {
	case body.Rate == apikeys.Unlimited:
		v = "unlimited"
	case body.Rate > 0:
		v = strconv.Itoa(body.Rate)
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyAPIRatePerMin: v}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "settings-error", err.Error(), "")
		return
	}
	dbpkg.AuditLog("apikey.rate_default", dbpkg.AuditActor(r), settings.KeyAPIRatePerMin, "value="+v)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleOSCredentialSave(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator can do this."), "")
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
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", i18n.T(i18n.Lang(r.Context()), "The request could not be read."), "")
		return
	}
	// The backup passphrase is set on the Backups page, which says to keep the
	// old one for the backups it sealed. Saved here under another label it
	// would become the newest and replace it with no word said.
	if strings.EqualFold(strings.TrimSpace(body.Provider), secrets.ProviderVayuKeep) {
		writeAPIError(w, r, http.StatusBadRequest, "secrets-error", i18n.T(i18n.Lang(r.Context()), "The backup passphrase is set on the Backups page."), "")
		return
	}
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", i18n.T(i18n.Lang(r.Context()), "Credentials are not available yet: their store is not ready."), "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", i18n.T(i18n.Lang(r.Context()), "Credentials are not available yet: their store is not ready."), "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator can do this."), "")
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
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", i18n.T(i18n.Lang(r.Context()), "Credentials are not available yet: their store is not ready."), "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only an administrator can do this."), "")
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
