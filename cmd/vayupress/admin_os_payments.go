package main

// admin_os_payments.go — one-click card gateways for the Monetization console.
//
// The operator pastes their OWN Stripe secret key once; it is stored encrypted
// (internal/secrets, AES-256-GCM) and verified live against the Stripe API.
// Checkout is then a server-created, redirect-based hosted session — no embedded
// SDK, no browser JS, no CSP relaxation (ADR-0090). Money settles straight into
// the operator's own account (direct keys, not a platform). Both Stripe (cards)
// and PayPal (auto-renewing subscriptions) are wired here.

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
)

// zeroOne renders a bool as "1"/"0" for a data-* attribute.
func zeroOne(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// stripeStatus reports whether an enabled Stripe key is stored, plus its masked
// hint for display (never the secret itself).
func (a *App) stripeStatus(ctx context.Context) (connected bool, hint string) {
	if a.secrets == nil {
		return false, ""
	}
	creds, err := a.secrets.List(ctx)
	if err != nil {
		return false, ""
	}
	for _, c := range creds {
		if c.Provider == secrets.ProviderStripe {
			return c.Enabled && c.HasSecret, c.Hint
		}
	}
	return false, ""
}

// handleStripeConnect stores the operator's Stripe secret key (and optional
// publishable key + webhook signing secret) encrypted, and enables the gateway.
// An empty secret on a re-save preserves the existing key (Upsert semantics), so
// the operator can add a webhook secret later without re-pasting the API key.
func (a *App) handleStripeConnect(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secret store not initialised", "")
		return
	}
	var in struct {
		SecretKey      string `json:"secret_key"`
		PublishableKey string `json:"publishable_key"`
		WebhookSecret  string `json:"webhook_secret"`
		Enabled        *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "invalid request body", "")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	sk := strings.TrimSpace(in.SecretKey)
	// Sanity-check the shape so an obviously-wrong paste fails fast. Stripe secret
	// keys are sk_… (live/test) or rk_… (restricted).
	if sk != "" && !strings.HasPrefix(sk, "sk_") && !strings.HasPrefix(sk, "rk_") {
		writeAPIError(w, r, http.StatusBadRequest, "bad-key", "That does not look like a Stripe secret key (expected sk_… or rk_…).", "")
		return
	}
	if _, err := a.secrets.Upsert(r.Context(), secrets.ProviderStripe, "Stripe secret key", strings.TrimSpace(in.PublishableKey), sk, enabled, false); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "secrets-error", err.Error(), "")
		return
	}
	if wh := strings.TrimSpace(in.WebhookSecret); wh != "" {
		if _, err := a.secrets.Upsert(r.Context(), secrets.ProviderStripeWebhook, "Stripe webhook secret", "", wh, true, false); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "secrets-error", err.Error(), "")
			return
		}
	}
	logging.LogInfo("payments", "stripe gateway saved")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true, "enabled": enabled})
}

// handleStripeTest verifies the stored Stripe secret key by calling the Stripe
// API (GET /v1/account), so the operator gets instant confirmation the key works.
func (a *App) handleStripeTest(w http.ResponseWriter, r *http.Request) {
	key, on := a.stripeSecretKey(r.Context())
	if !on {
		writeAPIError(w, r, http.StatusBadRequest, "not-connected", "Connect a Stripe secret key first.", "")
		return
	}
	sc := payments.NewStripeClient(a.outboundClient, key)
	if err := sc.Ping(r.Context()); err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "stripe-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleStripeDisconnect disables the Stripe + webhook credentials without
// deleting them, so reconnecting doesn't require re-pasting the key.
func (a *App) handleStripeDisconnect(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secret store not initialised", "")
		return
	}
	_, _ = a.secrets.Upsert(r.Context(), secrets.ProviderStripe, "Stripe secret key", "", "", false, false)
	_, _ = a.secrets.Upsert(r.Context(), secrets.ProviderStripeWebhook, "Stripe webhook secret", "", "", false, false)
	logging.LogInfo("payments", "stripe gateway disconnected")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true})
}

