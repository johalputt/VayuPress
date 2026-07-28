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

// mcpClientStats is the at-a-glance strip, matching Monetization.
//
// It counts only the keys the calling page minted, identified by label prefix.
// Counting every connector would put a number on the page that the page cannot
// explain — a key granted to Claude is not a Buzz agent — and an operator
// auditing which clients can reach their site needs those separated, not summed.
func mcpClientStats(endpoint string, keys []apikeys.Key, labelPrefix, countLabel string, dedicated bool, blockedHost string) string {
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
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/mcp")

	hostLabel, hostTone := "Main domain", ""
	switch {
	case dedicated:
		hostLabel = "Dedicated host"
	case blockedHost != "":
		hostLabel, hostTone = "Dedicated host blocked", "warn"
	}
	// A full-control key hands a client the whole site, so it gets its own tile
	// and its own tone: an operator should see how many exist without reading a
	// table.
	fullTone := ""
	if full > 0 {
		fullTone = "warn"
	}
	tile := func(value, label, tone string) string {
		cls := "stat-card"
		if tone != "" {
			cls += " stat-card--" + tone
		}
		return `<div class="` + cls + `"><div class="stat-card__label">` + html.EscapeString(label) +
			`</div><div class="stat-card__value">` + html.EscapeString(value) + `</div></div>`
	}
	return `<div class="stat-grid">` +
		tile(strconv.Itoa(live), countLabel, "") +
		tile(strconv.Itoa(full), "Full-control keys", fullTone) +
		tile(host, "Serving on", "") +
		tile(hostLabel, "Endpoint host", hostTone) +
		`</div>`
}

// mcpGrantTile renders one choice in a grant grid. primary marks the button a
// page wants an operator to reach for by default — which is a real decision, not
// styling: on a page whose common case is a shared team agent the safe grant
// should look like the default, and on a personal-assistant page it need not.
func mcpGrantTile(primary bool, name, badge, badgeCls, desc, caps, keyLabel, button string) string {
	btnCls := "btn"
	if primary {
		btnCls = "btn btn--primary"
	}
	extra := ""
	if caps == "*:*" {
		extra = " cx-grant--full"
	}
	return `<div class="cx-grant` + extra + `">
      <div class="cx-grant-head">
        <span class="settings-row-label">` + name + `</span>
        <span class="badge` + badgeCls + `">` + badge + `</span>
      </div>
      <p class="text-sm muted">` + desc + `</p>
      <button type="button" class="` + btnCls + `" data-mint="` + caps + `" data-label="` + keyLabel + `">` + button + `</button>
    </div>`
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
function mxSet(t,isErr){if(mxStatus){mxStatus.textContent=t;mxStatus.style.color=isErr?'var(--color-danger,#ef4444)':'var(--color-success,#22c55e)';}}
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
