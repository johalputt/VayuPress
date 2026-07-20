package main

// handlers_paidposts.go — one-time paid-post unlock (Phase 6). A reader who is a
// signed-in member can buy permanent access to a single gated post without a
// subscription, via the same one-time payment substrate as premium mail-IDs:
// Stripe one-time Checkout when connected, else the sovereign direct/offline
// gateway. The purchase settles through the shared fulfilment path (which flips
// the pending article_purchase to paid); the post then unlocks for that member.

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
)

// postOrderTier is the sentinel TierSlug carried by a paid-post purchase order so
// the shared fulfilment routes it to an article unlock, not a subscription.
const postOrderTier = "__post__"

// fulfillPostOrder fulfils a paid-post purchase: it flips the buyer's pending
// article purchase to paid (unlocking the post for them) and emails a receipt.
// No membership tier changes. Idempotent.
func (a *App) fulfillPostOrder(ctx context.Context, o *payments.Order) error {
	if err := a.members.MarkArticlePurchasePaidByOrder(ctx, o.Reference); err != nil {
		return err
	}
	go a.sendPaymentConfirmedEmail(o)
	a.dispatchWebhook("payment.completed.v1", map[string]interface{}{
		"reference": o.Reference, "email": o.Email, "kind": "post",
		"amount_cents": o.AmountCents, "currency": o.Currency, "gateway": o.Gateway,
	})
	logging.LogInfo("payments", "paid-post purchase settled: "+o.Reference)
	return nil
}

// handlePostCheckout opens a one-time order for buying access to a single gated,
// priced post and redirects to Stripe (or the offline instructions). It requires
// a signed-in member so the purchase — and the resulting access — is bound to
// their account.
//
//	POST /checkout/post/{slug}
func (a *App) handlePostCheckout(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if !api.IsValidSlug(slug) {
		a.handleNotFound(w, r)
		return
	}
	m := a.resolveMember(r)
	if m == nil {
		// Sign in first so the purchase is linked to the member and access is
		// granted immediately on return.
		http.Redirect(w, r, "/members", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	if a.payments == nil || a.members == nil || !a.paymentsEnabled(ctx) {
		http.Redirect(w, r, "/"+slug, http.StatusSeeOther)
		return
	}
	price := a.members.GetPostPriceCents(ctx, slug)
	level := a.members.GetAccess(ctx, slug)
	if price <= 0 || level == members.AccessPublic {
		// Not individually purchasable — nothing to buy.
		http.Redirect(w, r, "/"+slug, http.StatusSeeOther)
		return
	}
	if a.members.HasPurchasedArticle(ctx, m.Email, slug) {
		http.Redirect(w, r, "/"+slug, http.StatusSeeOther)
		return
	}
	title := slug
	var t string
	if dbpkg.Reader().QueryRow(`SELECT title FROM articles WHERE slug=?`, slug).Scan(&t) == nil && strings.TrimSpace(t) != "" {
		title = t
	}
	currency := a.payCurrency(ctx)
	stripeKey, stripeOn := a.stripeSecretKey(ctx)
	gateway := payments.GatewayDirect
	if stripeOn {
		gateway = payments.GatewayStripe
	}
	order, cerr := a.payments.Create(ctx, payments.OrderInput{
		Email: m.Email, Name: m.DisplayName(), TierSlug: postOrderTier,
		AmountCents: price, Currency: currency, Gateway: gateway,
	})
	if cerr != nil {
		http.Redirect(w, r, "/"+slug, http.StatusSeeOther)
		return
	}
	_ = a.members.CreateArticlePurchase(ctx, m.Email, slug, order.Reference)
	logging.LogInfo("payments", "paid-post order "+order.Reference+" opened for "+slug)

	if gateway == payments.GatewayStripe {
		origin := "https://" + config.Cfg.Domain
		sc := payments.NewStripeClient(a.outboundClient, stripeKey)
		checkoutURL, _, serr := sc.CreateCheckoutSession(ctx, payments.CheckoutParams{
			Mode: "payment", AmountCents: price, Currency: currency,
			ProductName: "Post: " + title, CustomerEmail: m.Email,
			ClientReferenceID: order.Reference,
			SuccessURL:        origin + "/checkout/success?session_id={CHECKOUT_SESSION_ID}",
			CancelURL:         origin + "/" + slug,
			Metadata:          map[string]string{"reference": order.Reference, "kind": "post"},
		})
		if serr == nil {
			http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
			return
		}
		logging.LogError("payments", "paid-post stripe checkout failed", serr.Error())
	}
	// Offline/direct: email the instructions + render the reference page.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	a.checkoutOfflineFallback(ctx, w, order, "post: "+title)
}
