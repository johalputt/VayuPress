// SPDX-License-Identifier: Apache-2.0

package main

// handlers_member_mailbox.go — a paid member self-provisions the VayuMail mailbox
// their tier includes (Phase 2b). The member picks a GENERIC address (reserved
// admin/role names are refused — see internal/vayuos/mail/reserved.go), sets a
// password, and gets a real mailbox with an auto PGP keypair + WKD and their
// tier's storage quota. The member cookie is SameSite=Lax, so a cross-site POST
// never carries it (CSRF-safe), matching the other member endpoints.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// premiumMailIDDefaultCents is the fallback price (minor units of the checkout
// currency) for a premium (vanity) mail address when the operator hasn't set one.
const premiumMailIDDefaultCents = 500

// premiumMailIDDefaultQuotaMB is the mailbox size given to a purchased premium
// address when the buyer's tier quota can't be resolved (e.g. a later downgrade).
const premiumMailIDDefaultQuotaMB = 1024

// mailIDOrderTier is the sentinel TierSlug carried by a premium mail-ID purchase
// order, so the shared fulfilment path routes it to a claimable grant instead of
// a membership subscription. Double-underscored so it can never collide with an
// operator-defined tier slug.
const mailIDOrderTier = "__mailid__"

// isPremiumLocalpart treats a name as premium (sellable, held back from the free
// claim) when EITHER the static classifier OR the operator's custom premium list
// says so.
func (a *App) isPremiumLocalpart(ctx context.Context, local string) bool {
	if mail.IsPremiumLocalpart(local) {
		return true
	}
	return a.members != nil && a.members.IsCustomPremiumLocalpart(ctx, local)
}

// premiumMailIDPriceCents returns the operator-configured price, in minor units
// of the checkout currency, for a premium (vanity) mail address.
func (a *App) premiumMailIDPriceCents(ctx context.Context) int {
	if a.siteSettings == nil {
		return premiumMailIDDefaultCents
	}
	n, err := strconv.Atoi(strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyPremiumMailIDPriceCents)))
	if err != nil || n < 0 {
		return premiumMailIDDefaultCents
	}
	return n
}

// mailIDTerms returns the acceptable-use / terms text a member must accept before
// a mailbox is provisioned to them.
func (a *App) mailIDTerms(ctx context.Context) string {
	if a.siteSettings == nil {
		return ""
	}
	return strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyMailIDTerms))
}

// mailboxEntitlement returns the member's tier and quota (bytes) when it includes
// a mailbox; ok=false for a free tier or a paid tier without a mailbox.
func (a *App) mailboxEntitlement(ctx context.Context, m *members.Member) (tier *members.Tier, quotaBytes int64, ok bool) {
	if m == nil || a.members == nil || !m.IsPaid() {
		return nil, 0, false
	}
	t, err := a.members.GetTier(ctx, m.Tier)
	if err != nil || t == nil || !t.MailEnabled {
		return nil, 0, false
	}
	return t, t.MailQuotaBytes(), true
}

// mailHost returns the primary VayuMail domain member mailboxes are created on,
// or "" when mail is unavailable.
func (a *App) mailHost() string {
	if a.vayuMail == nil {
		return ""
	}
	return a.vayuMail.Config().Domain
}

// mailboxLocalpartCheck validates a desired localpart for a member self-claim,
// returning ok plus a human reason when not.
func (a *App) mailboxLocalpartCheck(ctx context.Context, local string) (bool, string) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return false, "Mail is not available."
	}
	if !mail.ValidLocalpart(local) {
		return false, "Use letters, numbers and . _ - only (no spaces)."
	}
	if mail.IsReservedLocalpart(local) {
		return false, "That address is reserved."
	}
	// Premium (vanity) names are the operator's sellable inventory and are not
	// handed out on the free claim path — they are purchased (Phase 3 marketplace).
	if a.isPremiumLocalpart(ctx, local) {
		return false, "That's a premium address — it isn't included on the free claim."
	}
	host := a.mailHost()
	if host == "" {
		return false, "Mail is not available."
	}
	if a.vayuMail.Accounts().Exists(ctx, local+"@"+host) {
		return false, "That address is already taken."
	}
	return true, ""
}

