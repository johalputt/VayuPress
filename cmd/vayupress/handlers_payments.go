package main

// handlers_payments.go — sovereign monetization: checkout, the generic payment
// webhook, order fulfilment, and the admin order actions.
//
// Money never flows through an embedded SDK. A reader checks out, which opens a
// pending order with a quotable reference; the order is fulfilled either by the
// operator confirming an offline/direct payment in the Monetization console, or
// by a connected third-party processor posting a signature-verified webhook.
// Fulfilment upgrades the member, records the subscription at the order's true
// cadence/amount, and emails a receipt — all idempotently.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/emailtmpl"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
)

// ── Monetization gating + config helpers ──────────────────────────────────────

// paymentsEnabled reports whether the operator has switched the Payments module
// on. The store is always wired; the public surface is dark until enabled.
func (a *App) paymentsEnabled(ctx context.Context) bool {
	return a.siteSettings != nil && a.payments != nil && a.members != nil &&
		a.siteSettings.FeatureEnabled(ctx, settings.KeyFeaturePayments)
}

// adsEnabled reports whether the Advertising module is on.
func (a *App) adsEnabled(ctx context.Context) bool {
	return a.siteSettings != nil && a.ads != nil && a.siteSettings.FeatureEnabled(ctx, settings.KeyFeatureAds)
}

// googleAdsEnabled reports whether the Google AdSense module is on AND a
// publisher id is configured (both are required to emit any AdSense markup).
func (a *App) googleAdsEnabled(ctx context.Context) bool {
	return a.siteSettings != nil && a.siteSettings.FeatureEnabled(ctx, settings.KeyFeatureGoogleAds) && a.adsenseClient(ctx) != ""
}

// affiliateEnabled reports whether the affiliate-disclosure banner is on.
func (a *App) affiliateEnabled(ctx context.Context) bool {
	return a.siteSettings != nil && a.siteSettings.FeatureEnabled(ctx, settings.KeyFeatureAffiliate)
}

// adsenseClient returns the configured AdSense publisher id (may be "").
func (a *App) adsenseClient(ctx context.Context) string {
	if a.siteSettings == nil {
		return ""
	}
	return strings.TrimSpace(a.siteSettings.Get(ctx, settings.KeyAdsenseClient))
}

// adsenseConfigured is the no-context helper used by the Tools registry.
func (a *App) adsenseConfigured() bool {
	return a.adsenseClient(context.Background()) != ""
}

// payCurrency returns the configured checkout currency (defaults to USD).
func (a *App) payCurrency(ctx context.Context) string {
	if a.siteSettings == nil {
		return "USD"
	}
	c := strings.ToUpper(strings.TrimSpace(a.siteSettings.Get(ctx, settings.KeyPayCurrency)))
	if c == "" {
		return "USD"
	}
	return c
}

// directInstructions returns the operator's offline payment instructions.
func (a *App) directInstructions(ctx context.Context) string {
	if a.siteSettings == nil {
		return ""
	}
	return strings.TrimSpace(a.siteSettings.Get(ctx, settings.KeyPayDirectInstructions))
}

// ── Public checkout (built-in direct gateway) ─────────────────────────────────

