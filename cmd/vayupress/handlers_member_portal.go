// SPDX-License-Identifier: Apache-2.0

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
	"context"
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
	_, _ = w.Write([]byte(render.HalcyonMemberPage(a.memberSigninPage(r), render.MemberTask)))
}

// memberSigninPage builds the sign-in page as every theme but Halcyon shows
// it; Theme Studio's preview dresses the same page in the options being edited.
func (a *App) memberSigninPage(r *http.Request) string {
	brand := html.EscapeString(config.Cfg.Domain)
	nonce := render.CSPNonce(r)
	notice := ""
	if r.URL.Query().Get("check_email") == "1" {
		notice = `<div class="su-notice su-notice--ok" role="status">Check your inbox — we just emailed you a secure sign-in link. It is valid for 30 minutes.</div>`
	}
	if r.URL.Query().Get("deleted") == "1" {
		notice = `<div class="su-notice su-notice--ok" role="status">Your account is deleted, and you are signed out. You are welcome back any time.</div>`
	}

	// When VayuMail is active, offer a second sign-in path for members who hold a
	// mailbox: their @domain address + password (+ a 2FA code when enabled). This
	// posts to the existing /api/v1/members/vayumail-login endpoint.
	mailIDBlock := ""
	if a.vayuMailLoginEnabled() {
		mailIDBlock = `<div class="su-or"><span>or</span></div>
    <details class="su-alt">
      <summary>Sign in with your VayuMail address</summary>
      <div class="su-alt-body">
        <label class="su-label" for="vm-email">VayuMail address</label>
        <input class="su-input" id="vm-email" type="email" autocomplete="username" placeholder="you@` + brand + `">
        <label class="su-label" for="vm-pass">Password</label>
        <input class="su-input" id="vm-pass" type="password" autocomplete="current-password" placeholder="Your mailbox password">
        <div id="vm-code-wrap" hidden>
          <label class="su-label" for="vm-code">Two-factor code</label>
          <input class="su-input" id="vm-code" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000">
        </div>
        <p class="su-notice su-notice--err" id="vm-msg" role="alert" hidden></p>
        <button class="su-btn su-btn--ghost" id="vm-btn" type="button">Sign in →</button>
        <p class="su-foot"><a class="su-link" href="/mail/recover">Forgot your mailbox password?</a></p>
      </div>
    </details>`
	}

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · ` + brand + `</title>
<meta name="description" content="Sign in to your ` + brand + ` membership.">
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css?v=` + assetVer("css/signup.css") + `">
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
    ` + mailIDBlock + `
    <p class="su-foot">New here? <a href="/signup" class="su-link">Create a free account</a> · <a href="/pricing" class="su-link">View plans</a></p>
  </section>
  <p class="su-legal">Powered by VayuPress · your email is used only to send your sign-in link.</p>
</main>
<script nonce="` + nonce + `">
(function(){'use strict';
var f=document.querySelector('.su-form');
if(f){f.addEventListener('submit',function(){var b=f.querySelector('.su-btn');if(b){b.disabled=true;b.textContent='Sending your link…';}});}
var vb=document.getElementById('vm-btn');
if(vb){
  var em=document.getElementById('vm-email'),pw=document.getElementById('vm-pass'),cw=document.getElementById('vm-code-wrap'),cd=document.getElementById('vm-code'),msg=document.getElementById('vm-msg');
  function showErr(t){if(msg){msg.textContent=t;msg.hidden=false;}}
  vb.addEventListener('click',function(){
    var email=(em&&em.value||'').trim(),pass=(pw&&pw.value||''),code=(cd&&cd.value||'').replace(/\D/g,'');
    if(!email||!pass){showErr('Enter your address and password.');return;}
    vb.disabled=true;if(msg)msg.hidden=true;
    fetch('/api/v1/members/vayumail-login',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({email:email,password:pass,code:code})})
      .then(function(res){return res.json().then(function(j){return {ok:res.ok,j:j};},function(){return {ok:res.ok,j:{}};});})
      .then(function(r){
        var e=r.j&&r.j.error||{};
        if(r.ok&&r.j.authenticated){window.location.assign('/members/account');return;}
        if(e.code==='totp-required'){if(cw)cw.hidden=false;if(cd)cd.focus();showErr(e.message||'Enter your 6-digit code.');vb.disabled=false;return;}
        showErr(e.message||'That address and password did not match.');vb.disabled=false;
      })
      .catch(function(){showErr('Network error — please try again.');vb.disabled=false;});
  });
}
})();
</script>
</body></html>`
	return page
}

// =============================================================================
// Public: member account portal
// =============================================================================

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
	// One name for the person: their mailbox at this address, when they hold
	// one, sends under it too (3cs). setPersonName keeps the operator's note,
	// which members cannot edit.
	if err := a.setPersonName(r.Context(), m.Email, name); err != nil {
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
	// The account page's email switches save as they change, by fetch; they
	// are answered in JSON rather than sent round the page again.
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, r, http.StatusOK, map[string]bool{"saved": true})
		return
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
	// This page's content now depends on the member session, so it MUST declare
	// that: without Vary, a shared cache could store one member's personalised
	// page and serve it to everyone. The anonymous version stays cacheable (it is
	// the version search engines index and the one most visitors get); the
	// signed-in version is marked private and unstorable further down, once we
	// know who is reading.
	w.Header().Set("Vary", "Cookie")

	brand := html.EscapeString(config.Cfg.Domain)
	nonce := render.CSPNonce(r)

	var tiers []members.Tier
	if a.members != nil {
		tiers, _ = a.members.ListTiers(r.Context(), false)
	}
	payEnabled := a.paymentsEnabled(r.Context())

	// Who is reading this page? Offering "Get started" and "Sign in" to somebody
	// already signed in is the plans page telling them it does not know them — and
	// it hides the one action they actually came for, which is to upgrade. Resolve
	// the member so each card can speak to their real position.
	me := a.resolveMember(r)
	signedIn := me != nil
	if signedIn {
		// A page naming somebody's plan is theirs alone — never store it.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("CDN-Cache-Control", "no-store")
	} else {
		// Anonymous: safe (and good for speed) for an edge to reuse briefly.
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	currentTier := ""
	onPaid := false
	if signedIn {
		currentTier = me.Tier
		onPaid = me.IsPaid()
	}

	// Emphasis on every paid tier is emphasis on none: the old code set
	// pr-card--featured for every !IsFree() tier, so an install with three paid
	// plans drew a highlight ring around all three. Exactly one card gets it —
	// the cheapest paid tier, the easiest yes. Note what this deliberately is
	// NOT: a "most popular" badge. Popularity is a claim about data this page
	// does not have, and inventing it is the kind of small lie that makes the
	// honest parts of the page harder to believe.
	featuredIdx := -1
	for i := range tiers {
		if tiers[i].IsFree() {
			continue
		}
		if featuredIdx < 0 || effectiveMonthlyCents(tiers[i]) < effectiveMonthlyCents(tiers[featuredIdx]) {
			featuredIdx = i
		}
	}

	// bestSaving is the largest yearly discount on offer, for the toggle's label.
	// Only tiers priced in both cadences can have one.
	bestSaving := 0
	for i := range tiers {
		if s := yearlySavingPct(tiers[i]); s > bestSaving {
			bestSaving = s
		}
	}

	cards := ""
	anyYearly := false
	for i := range tiers {
		t := tiers[i]
		bothCadences := t.MonthlyCents > 0 && t.YearlyCents > 0
		if bothCadences {
			anyYearly = true
		}

		// Two prices, both rendered; which one shows is a class on the shell. Doing
		// it in CSS rather than by rewriting text means the yearly figure is in the
		// HTML for anyone reading with JS off or a screen reader.
		priceBlock := ""
		if t.IsFree() {
			priceBlock = `<div class="pr-price"><span class="pr-amount">Free</span></div>`
		} else {
			monthly := priceLabel(t.Currency, t.MonthlyCents)
			perMonth := `<div class="pr-price pr-price--monthly"><span class="pr-amount">` + esc(monthly) +
				`</span> <span class="pr-per">per month</span></div>`
			switch {
			case bothCadences:
				save := ""
				if s := yearlySavingPct(t); s > 0 {
					save = ` <span class="pr-save">save ` + strconv.Itoa(s) + `%</span>`
				}
				priceBlock = perMonth +
					`<div class="pr-price pr-price--yearly"><span class="pr-amount">` + esc(priceLabel(t.Currency, t.YearlyCents)) +
					`</span> <span class="pr-per">per year</span>` + save + `</div>`
			case t.MonthlyCents == 0:
				// Yearly-only tier: the toggle must not imply a monthly option that
				// checkout would silently convert.
				priceBlock = `<div class="pr-price"><span class="pr-amount">` + esc(priceLabel(t.Currency, t.YearlyCents)) +
					`</span> <span class="pr-per">per year</span></div>`
			default:
				priceBlock = perMonth
			}
		}

		// The no-JS fallback for the yearly figure. Hidden once the toggle is live,
		// so the same number is never on screen twice.
		yearly := ""
		if bothCadences {
			yearly = `<p class="pr-yearly">or ` + esc(priceLabel(t.Currency, t.YearlyCents)) + ` billed yearly</p>`
		}

		// A trial is the strongest thing a plan can offer and it was sitting unused
		// in the tier record, never rendered.
		trial := ""
		if t.TrialDays > 0 && !t.IsFree() {
			trial = `<p class="pr-trial">` + strconv.Itoa(t.TrialDays) + ` days free, then billed as above. Cancel any time.</p>`
		}

		benefits := ""
		// A real mailbox on the reader's own domain is the most distinctive thing a
		// VayuPress tier can include, and the page discarded it entirely. It leads
		// the list because it is the reason to pick this product over a newsletter.
		if t.MailEnabled {
			quota := "with unlimited storage"
			if t.MailQuotaMB > 0 {
				quota = "with " + esc(formatQuotaMB(t.MailQuotaMB)) + " of storage"
			}
			benefits += `<li class="pr-benefit--mail">Your own <strong>@` + brand + ` mailbox</strong> ` + quota +
				` — PGP encryption, WKD, and VayuTalk chat included</li>`
		}
		for _, b := range t.Benefits {
			benefits += `<li>` + esc(b) + `</li>`
		}

		featured := ""
		if i == featuredIdx {
			featured = " pr-card--featured"
		}

		// The call to action depends on where the reader already stands.
		isCurrent := signedIn && (t.Slug == currentTier || (t.IsFree() && !onPaid))
		cadence := "monthly"
		if t.MonthlyCents == 0 && t.YearlyCents > 0 {
			cadence = "yearly"
		}
		// A cadence toggle that changes the displayed price without changing where
		// the button goes would show a yearly figure and charge monthly. Both
		// destinations travel with the button; the toggle swaps between them.
		cadenceAttrs := ""
		if bothCadences {
			base := `/checkout?tier=` + esc(t.Slug) + `&amp;cadence=`
			cadenceAttrs = ` data-href-monthly="` + base + `monthly" data-href-yearly="` + base + `yearly"`
		}
		var cta string
		switch {
		case isCurrent:
			// Nothing to sell here — say so plainly instead of inviting a signup
			// that would do nothing.
			cta = `<span class="pr-cta pr-cta--current" aria-disabled="true">Your current plan</span>`
		case !signedIn && t.IsFree():
			cta = `<a class="pr-cta" href="/signup">Get started</a>`
		case !signedIn:
			cta = `<a class="pr-cta pr-cta--primary" href="/signup">Become a member</a>`
			if payEnabled {
				cta = `<a class="pr-cta pr-cta--primary"` + cadenceAttrs + ` href="/checkout?tier=` + esc(t.Slug) + `&amp;cadence=` + cadence + `">Subscribe</a>`
			}
		case t.IsFree():
			// A paying member looking at the free tier: downgrading is a billing
			// decision, so point at the account page rather than a checkout.
			cta = `<a class="pr-cta" href="/members/account">Manage in your account</a>`
		default:
			// Signed in, not on this paid tier — this is the upgrade they came for.
			cta = `<a class="pr-cta pr-cta--primary" href="/signup">Upgrade to ` + esc(t.Name) + `</a>`
			if payEnabled {
				cta = `<a class="pr-cta pr-cta--primary"` + cadenceAttrs + ` href="/checkout?tier=` + esc(t.Slug) + `&amp;cadence=` + cadence + `">Upgrade to ` + esc(t.Name) + `</a>`
			}
		}
		if isCurrent {
			featured += " pr-card--current"
		}
		here := ""
		if isCurrent {
			here = ` <span class="pr-here">You&rsquo;re here</span>`
		}
		cards += `<article class="pr-card` + featured + `">
      <h2 class="pr-name">` + esc(t.Name) + here + `</h2>
      <p class="pr-desc">` + esc(t.Description) + `</p>
      ` + priceBlock + `
      ` + yearly + `
      ` + trial + `
      <ul class="pr-benefits">` + benefits + `</ul>
      ` + cta + `
    </article>`
	}
	if cards == "" {
		cards = `<p class="pr-empty">Membership plans are not available yet.</p>`
	}

	// The toggle ships hidden and is revealed by the script below, so a reader
	// without JS sees exactly the page that worked before rather than a pair of
	// dead buttons. It only appears at all when some tier is priced both ways.
	toggle := ""
	if anyYearly {
		saveChip := ""
		if bestSaving > 0 {
			saveChip = ` <span class="pr-save">save up to ` + strconv.Itoa(bestSaving) + `%</span>`
		}
		toggle = `<div class="pr-toggle" id="pr-toggle" role="group" aria-label="Billing period" hidden>
    <button type="button" class="pr-toggle-btn is-on" data-pr-cadence="monthly" aria-pressed="true">Monthly</button>
    <button type="button" class="pr-toggle-btn" data-pr-cadence="yearly" aria-pressed="false">Yearly` + saveChip + `</button>
  </div>`
	}

	// Copy follows the reader. A signed-in member is not "becoming" a member, and
	// offering them a sign-in link is noise.
	prTitle, prLead := "Become a member", "Support independent publishing and unlock everything."
	prFooter := `<p class="pr-foot">Already a member? <a href="/members" class="su-link">Sign in</a></p>`
	if signedIn {
		prTitle, prLead = "Your membership", "Compare what each plan includes. Your current plan is marked."
		prFooter = `<p class="pr-foot"><a href="/members/account" class="su-link">Back to your account</a></p>`
	}

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Membership plans · ` + brand + `</title>
<meta name="description" content="Choose a membership plan to support ` + brand + ` and unlock premium posts.">
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css?v=` + assetVer("css/signup.css") + `">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">
<main class="pr-shell" id="main-content" data-cadence="monthly">
  <div class="pr-head">
    <h1>` + prTitle + `</h1>
    <p>` + prLead + `</p>
  </div>
  ` + toggle + `
  <div class="pr-grid">` + cards + `</div>
  ` + prFooter + `
</main>
<script nonce="` + nonce + `">
(function(){'use strict';
var shell=document.querySelector('.pr-shell');
var tog=document.getElementById('pr-toggle');
if(!shell||!tog){return;}
// Only now is the toggle real, so only now does it appear. Revealing it from
// markup would leave a dead control on any page whose script did not run.
tog.hidden=false;
shell.classList.add('pr-shell--js');
var btns=tog.querySelectorAll('[data-pr-cadence]');
function apply(cad){
  shell.setAttribute('data-cadence',cad);
  for(var i=0;i<btns.length;i++){
    var on=btns[i].getAttribute('data-pr-cadence')===cad;
    btns[i].classList.toggle('is-on',on);
    btns[i].setAttribute('aria-pressed',on?'true':'false');
  }
  // The button has to follow the price. A card priced in only one cadence keeps
  // the href it was rendered with, because switching it would send the reader to
  // a checkout that silently bills the other way.
  var ctas=shell.querySelectorAll('.pr-cta[data-href-'+cad+']');
  for(var j=0;j<ctas.length;j++){
    var h=ctas[j].getAttribute('data-href-'+cad);
    if(h){ctas[j].setAttribute('href',h);}
  }
}
for(var k=0;k<btns.length;k++){
  btns[k].addEventListener('click',function(e){apply(e.currentTarget.getAttribute('data-pr-cadence'));});
}
})();
</script>
</body></html>`
	_, _ = w.Write([]byte(render.HalcyonMemberPage(page, render.MemberSite)))
}

