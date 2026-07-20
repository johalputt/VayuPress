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
	"strconv"
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

	stripeKey, stripeOn := a.stripeSecretKey(r.Context())
	ppID, ppSecret, ppSandbox, paypalOn := a.paypalCreds(r.Context())

	if r.Method == http.MethodPost {
		emailAddr := strings.TrimSpace(strings.ToLower(r.PostFormValue("email")))
		name := strings.TrimSpace(r.PostFormValue("name"))
		method := strings.ToLower(strings.TrimSpace(r.PostFormValue("method")))

		// Resolve the gateway: an explicit method wins when connected; otherwise
		// prefer Stripe, then PayPal, then the built-in direct/offline gateway.
		gateway := payments.GatewayDirect
		switch {
		case method == "paypal" && paypalOn:
			gateway = payments.GatewayPayPal
		case method == "stripe" && stripeOn:
			gateway = payments.GatewayStripe
		case method == "" && stripeOn:
			gateway = payments.GatewayStripe
		case method == "" && paypalOn:
			gateway = payments.GatewayPayPal
		}

		order, cerr := a.payments.Create(r.Context(), payments.OrderInput{
			Email: emailAddr, Name: name, TierSlug: tier.Slug, Cadence: cadence,
			AmountCents: amount, Currency: currency, Gateway: gateway,
		})
		if cerr != nil {
			_, _ = w.Write([]byte(checkoutFormPage(tier, cadence, amount, currency, stripeOn, paypalOn, cerr.Error())))
			return
		}
		logging.LogInfo("payments", "order opened: "+order.Reference+" tier="+tier.Slug+" gateway="+gateway)
		a.dispatchWebhook("payment.order_created.v1", map[string]interface{}{"reference": order.Reference, "tier": order.TierSlug, "amount_cents": order.AmountCents, "currency": order.Currency})

		origin := "https://" + config.Cfg.Domain
		switch gateway {
		case payments.GatewayStripe:
			// Stripe-hosted Checkout Session tagged with our order reference; redirect
			// (top-level navigation — no CSP relaxation, no Stripe.js). /checkout/success
			// confirms + fulfils server-side.
			sc := payments.NewStripeClient(a.outboundClient, stripeKey)
			checkoutURL, _, serr := sc.CreateCheckoutSession(r.Context(), payments.CheckoutParams{
				PriceID: stripePriceFor(tier, cadence), AmountCents: amount, Currency: currency,
				Interval: intervalFor(cadence), ProductName: tier.Name, CustomerEmail: emailAddr,
				ClientReferenceID: order.Reference,
				SuccessURL:        origin + "/checkout/success?session_id={CHECKOUT_SESSION_ID}",
				CancelURL:         origin + "/pricing",
				Metadata:          map[string]string{"reference": order.Reference, "tier": tier.Slug},
				TrialDays:         tier.TrialDays,
			})
			if serr != nil {
				logging.LogError("payments", "stripe checkout session failed", serr.Error())
				a.checkoutOfflineFallback(r.Context(), w, order, tier.Name)
				return
			}
			http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
			return
		case payments.GatewayPayPal:
			// Ensure a PayPal billing plan for this price, create an auto-renewing
			// subscription, and redirect to PayPal's approval page. /checkout/paypal/return
			// confirms + fulfils server-side.
			pp := payments.NewPayPalClient(a.outboundClient, ppID, ppSecret, ppSandbox)
			planID, perr := a.ensurePayPalPlan(r.Context(), pp, tier, cadence, amount, currency)
			if perr != nil {
				logging.LogError("payments", "paypal plan ensure failed", perr.Error())
				a.checkoutOfflineFallback(r.Context(), w, order, tier.Name)
				return
			}
			_, approveURL, serr := pp.CreateSubscription(r.Context(), planID, emailAddr, order.Reference, origin+"/checkout/paypal/return", origin+"/pricing", brandName())
			if serr != nil {
				logging.LogError("payments", "paypal subscription failed", serr.Error())
				a.checkoutOfflineFallback(r.Context(), w, order, tier.Name)
				return
			}
			http.Redirect(w, r, approveURL, http.StatusSeeOther)
			return
		default:
			a.checkoutOfflineFallback(r.Context(), w, order, tier.Name)
			return
		}
	}
	_, _ = w.Write([]byte(checkoutFormPage(tier, cadence, amount, currency, stripeOn, paypalOn, "")))
}

// checkoutOfflineFallback emails the payer offline instructions and renders the
// reference page — used when a connected gateway errs, so a reader is never left
// at a dead end.
func (a *App) checkoutOfflineFallback(ctx context.Context, w http.ResponseWriter, order *payments.Order, tierName string) {
	go a.sendPaymentPendingEmail(order, tierName)
	_, _ = w.Write([]byte(a.checkoutInstructionsPage(ctx, order, tierName)))
}

// ── PayPal (auto-renewing subscriptions) ──────────────────────────────────────

