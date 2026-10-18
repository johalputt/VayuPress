// SPDX-License-Identifier: Apache-2.0

package main

// member_account_page.go — /members/account, the member's own account.
//
// Every setting is one row in one grammar: what it is, what it is now, and at
// most one thing to do about it. Nothing is folded away. The page it replaces
// (3cx) stacked a plan card, a billing card and six accordions, each opening on
// a card with its own emoji heading, so the facts a member came for were behind
// closed rows and the page used three designs at once. Controls that need more
// room (the picture chooser, the name field, two-factor set-up, the receipts and
// comments lists, deleting the account) open in place under their row.
//
// The page works without its script: the rows that would open in place are
// drawn open, and the forms post as forms. The script (memberAccountInlineJS)
// folds them, saves the email switches as they change, and sets the picture.

import (
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/avatar"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
)

// accountFacts is what the account page and the account panel say about one
// member, read once per request.
type accountFacts struct {
	m          *members.Member
	tierName   string
	sub        *members.Subscription
	stripe     bool // the plan renews through Stripe, so its renewal can be changed here
	mailHost   string
	mailbox    string // the member's own mailbox address; "" when they hold none
	entitled   bool   // the plan includes a mailbox not yet claimed
	console    bool   // the mailbox's role reaches the VayuOS console
	twoFA      bool
	usedBytes  int64
	quotaBytes int64
	bought     int // premium addresses bought and not yet set up
}

func (a *App) accountFactsFor(r *http.Request, m *members.Member) accountFacts {
	ctx := r.Context()
	f := accountFacts{m: m, tierName: "Free", mailHost: a.mailHost()}
	if t, err := a.members.GetTier(ctx, m.Tier); err == nil {
		f.tierName = t.Name
	} else if m.IsPaid() {
		f.tierName = "Premium"
	}
	if m.IsPaid() {
		f.sub, _ = a.members.ActiveSubscription(ctx, m.ID)
	}
	if _, on := a.stripeSecretKey(ctx); on && m.StripeCustomer != "" {
		f.stripe = true
	}
	if addr, ok := a.memberOwnMailbox(r); ok {
		f.mailbox = addr
		if a.vayuMailLoginEnabled() {
			_, f.twoFA = a.vayuMail.Accounts().TOTPStatus(ctx, addr)
			_, f.console = mailConsoleAccess(a.vayuMail.Accounts().RoleFor(ctx, addr))
			f.usedBytes, f.quotaBytes = a.vayuMail.MailboxUsage(addr), a.vayuMail.MailboxQuota(addr)
		}
	} else if _, _, ok := a.mailboxEntitlement(ctx, m); ok && f.mailHost != "" {
		f.entitled = true
	}
	if grants, _ := a.members.ClaimablePremiumGrants(ctx, m.Email); len(grants) > 0 {
		f.bought = len(grants)
	}
	return f
}

// renewal is the Plan section's second row: when the plan next changes, and
// what the member can do about it.
type renewal struct {
	Label, Value, Note string
	Action             string // "cancel", "keep", "renew" or ""
}

// renewalFor says when the plan next changes. Only Stripe renews by itself: a
// PayPal or offline order buys one period and is expired on read when it ends
// (members.ActiveSubscription), so offering to "cancel" it would be a button
// that changes nothing.
func renewalFor(sub *members.Subscription, stripe bool, now time.Time) (renewal, bool) {
	if sub == nil || sub.CurrentPeriodEnd == nil {
		return renewal{}, false
	}
	end := config.FormatSite(*sub.CurrentPeriodEnd, "2 January 2006")
	switch {
	case sub.Cadence == members.CadenceComplimentary:
		return renewal{Label: "Renews", Value: end, Note: "Complimentary: nothing is charged"}, true
	case sub.CancelAtPeriodEnd:
		r := renewal{Label: "Ends", Value: end, Note: "You keep your plan until then. Nothing more is charged."}
		if stripe {
			r.Action = "keep"
		}
		return r, true
	case !stripe:
		return renewal{Label: "Paid until", Value: end, Note: "It doesn't renew by itself", Action: "renew"}, true
	case sub.TrialEnd != nil && sub.TrialEnd.After(now):
		return renewal{Label: "Trial ends", Value: config.FormatSite(*sub.TrialEnd, "2 January 2006"),
			Note: "Your plan starts then and renews automatically", Action: "cancel"}, true
	default:
		return renewal{Label: "Next payment", Value: end, Note: "Renews automatically", Action: "cancel"}, true
	}
}

