package main

// admin_os_monetization.go — VayuOS Monetization console (/os/monetization).
//
// One surface for taking money: a headline of pending/paid/revenue, the order
// ledger with one-click "Mark paid" (which fulfils the member + emails a
// receipt) and "Cancel", plus the gateway configuration (offline instructions,
// currency, support email, and the connected-gateway webhook signing secret).
//
// CSP posture matches the rest of VayuOS: the only inline script carries the
// per-request nonce; every interpolated value is escaped before HTML emit; all
// writes go through CSRF-guarded fetches.

import (
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"

	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
)

// handleOSMonetization renders the Monetization console.
func (a *App) handleOSMonetization(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	ctx := r.Context()

	enabled := a.paymentsEnabled(ctx)
	currency := a.payCurrency(ctx)
	instructions := a.directInstructions(ctx)
	supportEmail := ""
	webhookConfigured := false
	if a.siteSettings != nil {
		supportEmail = a.siteSettings.Get(ctx, settings.KeyPaySupportEmail)
	}
	if a.secrets != nil {
		if s, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderPaymentGateway); s != "" {
			webhookConfigured = true
		}
	}

	var stats payments.Stats
	var orders []payments.Order
	if a.payments != nil {
		stats, _ = a.payments.Stats(ctx)
		orders, _ = a.payments.List(ctx, "", 200)
	}

	statusBanner := ""
	if !enabled {
		statusBanner = `<div class="settings-callout"><strong>Payments are off.</strong> <span class="text-sm muted">Readers cannot check out until you enable the Payments module.</span> <a class="btn btn--primary btn--sm mt-2" href="/os/tools">Enable in Tools &amp; Plugins →</a></div>`
	}

	body := `<div class="page-header">
  <h1>Monetization</h1>
  <div class="page-actions"><span id="mon-status" role="status" aria-live="polite" class="text-xs muted"></span></div>
</div>
` + statusBanner + `
<div class="stat-grid">
  <div class="stat-card"><div class="stat-card__label">Pending orders</div><div class="stat-card__value">` + strconv.Itoa(stats.Pending) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Paid orders</div><div class="stat-card__value">` + strconv.Itoa(stats.Paid) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Revenue collected</div><div class="stat-card__value">` + html.EscapeString(priceLabel(stats.Currency, stats.RevenueCents)) + `</div></div>
</div>

<div class="card">
  <div class="settings-block-title">Orders</div>
  <p class="text-sm muted mb-4">Every checkout records an order. For offline/direct payments, confirm receipt with <strong>Mark paid</strong> — that upgrades the member and emails them a receipt automatically.</p>
  ` + monetizationOrdersTable(orders) + `
</div>

<div class="card">
  <div class="settings-block-title">Direct / offline payment</div>
  <p class="text-sm muted mb-4">The dependency-free way to get paid. Publish how readers should pay (bank transfer, UPI, a payment link…); they quote their order reference, you confirm receipt above. No third-party gateway required.</p>
  <div class="field">
    <label class="field-label" for="mon-currency">Currency (ISO-4217)</label>
    <input id="mon-currency" class="input" type="text" maxlength="3" data-mon-key="` + settings.KeyPayCurrency + `" value="` + html.EscapeString(currency) + `" placeholder="USD" style="max-width:8rem;text-transform:uppercase">
  </div>
  <div class="field">
    <label class="field-label" for="mon-instructions">Payment instructions</label>
    <textarea id="mon-instructions" class="textarea font-mono" rows="6" data-mon-key="` + settings.KeyPayDirectInstructions + `" placeholder="Bank: …&#10;Account: …&#10;UPI: you@bank&#10;Or pay at: https://example.com/pay">` + html.EscapeString(instructions) + `</textarea>
    <span class="field-hint">Shown to readers on the checkout page and emailed with their order reference.</span>
  </div>
  <div class="field">
    <label class="field-label" for="mon-support">Support email (optional)</label>
    <input id="mon-support" class="input" type="email" data-mon-key="` + settings.KeyPaySupportEmail + `" value="` + html.EscapeString(supportEmail) + `" placeholder="billing@example.com">
  </div>
  <button type="button" class="btn btn--primary btn--sm" id="mon-save-btn">Save payment settings</button>
</div>

<div class="card">
  <div class="settings-block-title">Connected gateway (webhook)</div>
  <p class="text-sm muted mb-4">Connect any external processor. Configure it to POST a JSON event to <code>/api/v1/payments/webhook/&lt;name&gt;</code> with an <code>X-VayuPress-Signature</code> header (hex HMAC-SHA256 of the body, using the secret below) and a <code>reference</code> field matching the order. ` + webhookStatus(webhookConfigured) + `</p>
  <div class="field">
    <label class="field-label" for="mon-webhook-secret">Webhook signing secret</label>
    <input id="mon-webhook-secret" class="input font-mono" type="password" placeholder="Leave blank to keep current" autocomplete="new-password">
    <span class="field-hint">Stored encrypted at rest (AES-256-GCM). Used to verify every inbound gateway webhook.</span>
  </div>
  <button type="button" class="btn btn--primary btn--sm" id="mon-webhook-save">Save webhook secret</button>
</div>

<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('action-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
function jpost(url){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}}).then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});});}
function jsave(key,val){return fetch('/os/api/settings',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({key:key,value:val})});}
document.querySelectorAll('[data-order-action]').forEach(function(b){
  b.addEventListener('click',function(){
    var act=b.getAttribute('data-order-action');var id=b.getAttribute('data-id');
    if(act==='paid'&&!confirm('Confirm payment received for this order? The member will be upgraded and emailed a receipt.'))return;
    if(act==='cancel'&&!confirm('Cancel this order?'))return;
    b.disabled=true;
    jpost('/os/api/orders/'+encodeURIComponent(id)+'/'+act).then(function(res){
      if(res.ok){show(act==='paid'?'Payment confirmed':'Order canceled',false);setTimeout(function(){location.reload();},600);}
      else{b.disabled=false;show(res.d.detail||res.d.title||'Error',true);}
    }).catch(function(e){b.disabled=false;show('Error: '+e,true);});
  });
});
var saveBtn=document.getElementById('mon-save-btn');
if(saveBtn)saveBtn.addEventListener('click',function(){
  var fields=document.querySelectorAll('[data-mon-key]');var chain=Promise.resolve();var ok=true;
  saveBtn.disabled=true;show('Saving…',false);
  fields.forEach(function(el){chain=chain.then(function(){return jsave(el.getAttribute('data-mon-key'),el.value).then(function(r){if(!r.ok)ok=false;});});});
  chain.then(function(){saveBtn.disabled=false;show(ok?'Payment settings saved':'Some settings failed',!ok);}).catch(function(e){saveBtn.disabled=false;show('Error: '+e,true);});
});
var whBtn=document.getElementById('mon-webhook-save');
if(whBtn)whBtn.addEventListener('click',function(){
  var sec=(document.getElementById('mon-webhook-secret')||{}).value||'';
  if(!sec.trim()){show('Enter a secret first',true);return;}
  whBtn.disabled=true;show('Saving…',false);
  fetch('/os/api/credentials/save',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({provider:'payment_gateway',label:'Payment gateway webhook',secret:sec,enabled:true})})
    .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
    .then(function(res){whBtn.disabled=false;if(res.ok){show('Webhook secret saved',false);}else{show(res.d.detail||'Error',true);}})
    .catch(function(e){whBtn.disabled=false;show('Error: '+e,true);});
});
})();
</script>`

	writeOSHTML(w, adminOSLayout(nonce, "Monetization", "monetization", cfg, htmpl.HTML(body)))
}