// paypalCreds returns the operator's PayPal REST credentials (client id in the
// credential endpoint, secret encrypted) plus the sandbox flag, and whether a
// live PayPal checkout is available.
func (a *App) paypalCreds(ctx context.Context) (clientID, secret string, sandbox, ok bool) {
	if a.secrets == nil {
		return "", "", false, false
	}
	sec, ep := a.secrets.ProviderSecret(ctx, secrets.ProviderPayPal)
	sec = strings.TrimSpace(sec)
	ep = strings.TrimSpace(ep)
	if sec == "" || ep == "" {
		return "", "", false, false
	}
	sb := a.siteSettings != nil && a.siteSettings.Get(ctx, settings.KeyPayPalSandbox) == "on"
	return ep, sec, sb, true
}

// paypalPlanFingerprint keys the plan cache by everything that affects price, so
// a price change transparently yields a new plan (stale prices never charge).
func paypalPlanFingerprint(tierSlug, cadence string, amountCents int, currency string) string {
	sum := sha256.Sum256([]byte(tierSlug + "|" + cadence + "|" + strconv.Itoa(amountCents) + "|" + strings.ToUpper(currency)))
	return hex.EncodeToString(sum[:12])
}

// ensurePayPalPlan returns a PayPal billing-plan id for the tier+cadence+price,
// creating and caching the shared product and the plan on first use.
func (a *App) ensurePayPalPlan(ctx context.Context, pp *payments.PayPalClient, tier *members.Tier, cadence string, amountCents int, currency string) (string, error) {
	fp := paypalPlanFingerprint(tier.Slug, cadence, amountCents, currency)
	if id, ok := a.payments.PayPalPlanID(ctx, fp); ok {
		return id, nil
	}
	productID, ok := a.payments.PayPalProductID(ctx)
	if !ok || productID == "" {
		pid, err := pp.EnsureProduct(ctx, brandName()+" membership")
		if err != nil {
			return "", err
		}
		_ = a.payments.SavePayPalProduct(ctx, pid)
		productID = pid
	}
	planID, err := pp.CreatePlan(ctx, productID, tier.Name+" · "+cadence, amountCents, currency, intervalFor(cadence))
	if err != nil {
		return "", err
	}
	_ = a.payments.SavePayPalPlan(ctx, fp, planID)
	return planID, nil
}

// brandName is the display name used on hosted checkout pages.
func brandName() string {
	if config.Cfg.Domain != "" {
		return config.Cfg.Domain
	}
	return "VayuPress"
}

// handleCheckoutPayPalReturn is PayPal's return_url. It confirms the subscription
// against PayPal server-side (never trusting the browser), marks the matching
// order paid and fulfils it (upgrade + receipt). Idempotent.
func (a *App) handleCheckoutPayPalReturn(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	subID := strings.TrimSpace(r.URL.Query().Get("subscription_id"))
	ppID, ppSecret, ppSandbox, on := a.paypalCreds(r.Context())
	if on && a.payments != nil && a.members != nil && payments.ValidPayPalSubscriptionID(subID) {
		pp := payments.NewPayPalClient(a.outboundClient, ppID, ppSecret, ppSandbox)
		if sub, err := pp.GetSubscription(r.Context(), subID); err == nil && sub.Active() && sub.CustomID != "" {
			order, perr := a.payments.MarkPaid(r.Context(), sub.CustomID, sub.ID)
			switch {
			case perr == nil:
				if ferr := a.fulfillOrder(r.Context(), order); ferr != nil {
					logging.LogError("payments", "paypal fulfilment failed: "+order.Reference, ferr.Error())
				} else {
					logging.LogInfo("payments", "order paid via paypal: "+order.Reference)
				}
			case errors.Is(perr, payments.ErrAlreadyPaid):
				// already fulfilled (return refresh or webhook)
			default:
				logging.LogError("payments", "paypal return mark-paid failed", perr.Error())
			}
		}
	}
	_, _ = w.Write([]byte(checkoutThanksPage()))
}

// handleCheckoutSuccess is Stripe's success_url. It CONFIRMS the session against
// Stripe server-side (the browser is never trusted to assert payment), marks the
// matching order paid, fulfils it (upgrade + receipt), and links the member to
// their Stripe customer for later lifecycle webhooks. Idempotent: a refresh or a
// racing webhook is a no-op. A thank-you renders regardless.
func (a *App) handleCheckoutSuccess(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	key, on := a.stripeSecretKey(r.Context())
	if on && a.payments != nil && a.members != nil && payments.ValidStripeSessionID(sessionID) {
		sc := payments.NewStripeClient(a.outboundClient, key)
		if sess, err := sc.GetCheckoutSession(r.Context(), sessionID); err == nil && sess.Paid() && sess.ClientReferenceID != "" {
			order, perr := a.payments.MarkPaid(r.Context(), sess.ClientReferenceID, sess.ID)
			switch {
			case perr == nil:
				if sess.CustomerID != "" {
					_ = a.members.SetStripeCustomer(r.Context(), order.Email, sess.CustomerID)
				}
				if ferr := a.fulfillOrder(r.Context(), order); ferr != nil {
					logging.LogError("payments", "stripe fulfilment failed: "+order.Reference, ferr.Error())
				} else {
					logging.LogInfo("payments", "order paid via stripe checkout: "+order.Reference)
				}
			case errors.Is(perr, payments.ErrAlreadyPaid):
				// already fulfilled by a racing webhook or a page refresh
			default:
				logging.LogError("payments", "stripe success mark-paid failed", perr.Error())
			}
		}
	}
	_, _ = w.Write([]byte(checkoutThanksPage()))
}

