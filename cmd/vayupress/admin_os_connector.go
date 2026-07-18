package main

// admin_os_connector.go — the VayuOS "Claude connector" page (ADR-0139, Stage 2).
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
	"html"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/render"
)

// iconConnector is the sidebar/plug glyph for the Claude connector page.
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

// handleOSConnector renders the Claude connector page.
func (a *App) handleOSConnector(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	endpoint := publicMCPEndpoint(r)

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
		osConnectorEndpointCard(endpoint) +
		osConnectorGrantCard() +
		osConnectorConnectCard(endpoint) +
		osConnectorManageCard(connectors)

	full := adminOSShellHead(nonce, "Claude Connector", "connector", cfg) +
		body +
		adminOSShellFoot(nonce, osConnectorScript, pageUsesAlpine(body))
	writeOSHTML(w, full)
}

func osConnectorIntro() string {
	return `<div class="page-header">
  <h1>Claude Connector</h1>
  <div class="page-actions">
    <a class="btn btn--sm" href="/docs/compatibility/mcp" target="_blank" rel="noopener">Connector docs</a>
    <span id="cx-status" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<p class="text-sm muted mb-4">Connect <strong>Claude</strong> (or any Model Context Protocol client) directly to this site — the same way Claude connects to GitHub. Claude gets a native set of tools and can publish posts, build pages, read analytics, search content, and more. What it can do is decided <strong>entirely by the key you grant below</strong>: a full-control key lets Claude run the whole site; a limited key exposes only what you allow. Nothing else changes — the connector is just your API, spoken in Claude's language.</p>

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

func osConnectorEndpointCard(endpoint string) string {
	e := html.EscapeString(endpoint)
	return `<div class="card">
  <div class="settings-block-title">Your connector endpoint</div>
  <p class="text-sm muted mb-4">This is the single URL Claude connects to. It is served by VayuPress itself — no extra service, no extra port. Requests are authenticated with the key you grant below.</p>
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
  <div class="settings-block-title">Grant access to Claude</div>
  <p class="text-sm muted mb-4">Pick how much control to hand over. You can revoke any grant instantly below, and every action Claude takes is written to the audit log.</p>

  <div class="cx-grant-grid">
    <div class="cx-grant cx-grant--full">
      <div class="cx-grant-head">
        <span class="settings-row-label">Full control</span>
        <span class="badge badge--accent">Everything</span>
      </div>
      <p class="text-sm muted">A superuser key. Every current and future VayuMCP tool becomes available — posts, pages, media, analytics, and more as the toolset grows. This is the "give Claude the keys to the whole site" option.</p>
      <button type="button" class="btn btn--primary" data-mint="*:*" data-label="Claude (full control)">Grant full control</button>
    </div>

    <div class="cx-grant">
      <div class="cx-grant-head">
        <span class="settings-row-label">Author only</span>
        <span class="badge">Posts &amp; pages</span>
      </div>
      <p class="text-sm muted">Let Claude write, update, and organise content — create and edit posts and pages, search, and list — but nothing else. A safe default for a writing assistant.</p>
      <button type="button" class="btn" data-mint="posts:read,posts:write" data-label="Claude (author)">Grant author access</button>
    </div>

    <div class="cx-grant">
      <div class="cx-grant-head">
        <span class="settings-row-label">Read only</span>
        <span class="badge">Look, don't touch</span>
      </div>
      <p class="text-sm muted">Claude can read posts and pages, search content, and read analytics — but cannot change anything. Ideal for analysis, reporting, and audits.</p>
      <button type="button" class="btn" data-mint="posts:read,analytics:read" data-label="Claude (read-only)">Grant read-only access</button>
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
	return `<div class="card">
  <div class="settings-block-title">Connect Claude</div>
  <p class="text-sm muted mb-4">After you grant a key above, it is filled into the snippets below automatically. Choose your client:</p>

  <div class="settings-block-title text-sm">Claude Desktop</div>
  <p class="text-sm muted">Add this to your <code>claude_desktop_config.json</code> (Settings → Developer → Edit config), then restart Claude Desktop:</p>
  <pre class="cx-code font-mono" id="cx-cfg-desktop" data-endpoint="` + e + `">` + html.EscapeString(cfg) + `</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#cx-cfg-desktop">Copy config</button>
  </div>

  <div class="settings-block-title text-sm mt-4">Claude Code (CLI)</div>
  <p class="text-sm muted">One command:</p>
  <pre class="cx-code font-mono" id="cx-cfg-cli" data-endpoint="` + html.EscapeString(endpoint) + `">` + html.EscapeString(cli) + `</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#cx-cfg-cli">Copy command</button>
  </div>

  <p class="field-hint mt-4">A true one-click <strong>Connect</strong> button on claude.ai (OAuth sign-in, no key to copy) is on the roadmap — see <a href="/docs/adr/ADR-0139-vayu-mcp-connector" target="_blank" rel="noopener">ADR-0139</a>. Until then the key above works in every MCP client today.</p>
</div>`
}

// osConnectorManageCard lists the operator's live (non-revoked, external) keys so
// a connector can be revoked here. It intentionally mirrors a subset of the API
// Keys console; the full grid lives on /os/apikeys.
func osConnectorManageCard(keys []apikeys.Key) string {
	rows := ""
	for _, k := range keys {
		scope := "Limited"
		if k.Permissions.IsSuperuser() {
			scope = "Full control"
		}
		rows += `<tr>
      <td><div class="ak-key-label">` + html.EscapeString(k.Label) + `</div><code class="font-mono text-xs muted">` + html.EscapeString(apikeys.Mask(k.Prefix)) + `</code></td>
      <td>` + html.EscapeString(scope) + `</td>
      <td class="ak-row-actions"><button type="button" class="btn btn--sm" data-revoke="` + html.EscapeString(k.ID) + `">Revoke</button></td>
    </tr>`
	}
	if rows == "" {
		rows = `<tr><td colspan="3" class="text-sm muted ak-empty">No active connector keys yet. Grant one above to connect Claude.</td></tr>`
	}
	return `<div class="card">
  <div class="settings-block-title">Active connectors</div>
  <p class="text-sm muted mb-4">Keys you have granted. Revoking one disconnects that connector immediately — the old key stops working at once. Full management (rotate, expiry, per-section grants) lives on the <a href="/os/apikeys">API Keys</a> page.</p>
  <div class="table-wrap">
    <table class="table">
      <thead><tr><th>Key</th><th>Access</th><th></th></tr></thead>
      <tbody>` + rows + `</tbody>
    </table>
  </div>
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
    var label=mintBtn.getAttribute('data-label')||'Claude connector';
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
    if(!confirm('Revoke this connector key? Claude will be disconnected immediately.'))return;
    var id=revBtn.getAttribute('data-revoke');
    revBtn.disabled=true;
    cxPost('/os/api/apikeys/revoke',{id:id}).then(function(res){
      if(res.ok){location.reload();}else{revBtn.disabled=false;cxSet(res.d.detail||'Could not revoke',true);}
    }).catch(function(e){revBtn.disabled=false;cxSet('Error: '+e,true);});
    return;
  }
});
`
