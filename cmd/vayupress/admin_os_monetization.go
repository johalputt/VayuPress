// SPDX-License-Identifier: Apache-2.0

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

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
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
	premiumPriceStr := strconv.Itoa(a.premiumMailIDPriceCents(ctx))
	mailidTerms := a.mailIDTerms(ctx)
	var pricedPosts []members.PricedPost
	var grants []members.PremiumGrant
	gPending, gPaid, gClaimed := 0, 0, 0
	if a.members != nil {
		pricedPosts, _ = a.members.ListPricedPosts(ctx, 100)
		grants, _ = a.members.AllPremiumGrants(ctx, 50)
		gPending, gPaid, gClaimed = a.members.PremiumGrantCounts(ctx)
	}
	premiumSold := gPaid + gClaimed
	paidMembers := 0
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM members WHERE tier NOT IN ('free','')`).Scan(&paidMembers)
	}

	var stats payments.Stats
	var orders []payments.Order
	if a.payments != nil {
		stats, _ = a.payments.Stats(ctx)
		orders, _ = a.payments.List(ctx, "", 200)
	}

	// Connect state per gateway, for the accordion summary chips.
	stripeConnected, _ := a.stripeStatus(ctx)
	paypalConnected, _, _, _ := a.paypalStatus(ctx)
	btcpayConnected, _, _, _ := a.btcpayStatus(ctx)

	statusBanner := ""
	if !enabled {
		statusBanner = `<div class="settings-callout"><strong>Payments are off.</strong> <span class="text-sm muted">Readers cannot check out until you enable the Payments module.</span> <a class="btn btn--primary btn--sm mt-2" href="/os/tools">Enable in Tools &amp; Plugins →</a></div>`
	}

	revCurrency := stats.Currency
	if revCurrency == "" {
		revCurrency = currency
	}

	directCard := `<div class="card">
  <div class="settings-block-title">Direct / offline payment</div>
  <p class="text-sm muted mb-4">The dependency-free way to get paid. Publish how readers should pay (bank transfer, UPI, a payment link…); they quote their order reference, you confirm receipt in the ledger below. No third-party gateway required.</p>
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
</div>`

	connectedCard := `<div class="card">
  <div class="settings-block-title">Connected gateway (webhook)</div>
  <p class="text-sm muted mb-4">Connect any external processor. Configure it to POST a JSON event to <code>/api/v1/payments/webhook/&lt;name&gt;</code> with an <code>X-VayuPress-Signature</code> header (hex HMAC-SHA256 of the body, using the secret below) and a <code>reference</code> field matching the order. ` + webhookStatus(webhookConfigured) + `</p>
  <div class="field">
    <label class="field-label" for="mon-webhook-secret">Webhook signing secret</label>
    <input id="mon-webhook-secret" class="input font-mono" type="password" placeholder="Leave blank to keep current" autocomplete="new-password">
    <span class="field-hint">Stored encrypted at rest (AES-256-GCM). Used to verify every inbound gateway webhook.</span>
  </div>
  <button type="button" class="btn btn--primary btn--sm" id="mon-webhook-save">Save webhook secret</button>
</div>`

	mailidMarketCard := `<div class="card">
  <div class="settings-block-title">Premium mail-ID marketplace</div>
  <p class="text-sm muted mb-4">Members buy premium (vanity) VayuMail addresses from their account. Live sales below — <strong>` + strconv.Itoa(gClaimed) + `</strong> active, <strong>` + strconv.Itoa(gPaid) + `</strong> awaiting activation, <strong>` + strconv.Itoa(gPending) + `</strong> awaiting payment.</p>
  ` + premiumGrantsTable(grants) + `
  <div class="mt-2"><a class="btn btn--primary btn--sm" href="/os/monetization/mailids">Manage premium IDs →</a></div>