// stripeSecretKey returns the operator's connected Stripe secret key and whether
// a live Stripe checkout is available (an enabled key is present).
func (a *App) stripeSecretKey(ctx context.Context) (string, bool) {
	if a.secrets == nil {
		return "", false
	}
	key, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderStripe)
	key = strings.TrimSpace(key)
	return key, key != ""
}

// stripeWebhookSecret returns the Stripe endpoint signing secret, preferring the
// operator-editable encrypted store over the STRIPE_WEBHOOK_SECRET env var.
func (a *App) stripeWebhookSecret(ctx context.Context) string {
	if a.secrets != nil {
		if s, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderStripeWebhook); strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return strings.TrimSpace(config.Cfg.StripeWebhookSecret)
}

// intervalFor maps a checkout cadence to a Stripe recurring interval.
func intervalFor(cadence string) string {
	if cadence == payments.CadenceYearly {
		return "year"
	}
	return "month"
}

// stripePriceFor returns the tier's pre-created Stripe Price id for the cadence,
// or "" so the client builds an inline price (no Price setup required).
func stripePriceFor(t *members.Tier, cadence string) string {
	if t == nil {
		return ""
	}
	if cadence == payments.CadenceYearly {
		return t.StripeYearlyPrice
	}
	return t.StripeMonthlyPrice
}

func checkoutThanksPage() string {
	return checkoutShell("Thank you", `
<main class="pr-shell" id="main-content">
  <div class="pr-head">
    <h1>Thank you 🎉</h1>
    <p>Your payment was received and your membership is being activated.</p>
  </div>
  <div class="pr-card" style="max-width:34rem;margin:0 auto">
    <p>Access unlocks within a few seconds — a receipt is on its way to your inbox.</p>
    <p class="pr-foot"><a class="btn btn--primary pr-cta pr-cta--primary" href="/members/account">Go to your account</a></p>
  </div>
</main>`)
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
	// A premium mail-ID purchase is a one-time buy, not a membership: mark the
	// buyer's grant claimable rather than starting a subscription.
	if o.TierSlug == mailIDOrderTier {
		return a.fulfillMailIDOrder(ctx, o)
	}
	// A paid-post purchase is a one-time unlock of a single article.
	if o.TierSlug == postOrderTier {
		return a.fulfillPostOrder(ctx, o)
	}
	// A member ad purchase queues the ad for operator moderation.
	if o.TierSlug == adOrderTier {
		return a.fulfillAdOrder(ctx, o)
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

// fulfillMailIDOrder fulfils a paid premium mail-ID purchase: it flips the
// buyer's pending grant to claimable (they then set a password to provision the
// mailbox) and emails a receipt. No membership tier changes. Idempotent — a
// racing webhook or refresh re-marks an already-paid grant harmlessly.
func (a *App) fulfillMailIDOrder(ctx context.Context, o *payments.Order) error {
	if err := a.members.MarkPremiumGrantPaidByOrder(ctx, o.Reference); err != nil {
		return err
	}
	go a.sendPaymentConfirmedEmail(o)
	a.dispatchWebhook("payment.completed.v1", map[string]interface{}{
		"reference": o.Reference, "email": o.Email, "kind": "mailid",
		"amount_cents": o.AmountCents, "currency": o.Currency, "gateway": o.Gateway,
	})
	logging.LogInfo("payments", "premium mail-ID paid: "+o.Reference)
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

func checkoutFormPage(tier *members.Tier, cadence string, amountCents int, currency string, stripeOn, paypalOn bool, errMsg string) string {
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
	// One button per connected gateway (each submits its own method); the built-in
	// direct/offline gateway is the fallback when none is connected.
	buttons := `<button type="submit" class="btn btn--primary pr-cta pr-cta--primary" style="width:100%">Continue to payment</button>`
	if stripeOn || paypalOn {
		buttons = ""
		if stripeOn {
			buttons += `<button type="submit" name="method" value="stripe" class="btn btn--primary pr-cta pr-cta--primary" style="width:100%;margin-bottom:.5rem">Pay by card</button>`
		}
		if paypalOn {
			buttons += `<button type="submit" name="method" value="paypal" class="btn btn--ghost pr-cta" style="width:100%">Pay with PayPal</button>`
		}
	}
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
    `+buttons+`
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