// monetizationOrdersTable renders the order ledger, newest first.
func monetizationOrdersTable(orders []payments.Order) string {
	if len(orders) == 0 {
		return `<div class="table-empty">No orders yet. They appear here as readers check out.</div>`
	}
	rows := ""
	for i := range orders {
		o := orders[i]
		actions := ""
		if o.Status == payments.StatusPending {
			actions = `<button type="button" class="btn btn--primary btn--sm" data-order-action="paid" data-id="` + html.EscapeString(o.ID) + `">Mark paid</button>
        <button type="button" class="btn btn--ghost btn--sm" data-order-action="cancel" data-id="` + html.EscapeString(o.ID) + `">Cancel</button>`
		}
		rows += `<tr>
  <td class="row-title"><code>` + html.EscapeString(o.Reference) + `</code>
    <div class="row-meta">` + html.EscapeString(o.Email) + `</div></td>
  <td>` + html.EscapeString(o.TierSlug) + ` <span class="muted text-xs">· ` + html.EscapeString(o.Cadence) + `</span></td>
  <td>` + html.EscapeString(priceLabel(o.Currency, o.AmountCents)) + `</td>
  <td>` + html.EscapeString(o.Gateway) + `</td>
  <td>` + orderStatusPill(o.Status) + `</td>
  <td class="muted text-sm">` + o.CreatedAt.UTC().Format("2 Jan 2006") + `</td>
  <td class="row-actions">` + actions + `</td>
</tr>`
	}
	return `<div class="table-wrap"><table class="table">
  <thead><tr><th>Reference</th><th>Tier</th><th>Amount</th><th>Gateway</th><th>Status</th><th>Created</th><th></th></tr></thead>
  <tbody>` + rows + `</tbody>
</table></div>`
}

func orderStatusPill(status string) string {
	switch status {
	case payments.StatusPaid:
		return `<span class="status-pill status-pill--live">● paid</span>`
	case payments.StatusPending:
		return `<span class="status-pill status-pill--draft">● pending</span>`
	default:
		return `<span class="status-pill">● ` + html.EscapeString(status) + `</span>`
	}
}

func webhookStatus(configured bool) string {
	if configured {
		return `<strong style="color:var(--color-success,#22c55e)">A signing secret is configured.</strong>`
	}
	return `<strong>No signing secret set yet.</strong>`
}