// GET /api/v1/members/mailbox — the member's mailbox entitlement + current state.
func (a *App) handleMemberMailboxStatus(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	host := a.mailHost()
	tier, _, ok := a.mailboxEntitlement(r.Context(), m)
	resp := map[string]interface{}{
		"entitled":    ok && host != "",
		"domain":      host,
		"has_mailbox": false,
		"address":     "",
		"quota_mb":    0,
		"terms":       a.mailIDTerms(r.Context()),
	}
	if ok {
		resp["quota_mb"] = tier.MailQuotaMB
	}
	if a.members != nil {
		if addr := a.members.MailAddressFor(r.Context(), m.Email); addr != "" {
			resp["has_mailbox"] = true
			resp["address"] = addr
		}
		// Premium addresses the member has bought but not yet activated.
		if grants, _ := a.members.ClaimablePremiumGrants(r.Context(), m.Email); len(grants) > 0 {
			purchased := make([]map[string]string, 0, len(grants))
			for i := range grants {
				purchased = append(purchased, map[string]string{"localpart": grants[i].Localpart, "address": grants[i].Address()})
			}
			resp["purchased"] = purchased
		}
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// premiumMailboxCheck validates a desired PREMIUM localpart for purchase: a
// well-formed, non-reserved, premium (sellable) name that is not already taken.
func (a *App) premiumMailboxCheck(ctx context.Context, local string) (bool, string) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return false, "Mail is not available."
	}
	if !mail.ValidLocalpart(local) {
		return false, "Use letters, numbers and . _ - only (no spaces)."
	}
	if mail.IsReservedLocalpart(local) {
		return false, "That address is reserved."
	}
	if !a.isPremiumLocalpart(ctx, local) {
		return false, "That isn't a premium address — claim it free from your plan instead."
	}
	host := a.mailHost()
	if host == "" {
		return false, "Mail is not available."
	}
	if a.vayuMail.Accounts().Exists(ctx, local+"@"+host) {
		return false, "That address is already taken."
	}
	return true, ""
}

