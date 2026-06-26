package main

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
)

// iconKey is the sidebar icon for the API Keys console.
var iconKey = svgIcon("M8 11a3 3 0 100-6 3 3 0 000 6zm2.2 0l5.3 5.3M13 13l1.5 1.5M15 11l1.5 1.5")

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
    <span id="ak-status" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<p class="text-sm muted mb-4" style="max-width:60ch">Manage the keys that authenticate calls to your VayuPress API, and store the credentials VayuPress uses to talk to third-party services. Third-party secrets are encrypted at rest with AES-256-GCM and are shown masked — they never leave your server in clear text.</p>

<div id="ak-token-banner" class="card" hidden style="border:1px solid var(--color-success,#22c55e)">
  <div class="settings-block-title">Copy your new key now</div>
  <p class="text-sm muted">This is the only time the full key is shown. Store it somewhere safe — you won't be able to see it again.</p>
  <div style="display:flex;gap:.5rem;align-items:center;margin:.5rem 0">
    <input id="ak-token-value" class="input font-mono" type="text" readonly style="flex:1">
    <button type="button" class="btn btn--sm" id="ak-token-copy">Copy</button>
    <button type="button" class="btn btn--primary btn--sm" id="ak-token-done">Done</button>
  </div>
</div>

` + osAPIKeysOwnSection(keys) + osAPIKeysServicesSection(creds)

	full := adminOSShellHead(nonce, "API Keys", "apikeys", cfg) +
		body +
		adminOSShellFoot(nonce, osAPIKeysScript)
	writeOSHTML(w, full)
}

// osAPIKeysOwnSection renders the issued-token table and create form.
func osAPIKeysOwnSection(keys []apikeys.Key) string {
	rows := ""
	for _, k := range keys {
		var status, actions string
		if k.Scope == apikeys.ScopeInternal {
			// The system key is auto-managed: rotate only, never revoke/delete.
			status = `<span class="badge">System · auto-managed</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-rotate" data-id="` + html.EscapeString(k.ID) + `">Rotate</button>`
		} else if k.Revoked {
			status = `<span class="badge">Revoked</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-delete" data-id="` + html.EscapeString(k.ID) + `">Delete</button>`
		} else {
			status = `<span class="badge badge--success">Active</span>`
			actions = `<button type="button" class="btn btn--sm" data-action="ak-rotate" data-id="` + html.EscapeString(k.ID) + `">Rotate</button>
        <button type="button" class="btn btn--sm" data-action="ak-revoke" data-id="` + html.EscapeString(k.ID) + `">Revoke</button>`
		}
		last := "Never"
		if k.LastUsedAt != nil {
			last = k.LastUsedAt.UTC().Format("2006-01-02 15:04 MST")
		}
		rows += `<tr>
      <td>` + html.EscapeString(k.Label) + `</td>
      <td><code class="font-mono">` + html.EscapeString(apikeys.Mask(k.Prefix)) + `</code></td>
      <td class="text-xs muted">` + html.EscapeString(k.CreatedAt.UTC().Format("2006-01-02")) + `</td>
      <td class="text-xs muted">` + html.EscapeString(last) + `</td>
      <td>` + status + `</td>
      <td style="text-align:right;white-space:nowrap">` + actions + `</td>
    </tr>`
	}
	if rows == "" {
		rows = `<tr><td colspan="6" class="text-sm muted" style="text-align:center;padding:1.5rem">No keys issued yet. Create one to authenticate API requests.</td></tr>`
	}

	return `<div class="card">
  <div class="settings-block-title">VayuPress API keys</div>
  <p class="text-sm muted mb-4">Issue keys for scripts, integrations, and CI. Send a key as the <code>X-API-Key</code> header or <code>Authorization: Bearer &lt;key&gt;</code>. Rotating a key invalidates the old value immediately; revoking disables it without deleting the audit record. The <strong>System</strong> key is provisioned and managed automatically for internal use — it can be rotated but never revoked, and internal automation always picks up the current value with no manual step.</p>
  <div style="display:flex;gap:.5rem;align-items:flex-end;margin-bottom:1rem;flex-wrap:wrap">
    <div class="field" style="flex:1;min-width:14rem;margin:0">
      <label class="field-label" for="ak-new-label">Label</label>
      <input id="ak-new-label" class="input" type="text" placeholder="e.g. Deploy bot, Zapier, CI">
    </div>
    <button type="button" class="btn btn--primary" id="ak-create-btn">Create key</button>
  </div>
  <div class="table-wrap">
    <table class="table">
      <thead><tr><th>Label</th><th>Key</th><th>Created</th><th>Last used</th><th>Status</th><th></th></tr></thead>
      <tbody>` + rows + `</tbody>
    </table>
  </div>
  <p class="field-hint mt-2">A root key set via the <code>API_KEY</code> environment variable always remains valid as a bootstrap credential and is not listed here. Rotating any key here never affects your stored third-party secrets — they are encrypted with a separate, persistent key, so nothing needs re-entering.</p>
</div>`
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
  <div style="display:flex;gap:.5rem;align-items:flex-end;margin-bottom:1rem;flex-wrap:wrap">
    <div class="field" style="flex:1;min-width:10rem;margin:0">
      <label class="field-label" for="cc-label">Name</label>
      <input id="cc-label" class="input" type="text" placeholder="e.g. Sendgrid, Pushover">
    </div>
    <div class="field" style="flex:1;min-width:10rem;margin:0">
      <label class="field-label" for="cc-endpoint">Endpoint (optional)</label>
      <input id="cc-endpoint" class="input" type="text" placeholder="https://…">
    </div>
    <div class="field" style="flex:1;min-width:10rem;margin:0">
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

	return `<div class="settings-section" data-cred-card data-provider="` + html.EscapeString(p.Provider) + `" data-id="` + dataID + `" style="border:1px solid var(--border,#2a2a2a);border-radius:10px;padding:1rem;margin-bottom:1rem">
  <div style="display:flex;justify-content:space-between;align-items:center;gap:1rem;flex-wrap:wrap">
    <div>
      <div class="settings-row-label">` + html.EscapeString(p.Title) + `</div>
      <div class="text-sm muted" style="max-width:60ch">` + html.EscapeString(p.Desc) + `</div>
    </div>
    <label class="settings-row" style="gap:.5rem;margin:0"><span class="text-xs muted">Enabled</span>
      <input type="checkbox" class="toggle" data-cred-enabled` + checked + `></label>
  </div>
  ` + endpointField + `
  <div class="field">
    <label class="field-label">` + html.EscapeString(p.SecretLabel) + `</label>
    <input class="input font-mono" type="password" data-cred-secret placeholder="` + html.EscapeString(p.SecretPH) + `" autocomplete="new-password">
    <span class="field-hint" data-cred-hint>` + html.EscapeString(hintLine) + `</span>
  </div>
  <div style="display:flex;gap:.5rem;align-items:center;flex-wrap:wrap;margin-top:.5rem">
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
	return `<div class="settings-section" data-cred-card data-provider="custom" data-id="` + id + `" data-label="` + html.EscapeString(c.Label) + `" style="border:1px solid var(--border,#2a2a2a);border-radius:10px;padding:1rem;margin-bottom:.75rem">
  <div style="display:flex;justify-content:space-between;align-items:center;gap:1rem;flex-wrap:wrap">
    <div class="settings-row-label">` + html.EscapeString(c.Label) + `</div>
    <label class="settings-row" style="gap:.5rem;margin:0"><span class="text-xs muted">Enabled</span>
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
  <div style="display:flex;gap:.5rem;align-items:center;flex-wrap:wrap;margin-top:.5rem">
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
		Label string `json:"label"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "API key"
	}
	key, raw, err := a.apiKeys.Create(r.Context(), label)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "apikeys-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"id": key.ID, "token": raw})
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
	a.apiKeyMutate(w, r, func(id string) error { return a.apiKeys.Revoke(r.Context(), id) })
}

func (a *App) handleOSAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	a.apiKeyMutate(w, r, func(id string) error { return a.apiKeys.Delete(r.Context(), id) })
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

// ── Create / rotate / revoke / delete own keys ──
var createBtn=document.getElementById('ak-create-btn');
if(createBtn)createBtn.addEventListener('click',function(){
  var label=(document.getElementById('ak-new-label')||{}).value||'';
  createBtn.disabled=true;akSet('Creating…',false);
  jpost('/os/api/apikeys/create',{label:label}).then(function(res){
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
