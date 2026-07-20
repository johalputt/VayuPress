package main

// admin_os_payments.go — one-click card gateways for the Monetization console.
//
// The operator pastes their OWN Stripe secret key once; it is stored encrypted
// (internal/secrets, AES-256-GCM) and verified live against the Stripe API.
// Checkout is then a server-created, redirect-based Stripe-hosted session — no
// embedded SDK, no browser Stripe.js, no CSP relaxation (ADR-0090). Money settles
// straight into the operator's own Stripe account (direct keys, not Connect).
// PayPal lands here in a later phase alongside Stripe.

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/secrets"
)

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
