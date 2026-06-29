package main

// handlers_member_portal.go — the premium membership experience.
//
// This file adds the reader-facing surfaces that turn VayuPress memberships
// into a full membership product:
//
//   - GET  /members            a branded passwordless sign-in page
//   - GET  /members/account    the member portal (profile, plan, preferences)
//   - POST /members/account     update name + newsletter preference
//   - GET  /pricing            a public pricing page listing the membership tiers
//   - GET  /api/v1/tiers       the public tier catalogue as JSON
//
// plus the admin/JSON management API for tiers, member detail, labels, and the
// membership stats (MRR / growth) consumed by the Members console. Member pages
// authenticate via the passwordless session cookie (resolveMember); they never
// require the operator API key.

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

// =============================================================================
// Public: sign-in page
// =============================================================================

// GET /members — passwordless sign-in. Already-authenticated members are sent
// straight to their account portal.
func (a *App) handleMemberSigninPage(w http.ResponseWriter, r *http.Request) {
	if m := a.resolveMember(r); m != nil {
		http.Redirect(w, r, "/members/account", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "index, follow")

	brand := html.EscapeString(config.Cfg.Domain)
	nonce := render.CSPNonce(r)
	notice := ""
	if r.URL.Query().Get("check_email") == "1" {
		notice = `<div class="su-notice su-notice--ok" role="status">Check your inbox — we just emailed you a secure sign-in link. It is valid for 30 minutes.</div>`
	}

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · ` + brand + `</title>
<meta name="description" content="Sign in to your ` + brand + ` membership.">
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">
<main class="su-shell" id="main-content">
  <section class="su-card">
    <div class="su-brand">
      <img class="su-logo" src="/static/favicon-light.png" alt="" width="40" height="40">
      <span class="su-brand-name">` + brand + `</span>
    </div>
    <h1 class="su-title">Sign in</h1>
    <p class="su-sub">Enter your email and we'll send you a one-time sign-in link. No password required.</p>
    ` + notice + `
    <form class="su-form" method="POST" action="/members/login" novalidate>
      <label class="su-label" for="su-email">Email address</label>
      <input class="su-input" id="su-email" type="email" name="email" required autocomplete="email" placeholder="you@example.com" aria-label="Email address">
      <button class="su-btn" type="submit">Email me a sign-in link →</button>
    </form>
    <p class="su-foot">New here? <a href="/signup" class="su-link">Create a free account</a> · <a href="/pricing" class="su-link">View plans</a></p>
  </section>
  <p class="su-legal">Powered by VayuPress · your email is used only to send your sign-in link.</p>
</main>
<script nonce="` + nonce + `">
(function(){'use strict';
var f=document.querySelector('.su-form');
if(f){f.addEventListener('submit',function(){var b=f.querySelector('.su-btn');if(b){b.disabled=true;b.textContent='Sending your link…';}});}
})();
</script>
</body></html>`
	_, _ = w.Write([]byte(page))
}

// =============================================================================
// Public: member account portal
// =============================================================================

// GET /members/account — the signed-in member's portal.
func (a *App) handleMemberAccount(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		http.Redirect(w, r, "/members", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "no-store")

	brand := html.EscapeString(config.Cfg.Domain)
	esc := html.EscapeString

	// Resolve the member's current tier + subscription for display.
	tierName := "Free"
	if t, err := a.members.GetTier(r.Context(), m.Tier); err == nil {
		tierName = t.Name
	} else if m.IsPaid() {
		tierName = "Premium"
	}
	planLine := "You are on the <strong>" + esc(tierName) + "</strong> plan."
	planClass := "ma-plan"
	if m.IsPaid() {
		planClass = "ma-plan ma-plan--paid"
		if sub, _ := a.members.ActiveSubscription(r.Context(), m.ID); sub != nil {
			cad := sub.Cadence
			if cad == members.CadenceComplimentary {
				planLine += ` <span class="ma-muted">(complimentary)</span>`
			} else if sub.AmountCents > 0 {
				planLine += ` <span class="ma-muted">(` + esc(sub.Currency) + " " + esc(formatMoney(sub.AmountCents)) + " / " + esc(cad) + `)</span>`
			}
		}
	}

	notice := ""
	if r.URL.Query().Get("saved") == "1" {
		notice = `<div class="su-notice su-notice--ok" role="status">Your details were saved.</div>`
	}

	newsletterChecked := ""
	if m.NewsletterOptIn {
		newsletterChecked = " checked"
	}
	replyChecked := ""
	if m.ReplyNotify {
		replyChecked = " checked"
	}

	upgradeCTA := ""
	if !m.IsPaid() {
		upgradeCTA = `<div class="ma-upgrade">
      <p>Want full access to premium posts?</p>
      <a class="su-btn su-btn--inline" href="/pricing">See membership plans →</a>
    </div>`
	}

	since := m.CreatedAt.UTC().Format("2 January 2006")

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Your account · ` + brand + `</title>
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">
<main class="ma-shell" id="main-content">
  <div class="ma-head">
    <h1 class="ma-title">Hello, ` + esc(m.DisplayName()) + `</h1>
    <form method="POST" action="/members/logout"><button class="ma-signout" type="submit">Sign out</button></form>
  </div>
  ` + notice + `
  <section class="ma-card">
    <h2>Membership</h2>
    <p class="` + planClass + `">` + planLine + `</p>
    ` + upgradeCTA + `
  </section>
  <section class="ma-card">
    <h2>Account</h2>
    <div class="ma-row"><span>Email</span><span>` + esc(m.Email) + `</span></div>
    <div class="ma-row"><span>Member since</span><span>` + esc(since) + `</span></div>
  </section>
  <section class="ma-card">
    <h2>Your details</h2>
    <form method="POST" action="/members/account">
      <div class="ma-field">
        <label for="ma-name">Display name</label>
        <input id="ma-name" type="text" name="name" value="` + esc(m.Name) + `" placeholder="Your name" maxlength="120">
      </div>
      <h3 class="ma-subhead">🔔 Notifications</h3>
      <label class="ma-check">
        <input type="checkbox" name="reply_notify" value="1"` + replyChecked + `>
        <span>💬 Email me when someone replies to my comment</span>
      </label>
      <label class="ma-check">
        <input type="checkbox" name="newsletter" value="1"` + newsletterChecked + `>
        <span>📰 Send me new posts and the members newsletter</span>
      </label>
      <p class="ma-hint">You're in control — change these any time, or unsubscribe with one click from any email.</p>
      <button class="su-btn" type="submit">Save changes</button>
    </form>
  </section>
</main>
</body></html>`
	_, _ = w.Write([]byte(page))
}

// POST /members/account — update the signed-in member's profile + preferences.
func (a *App) handleMemberAccountUpdate(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		http.Redirect(w, r, "/members", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if len(name) > 120 {
		name = name[:120]
	}
	// Preserve any operator note (members cannot edit it).
	if err := a.members.UpdateProfile(r.Context(), m.Email, name, m.Note); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	newsletterOn := r.PostFormValue("newsletter") == "1"
	_ = a.members.SetNewsletterOptIn(r.Context(), m.Email, newsletterOn)
	_ = a.members.SetReplyNotify(r.Context(), m.Email, r.PostFormValue("reply_notify") == "1")
	// Keep the public newsletter subscriber list in sync so the member actually
	// receives (or stops receiving) broadcasts. As an authenticated member their
	// address is already verified, so we subscribe them confirmed — no opt-in.
	if a.newsletterStore != nil {
		if newsletterOn {
			_ = a.newsletterStore.SubscribeConfirmed(r.Context(), m.Email)
		} else {
			_ = a.newsletterStore.UnsubscribeEmail(r.Context(), m.Email)
		}
	}
	http.Redirect(w, r, "/members/account?saved=1", http.StatusSeeOther)
}

// =============================================================================
// Public: pricing page + tier catalogue
// =============================================================================

// GET /pricing — a public page listing the published membership tiers.
func (a *App) handlePricingPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "index, follow")

	brand := html.EscapeString(config.Cfg.Domain)
	esc := html.EscapeString

	var tiers []members.Tier
	if a.members != nil {
		tiers, _ = a.members.ListTiers(r.Context(), false)
	}
	payEnabled := a.paymentsEnabled(r.Context())

	cards := ""
	for i := range tiers {
		t := tiers[i]
		price := "Free"
		sub := ""
		if !t.IsFree() {
			price = priceLabel(t.Currency, t.MonthlyCents)
			sub = "per month"
			if t.MonthlyCents == 0 && t.YearlyCents > 0 {
				price = priceLabel(t.Currency, t.YearlyCents)
				sub = "per year"
			}
		}
		benefits := ""
		for _, b := range t.Benefits {
			benefits += `<li>` + esc(b) + `</li>`
		}
		featured := ""
		if !t.IsFree() {
			featured = " pr-card--featured"
		}
		yearly := ""
		if t.YearlyCents > 0 && t.MonthlyCents > 0 {
			yearly = `<p class="pr-yearly">or ` + esc(priceLabel(t.Currency, t.YearlyCents)) + ` billed yearly</p>`
		}
		cta := `<a class="pr-cta" href="/signup">Get started</a>`
		if !t.IsFree() {
			cta = `<a class="pr-cta pr-cta--primary" href="/signup">Become a member</a>`
			if payEnabled {
				cadence := "monthly"
				if t.MonthlyCents == 0 && t.YearlyCents > 0 {
					cadence = "yearly"
				}
				cta = `<a class="pr-cta pr-cta--primary" href="/checkout?tier=` + esc(t.Slug) + `&amp;cadence=` + cadence + `">Subscribe</a>`
			}
		}
		cards += `<article class="pr-card` + featured + `">
      <h2 class="pr-name">` + esc(t.Name) + `</h2>
      <p class="pr-desc">` + esc(t.Description) + `</p>
      <div class="pr-price"><span class="pr-amount">` + esc(price) + `</span> <span class="pr-per">` + esc(sub) + `</span></div>
      ` + yearly + `
      <ul class="pr-benefits">` + benefits + `</ul>
      ` + cta + `
    </article>`
	}
	if cards == "" {
		cards = `<p class="pr-empty">Membership plans are not available yet.</p>`
	}

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Membership plans · ` + brand + `</title>
<meta name="description" content="Choose a membership plan to support ` + brand + ` and unlock premium posts.">
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">
<main class="pr-shell" id="main-content">
  <div class="pr-head">
    <h1>Become a member</h1>
    <p>Support independent publishing and unlock everything.</p>
  </div>
  <div class="pr-grid">` + cards + `</div>
  <p class="pr-foot">Already a member? <a href="/members" class="su-link">Sign in</a></p>
</main>
</body></html>`
	_, _ = w.Write([]byte(page))
}

// GET /api/v1/tiers — public tier catalogue.
func (a *App) handleTiersPublic(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeJSON(w, r, http.StatusOK, map[string]interface{}{"tiers": []members.Tier{}})
		return
	}
	tiers, err := a.members.ListTiers(r.Context(), false)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"tiers": tiers})
}

// =============================================================================
// Admin: tier management
// =============================================================================

// GET /api/v1/admin/tiers — every tier (including hidden / archived).
func (a *App) handleTierListAdmin(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	tiers, err := a.members.ListTiers(r.Context(), true)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"tiers": tiers})
}

// tierBody is the JSON shape accepted by tier create/update.
type tierBody struct {
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	MonthlyCents       int      `json:"monthly_cents"`
	YearlyCents        int      `json:"yearly_cents"`
	Currency           string   `json:"currency"`
	Benefits           []string `json:"benefits"`
	Visibility         string   `json:"visibility"`
	Sort               int      `json:"sort"`
	TrialDays          int      `json:"trial_days"`
	StripeMonthlyPrice string   `json:"stripe_monthly_price"`
	StripeYearlyPrice  string   `json:"stripe_yearly_price"`
}

func (b tierBody) toInput() members.TierInput {
	return members.TierInput{
		Name: b.Name, Description: b.Description, MonthlyCents: b.MonthlyCents,
		YearlyCents: b.YearlyCents, Currency: b.Currency, Benefits: b.Benefits,
		Visibility: b.Visibility, Sort: b.Sort, TrialDays: b.TrialDays,
		StripeMonthlyPrice: b.StripeMonthlyPrice, StripeYearlyPrice: b.StripeYearlyPrice,
	}
}

// POST /api/v1/admin/tiers
func (a *App) handleTierCreate(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	var body tierBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	tier, err := a.members.CreateTier(r.Context(), body.toInput())
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "tier-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusCreated, tier)
}

// PUT /api/v1/admin/tiers/{id}
func (a *App) handleTierUpdate(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	var body tierBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if err := a.members.UpdateTier(r.Context(), chi.URLParam(r, "id"), body.toInput()); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "tier-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// DELETE /api/v1/admin/tiers/{id} — archives the tier (built-ins are protected).
func (a *App) handleTierDelete(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	if err := a.members.ArchiveTier(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "tier-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "archived"})
}

// =============================================================================
// Admin: stats, member detail, labels
// =============================================================================

// GET /api/v1/admin/members/stats — MRR, tier distribution, growth series.
func (a *App) handleMemberStats(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	stats, err := a.members.Stats(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	series, _ := a.members.SignupsByDay(r.Context(), days)
	revenue, _ := a.members.RevenueByTier(r.Context())
	activity, _ := a.members.RecentEvents(r.Context(), 25)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"stats":    stats,
		"signups":  series,
		"revenue":  revenue,
		"activity": activity,
	})
}

// GET /api/v1/admin/members/{email} — a single member with subscription detail.
func (a *App) handleMemberDetail(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	m, err := a.members.Get(r.Context(), chi.URLParam(r, "email"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No member with that email", "")
		return
	}
	sub, _ := a.members.ActiveSubscription(r.Context(), m.ID)
	activity, _ := a.members.EventsForMember(r.Context(), m.ID, 50)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"member": m, "subscription": sub, "activity": activity})
}

// labelBody is the JSON shape for adding a label.
type labelBody struct {
	Label string `json:"label"`
}

// POST /api/v1/admin/members/{email}/labels  {label}
func (a *App) handleMemberLabelAdd(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	m, err := a.members.Get(r.Context(), chi.URLParam(r, "email"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No member with that email", "")
		return
	}
	var body labelBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if err := a.members.AddLabel(r.Context(), m.ID, body.Label); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "label-error", err.Error(), "")
		return
	}
	labels, _ := a.members.LabelsForMember(r.Context(), m.ID)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"labels": labels})
}

// DELETE /api/v1/admin/members/{email}/labels/{label}
func (a *App) handleMemberLabelRemove(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	m, err := a.members.Get(r.Context(), chi.URLParam(r, "email"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No member with that email", "")
		return
	}
	if err := a.members.RemoveLabel(r.Context(), m.ID, chi.URLParam(r, "label")); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "label-error", err.Error(), "")
		return
	}
	labels, _ := a.members.LabelsForMember(r.Context(), m.ID)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"labels": labels})
}

// =============================================================================
// Admin: export + subscription lifecycle actions
// =============================================================================

// GET /os/api/members/export.csv (and /api/v1/admin/members/export.csv)
// Streams every member as a CSV download — handy for backups and for migrating
// an existing audience in from another platform.
func (a *App) handleMembersExportCSV(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="members.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := a.members.ExportCSV(r.Context(), w); err != nil {
		// Headers may already be sent; log-only fallback keeps the stream honest.
		writeAPIError(w, r, http.StatusInternalServerError, "export-error", err.Error(), "")
		return
	}
}

// PUT /os/api/members/{email}/cancel  {immediate?}
// Cancels a member's subscription. By default the cancellation is scheduled for
// the end of the paid period (the member keeps access until then); pass
// {"immediate": true} to revoke access right away and drop them to free.
func (a *App) handleMemberCancel(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	m, err := a.members.Get(r.Context(), chi.URLParam(r, "email"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No member with that email", "")
		return
	}
	var body struct {
		Immediate bool `json:"immediate"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body is fine → scheduled cancel
	if body.Immediate {
		if err := a.members.CancelSubscription(r.Context(), m.ID); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "cancel-error", err.Error(), "")
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "canceled"})
		return
	}
	if err := a.members.ScheduleCancellation(r.Context(), m.ID); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "cancel-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "scheduled"})
}

// =============================================================================
// helpers
// =============================================================================

// formatMoney renders integer cents as a major-unit string (e.g. 500 -> "5.00").
func formatMoney(cents int) string { return fmt.Sprintf("%.2f", float64(cents)/100) }

// priceLabel renders a currency-prefixed price for common currencies.
func priceLabel(currency string, cents int) string {
	sym := map[string]string{"USD": "$", "EUR": "€", "GBP": "£", "INR": "₹", "AUD": "A$", "CAD": "C$"}[strings.ToUpper(currency)]
	if sym != "" {
		return sym + formatMoney(cents)
	}
	return formatMoney(cents) + " " + strings.ToUpper(currency)
}