// paymentGatewaysCard renders the Stripe one-click connect card plus its own
// nonce-scoped script. It is injected into the Monetization console.
func (a *App) paymentGatewaysCard(nonce string, ctx context.Context) string {
	connected, hint := a.stripeStatus(ctx)
	statusLine := `<strong>Not connected.</strong> <span class="text-sm muted">Paste your Stripe secret key to accept cards, Apple&nbsp;Pay and Google&nbsp;Pay through a secure hosted checkout.</span>`
	saveLabel := "Save &amp; connect"
	disconnect := ""
	if connected {
		statusLine = `<strong style="color:var(--color-success,#22c55e)">● Connected</strong> <span class="text-sm muted font-mono">` + html.EscapeString(hint) + `</span>`
		saveLabel = "Update key"
		disconnect = `<button type="button" class="btn btn--ghost btn--sm" id="pay-stripe-disconnect">Disconnect</button>`
	}
	return `<div class="card">
  <div class="settings-block-title">Card payments · Stripe <span class="muted text-xs">(one-click)</span></div>
  <p class="text-sm muted mb-4">` + statusLine + ` VayuPress never embeds Stripe's SDK — your reader is sent to a Stripe-hosted checkout and returns to your site, so nothing weakens your strict security policy. Payments go straight to <strong>your own</strong> Stripe account.</p>
  <div class="field">
    <label class="field-label" for="pay-stripe-sk">Stripe secret key</label>
    <input id="pay-stripe-sk" class="input font-mono" type="password" placeholder="sk_live_…  (leave blank to keep current)" autocomplete="new-password">
    <span class="field-hint">Stripe → Developers → API keys. Stored encrypted at rest (AES-256-GCM); never sent to readers.</span>
  </div>
  <div class="field">
    <label class="field-label" for="pay-stripe-wh">Webhook signing secret <span class="muted">(optional)</span></label>
    <input id="pay-stripe-wh" class="input font-mono" type="password" placeholder="whsec_…  (for cancellations &amp; renewals)" autocomplete="new-password">
    <span class="field-hint">Point a Stripe webhook at <code>/api/v1/stripe/webhook</code> and paste its signing secret to keep subscriptions in sync. Not required to start taking payments.</span>
  </div>
  <div style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
    <button type="button" class="btn btn--primary btn--sm" id="pay-stripe-save">` + saveLabel + `</button>
    <button type="button" class="btn btn--ghost btn--sm" id="pay-stripe-test">Test connection</button>
    ` + disconnect + `
    <span id="pay-stripe-msg" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('pay-stripe-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.style.color=e?'#ef4444':'';}
function jpost(url,body){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:body?JSON.stringify(body):null}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
function errMsg(d){return d&&(d.detail||(d.error&&(d.error.message||d.error))||d.title)||'Error';}
var saveBtn=document.getElementById('pay-stripe-save');
if(saveBtn)saveBtn.addEventListener('click',function(){
  var sk=((document.getElementById('pay-stripe-sk')||{}).value||'').trim();
  var wh=((document.getElementById('pay-stripe-wh')||{}).value||'').trim();
  saveBtn.disabled=true;show('Saving…',false);
  jpost('/os/api/payments/stripe/connect',{secret_key:sk,webhook_secret:wh,enabled:true}).then(function(res){
    saveBtn.disabled=false;
    if(res.ok){show('Saved ✓',false);setTimeout(function(){location.reload();},700);}else{show(errMsg(res.d),true);}
  }).catch(function(e){saveBtn.disabled=false;show('Error: '+e,true);});
});
var testBtn=document.getElementById('pay-stripe-test');
if(testBtn)testBtn.addEventListener('click',function(){
  testBtn.disabled=true;show('Testing…',false);
  jpost('/os/api/payments/stripe/test',null).then(function(res){
    testBtn.disabled=false;
    if(res.ok){show('Connection OK ✓',false);}else{show(errMsg(res.d),true);}
  }).catch(function(e){testBtn.disabled=false;show('Error: '+e,true);});
});
var dcBtn=document.getElementById('pay-stripe-disconnect');
if(dcBtn)dcBtn.addEventListener('click',function(){
  if(!confirm('Disconnect Stripe? Card checkout stops; your key is kept so you can reconnect.'))return;
  dcBtn.disabled=true;
  jpost('/os/api/payments/stripe/disconnect',null).then(function(res){if(res.ok){location.reload();}else{dcBtn.disabled=false;show('Error',true);}});
});
})();
</script>`
}