// POST /api/v1/members/mailbox/premium/checkout {localpart} — buy a premium
// (vanity) address. Opens a one-time order (Stripe one-time when connected, else
// the sovereign direct/offline gateway) plus a pending grant; on payment the
// grant becomes claimable via .../premium/activate.
func (a *App) handleMemberMailIDCheckout(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	if a.payments == nil || !a.paymentsEnabled(r.Context()) {
		writeAPIError(w, r, http.StatusServiceUnavailable, "payments-off", "Payments are not enabled.", "")
		return
	}
	if _, _, ok := a.mailboxEntitlement(r.Context(), m); !ok {
		writeAPIError(w, r, http.StatusForbidden, "not-entitled", "Your plan does not include VayuMail.", "")
		return
	}
	var in struct {
		Localpart string `json:"localpart"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(in.Localpart))
	if ok, reason := a.premiumMailboxCheck(r.Context(), local); !ok {
		writeAPIError(w, r, http.StatusBadRequest, "bad-localpart", reason, "")
		return
	}
	host := a.mailHost()
	price := a.premiumMailIDPriceCents(r.Context())
	currency := a.payCurrency(r.Context())
	stripeKey, stripeOn := a.stripeSecretKey(r.Context())
	gateway := payments.GatewayDirect
	if stripeOn {
		gateway = payments.GatewayStripe
	}
	order, cerr := a.payments.Create(r.Context(), payments.OrderInput{
		Email: m.Email, Name: m.DisplayName(), TierSlug: mailIDOrderTier,
		AmountCents: price, Currency: currency, Gateway: gateway,
	})
	if cerr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "order-failed", cerr.Error(), "")
		return
	}
	if _, gerr := a.members.CreatePremiumGrant(r.Context(), m.Email, local, host, order.Reference); gerr != nil {
		writeAPIError(w, r, http.StatusConflict, "grant-failed", gerr.Error(), "")
		return
	}
	logging.LogInfo("members", "premium mail-ID order "+order.Reference+" opened for "+local+"@"+host)
	if gateway == payments.GatewayStripe {
		origin := "https://" + config.Cfg.Domain
		sc := payments.NewStripeClient(a.outboundClient, stripeKey)
		checkoutURL, _, serr := sc.CreateCheckoutSession(r.Context(), payments.CheckoutParams{
			Mode: "payment", AmountCents: price, Currency: currency,
			ProductName: "Premium address " + local + "@" + host, CustomerEmail: m.Email,
			ClientReferenceID: order.Reference,
			SuccessURL:        origin + "/checkout/success?session_id={CHECKOUT_SESSION_ID}",
			CancelURL:         origin + "/members/account",
			Metadata:          map[string]string{"reference": order.Reference, "kind": "mailid"},
		})
		if serr == nil {
			writeJSON(w, r, http.StatusOK, map[string]interface{}{"mode": "stripe", "checkout_url": checkoutURL, "reference": order.Reference})
			return
		}
		logging.LogError("members", "premium stripe checkout failed", serr.Error())
		// fall through to the offline instructions so the buyer is never stuck
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"mode": "direct", "reference": order.Reference,
		"amount": priceLabel(currency, price), "instructions": a.directInstructions(r.Context()),
	})
}

// POST /api/v1/members/mailbox/premium/activate {localpart,password,accept_terms}
// — provision a premium address the member has PAID for (a claimable grant),
// exactly like the included-mailbox claim but authorised by the grant rather than
// the free-tier entitlement.
func (a *App) handleMemberMailIDActivate(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Localpart   string `json:"localpart"`
		Password    string `json:"password"`
		AcceptTerms bool   `json:"accept_terms"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(in.Localpart))
	grant, gerr := a.members.ClaimablePremiumGrant(r.Context(), m.Email, local)
	if gerr != nil || grant == nil {
		writeAPIError(w, r, http.StatusForbidden, "no-grant", "You have not purchased that address.", "")
		return
	}
	terms := a.mailIDTerms(r.Context())
	if terms != "" && !in.AcceptTerms {
		writeAPIError(w, r, http.StatusBadRequest, "terms-required", "Please accept the mailbox terms to continue.", "")
		return
	}
	if len(in.Password) < 8 {
		writeAPIError(w, r, http.StatusBadRequest, "weak-password", "Password must be at least 8 characters.", "")
		return
	}
	host := grant.Domain
	if host == "" {
		host = a.mailHost()
	}
	if a.vayuMail.Accounts().Exists(r.Context(), local+"@"+host) {
		writeAPIError(w, r, http.StatusConflict, "taken", "That address was just taken.", "")
		return
	}
	quotaBytes := int64(premiumMailIDDefaultQuotaMB) * 1024 * 1024
	if _, q, ok := a.mailboxEntitlement(r.Context(), m); ok && q > 0 {
		quotaBytes = q
	}
	hash, herr := auth.HashSecretArgon2id(in.Password)
	if herr != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "hash-failed", "Could not secure the password", "")
		return
	}
	email, perr := a.provisionMailbox(r.Context(), "", local, host, hash, m.DisplayName(), mail.RoleMailbox, quotaBytes)
	if perr != nil {
		writeAPIError(w, r, http.StatusBadRequest, "provision-failed", perr.Error(), "")
		return
	}
	termsSum := sha256.Sum256([]byte(terms))
	if aerr := a.members.RecordMailIDAgreement(r.Context(), m.Email, email, hex.EncodeToString(termsSum[:])); aerr != nil {
		logging.LogError("members", "record mailid agreement failed", aerr.Error())
	}
	_ = a.members.MarkPremiumGrantClaimed(r.Context(), grant.ID)
	logging.LogInfo("members", "member activated premium mailbox "+email)
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{"address": email})
}

