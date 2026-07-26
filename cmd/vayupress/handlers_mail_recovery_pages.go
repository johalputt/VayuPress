package main

// handlers_mail_recovery_pages.go — the public recovery pages.
//
// These reuse the sign-in shell (signup.css) so recovery does not look like a
// different, less trustworthy site than the one the holder signed in to. A
// password-reset page that looks unfamiliar is a page people abandon — or worse,
// one a phishing clone becomes indistinguishable from.

import (
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
)

// renderRecoveryPage wraps a card body in the shell. Recovery pages are
// per-request and must never be cached: a reset form sitting in a shared cache
// with a token in it is a credential left on a shelf.
func (a *App) renderRecoveryPage(w http.ResponseWriter, r *http.Request, card string) {
	brand := render.GetActiveSettings().Name
	if brand == "" {
		brand = config.Cfg.Domain
	}
	if brand == "" {
		brand = "VayuMail"
	}
	esc := html.EscapeString(brand)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	// A reset link is a bearer credential in the URL, so it must not travel in a
	// Referer header to any third party the page happens to reach.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta name="referrer" content="no-referrer">
<title>Mailbox recovery · ` + esc + `</title>
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css?v=` + assetVer("css/signup.css") + `">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="su-body">
<main class="su-shell" id="main-content">
  <section class="su-card">
    <div class="su-brand">
      <img class="su-logo" src="/static/favicon-light.png" alt="" width="40" height="40">
      <span class="su-brand-name">` + esc + `</span>
    </div>
` + card + `
  </section>
  <p class="su-legal">Powered by VayuPress · recovery links expire after 30 minutes and can be used once.</p>
</main>
</body></html>`))
}

// recoveryNotice renders an inline message. Errors are shown, never swallowed —
// but the CONTENT of each message is chosen at the call site so it cannot leak
// whether an account exists.
func recoveryNotice(msg string) string {
	if msg == "" {
		return ""
	}
	return `<p class="su-note" role="alert">` + html.EscapeString(msg) + `</p>`
}

// recoveryFormPage asks for the address to send a link to.
func recoveryFormPage(errMsg string) string {
	return `<h1 class="su-title">Recover your mailbox</h1>
<p class="su-sub">Enter your mail address and we will send a reset link to the recovery address on file for it.</p>
` + recoveryNotice(errMsg) + `
<form class="su-form" method="POST" action="/mail/recover" novalidate>
  <label class="su-label" for="rc-email">Your mail address</label>
  <input class="su-input" id="rc-email" type="email" name="email" required autocomplete="username"
         placeholder="you@` + html.EscapeString(config.Cfg.Domain) + `" aria-label="Your mail address">
  <button class="su-btn" type="submit">Send a reset link →</button>
</form>
<p class="su-foot">Have a recovery code instead? <a class="su-link" href="/mail/recover/code">Use a recovery code</a></p>
<p class="su-foot"><a class="su-link" href="/os/login">Back to sign in</a></p>`
}

// recoverySentPage is shown for EVERY accepted request — existing mailbox or
// not, rate-limited or not. It is the enumeration guarantee made visible: there
// is one page, so there is nothing to compare.
func recoverySentPage() string {
	return `<h1 class="su-title">Check your recovery address</h1>
<p class="su-sub">` + recoverySameAnswer + `</p>
<p class="su-foot">Nothing arrived? The mailbox may have no recovery address on file. ` +
		`<a class="su-link" href="/mail/recover/code">Use a recovery code</a>, or ask your administrator.</p>
<p class="su-foot"><a class="su-link" href="/os/login">Back to sign in</a></p>`
}

// recoveryPasswordFormPage collects the new password for a link-based reset.
func recoveryPasswordFormPage(token, errMsg string) string {
	return `<h1 class="su-title">Choose a new password</h1>
<p class="su-sub">This link can be used once. Pick something you have not used elsewhere.</p>
` + recoveryNotice(errMsg) + `
<form class="su-form" method="POST" action="/mail/recover/reset" novalidate>
  <input type="hidden" name="token" value="` + html.EscapeString(token) + `">
  <label class="su-label" for="rc-pass">New password</label>
  <input class="su-input" id="rc-pass" type="password" name="password" required minlength="8"
         autocomplete="new-password" aria-label="New password">
  <label class="su-label" for="rc-confirm">Confirm password</label>
  <input class="su-input" id="rc-confirm" type="password" name="confirm" required minlength="8"
         autocomplete="new-password" aria-label="Confirm password">
  <button class="su-btn" type="submit">Set my new password →</button>
</form>`
}

// recoveryCodeFormPage collects address, code and new password in one step. One
// step is deliberate: a two-step flow would have to confirm the code before
// asking for a password, and that confirmation is exactly the oracle that tells
// an attacker which addresses and codes are real.
func recoveryCodeFormPage(addr, errMsg string) string {
	return `<h1 class="su-title">Use a recovery code</h1>
<p class="su-sub">Enter one of the codes you saved when you set up recovery. Each code works once.</p>
` + recoveryNotice(errMsg) + `
<form class="su-form" method="POST" action="/mail/recover/code" novalidate>
  <label class="su-label" for="rc-email">Your mail address</label>
  <input class="su-input" id="rc-email" type="email" name="email" required autocomplete="username"
         value="` + html.EscapeString(addr) + `" aria-label="Your mail address">
  <label class="su-label" for="rc-code">Recovery code</label>
  <input class="su-input" id="rc-code" type="text" name="code" required autocomplete="one-time-code"
         placeholder="XXXX-XXXX-XXXX" spellcheck="false" aria-label="Recovery code">
  <label class="su-label" for="rc-pass">New password</label>
  <input class="su-input" id="rc-pass" type="password" name="password" required minlength="8"
         autocomplete="new-password" aria-label="New password">
  <label class="su-label" for="rc-confirm">Confirm password</label>
  <input class="su-input" id="rc-confirm" type="password" name="confirm" required minlength="8"
         autocomplete="new-password" aria-label="Confirm password">
  <button class="su-btn" type="submit">Set my new password →</button>
</form>
<p class="su-foot"><a class="su-link" href="/mail/recover">Send a reset link instead</a></p>`
}

// recoveryDonePage reports exactly what the reset destroyed.
//
// Listing it is not decoration. The holder needs to know their other devices
// were signed out (so they are not surprised), and if the numbers are higher
// than they expect — devices they do not recognise — that is the signal that
// someone else was in the account.
func recoveryDonePage(addr string, out mailResetOutcome) string {
	var b strings.Builder
	b.WriteString(`<h1 class="su-title">Your password is set</h1>`)
	b.WriteString(`<p class="su-sub">You can sign in to <strong>` + html.EscapeString(addr) +
		`</strong> now. For your security, everything else was disconnected:</p><ul class="su-list">`)
	b.WriteString(`<li>` + strconv.Itoa(out.AppPasswordsRevoked) + ` connected app` +
		plural(out.AppPasswordsRevoked) + ` signed out — set your mail app up again</li>`)
	b.WriteString(`<li>` + strconv.Itoa(out.SessionsRevoked) + ` web session` +
		plural(out.SessionsRevoked) + ` ended</li>`)
	if out.QueueHeld > 0 {
		b.WriteString(`<li>` + strconv.Itoa(out.QueueHeld) + ` unsent message` +
			plural(out.QueueHeld) + ` held for review</li>`)
	}
	b.WriteString(`</ul>`)
	if out.NotifiedContact != "" {
		b.WriteString(`<p class="su-foot">A confirmation went to your recovery address.</p>`)
	}
	if len(out.Problems) > 0 {
		// Never hide a partial failure behind a success page: the holder must know
		// if something that should have been disconnected might not have been.
		b.WriteString(`<p class="su-note" role="alert">Some cleanup steps did not complete. ` +
			`Tell your administrator, and check your connected devices.</p>`)
	}
	b.WriteString(`<p class="su-foot"><a class="su-link" href="/os/login">Go to sign in →</a></p>`)
	return b.String()
}

// recoveryNoticePage is a bare message card.
func recoveryNoticePage(title, msg string) string {
	return `<h1 class="su-title">` + html.EscapeString(title) + `</h1>
<p class="su-sub">` + html.EscapeString(msg) + `</p>
<p class="su-foot"><a class="su-link" href="/mail/recover">Start again</a> · ` +
		`<a class="su-link" href="/os/login">Back to sign in</a></p>`
}