// effectiveMonthlyCents is what a tier costs per month however it is billed, so
// tiers priced only yearly can be compared with tiers priced only monthly.
func effectiveMonthlyCents(t members.Tier) int {
	if t.MonthlyCents > 0 {
		return t.MonthlyCents
	}
	if t.YearlyCents > 0 {
		return t.YearlyCents / 12
	}
	return 0
}

// yearlySavingPct is the discount for paying yearly, rounded down so the page
// never advertises a saving larger than the one the reader actually gets. Zero
// when the tier is not priced both ways, or when yearly is not in fact cheaper.
func yearlySavingPct(t members.Tier) int {
	if t.MonthlyCents <= 0 || t.YearlyCents <= 0 {
		return 0
	}
	full := t.MonthlyCents * 12
	if t.YearlyCents >= full {
		return 0
	}
	return (full - t.YearlyCents) * 100 / full
}

// formatQuotaMB renders a mailbox quota the way a reader would say it.
func formatQuotaMB(mb int) string {
	if mb >= 1024 && mb%1024 == 0 {
		return strconv.Itoa(mb/1024) + " GB"
	}
	if mb >= 1024 {
		return strconv.FormatFloat(float64(mb)/1024, 'f', 1, 64) + " GB"
	}
	return strconv.Itoa(mb) + " MB"
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
	MailEnabled        bool     `json:"mail_enabled"`
	MailQuotaMB        int      `json:"mail_quota_mb"`
}