// handleCheckoutPage renders the checkout page for a tier+cadence. GET shows the
// form; POST opens the order and shows payment instructions + the reference. It
// is a plain HTML form flow (no JS) so it satisfies the strict CSP and works for
// signed-out readers.
func (a *App) handleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	if !a.paymentsEnabled(r.Context()) {
		http.Redirect(w, r, "/pricing", http.StatusSeeOther)
		return
	}
	tierSlug := strings.TrimSpace(r.URL.Query().Get("tier"))
	cadence := normalizeCadence(r.URL.Query().Get("cadence"))
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		tierSlug = strings.TrimSpace(r.PostFormValue("tier"))
		cadence = normalizeCadence(r.PostFormValue("cadence"))
	}

	tier, err := a.members.GetTier(r.Context(), tierSlug)
	if err != nil || tier == nil || tier.IsFree() {
		http.Redirect(w, r, "/pricing", http.StatusSeeOther)
		return
	}
	amount := tier.MonthlyCents
	if cadence == payments.CadenceYearly && tier.YearlyCents > 0 {
		amount = tier.YearlyCents
	}
	currency := tier.Currency
	if currency == "" {
		currency = a.payCurrency(r.Context())
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodPost {
		emailAddr := strings.TrimSpace(strings.ToLower(r.PostFormValue("email")))
		name := strings.TrimSpace(r.PostFormValue("name"))
		order, cerr := a.payments.Create(r.Context(), payments.OrderInput{
			Email: emailAddr, Name: name, TierSlug: tier.Slug, Cadence: cadence,
			AmountCents: amount, Currency: currency, Gateway: payments.GatewayDirect,
		})
		if cerr != nil {
			_, _ = w.Write([]byte(checkoutFormPage(tier, cadence, amount, currency, cerr.Error())))
			return
		}
		// Email the payer their instructions + reference (best-effort).
		go a.sendPaymentPendingEmail(order, tier.Name)
		logging.LogInfo("payments", "order opened: "+order.Reference+" tier="+tier.Slug)
		a.dispatchWebhook("payment.order_created.v1", map[string]interface{}{"reference": order.Reference, "tier": order.TierSlug, "amount_cents": order.AmountCents, "currency": order.Currency})
		_, _ = w.Write([]byte(a.checkoutInstructionsPage(r.Context(), order, tier.Name)))
		return
	}
	_, _ = w.Write([]byte(checkoutFormPage(tier, cadence, amount, currency, "")))
}

// ── Generic payment webhook (connected third-party gateways) ──────────────────

