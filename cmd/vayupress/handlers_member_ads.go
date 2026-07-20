package main

// handlers_member_ads.go — member self-serve advertising ("advertise here",
// Phase 5b). Any signed-in member (paid or free) submits an image ad for a
// placement and pays a flat fee; the ad enters the operator's moderation queue
// (pending_review) and only goes live once the operator approves it. Payment uses
// the same one-time substrate as premium mail-IDs / paid posts (Stripe one-time
// when connected, else the sovereign direct/offline gateway). Only image ads are
// accepted from members — no member-authored HTML ever reaches the page.

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/ads"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/settings"
)

// adSlotPriceDefaultCents is the fallback member-ad price when unset.
const adSlotPriceDefaultCents = 1000

// adOrderTier is the sentinel TierSlug carried by a member-ad purchase order so
// the shared fulfilment routes it to the ad moderation queue.
const adOrderTier = "__ad__"

// adSlotPriceCents returns the operator-set price a member pays to submit an ad.
func (a *App) adSlotPriceCents(ctx context.Context) int {
	if a.siteSettings == nil {
		return adSlotPriceDefaultCents
	}
	n, err := strconv.Atoi(strings.TrimSpace(a.siteSettings.Get(ctx, settings.KeyAdSlotPriceCents)))
	if err != nil || n < 0 {
		return adSlotPriceDefaultCents
	}
	return n
}

// fulfillAdOrder advances a member ad from pending_payment to the moderation
// queue on payment confirmation, and emails a receipt.
func (a *App) fulfillAdOrder(ctx context.Context, o *payments.Order) error {
	if a.ads == nil {
		return nil
	}
	if err := a.ads.MarkAdPaidByOrder(ctx, o.Reference); err != nil {
		return err
	}
	go a.sendPaymentConfirmedEmail(o)
	a.dispatchWebhook("payment.completed.v1", map[string]interface{}{
		"reference": o.Reference, "email": o.Email, "kind": "ad",
		"amount_cents": o.AmountCents, "currency": o.Currency, "gateway": o.Gateway,
	})
	logging.LogInfo("payments", "member ad paid, queued for review: "+o.Reference)
	return nil
}

// GET /api/v1/members/ads — the member's submitted ads + the advertise price.
func (a *App) handleMemberAdsStatus(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	available := a.ads != nil && a.payments != nil && a.adsEnabled(r.Context()) && a.paymentsEnabled(r.Context())
	out := map[string]interface{}{
		"available": available,
		"price":     priceLabel(a.payCurrency(r.Context()), a.adSlotPriceCents(r.Context())),
		"ads":       []interface{}{},
	}
	if a.ads != nil {
		if list, _ := a.ads.MemberAds(r.Context(), m.Email); len(list) > 0 {
			rows := make([]map[string]string, 0, len(list))
			for i := range list {
				rows = append(rows, map[string]string{"placement": list[i].Placement, "status": list[i].Status, "image_url": list[i].ImageURL})
			}
			out["ads"] = rows
		}
	}
	writeJSON(w, r, http.StatusOK, out)
}

// POST /api/v1/members/ads {placement,image_url,link_url,alt} — submit an ad and
// start checkout for its flat fee.
func (a *App) handleMemberAdSubmit(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	ctx := r.Context()
	if a.ads == nil || a.payments == nil || !a.adsEnabled(ctx) || !a.paymentsEnabled(ctx) {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ads-unavailable", "Advertising is not available right now.", "")
		return
	}
	var in struct {
		Placement string `json:"placement"`
		ImageURL  string `json:"image_url"`
		LinkURL   string `json:"link_url"`
		Alt       string `json:"alt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	in.Placement = strings.TrimSpace(in.Placement)
	if !ads.ValidPlacement(in.Placement) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-placement", "Choose where your ad should appear.", "")
		return
	}
	if strings.TrimSpace(in.ImageURL) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "image-required", "An image URL is required.", "")
		return
	}
	price := a.adSlotPriceCents(ctx)
	currency := a.payCurrency(ctx)
	stripeKey, stripeOn := a.stripeSecretKey(ctx)
	gateway := payments.GatewayDirect
	if stripeOn {
		gateway = payments.GatewayStripe
	}
	order, cerr := a.payments.Create(ctx, payments.OrderInput{
		Email: m.Email, Name: m.DisplayName(), TierSlug: adOrderTier,
		AmountCents: price, Currency: currency, Gateway: gateway,
	})
	if cerr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "order-failed", cerr.Error(), "")
		return
	}
	if _, aerr := a.ads.CreateMemberAd(ctx, ads.MemberAdInput{
		OwnerEmail: m.Email, Placement: in.Placement, ImageURL: in.ImageURL,
		LinkURL: in.LinkURL, AltText: in.Alt, OrderRef: order.Reference,
	}); aerr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "ad-failed", aerr.Error(), "")
		return
	}
	logging.LogInfo("payments", "member ad order "+order.Reference+" opened for "+m.Email)

	if gateway == payments.GatewayStripe {
		origin := "https://" + config.Cfg.Domain
		sc := payments.NewStripeClient(a.outboundClient, stripeKey)
		checkoutURL, _, serr := sc.CreateCheckoutSession(ctx, payments.CheckoutParams{
			Mode: "payment", AmountCents: price, Currency: currency,
			ProductName: "Ad placement — " + in.Placement, CustomerEmail: m.Email,
			ClientReferenceID: order.Reference,
			SuccessURL:        origin + "/checkout/success?session_id={CHECKOUT_SESSION_ID}",
			CancelURL:         origin + "/members/account",
			Metadata:          map[string]string{"reference": order.Reference, "kind": "ad"},
		})
		if serr == nil {
			writeJSON(w, r, http.StatusOK, map[string]interface{}{"mode": "stripe", "checkout_url": checkoutURL, "reference": order.Reference})
			return
		}
		logging.LogError("payments", "member ad stripe checkout failed", serr.Error())
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"mode": "direct", "reference": order.Reference,
		"amount": priceLabel(currency, price), "instructions": a.directInstructions(ctx),
	})
}