func (b tierBody) toInput() members.TierInput {
	return members.TierInput{
		Name: b.Name, Description: b.Description, MonthlyCents: b.MonthlyCents,
		YearlyCents: b.YearlyCents, Currency: b.Currency, Benefits: b.Benefits,
		Visibility: b.Visibility, Sort: b.Sort, TrialDays: b.TrialDays,
		StripeMonthlyPrice: b.StripeMonthlyPrice, StripeYearlyPrice: b.StripeYearlyPrice,
		MailEnabled: b.MailEnabled, MailQuotaMB: b.MailQuotaMB,
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

// freePaidBenefits returns the benefit bullet lists for the Free tier and the
// primary paid tier, preferring the operator's own per-tier benefit lists and
// falling back to sensible defaults so the Free-vs-Premium comparison shown on
// the signup page and the member dashboard is never empty.
func (a *App) freePaidBenefits(ctx context.Context) (free, paid []string) {
	free = []string{"Read every free post", "Join the discussion in comments", "The members newsletter", "Your reader profile & preferences"}
	paid = []string{"Everything in Free", "Unlock all premium posts", "Your own VayuMail address (PGP mail)", "Encrypted VayuTalk messaging", "Two-factor security on your mail ID"}
	if a.members == nil {
		return free, paid
	}
	tiers, err := a.members.ListTiers(ctx, false)
	if err != nil {
		return free, paid
	}
	for i := range tiers {
		if tiers[i].IsFree() {
			if len(tiers[i].Benefits) > 0 {
				free = tiers[i].Benefits
			}
		} else if len(tiers[i].Benefits) > 0 {
			paid = tiers[i].Benefits
			break
		}
	}
	return free, paid
}

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