// planPrice is the plan's price in words: "₹199 a month".
func planPrice(sub *members.Subscription) string {
	switch {
	case sub == nil:
		return ""
	case sub.Cadence == members.CadenceComplimentary:
		return "Complimentary"
	case sub.AmountCents <= 0:
		return ""
	case sub.Cadence == members.CadenceYearly:
		return priceLabel(sub.Currency, sub.AmountCents) + " a year"
	default:
		return priceLabel(sub.Currency, sub.AmountCents) + " a month"
	}
}

// pictureLabel names the picture a member has chosen.
func pictureLabel(choice string) string {
	switch {
	case choice == members.AvatarPhoto:
		return "Your photo"
	case strings.HasPrefix(choice, "cartoon:"):
		return "A cartoon"
	default:
		return "Made for you"
	}
}

// boughtLabel counts the bought addresses ("address" takes "es").
func boughtLabel(n int) string {
	if n == 1 {
		return "1 address ready to set up"
	}
	return strconv.Itoa(n) + " addresses ready to set up"
}

// bytesLabel renders a storage size the way a person reads it.
func bytesLabel(n int64) string {
	const mb, gb = 1 << 20, 1 << 30
	switch {
	case n >= gb:
		return strconv.FormatFloat(float64(n)/gb, 'f', 1, 64) + " GB"
	case n >= mb:
		return strconv.FormatInt(n/mb, 10) + " MB"
	default:
		return strconv.FormatInt(n/1024, 10) + " KB"
	}
}

func acctSection(id, title, note string, rows ...string) string {
	return `<section class="acct-sec" id="` + id + `" aria-labelledby="` + id + `-h"><div class="acct-sec__h"><h2 id="` + id + `-h">` +
		html.EscapeString(title) + `</h2><p class="acct-sec__note" data-acct-note>` + html.EscapeString(note) + `</p></div>` +
		`<ul class="acct-rows">` + strings.Join(rows, "") + `</ul></section>`
}

// acctRow is one setting. value and action are markup; more, when set, is the
// control that opens in place under the row.
func acctRow(id, key, value, action, more string) string {
	idAttr := ""
	if id != "" {
		idAttr = ` id="` + id + `"`
	}
	if more != "" {
		more = `<div class="acct-more" id="` + id + `-more" data-acct-more>` + more + `</div>`
	}
	return `<li class="acct-row"` + idAttr + `><span class="acct-row__k">` + html.EscapeString(key) + `</span><div class="acct-row__v">` +
		value + `</div><div class="acct-row__a">` + action + `</div>` + more + `</li>`
}

// acctValue is a row's value with its line of explanation.
func acctValue(v, note string) string {
	if note == "" {
		return html.EscapeString(v)
	}
	return html.EscapeString(v) + `<small>` + html.EscapeString(note) + `</small>`
}

// acctOpen is the button that opens a row's control in place.
func acctOpen(row, label, cls string) string {
	return `<button type="button" class="acct-btn` + cls + `" data-acct-open="` + row + `-more" aria-controls="` + row + `-more" aria-expanded="false">` +
		html.EscapeString(label) + `</button>`
}

func acctLink(href, label, cls string) string {
	return `<a class="acct-btn` + cls + `" href="` + html.EscapeString(href) + `">` + html.EscapeString(label) + `</a>`
}

// acctState is a state word with its dot: on, off or a warning.
func acctState(label, tone string) string {
	return `<span class="acct-state acct-state--` + tone + `">` + html.EscapeString(label) + `</span>`
}

// accountNotice is the one line that answers what the member just did.
func accountNotice(r *http.Request, f accountFacts) string {
	q := r.URL.Query()
	ok := func(s string) string { return `<p class="acct-notice" role="status">` + html.EscapeString(s) + `</p>` }
	bad := func(s string) string {
		return `<p class="acct-notice acct-notice--err" role="alert">` + html.EscapeString(s) + `</p>`
	}
	switch {
	case q.Get("saved") == "1":
		return ok("Saved.")
	case q.Get("saved") == "picture":
		return ok("Your picture is saved. It is the one on your comments and on mail you send.")
	case q.Get("mail") == "claimed":
		return ok("Your address is ready. Sign in with it here, in the VayuMail app or in any mail app.")
	case q.Get("twofa") == "on":
		return ok("Two-factor is on. Your mailbox will ask for a code from your app when you sign in.")
	case q.Get("twofa") == "off":
		return ok("Two-factor is off.")
	case q.Get("renewal") == "off":
		end := ""
		if f.sub != nil && f.sub.CurrentPeriodEnd != nil {
			end = " You keep it until " + config.FormatSite(*f.sub.CurrentPeriodEnd, "2 January 2006") + "."
		}
		return ok("Your plan will not renew." + end)
	case q.Get("renewal") == "on":
		return ok("Your plan renews as before.")
	case q.Get("renewal") == "failed":
		return bad("Your plan could not be changed, and nothing was charged or cancelled. Try again in a minute.")
	case q.Get("delete") == "confirm":
		return bad("Type your email address exactly as it is shown to delete your account.")
	case q.Get("delete") == "failed":
		return bad("Your account was not deleted: the payments for it could not be stopped. Nothing was changed; try again in a minute.")
	}
	return ""
}