</div>`

	addrMarketCard := `<div class="card">
  <div class="settings-block-title">VayuMail address marketplace</div>
  <p class="text-sm muted mb-4">Premium (vanity) addresses — ultra-short handles and sought-after words — are held back from the free member claim so you can sell them. Set their price, and the terms a member must accept before any address is provisioned to them.</p>
  <div class="field">
    <label class="field-label" for="mon-mailid-price">Premium address price</label>
    <input id="mon-mailid-price" class="input" type="number" min="0" step="1" data-mail-key="` + settings.KeyPremiumMailIDPriceCents + `" value="` + html.EscapeString(premiumPriceStr) + `" placeholder="500" style="max-width:10rem">
    <span class="field-hint">In minor units of your checkout currency (e.g. 500 = ` + html.EscapeString(priceLabel(currency, 500)) + `).</span>
  </div>
  <div class="field">
    <label class="field-label" for="mon-mailid-terms">Mailbox terms (acceptable-use agreement)</label>
    <textarea id="mon-mailid-terms" class="textarea" rows="6" data-mail-key="` + settings.KeyMailIDTerms + `" placeholder="Members must accept these terms before an address is provisioned…">` + html.EscapeString(mailidTerms) + `</textarea>
    <span class="field-hint">Shown with a required &ldquo;I agree&rdquo; checkbox on the claim form. Every acceptance is recorded (address + a hash of this text + time) as your proof of agreement. Leave blank to disable the requirement.</span>
  </div>
  <button type="button" class="btn btn--primary btn--sm" id="mon-mailid-save">Save mailbox settings</button>
</div>`

	paidPostsCard := `<div class="card">
  <div class="settings-block-title">Paid posts</div>
  <p class="text-sm muted mb-4">Charge a one-time price for access to a single post — readers buy it (card or offline) without a subscription. Set a post's access level and price by slug; a price of 0 removes the individual sale.</p>
  <div class="field" style="display:flex;gap:.5rem;flex-wrap:wrap;align-items:flex-end">
    <div style="flex:2;min-width:10rem"><label class="field-label" for="pp-slug">Post slug</label>
    <input id="pp-slug" class="input" type="text" placeholder="my-post" autocomplete="off" spellcheck="false"></div>
    <div style="flex:1;min-width:8rem"><label class="field-label" for="pp-level">Access</label>
    <select id="pp-level" class="input"><option value="paid">Paid members</option><option value="members">Members</option><option value="public">Public</option></select></div>
    <div style="flex:1;min-width:7rem"><label class="field-label" for="pp-price">Price (cents)</label>
    <input id="pp-price" class="input" type="number" min="0" step="1" placeholder="300"></div>
    <button type="button" class="btn btn--primary btn--sm" id="pp-save">Set</button>
  </div>
  ` + paidPostsTable(pricedPosts, currency) + `
</div>`

	ordersCard := `<div class="card">
  <div class="settings-block-title">Order ledger</div>
  <p class="text-sm muted mb-4">Every checkout records an order. For offline/direct payments, confirm receipt with <strong>Mark paid</strong> — that fulfils the purchase and emails a receipt automatically.</p>
  ` + monetizationOrdersTable(orders) + `
</div>`

	body := `<div class="page-header">
  <h1>Monetization</h1>
  <div class="page-actions"><span id="mon-status" role="status" aria-live="polite" class="text-xs muted"></span></div>
</div>
<p class="page-sub">Your whole revenue engine in one place — payments, membership plans, the premium mail-ID marketplace, paid posts and every order. Tap a card to expand it.</p>
` + statusBanner + `
<div class="stat-grid">
  <div class="stat-card"><div class="stat-card__label">Revenue collected</div><div class="stat-card__value">` + html.EscapeString(priceLabel(revCurrency, stats.RevenueCents)) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Paid members</div><div class="stat-card__value">` + strconv.Itoa(paidMembers) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Pending orders</div><div class="stat-card__value">` + strconv.Itoa(stats.Pending) + `</div></div>
  <div class="stat-card"><div class="stat-card__label">Premium addresses sold</div><div class="stat-card__value">` + strconv.Itoa(premiumSold) + `</div></div>
</div>

<div class="section-head"><span class="section-head__title">Payment methods</span><span class="section-head__hint">Cards, PayPal, crypto (anonymous-friendly) — or take payments directly. Funds always settle into your own accounts.</span></div>
<div class="mon-stack">` +
		monAcc("💳", "Card payments · Stripe", "Cards, Apple&nbsp;Pay &amp; Google&nbsp;Pay via a hosted checkout", monChip(stripeConnected, "Connected", "Not set up"), false, a.paymentGatewaysCard(nonce, ctx)) +
		monAcc("🅿️", "PayPal", "Auto-renewing subscriptions", monChip(paypalConnected, "Connected", "Not set up"), false, a.paypalConnectCard(nonce, ctx)) +
		monAcc("🪙", "Crypto · BTCPay Server", "BTC · XMR · ETH · USDT — for anonymous / Tor buyers", monChip(btcpayConnected, "Connected", "Not set up"), false, a.btcpayConnectCard(nonce, ctx)) +
		monAcc("🏦", "Direct / offline payment", "Bank transfer, UPI or any link — no gateway", `<span class="mon-chip mon-chip--on">● Always on</span>`, false, directCard) +
		monAcc("🔌", "Connected gateway (webhook)", "Any external processor via a signed webhook", monChip(webhookConfigured, "Configured", "Not set up"), false, connectedCard) +
		`</div>