// ── PayPal (auto-renewing subscriptions) ──────────────────────────────────────

// paypalStatus reports whether enabled PayPal creds are stored, a masked hint,
// the (non-secret) client id, and the sandbox flag.
func (a *App) paypalStatus(ctx context.Context) (connected bool, hint, clientID string, sandbox bool) {
	sandbox = a.siteSettings != nil && a.siteSettings.Get(ctx, settings.KeyPayPalSandbox) == "on"
	if a.secrets == nil {
		return false, "", "", sandbox
	}
	creds, err := a.secrets.List(ctx)
	if err != nil {
		return false, "", "", sandbox
	}
	for _, c := range creds {
		if c.Provider == secrets.ProviderPayPal {
			return c.Enabled && c.HasSecret, c.Hint, c.Endpoint, sandbox
		}
	}
	return false, "", "", sandbox
}

// handlePayPalConnect stores the operator's PayPal REST credentials (client id in
// the credential endpoint, secret encrypted) and the sandbox flag, enabling the
// gateway. Both client id and secret are required.
func (a *App) handlePayPalConnect(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secret store not initialised", "")
		return
	}
	var in struct {
		ClientID string `json:"client_id"`
		Secret   string `json:"secret"`
		Sandbox  *bool  `json:"sandbox"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "invalid request body", "")
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	cid := strings.TrimSpace(in.ClientID)
	sec := strings.TrimSpace(in.Secret)
	if cid == "" || sec == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-key", "Enter both your PayPal client id and secret.", "")
		return
	}
	if _, err := a.secrets.Upsert(r.Context(), secrets.ProviderPayPal, "PayPal REST", cid, sec, enabled, false); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "secrets-error", err.Error(), "")
		return
	}
	if a.siteSettings != nil {
		val := "off"
		if in.Sandbox != nil && *in.Sandbox {
			val = "on"
		}
		_ = a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyPayPalSandbox: val})
	}
	logging.LogInfo("payments", "paypal gateway saved")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true, "enabled": enabled})
}

// handlePayPalTest verifies the stored PayPal credentials by acquiring an OAuth
// access token against the selected (live/sandbox) host.
func (a *App) handlePayPalTest(w http.ResponseWriter, r *http.Request) {
	cid, sec, sb, ok := a.paypalCreds(r.Context())
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "not-connected", "Connect PayPal first.", "")
		return
	}
	pp := payments.NewPayPalClient(a.outboundClient, cid, sec, sb)
	if err := pp.Ping(r.Context()); err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "paypal-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true})
}

// handlePayPalDisconnect disables the PayPal credential without deleting it.
func (a *App) handlePayPalDisconnect(w http.ResponseWriter, r *http.Request) {
	if a.secrets == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "secrets-error", "secret store not initialised", "")
		return
	}
	if creds, err := a.secrets.List(r.Context()); err == nil {
		for _, c := range creds {
			if c.Provider == secrets.ProviderPayPal {
				_ = a.secrets.SetEnabled(r.Context(), c.ID, false)
			}
		}
	}
	logging.LogInfo("payments", "paypal gateway disconnected")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"ok": true})
}

// paypalConnectCard renders the PayPal one-click connect card + its nonce script.
func (a *App) paypalConnectCard(nonce string, ctx context.Context) string {
	connected, hint, clientID, sandbox := a.paypalStatus(ctx)
	statusLine := `<strong>Not connected.</strong> <span class="text-sm muted">Add your PayPal REST credentials to accept auto-renewing PayPal subscriptions.</span>`
	saveLabel := "Save &amp; connect"
	disconnect := ""
	if connected {
		env := "Live"
		if sandbox {
			env = "Sandbox"
		}
		statusLine = `<strong style="color:var(--color-success,#22c55e)">● Connected</strong> <span class="text-sm muted font-mono">` + html.EscapeString(hint) + `</span> <span class="text-xs muted">· ` + env + `</span>`
		saveLabel = "Update credentials"
		disconnect = `<button type="button" class="btn btn--ghost btn--sm" id="pay-pp-disconnect">Disconnect</button>`
	}
	checked := ""
	if sandbox {
		checked = " checked"
	}
	return `<div class="card">
  <div class="settings-block-title">PayPal <span class="muted text-xs">(auto-renewing subscriptions)</span></div>
  <p class="text-sm muted mb-4">` + statusLine + ` VayuPress creates the PayPal billing plan for you and sends the reader to a PayPal-hosted approval page (no PayPal SDK, CSP untouched). Funds settle into <strong>your own</strong> PayPal account.</p>
  <div class="field">
    <label class="field-label" for="pay-pp-id">Client ID</label>
    <input id="pay-pp-id" class="input font-mono" type="text" value="` + html.EscapeString(clientID) + `" placeholder="AY…">
    <span class="field-hint">PayPal Developer Dashboard → Apps &amp; Credentials → your app.</span>
  </div>
  <div class="field">
    <label class="field-label" for="pay-pp-secret">Secret</label>
    <input id="pay-pp-secret" class="input font-mono" type="password" placeholder="EL…  (re-enter to update)" autocomplete="new-password">
    <span class="field-hint">Stored encrypted at rest (AES-256-GCM).</span>
  </div>
  <label class="field" style="display:flex;align-items:center;gap:.5rem;cursor:pointer">
    <input id="pay-pp-sandbox" type="checkbox"` + checked + `> <span class="text-sm">Use PayPal <strong>sandbox</strong> (testing credentials)</span>
  </label>
  <div style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:center">
    <button type="button" class="btn btn--primary btn--sm" id="pay-pp-save">` + saveLabel + `</button>
    <button type="button" class="btn btn--ghost btn--sm" id="pay-pp-test">Test connection</button>
    ` + disconnect + `
    <span id="pay-pp-msg" role="status" aria-live="polite" class="text-xs muted"></span>
  </div>
</div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('pay-pp-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.style.color=e?'#ef4444':'';}
function jpost(url,body){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:body?JSON.stringify(body):null}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
function errMsg(d){return d&&(d.detail||(d.error&&(d.error.message||d.error))||d.title)||'Error';}
var saveBtn=document.getElementById('pay-pp-save');
if(saveBtn)saveBtn.addEventListener('click',function(){
  var id=((document.getElementById('pay-pp-id')||{}).value||'').trim();
  var sec=((document.getElementById('pay-pp-secret')||{}).value||'').trim();
  var sb=!!(document.getElementById('pay-pp-sandbox')||{}).checked;
  saveBtn.disabled=true;show('Saving…',false);
  jpost('/os/api/payments/paypal/connect',{client_id:id,secret:sec,sandbox:sb,enabled:true}).then(function(res){
    saveBtn.disabled=false;
    if(res.ok){show('Saved ✓',false);setTimeout(function(){location.reload();},700);}else{show(errMsg(res.d),true);}
  }).catch(function(e){saveBtn.disabled=false;show('Error: '+e,true);});
});
var testBtn=document.getElementById('pay-pp-test');
if(testBtn)testBtn.addEventListener('click',function(){
  testBtn.disabled=true;show('Testing…',false);
  jpost('/os/api/payments/paypal/test',null).then(function(res){
    testBtn.disabled=false;
    if(res.ok){show('Connection OK ✓',false);}else{show(errMsg(res.d),true);}
  }).catch(function(e){testBtn.disabled=false;show('Error: '+e,true);});
});
var dcBtn=document.getElementById('pay-pp-disconnect');
if(dcBtn)dcBtn.addEventListener('click',function(){
  if(!confirm('Disconnect PayPal? New PayPal checkouts will stop.'))return;
  dcBtn.disabled=true;
  jpost('/os/api/payments/paypal/disconnect',null).then(function(res){if(res.ok){location.reload();}else{dcBtn.disabled=false;show('Error',true);}});
});
})();
</script>`
}