// handlePaymentWebhook fulfils an order when a connected processor posts a
// signature-verified event. The shared secret is stored encrypted under the
// payment_gateway provider. The body must be JSON containing at least a
// "reference" (the VayuPress order reference) and may carry a gateway "id".
//
//	POST /api/v1/payments/webhook/{gateway}
//	X-VayuPress-Signature: <hex hmac-sha256 of the raw body>
func (a *App) handlePaymentWebhook(w http.ResponseWriter, r *http.Request) {
	if a.payments == nil || a.secrets == nil {
		http.Error(w, "payments not configured", http.StatusServiceUnavailable)
		return
	}
	gateway := chi.URLParam(r, "gateway")
	secret, _ := a.secrets.ProviderSecret(r.Context(), secrets.ProviderPaymentGateway)
	if strings.TrimSpace(secret) == "" {
		http.Error(w, "gateway not configured", http.StatusServiceUnavailable)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	sig := r.Header.Get("X-VayuPress-Signature")
	if sig == "" {
		sig = r.Header.Get("X-Signature")
	}
	if !verifyHMACHex(sig, payload, secret) {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	var evt struct {
		Reference string `json:"reference"`
		ID        string `json:"id"`
		Status    string `json:"status"`
	}
	if jerr := json.Unmarshal(payload, &evt); jerr != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	ref := strings.TrimSpace(evt.Reference)
	if ref == "" {
		http.Error(w, "missing reference", http.StatusBadRequest)
		return
	}
	if evt.Status != "" && evt.Status != "paid" && evt.Status != "succeeded" && evt.Status != "completed" {
		// Acknowledge non-payment events without acting.
		w.WriteHeader(http.StatusOK)
		return
	}
	gwRef := evt.ID
	if gwRef == "" {
		gwRef = gateway
	}
	order, perr := a.payments.MarkPaid(r.Context(), ref, gwRef)
	if errors.Is(perr, payments.ErrAlreadyPaid) {
		w.WriteHeader(http.StatusOK) // idempotent: already fulfilled
		return
	}
	if perr != nil {
		http.Error(w, "unknown order", http.StatusNotFound)
		return
	}
	if ferr := a.fulfillOrder(r.Context(), order); ferr != nil {
		logging.LogError("payments", "fulfilment failed: "+order.Reference, ferr.Error())
		http.Error(w, "fulfilment error", http.StatusInternalServerError)
		return
	}
	logging.LogInfo("payments", "order paid via webhook("+gateway+"): "+order.Reference)
	w.WriteHeader(http.StatusOK)
}

// ── Order fulfilment ──────────────────────────────────────────────────────────

// fulfillOrder upgrades the member to the order's tier, records a subscription
// at the order's true cadence/amount, emails a receipt, and fires the
// payment.completed event. Callers guarantee idempotency by acting only on the
// transition into paid (MarkPaid returns ErrAlreadyPaid otherwise).
func (a *App) fulfillOrder(ctx context.Context, o *payments.Order) error {
	if a.members == nil {
		return errors.New("members not initialised")
	}
	m, err := a.members.Upsert(ctx, o.Email)
	if err != nil {
		return err
	}
	cadence := members.CadenceMonthly
	if o.Cadence == payments.CadenceYearly {
		cadence = members.CadenceYearly
	}
	if err := a.members.StartSubscription(ctx, members.SubscriptionInput{
		MemberID: m.ID, TierSlug: o.TierSlug, Cadence: cadence,
		AmountCents: o.AmountCents, Currency: o.Currency,
	}); err != nil {
		return err
	}
	if o.Name != "" && m.Name == "" {
		_ = a.members.UpdateProfile(ctx, o.Email, o.Name, m.Note)
	}
	go a.sendPaymentConfirmedEmail(o)
	a.dispatchWebhook("payment.completed.v1", map[string]interface{}{
		"reference": o.Reference, "email": o.Email, "tier": o.TierSlug,
		"amount_cents": o.AmountCents, "currency": o.Currency, "gateway": o.Gateway,
	})
	logging.LogInfo("payments", "member fulfilled to "+o.TierSlug+": "+o.Email)
	return nil
}

// ── Confirmation emails ───────────────────────────────────────────────────────

func (a *App) sendPaymentPendingEmail(o *payments.Order, tierName string) {
	if a.mailer == nil || o == nil {
		return
	}
	ctx := context.Background()
	msg := a.renderEmail(emailtmpl.PaymentPending, map[string]interface{}{
		"Domain":       config.Cfg.Domain,
		"Name":         orderDisplayName(o),
		"TierName":     tierName,
		"Amount":       o.AmountMajor(),
		"Currency":     o.Currency,
		"Cadence":      o.Cadence,
		"Reference":    o.Reference,
		"Instructions": a.directInstructions(ctx),
	})
	if err := a.mailer.Send(email.Message{To: o.Email, Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML}); err != nil {
		logging.LogError("payments", "pending email failed", err.Error())
	}
}

func (a *App) sendPaymentConfirmedEmail(o *payments.Order) {
	if a.mailer == nil || o == nil {
		return
	}
	tierName := o.TierSlug
	if a.members != nil {
		if t, err := a.members.GetTier(context.Background(), o.TierSlug); err == nil && t != nil {
			tierName = t.Name
		}
	}
	msg := a.renderEmail(emailtmpl.PaymentConfirmed, map[string]interface{}{
		"Domain":    config.Cfg.Domain,
		"Name":      orderDisplayName(o),
		"TierName":  tierName,
		"Amount":    o.AmountMajor(),
		"Currency":  o.Currency,
		"Reference": o.Reference,
		"Link":      "https://" + config.Cfg.Domain + "/members/account",
	})
	if err := a.mailer.Send(email.Message{To: o.Email, Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML}); err != nil {
		logging.LogError("payments", "confirmation email failed", err.Error())
	}
}

func orderDisplayName(o *payments.Order) string {
	if o.Name != "" {
		return o.Name
	}
	if i := strings.IndexByte(o.Email, '@'); i > 0 {
		return o.Email[:i]
	}
	return "there"
}

// ── Admin order actions (session + CSRF, mounted under /os) ───────────────────

// handleOSOrdersList returns the order ledger as JSON.
func (a *App) handleOSOrdersList(w http.ResponseWriter, r *http.Request) {
	if a.payments == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "payments-error", "payments not initialised", "")
		return
	}
	status := r.URL.Query().Get("status")
	list, err := a.payments.List(r.Context(), status, 500)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	stats, _ := a.payments.Stats(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"orders": list, "stats": stats})
}

// handleOSOrderMarkPaid confirms an offline payment: it flips the order to paid
// and fulfils it (upgrade + receipt). Idempotent — a second confirm is a no-op.
func (a *App) handleOSOrderMarkPaid(w http.ResponseWriter, r *http.Request) {
	if a.payments == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "payments-error", "payments not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	order, err := a.payments.MarkPaid(r.Context(), id, "")
	if errors.Is(err, payments.ErrAlreadyPaid) {
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "already-paid"})
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "order-error", err.Error(), "")
		return
	}
	if ferr := a.fulfillOrder(r.Context(), order); ferr != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "fulfilment-error", ferr.Error(), "")
		return
	}
	logging.LogInfo("payments", "order confirmed by operator: "+order.Reference)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "paid", "reference": order.Reference})
}

