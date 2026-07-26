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

	brand := html.EscapeString(config.Cfg.Domain)
	nonce := render.CSPNonce(r)
	notice := ""
	if r.URL.Query().Get("check_email") == "1" {
		notice = `<div class="su-notice su-notice--ok" role="status">Check your inbox — we just emailed you a secure sign-in link. It is valid for 30 minutes.</div>`
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
	nonce := render.CSPNonce(r)
	paid := m.IsPaid()

	// Resolve the member's current tier + subscription for display.
	tierName := "Free"
	if t, err := a.members.GetTier(r.Context(), m.Tier); err == nil {
		tierName = t.Name
	} else if paid {
		tierName = "Premium"
	}
	planPrice := ""
	planClass := "ma-plan-badge"
	if paid {
		planClass = "ma-plan-badge ma-plan-badge--paid"
		if sub, _ := a.members.ActiveSubscription(r.Context(), m.ID); sub != nil {
			if sub.Cadence == members.CadenceComplimentary {
				planPrice = `<span class="ma-plan-price">Complimentary</span>`
			} else if sub.AmountCents > 0 {
				planPrice = `<span class="ma-plan-price">` + esc(sub.Currency) + " " + esc(formatMoney(sub.AmountCents)) + " / " + esc(sub.Cadence) + `</span>`
			}
		}
	}

	// ── Free vs Paid benefits (prefer the operator's own tier benefit lists) ──
	freeBenefits, paidBenefits := a.freePaidBenefits(r.Context())
	benefitLis := func(items []string) string {
		out := ""
		for _, b := range items {
			out += `<li>` + esc(b) + `</li>`
		}
		return out
	}

	notice := ""
	if r.URL.Query().Get("saved") == "1" {
		notice = `<div class="su-notice su-notice--ok" role="status">Your details were saved.</div>`
	}
	if r.URL.Query().Get("mail") == "claimed" {
		notice += `<div class="su-notice su-notice--ok" role="status">🎉 Your VayuMail address is ready. You can now sign in with it and turn on two-factor security below.</div>`
	}
	switch r.URL.Query().Get("twofa") {
	case "on":
		notice += `<div class="su-notice su-notice--ok" role="status">🔐 Two-factor authentication is on. You'll enter a code from your app each time you sign in with your VayuMail address.</div>`
	case "off":
		notice += `<div class="su-notice su-notice--ok" role="status">Two-factor authentication is off.</div>`
	}

	newsletterChecked := ""
	if m.NewsletterOptIn {
		newsletterChecked = " checked"
	}
	replyChecked := ""
	if m.ReplyNotify {
		replyChecked = " checked"
	}

	// ── Plan card ──
	planUpgrade := ""
	if !paid {
		planUpgrade = `<a class="ma-cta-primary" href="/pricing">Upgrade to Premium →</a>`
	}
	whichBenefits := freeBenefits
	benefitsTitle := "What you get today"
	if paid {
		whichBenefits = paidBenefits
		benefitsTitle = "Your Premium benefits"
	}
	planCard := `<section class="ma-card ma-card--plan">
    <div class="ma-plan-top">
      <span class="` + planClass + `">` + esc(tierName) + `</span>` + planPrice + `
    </div>
    <h2 class="ma-plan-h">` + benefitsTitle + `</h2>
    <ul class="ma-benefits">` + benefitLis(whichBenefits) + `</ul>
    ` + planUpgrade + `
  </section>`

	// ── Free vs Paid comparison (helps a free member see what upgrading unlocks) ──
	compareCard := ""
	if !paid {
		compareCard = `<section class="ma-card">
    <h2>Free vs Premium</h2>
    <p class="ma-hint">Upgrade any time — keep everything you have, and unlock the rest.</p>
    <div class="ma-compare">
      <div class="ma-plan-col ma-plan-col--current">
        <div class="ma-plan-col-h">Free <span class="ma-col-tag">You're here</span></div>
        <ul class="ma-benefits">` + benefitLis(freeBenefits) + `</ul>
      </div>
      <div class="ma-plan-col ma-plan-col--paid">
        <div class="ma-plan-col-h">Premium <span class="ma-col-tag ma-col-tag--paid">✦ Best value</span></div>
        <ul class="ma-benefits">` + benefitLis(paidBenefits) + `</ul>
        <a class="ma-cta-primary" href="/pricing">See plans & upgrade →</a>
      </div>
    </div>
  </section>`
	}

	// ── VayuMail ID + Security (2FA) cards ──
	mailboxEmail, hasMailbox := a.memberOwnMailbox(r)
	_, _, mailEntitled := a.mailboxEntitlement(r.Context(), m)
	host := a.mailHost()
	mailCard := ""
	securityCard := ""
	scriptTag := ""
	// Summary state for the collapsed rows, so the page reads at a glance without
	// anything having to be expanded.
	mailSub, mailChip := "", ""
	secSub, secChip := "", ""
	switch {
	case hasMailbox:
		twoFAOn := false
		console := false
		if a.vayuMailLoginEnabled() {
			if _, on := a.vayuMail.Accounts().TOTPStatus(r.Context(), mailboxEmail); on {
				twoFAOn = true
			}
			if _, c := mailConsoleAccess(a.vayuMail.Accounts().RoleFor(r.Context(), mailboxEmail)); c {
				console = true
			}
		}
		consoleLink := ""
		if console {
			consoleLink = ` <a class="su-link" href="/os">Open VayuOS →</a>`
		}
		mailCard = `<section class="ma-card">
    <h2>📮 Your VayuMail ID</h2>
    <div class="ma-mailid">` + esc(mailboxEmail) + `</div>
    <p class="ma-hint">Sign in here with this address, in the VayuMail app, or any IMAP client.` + consoleLink + `</p>
  </section>`
		mailSub, mailChip = mailboxEmail, maChip("Ready", true)
		securityCard = renderMemberSecurityCard(twoFAOn)
		secSub = "Protects your mailbox sign-in"
		secChip = maChip("Off", false)
		if twoFAOn {
			secChip = maChip("On", true)
		}
		scriptTag = memberAccountInlineJS(nonce)
	case mailEntitled && host != "":
		terms := ""
		if a.mailIDTerms(r.Context()) != "" {
			terms = `<label class="ma-check ma-claim-terms">
        <input type="checkbox" id="ma-claim-terms">
        <span>I accept the <a href="#" class="su-link">mailbox terms</a></span>
      </label>`
		}
		mailCard = `<section class="ma-card ma-card--claim">
    <h2>📮 Claim your VayuMail address</h2>
    <p class="ma-hint">Your plan includes a private, PGP-encrypted mailbox on <strong>` + esc(host) + `</strong>. Pick your address and a password — you'll use them to sign in here, in the app, and over IMAP.</p>
    <div class="ma-claim" data-claim-domain="` + esc(host) + `">
      <div class="ma-claim-row">
        <input class="ma-input" id="ma-claim-local" type="text" placeholder="you" autocomplete="off" spellcheck="false" maxlength="64">
        <span class="ma-claim-at">@` + esc(host) + `</span>
      </div>
      <input class="ma-input" id="ma-claim-pass" type="password" placeholder="Mailbox password (min 8 characters)" autocomplete="new-password" minlength="8">
      ` + terms + `
      <p class="ma-claim-msg" id="ma-claim-msg" role="status"></p>
      <button class="ma-cta-primary" id="ma-claim-btn" type="button">Create my mail address →</button>
    </div>
  </section>`
		mailSub, mailChip = "Included with your plan", maChip("Claim yours", false)
		scriptTag = memberAccountInlineJS(nonce)
	case !paid:
		mailCard = `<section class="ma-card ma-card--perk">
    <h2>📮 Get your own email address</h2>
    <p class="ma-hint">A private <strong>@` + esc(host) + `</strong> VayuMail address — with automatic PGP encryption and two-factor security — is included with Premium.</p>
    <a class="ma-cta-primary" href="/pricing">Upgrade to get yours →</a>
  </section>`
		mailSub, mailChip = "Included with Premium", maChip("Premium", false)
		if host == "" {
			mailCard = ""
		}
	}

	since := config.FormatSite(m.CreatedAt, "2 January 2006")

	// Everything below the plan and billing summary collapses into one row each, in
	// the console's Monetization grammar: the page can be scanned rather than read.
	// Exactly one row starts open — mail, which is the thing members come here to
	// do — so the page is useful immediately without being a wall of cards.
	// Rows are collected first, then the FIRST one is opened. Marking a specific row
	// open would leave the page with nothing expanded whenever that row happens not
	// to render — mail, for instance, is absent when no mail host is configured.
	type accRow struct{ icon, title, sub, chip, body string }
	rows := []accRow{}
	if mailCard != "" {
		rows = append(rows, accRow{"&#128236;", "VayuMail address", mailSub, mailChip, mailCard})
	}
	if securityCard != "" {
		rows = append(rows, accRow{"&#128274;", "Two-factor authentication", secSub, secChip, securityCard})
	}
	rows = append(rows, accRow{"&#128100;", "Sign-in details", m.Email, "",
		`<section class="ma-card">
    <div class="ma-row"><span>Email</span><span>` + esc(m.Email) + `</span></div>
    <div class="ma-row"><span>Member since</span><span>` + esc(since) + `</span></div>
  </section>`})
	// Titles are escaped by maAcc, so this passes a literal ampersand — pre-escaping
	// it here would render as "&amp;".
	rows = append(rows, accRow{"&#9881;", "Name & notifications", "How you appear and what we email you", "",
		memberDetailsCard(m, replyChecked, newsletterChecked)})
	// Receipts and comments are APPENDED, never prepended. The open row is chosen
	// positionally (i == 0), so putting either of these first would silently steal
	// the open state from the VayuMail row — which is the thing members come here
	// to do. Both render nothing at all for a member with no orders / no comments,
	// so neither adds an empty row that invites a pointless click.
	if body, sub, chip := a.memberReceiptsSection(r.Context(), m); body != "" {
		rows = append(rows, accRow{"&#129534;", "Receipts", sub, chip, body})
	}
	if body, sub, chip := a.memberCommentsSection(r.Context(), m); body != "" {
		rows = append(rows, accRow{"&#128172;", "Your comments", sub, chip, body})
	}

	accordions := maSectionHead("Your account", "Tap a row to open it")
	for i, row := range rows {
		accordions += maAcc(row.icon, row.title, row.sub, row.chip, row.body, i == 0)
	}
	if compareCard != "" {
		accordions += maSectionHead("Upgrade", "What Premium adds") +
			maAcc("&#10024;", "Compare plans", "Free vs Premium", "", compareCard, false)
	}

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Your account · ` + brand + `</title>
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css?v=` + assetVer("css/signup.css") + `">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">
<main class="ma-shell" id="main-content">
  <div class="ma-head">
    <div>
      <h1 class="ma-title">Hello, ` + esc(m.DisplayName()) + `</h1>
      <p class="ma-subtitle">Manage your membership, mail and security.</p>
    </div>
    <form method="POST" action="/members/logout"><button class="ma-signout" type="submit">Sign out</button></form>
  </div>
  ` + notice + `
  ` + planCard + `
  ` + a.memberBillingCard(r.Context(), m) + `
  ` + accordions + `
</main>` + scriptTag + `
</body></html>`
	_, _ = w.Write([]byte(page))
}

// memberDetailsCard is the "Your details" form: display name plus the two
// notification preferences.
func memberDetailsCard(m *members.Member, replyChecked, newsletterChecked string) string {
	esc := html.EscapeString
	return `<section class="ma-card">
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
  </section>`
}

// renderMemberSecurityCard renders the member's mailbox 2FA card. When 2FA is on
// it offers a code-gated disable; when off it offers a scannable-QR enrolment
// that mirrors the operator flow. All the dynamic values (QR, key, URI) are
// filled client-side from the JSON the /api/v1/members/totp/* endpoints return.
func renderMemberSecurityCard(enabled bool) string {
	if enabled {
		return `<section class="ma-card ma-2fa" data-totp-card>
    <h2>🔐 Two-factor authentication <span class="ma-badge ma-badge--ok">On</span></h2>
    <p class="ma-hint">Your VayuMail sign-in asks for a 6-digit code from your authenticator app. Lost your device? Ask the site owner to reset it.</p>
    <div class="ma-2fa-off">
      <label class="ma-field"><span>Enter your current code to turn 2FA off</span>
        <input class="ma-input" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000" data-totp-code></label>
      <button class="ma-cta-danger" type="button" data-totp-disable>Turn off 2FA</button>
    </div>
  </section>`
	}
	return `<section class="ma-card ma-2fa" data-totp-card>
    <h2>🔐 Two-factor authentication <span class="ma-badge ma-badge--warn">Off</span></h2>
    <p class="ma-hint">Add a one-time code from an authenticator app (Google Authenticator, Aegis, 1Password…) to your VayuMail sign-in. It takes about 30 seconds.</p>
    <button class="ma-cta-primary" type="button" data-totp-begin>Set up 2FA</button>
    <div class="ma-2fa-enroll" data-totp-enroll hidden>
      <div class="ma-2fa-grid">
        <div class="ma-2fa-qrbox">
          <img data-totp-qr alt="2FA setup QR code" width="188" height="188" class="ma-2fa-qr">
        </div>
        <div class="ma-2fa-steps">
          <p><strong>1.</strong> Scan this QR in your authenticator app — your address fills in automatically.</p>
          <p class="ma-2fa-manual"><strong>Can't scan?</strong> Add this key by hand:<br><code data-totp-key class="ma-2fa-key"></code></p>
          <p><a data-totp-uri href="#" rel="noopener" class="su-link">Open in authenticator app ↗</a></p>
        </div>
      </div>
      <label class="ma-field"><span><strong>2.</strong> Enter the 6-digit code it shows</span>
        <input class="ma-input" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000" data-totp-code></label>
      <p class="ma-claim-msg" data-totp-msg role="status"></p>
      <button class="ma-cta-primary" type="button" data-totp-verify>Verify &amp; turn on</button>
    </div>
  </section>`
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
	// This page's content now depends on the member session, so it MUST declare
	// that: without Vary, a shared cache could store one member's personalised
	// page and serve it to everyone. The anonymous version stays cacheable (it is
	// the version search engines index and the one most visitors get); the
	// signed-in version is marked private and unstorable further down, once we
	// know who is reading.
	w.Header().Set("Vary", "Cookie")

	brand := html.EscapeString(config.Cfg.Domain)
	esc := html.EscapeString
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
	_, _ = w.Write([]byte(page))
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