// GET /api/v1/members/mailbox/available?localpart=X — live availability check.
func (a *App) handleMemberMailboxAvailable(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("localpart")))
	available, reason := a.mailboxLocalpartCheck(r.Context(), local)
	// premium flags a well-formed, non-reserved vanity name so the portal can
	// surface it as sellable inventory rather than a plain "unavailable".
	premium := mail.ValidLocalpart(local) && !mail.IsReservedLocalpart(local) && a.isPremiumLocalpart(r.Context(), local)
	resp := map[string]interface{}{"localpart": local, "available": available, "reason": reason, "premium": premium}
	if premium {
		cents := a.premiumMailIDPriceCents(r.Context())
		cur := a.payCurrency(r.Context())
		resp["price_cents"] = cents
		resp["currency"] = cur
		resp["price"] = priceLabel(cur, cents)
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// POST /api/v1/members/mailbox/claim {localpart, password} — provisions the
// mailbox the member's tier includes.
func (a *App) handleMemberMailboxClaim(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in", "")
		return
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	tier, quotaBytes, ok := a.mailboxEntitlement(r.Context(), m)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "not-entitled", "Your plan does not include a mailbox.", "")
		return
	}
	if a.members.MailAddressFor(r.Context(), m.Email) != "" {
		writeAPIError(w, r, http.StatusConflict, "already-claimed", "You already have a mailbox.", "")
		return
	}
	var in struct {
		Localpart   string `json:"localpart"`
		Password    string `json:"password"`
		AcceptTerms bool   `json:"accept_terms"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	// Terms gate: when the operator has mail-ID terms in force, a member must
	// accept them before an address is provisioned; the acceptance is recorded
	// (below) as proof. Clearing the terms text relaxes the gate.
	terms := a.mailIDTerms(r.Context())
	if terms != "" && !in.AcceptTerms {
		writeAPIError(w, r, http.StatusBadRequest, "terms-required", "Please accept the mailbox terms to continue.", "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(in.Localpart))
	if okc, reason := a.mailboxLocalpartCheck(r.Context(), local); !okc {
		writeAPIError(w, r, http.StatusBadRequest, "bad-localpart", reason, "")
		return
	}
	if len(in.Password) < 8 {
		writeAPIError(w, r, http.StatusBadRequest, "weak-password", "Password must be at least 8 characters.", "")
		return
	}
	hash, err := auth.HashSecretArgon2id(in.Password)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "hash-failed", "Could not secure the password", "")
		return
	}
	// Atomically reserve the member's single mailbox slot BEFORE creating anything
	// (audit M13): only the first concurrent claim transitions mail_address from
	// empty, so N parallel claims with different localparts can no longer each
	// provision a real mailbox. The early MailAddressFor check above is a fast-path
	// for the common case; this is the authoritative guard.
	intended := local + "@" + a.mailHost()
	claimed, cerr := a.members.ClaimMailAddress(r.Context(), m.Email, intended)
	if cerr != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "claim-failed", "Could not reserve the mailbox", "")
		return
	}
	if !claimed {
		writeAPIError(w, r, http.StatusConflict, "already-claimed", "You already have a mailbox.", "")
		return
	}
	email, perr := a.provisionMailbox(r.Context(), "", local, a.mailHost(), hash, m.DisplayName(), mail.RoleMailbox, quotaBytes)
	if perr != nil {
		// Release the reservation so the member can retry (e.g. with another
		// localpart) rather than being permanently stuck "already-claimed".
		_ = a.members.ClearMailAddressIf(r.Context(), m.Email, intended)
		writeAPIError(w, r, http.StatusBadRequest, "provision-failed", perr.Error(), "")
		return
	}
	// Reconcile the reserved address with the one actually provisioned (normally
	// identical); the reservation already holds the slot.
	if !strings.EqualFold(email, intended) {
		_ = a.members.SetMailAddress(r.Context(), m.Email, email)
	}
	// Record the member's acceptance of the exact terms in force (address + a
	// SHA-256 of the terms text + timestamp) so the operator has durable proof of
	// agreement for the address just provisioned.
	termsSum := sha256.Sum256([]byte(terms))
	if aerr := a.members.RecordMailIDAgreement(r.Context(), m.Email, email, hex.EncodeToString(termsSum[:])); aerr != nil {
		logging.LogError("members", "record mailid agreement failed", aerr.Error())
	}
	logging.LogInfo("members", "member claimed mailbox "+email)
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{"address": email, "quota_mb": tier.MailQuotaMB})
}