// handleOSOrderCancel marks an order canceled (no fulfilment).
func (a *App) handleOSOrderCancel(w http.ResponseWriter, r *http.Request) {
	if a.payments == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "payments-error", "payments not initialised", "")
		return
	}
	if err := a.payments.SetStatus(r.Context(), chi.URLParam(r, "id"), payments.StatusCanceled); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "order-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "canceled"})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func normalizeCadence(c string) string {
	if strings.TrimSpace(strings.ToLower(c)) == payments.CadenceYearly {
		return payments.CadenceYearly
	}
	return payments.CadenceMonthly
}

// verifyHMACHex constant-time compares a hex HMAC-SHA256 signature of payload.
func verifyHMACHex(sigHex string, payload []byte, secret string) bool {
	sigHex = strings.TrimSpace(strings.TrimPrefix(sigHex, "sha256="))
	if sigHex == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(sigHex)) == 1
}

// ── Public checkout page markup (CSP-safe, no inline JS) ───────────────────────

func checkoutFormPage(tier *members.Tier, cadence string, amountCents int, currency, errMsg string) string {
	esc := html.EscapeString
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div class="su-error" role="alert">` + esc(errMsg) + `</div>`
	}
	per := "month"
	if cadence == payments.CadenceYearly {
		per = "year"
	}
	price := priceLabel(currency, amountCents)
	return checkoutShell("Checkout · "+esc(tier.Name), `
<main class="pr-shell" id="main-content">
  <div class="pr-head">
    <h1>Subscribe to `+esc(tier.Name)+`</h1>
    <p>`+esc(price)+` per `+esc(per)+` · secure, sovereign checkout</p>
  </div>
  `+errHTML+`
  <form class="login-form" method="POST" action="/checkout" novalidate style="max-width:28rem;margin:0 auto">
    <input type="hidden" name="tier" value="`+esc(tier.Slug)+`">
    <input type="hidden" name="cadence" value="`+esc(cadence)+`">
    <div class="field">
      <label class="field-label" for="co-name">Your name</label>
      <input id="co-name" class="input" type="text" name="name" placeholder="Jane Doe" autocomplete="name">
    </div>
    <div class="field">
      <label class="field-label" for="co-email">Email</label>
      <input id="co-email" class="input" type="email" name="email" placeholder="you@example.com" autocomplete="email" required autofocus>
    </div>
    <button type="submit" class="btn btn--primary pr-cta pr-cta--primary">Continue to payment</button>
  </form>
  <p class="pr-foot">Already a member? <a href="/members" class="su-link">Sign in</a></p>
</main>`)
}

func (a *App) checkoutInstructionsPage(ctx context.Context, o *payments.Order, tierName string) string {
	esc := html.EscapeString
	instructions := a.directInstructions(ctx)
	instrBlock := `<p class="su-muted">Payment instructions have not been configured yet. Please contact us to complete your subscription.</p>`
	if instructions != "" {
		instrBlock = `<pre class="co-instructions">` + esc(instructions) + `</pre>`
	}
	return checkoutShell("Order "+esc(o.Reference), `
<main class="pr-shell" id="main-content">
  <div class="pr-head">
    <h1>Almost there</h1>
    <p>Your order for <strong>`+esc(tierName)+`</strong> is reserved.</p>
  </div>
  <div class="pr-card" style="max-width:34rem;margin:0 auto">
    <p>Please send <strong>`+esc(priceLabel(o.Currency, o.AmountCents))+`</strong> and quote this reference so we can match your payment:</p>
    <p class="co-reference"><code>`+esc(o.Reference)+`</code></p>
    `+instrBlock+`
    <p class="su-muted">A copy of these instructions has been emailed to <strong>`+esc(o.Email)+`</strong>. Your access unlocks as soon as we confirm receipt.</p>
  </div>
  <p class="pr-foot"><a href="/" class="su-link">Return to the site</a></p>
</main>`)
}

// checkoutShell wraps body in a minimal public HTML document that reuses the
// public theme + signup stylesheet (no inline styles beyond the existing
// utility attributes used elsewhere on these pages).
func checkoutShell(title, body string) string {
	brand := html.EscapeString(config.Cfg.Domain)
	return `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + title + ` · ` + brand + `</title>
<meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">` + body + `
</body></html>`
}
