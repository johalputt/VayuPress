// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_mcpclient.go — the pieces every per-client connector page shares
// (ADR-0147).
//
// VayuMCP is one endpoint, but the clients that reach it need different
// instructions, and folding them all into /os/connector made that page answer a
// question nobody asked ("which of these four blocks is mine?") before the one
// they did. Buzz and Claude each have their own page now; /os/connector keeps the
// endpoint, the grants and the protocol.
//
// Three pages doing the same job would otherwise mean three copies of the same
// mint-key-then-fill-the-snippet controller, drifting apart the first time one is
// fixed. So the mechanism lives here once and the pages carry only their own
// prose:
//
//   - one set of element IDs, so the shared script finds its controls
//   - one token banner
//   - one stat strip, counting the keys THAT page minted
//   - one snippet renderer, with a placeholder the script rewrites live
//
// A page supplies its copy and its capability presets; nothing else.

import (
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/ui"
)

// liveConnectorKeys returns the grants an operator can actually act on: external,
// non-revoked keys. Internal/system keys are not connectors and a revoked key is
// not a grant, so neither belongs in a count or a list on these pages.
func (a *App) liveConnectorKeys(r *http.Request) []apikeys.Key {
	if a.apiKeys == nil {
		return nil
	}
	all, _ := a.apiKeys.List(r.Context())
	var keys []apikeys.Key
	for _, k := range all {
		if k.Scope == apikeys.ScopeInternal || k.Revoked {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

// Shared element IDs. The script below is written against exactly these, so a
// page that renders the banner must not rename them.
const (
	mcpStatusID = "mx-status"
	mcpBannerID = "mx-token-banner"
	mcpTokenID  = "mx-token-value"
	mcpCopyID   = "mx-token-copy"
	mcpDoneID   = "mx-token-done"
)

// keyTemplatePlaceholder is swapped for the real key by the page script once one
// is minted, so the snippets on screen become genuinely copy-paste instead of
// leaving the operator to hand-edit a token into several places.
const keyTemplatePlaceholder = "__KEY__"

// mcpClientTokenBanner renders the one-time key reveal. It is hidden until a key
// is minted; the script unhides it, fills the value and rewrites every snippet.
func mcpClientTokenBanner(note string) string {
	return `<div id="` + mcpBannerID + `" class="card ak-token-banner" hidden>
  <div class="settings-block-title">Copy your new key now</div>
  <p class="text-sm muted">` + note + `</p>
  <div class="ak-token-row">
    <input id="` + mcpTokenID + `" class="input font-mono ak-token-input" type="text" readonly>
    <button type="button" class="btn btn--sm" id="` + mcpCopyID + `">Copy key</button>
    <button type="button" class="btn btn--primary btn--sm" id="` + mcpDoneID + `">Done</button>
  </div>
</div>`
}

// mcpClientState is a connector page's state beside its title: how many of
// the keys it minted are live, and how many of those hold full control.
//
// It counts only the keys the calling page minted, identified by label prefix.
// Counting every connector would put a number on the page that the page cannot
// explain — a key granted to Claude is not a Buzz agent — and an operator
// auditing which clients can reach their site needs those separated, not summed.
// A full-control key hands a client the whole site, so how many exist is its
// own warning, readable without opening the list.
func mcpClientState(keys []apikeys.Key, labelPrefix, noun string) ui.HTML {
	live, full := 0, 0
	for _, k := range keys {
		if !strings.HasPrefix(k.Label, labelPrefix) {
			continue
		}
		live++
		if k.Permissions.IsSuperuser() {
			full++
		}
	}
	return connectorState(live, full, 0, noun)
}

// connectorState words live and full-control counts as the page's state.
func connectorState(live, full, paused int, noun string) ui.HTML {
	st := ui.State("neutral", "Nothing connected yet")
	if live > 0 {
		st = ui.State("ok", strconv.Itoa(live)+" "+noun+plural(live)+" connected")
	}
	if paused > 0 {
		st += " " + ui.State("neutral", strconv.Itoa(paused)+" paused")
	}
	if full > 0 {
		st += " " + ui.State("warn", strconv.Itoa(full)+" with full control")
	}
	return st
}

// mcpEndpointRows are the endpoint every client connects to, with Copy, and
// which host serves it. The note says why the offered endpoint may differ from
// the address in the browser bar: one that silently disagrees reads as a
// mistake, and an operator who "corrects" it walks into the exact failure the
// dedicated host exists to avoid.
func mcpEndpointRows(id, endpoint, apex string, dedicated bool, blockedHost string) []ui.Row {
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/mcp")
	which, why := ui.State("neutral", "Main domain"), "A proxy that challenges visitors can break this endpoint: a client has no browser to answer it. A dedicated mcp.<your-domain> host with the proxy off avoids that, and is offered here once it answers."
	switch {
	case dedicated:
		which, why = ui.State("ok", "Dedicated host"), host+" is offered instead of "+apex+" because it is not proxied, so a challenge on your main domain can never sit in front of it."
	case blockedHost != "":
		which, why = ui.State("warn", "Dedicated host blocked"), blockedHost+" is answered by something in front of this server. Switch it to DNS only at your DNS provider; until then the endpoint stays on your main domain."
	}
	return []ui.Row{
		{Label: "Endpoint", Hint: "The one URL every MCP client connects to, served by VayuPress itself.",
			Control: ui.HTML(`<code class="space-addr" id="` + id + `">` + string(ui.Text(endpoint)) + `</code><button type="button" class="btn btn--sm" data-copy="#` + id + `">Copy</button>`)},
		{Label: "Host", Hint: why, Control: which},
	}
}

// mcpGrantRow is one grant a page offers: what it allows, and the button that
// mints it. primary marks the grant a page wants an operator to reach for by
// default — a real decision, not styling: on a page whose common case is a
// shared team agent the safe grant should look like the default, and on a
// personal-assistant page it need not.
func mcpGrantRow(primary bool, name, desc, caps, keyLabel, button string) ui.Row {
	cls := "btn btn--sm"
	if primary {
		cls = "btn btn--primary btn--sm"
	}
	return ui.Row{Label: name, Hint: desc,
		Control: ui.HTML(`<button type="button" class="` + cls + `" data-mint="` + html.EscapeString(caps) + `" data-label="` + html.EscapeString(keyLabel) + `">` + html.EscapeString(button) + `</button>`)}
}

// mcpSnippet renders one copyable block. The template carries the placeholder the
// script substitutes; what is on screen before a key exists is a named
// placeholder rather than a blank, because a config with an empty token reads as
// broken rather than as pending.
func mcpSnippet(id, tpl string) string {
	shown := strings.ReplaceAll(tpl, keyTemplatePlaceholder, "YOUR_KEY_HERE")
	return `<pre class="cx-code font-mono" id="` + id + `" data-tpl="` + html.EscapeString(tpl) + `">` +
		html.EscapeString(shown) + `</pre>
  <div class="ak-cred-actions">
    <button type="button" class="btn btn--sm" data-copy="#` + id + `">Copy</button>
  </div>`
}

// mcpClientScript is the nonce-gated controller shared by every per-client page.
// It runs inside the shared bootstrap IIFE (adminOSShellFoot), so csrf() is
// already in scope.
//
// It handles three things and nothing else: mint a key from a [data-mint] button,
// copy from a [data-copy] button, and rewrite every [data-tpl] snippet with the
// freshly minted key.
const mcpClientScript = `
var mxStatus=document.getElementById('` + mcpStatusID + `');
function mxSet(t,isErr){if(mxStatus){mxStatus.textContent=t;mxStatus.style.color=isErr?'var(--danger)':'var(--ok)';}}
function mxPost(url,payload){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify(payload||{})}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
function mxCopy(text){if(navigator.clipboard){navigator.clipboard.writeText(text);}return true;}

var mxBanner=document.getElementById('` + mcpBannerID + `');
var mxTokenVal=document.getElementById('` + mcpTokenID + `');
function mxFill(tok){
  document.querySelectorAll('[data-tpl]').forEach(function(el){
    el.textContent=el.getAttribute('data-tpl').split('` + keyTemplatePlaceholder + `').join(tok);
  });
}
function mxShowToken(tok){
  if(mxTokenVal){mxTokenVal.value=tok;}
  if(mxBanner){mxBanner.hidden=false;mxBanner.scrollIntoView({behavior:'smooth',block:'start'});}
  mxFill(tok);
}
var mxCopyBtn=document.getElementById('` + mcpCopyID + `');
if(mxCopyBtn)mxCopyBtn.addEventListener('click',function(){if(mxTokenVal){mxTokenVal.select();mxCopy(mxTokenVal.value);mxSet('Key copied',false);}});
var mxDoneBtn=document.getElementById('` + mcpDoneID + `');
if(mxDoneBtn)mxDoneBtn.addEventListener('click',function(){location.reload();});

document.addEventListener('click',function(ev){
  var mintBtn=ev.target.closest('[data-mint]');
  if(mintBtn){
    var caps=mintBtn.getAttribute('data-mint').split(',');
    var label=mintBtn.getAttribute('data-label')||'MCP client';
    mintBtn.disabled=true;mxSet('Creating key…',false);
    mxPost('/os/api/apikeys/create',{label:label,capabilities:caps}).then(function(res){
      mintBtn.disabled=false;
      if(res.ok&&res.d.token){mxShowToken(res.d.token);mxSet('Key granted — copy the configuration above',false);}
      else{mxSet(res.d.detail||res.d.title||'Could not create key',true);}
    }).catch(function(e){mintBtn.disabled=false;mxSet('Error: '+e,true);});
    return;
  }
  var cp=ev.target.closest('[data-copy]');
  if(cp){
    var el=document.querySelector(cp.getAttribute('data-copy'));
    if(el){mxCopy(el.value!==undefined?el.value:el.textContent);mxSet('Copied',false);}
    return;
  }
});
`