<div class="section-head"><span class="section-head__title">Products &amp; pricing</span><span class="section-head__hint">Everything you sell — mail-IDs, paid posts, plans</span></div>
<div class="mon-stack">` +
		monAcc("✉️", "Premium mail-ID marketplace", strconv.Itoa(premiumSold)+" sold · "+strconv.Itoa(gPending)+" awaiting payment", monChip(premiumSold > 0, "Live", "No sales yet"), false, mailidMarketCard) +
		monAcc("🏷️", "VayuMail address marketplace", "Price &amp; terms for vanity addresses", `<span class="mon-chip mon-chip--on">● `+html.EscapeString(priceLabel(currency, a.premiumMailIDPriceCents(ctx)))+`</span>`, false, addrMarketCard) +
		monAcc("📄", "Paid posts", "One-time access pricing, per post", monChip(len(pricedPosts) > 0, strconv.Itoa(len(pricedPosts))+" priced", "None yet"), false, paidPostsCard) +
		`</div>

<div class="section-head"><span class="section-head__title">Orders</span><span class="section-head__hint">Every payment — memberships, mail-IDs &amp; paid posts</span></div>
<div class="mon-stack">` +
		monAcc("🧾", "Order ledger", "Confirm offline payments · full history", monChip(stats.Pending > 0, strconv.Itoa(stats.Pending)+" pending", "All settled"), true, ordersCard) +
		`</div>

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
var midBtn=document.getElementById('mon-mailid-save');
if(midBtn)midBtn.addEventListener('click',function(){
  var fields=document.querySelectorAll('[data-mail-key]');var chain=Promise.resolve();var ok=true;
  midBtn.disabled=true;show('Saving…',false);
  fields.forEach(function(el){chain=chain.then(function(){return jsave(el.getAttribute('data-mail-key'),el.value).then(function(r){if(!r.ok)ok=false;});});});
  chain.then(function(){midBtn.disabled=false;show(ok?'Mailbox settings saved':'Some settings failed',!ok);}).catch(function(e){midBtn.disabled=false;show('Error: '+e,true);});
});
var ppBtn=document.getElementById('pp-save');
if(ppBtn)ppBtn.addEventListener('click',function(){
  var slug=(document.getElementById('pp-slug').value||'').trim();
  var level=document.getElementById('pp-level').value;
  var price=parseInt(document.getElementById('pp-price').value||'0',10)||0;
  if(!slug){show('Enter a post slug first',true);return;}
  ppBtn.disabled=true;show('Saving…',false);
  fetch('/api/v1/admin/articles/'+encodeURIComponent(slug)+'/access',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({level:level,price_cents:price})})
    .then(function(r){ppBtn.disabled=false;if(r.ok){show('Saved '+slug,false);setTimeout(function(){location.reload();},600);}else{r.json().then(function(d){show((d.error&&d.error.message)||'Error',true);}).catch(function(){show('Error',true);});}})
    .catch(function(e){ppBtn.disabled=false;show('Error: '+e,true);});
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

	writeOSHTML(w, r, adminOSLayout(nonce, "Monetization", "monetization", cfg, htmpl.HTML(body)))
}

