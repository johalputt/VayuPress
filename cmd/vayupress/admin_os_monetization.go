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

	"github.com/johalputt/vayupress/internal/ads"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
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
		supportEmail = a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyPaySupportEmail)
	}
	if a.secrets != nil {
		if s, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderPaymentGateway); s != "" {
			webhookConfigured = true
		}
	}
	premiumPriceStr := strconv.Itoa(a.premiumMailIDPriceCents(ctx))
	mailidTerms := a.mailIDTerms(ctx)
	var pricedPosts []members.PricedPost
	gPending, gPaid, gClaimed := 0, 0, 0
	if a.members != nil {
		pricedPosts, _ = a.members.ListPricedPosts(ctx, 100)
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

	revCurrency := stats.Currency
	if revCurrency == "" {
		revCurrency = currency
	}
	tiers := 0
	if a.members != nil {
		if ts, err := a.members.ListTiers(ctx, true); err == nil {
			tiers = len(ts)
		}
	}
	adsWaiting := 0
	if a.ads != nil {
		if pending, err := a.ads.ListByStatus(ctx, ads.StatusPendingReview); err == nil {
			adsWaiting = len(pending)
		}
	}

	count := func(n int, word string) string { return strconv.Itoa(n) + " " + word + plural(n) }

	// The page's state, beside its title: whether readers can pay, what has
	// come in, and what is waiting on the operator.
	state := ui.State("neutral", "Payments are off")
	action := ui.HTML(`<button type="button" class="btn btn--primary btn--sm" data-action="module-on" data-module="payments">Turn on payments</button>`)
	if enabled {
		line := "Taking payments"
		if stats.RevenueCents > 0 {
			line += " · " + priceLabel(revCurrency, stats.RevenueCents) + " collected"
		}
		state, action = ui.State("ok", line), ""
	}
	if stats.Pending > 0 {
		state += ui.HTML(" ") + ui.State("warn", count(stats.Pending, "order")+" to confirm")
	}

	// A gateway row: its state, and the button that opens its form in a sheet.
	gateway := func(icon, label, hint string, on bool, onWord, sheet string) ui.Row {
		st, verb := ui.State("neutral", "Not set up"), "Set up"
		if on {
			st, verb = ui.State("ok", onWord), "Manage"
		}
		return ui.Row{Icon: icon, Label: label, Hint: hint,
			Control: st + ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="`+sheet+`">`+verb+`</button>`)}
	}
	// A row that leads to where the thing is managed, with its count.
	goRow := func(label, hint, count, href string) ui.Row {
		return ui.Row{Label: label, Hint: hint,
			Control: ui.HTML(`<a class="settings-row-go" href="` + href + `">` + string(ui.Text(count)) + string(ui.Icon("chev-r")) + `</a>`)}
	}
	field := func(id, key, kind, label, value string) ui.HTML {
		return settingControl(settingField{ID: id, Key: key, Kind: kind, Label: label}, value)
	}

	pay := ui.Rows(
		gateway("card", "Cards", "Stripe, with Apple Pay and Google Pay, through your own keys.", stripeConnected, "Connected", "mon-stripe"),
		gateway("coin", "PayPal", "Subscriptions that renew on their own.", paypalConnected, "Connected", "mon-paypal"),
		gateway("key", "Crypto, through your BTCPay Server", "Bitcoin, Monero, Ethereum and stablecoins, paid into your own wallet. No processor, no KYC.", btcpayConnected, "Connected", "mon-btcpay"),
		ui.Row{Icon: "send", Label: "Direct transfer", Hint: "Bank transfer, UPI or a payment link, confirmed by you in the orders below.", Control: ui.State("ok", "Always on")},
		gateway("plug", "Another processor", "Any gateway that can send a signed webhook.", webhookConfigured, "Connected", "mon-webhook"),
	)
	checkout := ui.Rows(
		ui.Row{Label: "Currency", Hint: "Three letters, ISO 4217: USD, EUR, INR.", ID: "mon-currency",
			Control: field("mon-currency", settings.KeyPayCurrency, "text", "Currency", currency)},
		ui.Row{Label: "How to pay you directly", Hint: "Shown at checkout and emailed with the order reference: bank details, a UPI address, a payment link.", ID: "mon-instructions",
			Control: field("mon-instructions", settings.KeyPayDirectInstructions, "textarea", "How to pay you directly", instructions)},
		ui.Row{Label: "Billing email", Hint: "Where readers write about a payment. Optional.", ID: "mon-support",
			Control: field("mon-support", settings.KeyPaySupportEmail, "email", "Billing email", supportEmail)},
	)
	sell := ui.Rows(
		goRow("Membership tiers", "What members pay for, and what each tier opens.", count(tiers, "tier"), "/os/members"),
		ui.Row{Label: "Paid posts", Hint: "A one-time unlock on a single post.",
			Control: ui.HTML(`<span class="settings-row-count">` + string(ui.Text(count(len(pricedPosts), "post"))) + `</span><button type="button" class="btn btn--sm" data-sheet="mon-paid-posts">Price a post</button>`)},
		goRow("Premium addresses", "Short and sought-after mail addresses, held back for sale.", strconv.Itoa(premiumSold)+" sold · "+strconv.Itoa(gPending)+" awaiting payment", "/os/monetization/mailids"),
		goRow("Member ads", "Image ads your members buy, shown once you approve them.", strconv.Itoa(adsWaiting)+" waiting", "/os/ads"),
	)
	addresses := ui.Rows(
		ui.Row{Label: "Price", Hint: "In minor units of your currency: 500 is " + priceLabel(currency, 500) + ".", ID: "mon-mailid-price",
			Control: field("mon-mailid-price", settings.KeyPremiumMailIDPriceCents, "text", "Price", premiumPriceStr)},
		ui.Row{Label: "Terms a buyer accepts", Hint: "Shown with a required checkbox before an address is issued; each acceptance is kept with a hash of this text. Empty means none.", ID: "mon-mailid-terms",
			Control: field("mon-mailid-terms", settings.KeyMailIDTerms, "textarea", "Terms a buyer accepts", mailidTerms)},
	)

	paidPostsSheet := `<p class="text-sm muted mb-4">Set a post's access and its one-time price by its slug. A price of 0 ends the single sale.</p>
<div class="field"><label class="field-label" for="pp-slug">Post slug</label><input id="pp-slug" class="input" type="text" placeholder="my-post" autocomplete="off" spellcheck="false"></div>
<div class="field"><label class="field-label" for="pp-level">Who can read it</label><select id="pp-level" class="input"><option value="paid">Paid members</option><option value="members">Members</option><option value="public">Everyone</option></select></div>
<div class="field"><label class="field-label" for="pp-price">Price, in cents</label><input id="pp-price" class="input" type="number" min="0" step="1" placeholder="300"></div>
<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" id="pp-save">Set the price</button></div>
<div class="mt-4">` + paidPostsTable(pricedPosts, currency) + `</div>`
	webhookSheet := `<p class="text-sm muted mb-4">Point the processor at <code>/api/v1/payments/webhook/&lt;name&gt;</code>. It signs each JSON event with <code>X-VayuPress-Signature</code> (hex HMAC-SHA256 of the body with this secret) and sends the order's <code>reference</code>. ` + webhookStatus(webhookConfigured) + `</p>
<div class="field"><label class="field-label" for="mon-webhook-secret">Signing secret</label><input id="mon-webhook-secret" class="input font-mono" type="password" placeholder="Leave empty to keep the current one" autocomplete="new-password"><span class="field-hint">Stored encrypted (AES-256-GCM).</span></div>
<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" id="mon-webhook-save">Save the secret</button></div>`

	ordersTable := ui.HTML(monetizationOrdersTable(orders))
	page := ui.SettingsPage("Monetization", state, "",
		ui.HTML(func() string {
			if action == "" {
				return ""
			}
			return `<div class="page-lead">` + string(action) + `</div>`
		}()),
		ui.Section("How people pay", "Funds settle into your own accounts", pay),
		ui.Section("Checkout", "", checkout),
		ui.Section("What you sell", "", sell),
		ui.Section("Premium addresses", "", addresses),
		ui.Section("Orders", "Confirm a direct payment once it arrives", ordersTable),
		ui.Sheet("mon-stripe", "Cards", ui.HTML(a.paymentGatewaysCard(nonce, ctx))),
		ui.Sheet("mon-paypal", "PayPal", ui.HTML(a.paypalConnectCard(nonce, ctx))),
		ui.Sheet("mon-btcpay", "Crypto, through your BTCPay Server", ui.HTML(a.btcpayConnectCard(nonce, ctx))),
		ui.Sheet("mon-webhook", "Another processor", ui.HTML(webhookSheet)),
		ui.Sheet("mon-paid-posts", "Paid posts", ui.HTML(paidPostsSheet)),
		ui.SaveBar(),
	)
	body := string(page) + `
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
    var go=function(){
      b.disabled=true;
      jpost('/os/api/orders/'+encodeURIComponent(id)+'/'+act).then(function(res){
        if(res.ok){show(act==='paid'?'Payment confirmed':'Order canceled',false);setTimeout(function(){location.reload();},600);}
        else{b.disabled=false;show(res.d.detail||res.d.title||'Error',true);}
      }).catch(function(e){b.disabled=false;show('Error: '+e,true);});
    };
    if(act==='paid'){vpConfirm({title:'Confirm payment received for this order?',message:'The member will be upgraded and emailed a receipt.',confirm:'Confirm payment'},go);}
    else if(act==='cancel'){vpConfirm({title:'Cancel this order?',confirm:'Cancel order'},go);}
    else{go();}
  });
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

	writeOSHTML(w, r, settingsLayout(nonce, "Monetization", "monetization", cfg, htmpl.HTML(body)))
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
		return `<strong class="tone-ok">A signing secret is configured.</strong>`
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
// osStatTile renders one house-style stat tile.
//
// It exists because three idioms for the same thing had grown up: the literal
// `stat-card` markup here, `vmStatTile` emitting `vm-stat`, and
// `osStatCardDelta` emitting a `card` with a `card-title` — so "the four numbers
// at the top of a page" looked different depending on which page you were on.
// §11 names stat-card as the house style; this is that markup, once.
//
// tone is "" or a modifier such as "warn".
func osStatTile(label, value, tone string) string {
	return string(ui.Figure{Label: label, Value: value, Tone: tone}.Cell())
}

// monAcc is the console's disclosure for the pages that build their markup as
// strings: icon, chip and body are markup; title and subtitle are text, which
// ui.Disclosure escapes once.
func monAcc(icon, title, subtitle, chip string, open bool, body string) string {
	return string(ui.Disclosure(ui.HTML(icon), title, subtitle, ui.HTML(chip), open, ui.HTML(body)))
}