func (a *App) handleMemberAccount(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		http.Redirect(w, r, "/members", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Cache-Control", "no-store")
	f := a.accountFactsFor(r, m)
	_, _ = w.Write([]byte(render.HalcyonMemberPage(a.accountPage(r, f), render.MemberSite)))
}

func (a *App) accountPage(r *http.Request, f accountFacts) string {
	ctx := r.Context()
	m := f.m
	brand := html.EscapeString(config.Cfg.Domain)
	nonce := render.CSPNonce(r)
	picURL := "/api/v1/members/avatar/" + m.ID + "?v=" + avatarVer(m.AvatarChoice, m.Gender)
	since := config.FormatSite(m.CreatedAt, "2 January 2006")

	var secs []string
	index := []string{"profile", "Profile", "plan", "Plan"}

	// ── Profile ──
	picker := `<div class="acct-pick" role="group" aria-label="Choose your picture">`
	choice := func(c, label string) string {
		pressed := "false"
		if m.AvatarChoice == c {
			pressed = "true"
		}
		key := c
		if n, ok := cartoonIndex(c); ok {
			key = strconv.Itoa(n)
		} else if c == "" {
			key = "auto"
		}
		return `<button type="button" class="acct-pick__opt" data-acct-avatar="` + c + `" aria-pressed="` + pressed + `" aria-label="` + label +
			`"><img src="/api/v1/members/avatar/preview/` + key + `" alt="" width="52" height="52" loading="lazy"></button>`
	}
	picker += choice("", "Made for you")
	for n := 0; n < avatar.CartoonCount; n++ {
		picker += choice("cartoon:"+strconv.Itoa(n), "Cartoon "+strconv.Itoa(n+1))
	}
	picker += `<label class="acct-pick__opt acct-pick__up" aria-label="Upload a photo"><input type="file" accept="image/png,image/jpeg,image/webp,image/gif" data-acct-upload hidden><span aria-hidden="true">+</span></label></div>` +
		`<p class="acct-more__foot" data-acct-pick-msg role="status">A photo can be up to 100 KB. The same picture is on your comments and on mail you send.</p>`
	nameForm := `<div class="acct-edit"><label class="acct-sr" for="acct-name">Name</label>` +
		`<input class="acct-input" id="acct-name" type="text" name="name" form="acct-form" value="` + esc(m.Name) + `" placeholder="Your name" maxlength="120" autocomplete="name">` +
		`<button class="acct-btn acct-btn--primary" type="submit" form="acct-form">Save</button></div>`
	nameNote := "On your comments"
	if f.mailbox != "" {
		nameNote = "On your comments and on mail you send"
	}
	deleteNote := "Your comments stay on the posts, under the name you gave them."
	if f.stripe {
		deleteNote += " Your payments to this site stop at once."
	}
	if f.mailbox != "" {
		deleteNote += " Your mailbox " + f.mailbox + " is not deleted with your account; the site's owner can remove it."
	}
	deleteForm := `<form class="acct-delete" method="POST" action="/members/account/delete">` +
		`<p>` + html.EscapeString("This signs you out everywhere and removes your account, your plan and your settings. "+deleteNote) + `</p>` +
		`<label class="acct-field" for="acct-del">Type <b>` + esc(m.Email) + `</b> to confirm</label>` +
		`<div class="acct-edit"><input class="acct-input" id="acct-del" type="text" name="confirm" autocomplete="off" spellcheck="false" required>` +
		`<button class="acct-btn acct-btn--danger" type="submit">Delete my account</button></div></form>`
	secs = append(secs, acctSection("profile", "Profile", "How you appear on "+config.Cfg.Domain,
		acctRow("picture", "Picture", `<span class="acct-inl"><img class="acct-av" src="`+picURL+`" alt="" width="36" height="36"><span>`+
			acctValue(pictureLabel(m.AvatarChoice), "Or a photo of your own")+`</span></span>`, acctOpen("picture", "Change", ""), picker),
		acctRow("name", "Name", acctValue(m.DisplayName(), nameNote), acctOpen("name", "Edit", ""), nameForm),
		acctRow("", "Email", acctValue(m.Email, "You sign in with this address"), "", ""),
		acctRow("leave", "Delete account", acctValue("Remove your account", "This can't be undone"), acctOpen("leave", "Delete…", " acct-btn--quiet"), deleteForm),
	))

	// ── Plan ──
	var planRows []string
	if !m.IsPaid() {
		_, paid := a.freePaidBenefits(ctx)
		var adds []string
		for _, b := range paid {
			if !strings.HasPrefix(strings.ToLower(b), "everything in") && len(adds) < 3 {
				adds = append(adds, b)
			}
		}
		note := ""
		if len(adds) > 0 {
			note = "Premium adds: " + strings.Join(adds, " · ")
		}
		planRows = append(planRows, acctRow("", "Plan", acctValue("Free", note), acctLink("/pricing", "See plans", " acct-btn--primary"), ""))
	} else {
		note := planPrice(f.sub)
		if f.sub == nil {
			note = "Given to you by " + config.Cfg.Domain
		}
		planRows = append(planRows, acctRow("", "Plan", acctValue(f.tierName, note), acctLink("/pricing", "Change plan", ""), ""))
		if rn, ok := renewalFor(f.sub, f.stripe, time.Now()); ok {
			action := ""
			switch rn.Action {
			case "cancel":
				action = `<form method="POST" action="/members/account/renewal"><input type="hidden" name="renew" value="0">` +
					`<button class="acct-btn acct-btn--quiet" type="submit">Cancel renewal</button></form>`
			case "keep":
				action = `<form method="POST" action="/members/account/renewal"><input type="hidden" name="renew" value="1">` +
					`<button class="acct-btn" type="submit">Keep my plan</button></form>`
			case "renew":
				action = acctLink("/pricing", "Renew", "")
			}
			planRows = append(planRows, acctRow("", rn.Label, acctValue(rn.Value, rn.Note), action, ""))
		}
	}
	if body, n, last := a.memberReceiptsSection(ctx, m); body != "" {
		planRows = append(planRows, acctRow("receipts", "Receipts", acctValue(countOf(n, "payment"), last), acctOpen("receipts", "View", ""), body))
	}
	planNote := "Member since " + since
	secs = append(secs, acctSection("plan", "Plan", planNote, planRows...))

	// ── Mail ──
	switch {
	case f.mailbox != "":
		used := bytesLabel(f.usedBytes) + " used"
		if f.quotaBytes > 0 {
			used = bytesLabel(f.usedBytes) + " of " + bytesLabel(f.quotaBytes) + " used"
		}
		open := acctLink("/os/vayumail/inbox", "Open VayuMail", " acct-btn--primary")
		if f.console {
			open = acctLink("/os/", "Open VayuOS", " acct-btn--primary")
		}
		rows := []string{
			acctRow("", "Address", acctValue(f.mailbox, used), open, ""),
			acctRow("", "Mailbox settings", acctValue("Forwarding, vacation reply, filters, signature", ""), acctLink("/os/vayumail/settings", "Open", ""), ""),
		}
		if a.memberMayTalk() {
			rows = append(rows, acctRow("", "VayuTalk", acctValue("Encrypted chat", "With anyone who has an address on "+f.mailHost), acctLink("/os/talk", "Open", ""), ""))
		}
		if f.bought > 0 {
			rows = append(rows, acctRow("", "Bought addresses", acctValue(boughtLabel(f.bought), ""),
				`<button type="button" class="acct-btn" data-acct-portal="mailbox">Set up</button>`, ""))
		}
		secs = append(secs, acctSection("mail", "Mail", "Your own address on "+f.mailHost, rows...))
		index = append(index, "mail", "Mail")
	case f.entitled:
		// Claiming, and buying a name of one's own, run in the account panel's
		// mailbox view: one place for the address checks, the terms and the
		// purchase, rather than a second claim form here.
		secs = append(secs, acctSection("mail", "Mail", "Included with your plan",
			acctRow("", "Address", acctValue("Choose your address", "A private, encrypted mailbox on "+f.mailHost),
				`<button type="button" class="acct-btn acct-btn--primary" data-acct-portal="mailbox">Choose</button>`, ""),
		))
		index = append(index, "mail", "Mail")
	case !m.IsPaid() && f.mailHost != "":
		secs = append(secs, acctSection("mail", "Mail", "Included with Premium",
			acctRow("", "Address", acctValue("you@"+f.mailHost, "A private, encrypted mailbox of your own"), acctLink("/pricing", "See plans", ""), ""),
		))
		index = append(index, "mail", "Mail")
	}

	// ── Security ──
	var secRows []string
	if f.mailbox != "" {
		if f.twoFA {
			secRows = append(secRows, acctRow("twofa", "Two-factor", acctState("On", "ok")+`<small>Your mailbox asks for a code from your app when you sign in</small>`,
				acctOpen("twofa", "Turn off", ""), memberTwoFactorOff()))
		} else {
			secRows = append(secRows, acctRow("twofa", "Two-factor", acctState("Off", "warn")+`<small>Ask for a code from your phone when you sign in to your mailbox</small>`,
				acctOpen("twofa", "Turn on", ""), memberTwoFactorOn()))
		}
	}
	signin := "Or your VayuMail address and password"
	if f.mailbox == "" {
		signin = "No password to remember"
	}
	secRows = append(secRows,
		acctRow("", "Sign-in", acctValue("A link sent to "+m.Email, signin), "", ""),
		acctRow("", "This device", acctValue("Signed in", ""), `<form method="POST" action="/members/logout"><button class="acct-btn" type="submit">Sign out</button></form>`, ""))
	secs = append(secs, acctSection("security", "Security", "Signing in to your account", secRows...))
	index = append(index, "security", "Security")

	// ── Emails ──
	sw := func(name, label string, on bool) string {
		checked := ""
		if on {
			checked = " checked"
		}
		return `<label class="acct-switch"><input type="checkbox" role="switch" name="` + name + `" value="1" form="acct-form"` + checked +
			` data-acct-switch><span class="acct-switch__track" aria-hidden="true"></span><span class="acct-sr">` + label + `</span></label>`
	}
	secs = append(secs, acctSection("emails", "Emails from "+config.Cfg.Domain, "Saved as you change them",
		acctRow("", "Replies", acctValue("When someone replies to your comment", ""), sw("reply_notify", "Email me replies to my comments", m.ReplyNotify), ""),
		acctRow("", "Newsletter", acctValue("New posts and the members’ newsletter", ""), sw("newsletter", "Send me the newsletter", m.NewsletterOptIn), ""),
	)+`<p class="acct-nojs"><button class="acct-btn" type="submit" form="acct-form">Save email choices</button></p>`)
	index = append(index, "emails", "Emails")

	// ── Activity ──
	var actRows []string
	if body, n, live, review := a.memberCommentsSection(ctx, m); body != "" {
		states := acctState(strconv.Itoa(live)+" live", "ok")
		if review > 0 {
			states += " " + acctState(strconv.Itoa(review)+" in review", "off")
		}
		actRows = append(actRows, acctRow("comments", "Comments", html.EscapeString(countOf(n, "comment"))+`<small>`+states+`</small>`, acctOpen("comments", "View", ""), body))
	}
	actRows = append(actRows, acctRow("", "Advertising", acctValue("Put your product in front of this site’s readers", ""),
		`<button type="button" class="acct-btn" data-acct-portal="advertise">Open</button>`, ""))
	secs = append(secs, acctSection("activity", "Activity", "What you have done here", actRows...))
	index = append(index, "activity", "Activity")

	var idx strings.Builder
	for i := 0; i+1 < len(index); i += 2 {
		idx.WriteString(`<a href="#` + index[i] + `">` + index[i+1] + `</a>`)
	}

	pic := `<img class="acct-av" src="` + picURL + `" alt="" width="80" height="80">`
	meta := html.EscapeString(f.tierName) + " member"
	if f.mailbox != "" {
		meta += " · " + html.EscapeString(f.mailbox)
	} else {
		meta += " since " + html.EscapeString(since)
	}

	return `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Your account · ` + brand + `</title>
<script nonce="` + nonce + `">document.documentElement.classList.add('acct-js')</script>
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css?v=` + assetVer("css/signup.css") + `">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
` + string(render.PortalJSLink()) + `
</head>
<body class="su-body acct-body">
<form id="acct-form" method="POST" action="/members/account" hidden></form>
<main class="acct" id="main-content">
<nav class="acct-idx" aria-label="Your account">` + idx.String() +
		`<form method="POST" action="/members/logout"><button class="acct-idx__out" type="submit">Sign out</button></form></nav>
<div class="acct-main">
<header class="acct-who"><div class="acct-who__pic">` + pic +
		`<a class="acct-who__edit" href="#picture" data-acct-open="picture-more" aria-label="Change your picture"><svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg></a></div>
<div><h1 class="acct-who__name">` + esc(m.DisplayName()) + `</h1><p class="acct-who__meta">` + meta + `</p></div></header>
` + accountNotice(r, f) + strings.Join(secs, "") + `
<div class="acct-end"><span>Your account on ` + brand + `</span><form method="POST" action="/members/logout"><button class="acct-btn" type="submit">Sign out</button></form></div>
</div>
</main>` + memberAccountInlineJS(nonce) + `
</body></html>`
}

// memberTwoFactorOn is two-factor set-up, opened under its row: a QR code and
// key filled in from /api/v1/members/totp/begin, then a code to confirm.
func memberTwoFactorOn() string {
	return `<div class="acct-2fa" data-totp-card>
  <p>Use an authenticator app (Google Authenticator, Aegis, 1Password…). It takes about 30 seconds.</p>
  <button class="acct-btn acct-btn--primary" type="button" data-totp-begin>Show my QR code</button>
  <div class="acct-2fa__enrol" data-totp-enroll hidden>
    <img data-totp-qr alt="QR code for your authenticator app" width="168" height="168" class="acct-2fa__qr">
    <div>
      <p>Scan it in your app, or add this key by hand:<br><code data-totp-key></code></p>
      <p><a data-totp-uri href="#" rel="noopener">Open in your authenticator app</a></p>
      <label class="acct-field" for="acct-totp">Then enter the 6-digit code it shows</label>
      <div class="acct-edit"><input class="acct-input" id="acct-totp" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000" data-totp-code>
      <button class="acct-btn acct-btn--primary" type="button" data-totp-verify>Turn on</button></div>
    </div>
  </div>
  <p class="acct-more__foot" data-totp-msg role="status"></p>
</div>`
}

// memberTwoFactorOff turns two-factor off, which takes a current code.
func memberTwoFactorOff() string {
	return `<div class="acct-2fa" data-totp-card>
  <label class="acct-field" for="acct-totp-off">Enter a current code from your app to turn two-factor off</label>
  <div class="acct-edit"><input class="acct-input" id="acct-totp-off" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000" data-totp-code>
  <button class="acct-btn acct-btn--danger" type="button" data-totp-disable>Turn off</button></div>
  <p class="acct-more__foot" data-totp-msg role="status">Lost your phone? Ask the site's owner to reset it.</p>
</div>`
}

// accountSnapshot is the account panel's summary: the facts the account page
// shows, for /api/v1/members/me?detail=1. It is read only when the panel opens,
// so a signed-in reader's page views do not pay for it.
func (a *App) accountSnapshot(r *http.Request, m *members.Member) map[string]interface{} {
	f := a.accountFactsFor(r, m)
	plan := map[string]interface{}{"name": f.tierName, "paid": m.IsPaid()}
	if rn, ok := renewalFor(f.sub, f.stripe, time.Now()); ok {
		plan["when"] = rn.Label + " " + shortDate(rn.Value)
	}
	out := map[string]interface{}{
		"plan":   plan,
		"emails": map[string]bool{"replies": m.ReplyNotify, "newsletter": m.NewsletterOptIn},
	}
	if f.mailbox != "" {
		out["mailbox"] = f.mailbox
		out["twofa"] = f.twoFA
		out["talk"] = a.memberMayTalk()
	} else if f.entitled {
		out["mail_claim"] = true
	}
	if live, review, ok := a.memberCommentCounts(r.Context(), m); ok {
		out["comments"] = map[string]int{"live": live, "review": review}
	}
	return out
}

// memberMayTalk reports whether a mailbox holder can open VayuTalk in the
// browser: it is switched on, and this is not the Tor world, where the chat
// identity is not the holder's own address (mailOnlyPathAllowed).
func (a *App) memberMayTalk() bool {
	return a.vayuTalkEnabled() && !config.Cfg.OnionMode
}

// shortDate turns "2 November 2026" into "2 Nov", for the panel's one line.
func shortDate(long string) string {
	f := strings.Fields(long)
	if len(f) != 3 || len(f[1]) < 3 {
		return long
	}
	return f[0] + " " + f[1][:3]
}