// paidPostsTable lists the posts that carry a one-time price.
func paidPostsTable(posts []members.PricedPost, currency string) string {
	if len(posts) == 0 {
		return `<p class="text-sm muted">No paid posts yet. Set a price above to sell one-time access to a post.</p>`
	}
	rows := ""
	for _, p := range posts {
		rows += `<tr>` +
			`<td class="row-title"><code>` + html.EscapeString(p.Slug) + `</code></td>` +
			`<td>` + html.EscapeString(p.Level) + `</td>` +
			`<td>` + html.EscapeString(priceLabel(currency, p.PriceCents)) + `</td>` +
			`</tr>`
	}
	return `<div class="table-wrap"><table class="table">` +
		`<thead><tr><th>Slug</th><th>Access</th><th>Price</th></tr></thead>` +
		`<tbody>` + rows + `</tbody></table></div>`
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
  <td>` + html.EscapeString(orderProductLabel(o.TierSlug)) + `</td>
  <td>` + html.EscapeString(priceLabel(o.Currency, o.AmountCents)) + `</td>
  <td>` + html.EscapeString(o.Gateway) + `</td>
  <td>` + orderStatusPill(o.Status) + `</td>
  <td class="muted text-sm">` + config.FormatSite(o.CreatedAt, "2 Jan 2006") + `</td>
  <td class="row-actions">` + actions + `</td>
</tr>`
	}
	return `<div class="table-wrap"><table class="table">
  <thead><tr><th>Reference</th><th>Product</th><th>Amount</th><th>Gateway</th><th>Status</th><th>Created</th><th></th></tr></thead>
  <tbody>` + rows + `</tbody>
</table></div>`
}

// orderProductLabel decodes the sentinel tier slugs the one-time products use so
// the order ledger reads plainly.
func orderProductLabel(tierSlug string) string {
	switch tierSlug {
	case mailIDOrderTier:
		return "Premium mail-ID"
	case postOrderTier:
		return "Paid post"
	case adOrderTier:
		return "Ad placement"
	default:
		return "Membership: " + tierSlug
	}
}

// premiumGrantPill maps a premium-address grant's status to a coloured pill.
func premiumGrantPill(status string) string {
	switch status {
	case members.GrantClaimed:
		return `<span class="status-pill status-pill--live">● active</span>`
	case members.GrantPaid:
		return `<span class="status-pill status-pill--draft">● paid · awaiting activation</span>`
	case members.GrantPending:
		return `<span class="status-pill">● awaiting payment</span>`
	default:
		return `<span class="status-pill">● ` + html.EscapeString(status) + `</span>`
	}
}

// premiumGrantsTable renders recent premium-address sales (read-only overview).
func premiumGrantsTable(grants []members.PremiumGrant) string {
	if len(grants) == 0 {
		return `<div class="table-empty">No premium addresses sold yet. They appear here as members buy vanity IDs from their account.</div>`
	}
	rows := ""
	for i := range grants {
		g := grants[i]
		rows += `<tr>` +
			`<td class="row-title"><code>` + html.EscapeString(g.Address()) + `</code></td>` +
			`<td class="muted text-sm">` + html.EscapeString(g.Email) + `</td>` +
			`<td>` + premiumGrantPill(g.Status) + `</td>` +
			`<td class="muted text-sm">` + config.FormatSite(g.CreatedAt, "2 Jan 2006") + `</td>` +
			`</tr>`
	}
	return `<div class="table-wrap"><table class="table">` +
		`<thead><tr><th>Address</th><th>Buyer</th><th>Status</th><th>Purchased</th></tr></thead>` +
		`<tbody>` + rows + `</tbody></table></div>`
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

// monChip renders a small connected/not-connected status pill for an accordion
// summary, so the state of each option is readable at a glance while collapsed.
func monChip(on bool, onLabel, offLabel string) string {
	if on {
		return `<span class="mon-chip mon-chip--on">● ` + html.EscapeString(onLabel) + `</span>`
	}
	return `<span class="mon-chip mon-chip--off">○ ` + html.EscapeString(offLabel) + `</span>`
}

// monAcc wraps a card body in a premium, animated collapsible accordion. The
// summary carries an icon, title, one-line subtitle and a status chip; the body
// (an existing card) reveals with a smooth fade/slide and the chevron rotates.
// It is pure CSS (native <details>) — no JS, CSP-safe, keyboard-accessible.
func monAcc(icon, title, subtitle, chip string, open bool, body string) string {
	openAttr := ""
	if open {
		openAttr = " open"
	}
	return `<details class="mon-acc"` + openAttr + `>
  <summary class="mon-acc__sum">
    <span class="mon-acc__ic" aria-hidden="true">` + icon + `</span>
    <span class="mon-acc__head"><span class="mon-acc__title">` + title + `</span><span class="mon-acc__sub">` + subtitle + `</span></span>
    ` + chip + `
    <svg class="mon-acc__chev" viewBox="0 0 20 20" width="16" height="16" fill="none" aria-hidden="true"><path d="M6 8l4 4 4-4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>
  </summary>
  <div class="mon-acc__body">` + body + `</div>
</details>`
}
