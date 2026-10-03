// SPDX-License-Identifier: Apache-2.0

package main

// handlers_mail_recovery_pages.go — the public recovery pages.
//
// These are built on the console sign-in's own shell (authPageShell) so recovery
// does not look like a different, less trustworthy site than the one the holder
// signed in to. A
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

// renderRecoveryPage wraps a card body in the console sign-in's own shell. Recovery pages are
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	// A reset link is a bearer credential in the URL, so it must not travel in a
	// Referer header to any third party the page happens to reach.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	_, _ = w.Write([]byte(authPageShell("Mailbox recovery · "+brand, `
  <div class="login-brandline">`+saMark()+`<span>VayuPress</span></div>
  <div class="login-card">
`+card+`
  </div>
  <div class="login-footer">`+saEdition()+`<span>Recovery links expire after 30 minutes and can be used once.</span></div>`)))
}

// recoveryNotice renders an inline message. Errors are shown, never swallowed —
// but the CONTENT of each message is chosen at the call site so it cannot leak
// whether an account exists.
func recoveryNotice(msg string) string {
	if msg == "" {
		return ""
	}
	return `<p class="login-error" role="alert">` + html.EscapeString(msg) + `</p>`
}

// recoveryFormPage asks for the address to send a link to.
func recoveryFormPage(errMsg string) string {
	return `<h1 class="login-title">Recover your mailbox</h1>
<p class="login-sub">Enter your mail address and we will send a reset link to the recovery address on file for it.</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover" novalidate>
  <div class="field">
    <label class="field-label" for="rc-email">Your mail address</label>
    <input class="input" id="rc-email" type="email" name="email" required autocomplete="username"
      placeholder="you@` + html.EscapeString(config.Cfg.Domain) + `" aria-label="Your mail address">
  </div>
  <button class="btn btn--primary login-submit" type="submit">Send a reset link</button>
</form>
<p class="login-recover">Have a recovery code instead? <a href="/mail/recover/code">Use a recovery code</a></p>
<p class="login-recover">Neither? <a href="/mail/recover/ask">Ask your administrator</a></p>
<p class="login-recover"><a href="/os/login">Back to sign in</a></p>`
}

// recoverySentPage is shown for EVERY accepted request — existing mailbox or
// not, rate-limited or not. It is the enumeration guarantee made visible: there
// is one page, so there is nothing to compare.
func recoverySentPage() string {
	return `<h1 class="login-title">Check your recovery address</h1>
<p class="login-sub">` + recoverySameAnswer + `</p>
<p class="login-recover">Nothing arrived? The mailbox may have no recovery address on file. ` +
		`<a href="/mail/recover/code">Use a recovery code</a>, or ask your administrator.</p>
<p class="login-recover"><a href="/os/login">Back to sign in</a></p>`
}

// recoveryPasswordFormPage collects the new password for a link-based reset.
func recoveryPasswordFormPage(token, errMsg string) string {
	return `<h1 class="login-title">Choose a new password</h1>
<p class="login-sub">This link can be used once. Pick something you have not used elsewhere.</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover/reset" novalidate>
  <input type="hidden" name="token" value="` + html.EscapeString(token) + `">
  <div class="field">
    <label class="field-label" for="rc-pass">New password</label>
    <input class="input" id="rc-pass" type="password" name="password" required minlength="8"
      autocomplete="new-password" aria-label="New password">
  </div>
  <div class="field">
    <label class="field-label" for="rc-confirm">Confirm password</label>
    <input class="input" id="rc-confirm" type="password" name="confirm" required minlength="8"
      autocomplete="new-password" aria-label="Confirm password">
  </div>
  <button class="btn btn--primary login-submit" type="submit">Set my new password</button>
</form>`
}

// recoveryCodeFormPage collects address, code and new password in one step. One
// step is deliberate: a two-step flow would have to confirm the code before
// asking for a password, and that confirmation is exactly the oracle that tells
// an attacker which addresses and codes are real.
func recoveryCodeFormPage(addr, errMsg string) string {
	return `<h1 class="login-title">Use a recovery code</h1>
<p class="login-sub">Enter one of the codes you saved when you set up recovery. Each code works once.</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover/code" novalidate>
  <div class="field">
    <label class="field-label" for="rc-email">Your mail address</label>
    <input class="input" id="rc-email" type="email" name="email" required autocomplete="username"
      value="` + html.EscapeString(addr) + `" aria-label="Your mail address">
  </div>
  <div class="field">
    <label class="field-label" for="rc-code">Recovery code</label>
    <input class="input" id="rc-code" type="text" name="code" required autocomplete="one-time-code"
      placeholder="XXXX-XXXX-XXXX" spellcheck="false" aria-label="Recovery code">
  </div>
  <div class="field">
    <label class="field-label" for="rc-pass">New password</label>
    <input class="input" id="rc-pass" type="password" name="password" required minlength="8"
      autocomplete="new-password" aria-label="New password">
  </div>
  <div class="field">
    <label class="field-label" for="rc-confirm">Confirm password</label>
    <input class="input" id="rc-confirm" type="password" name="confirm" required minlength="8"
      autocomplete="new-password" aria-label="Confirm password">
  </div>
  <button class="btn btn--primary login-submit" type="submit">Set my new password</button>
</form>
<p class="login-recover"><a href="/mail/recover">Send a reset link instead</a> · ` +
		`<a href="/mail/recover/ask">Ask your administrator</a></p>`
}

// recoveryDonePage reports exactly what the reset destroyed.
//
// Listing it is not decoration. The holder needs to know their other devices
// were signed out (so they are not surprised), and if the numbers are higher
// than they expect — devices they do not recognise — that is the signal that
// someone else was in the account.
func recoveryDonePage(addr string, out mailResetOutcome) string {
	var b strings.Builder
	b.WriteString(`<h1 class="login-title">Your password is set</h1>`)
	b.WriteString(`<p class="login-sub">You can sign in to <strong>` + html.EscapeString(addr) +
		`</strong> now. For your security, everything else was disconnected:</p><ul class="login-list">`)
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
		b.WriteString(`<p class="login-recover">A confirmation went to your recovery address.</p>`)
	}
	if len(out.Problems) > 0 {
		// Never hide a partial failure behind a success page: the holder must know
		// if something that should have been disconnected might not have been.
		b.WriteString(`<p class="login-error" role="alert">Some cleanup steps did not complete. ` +
			`Tell your administrator, and check your connected devices.</p>`)
	}
	b.WriteString(`<p class="login-recover"><a href="/os/login">Go to sign in</a></p>`)
	return b.String()
}

// recoveryNoticePage is a bare message card.
func recoveryNoticePage(title, msg string) string {
	return `<h1 class="login-title">` + html.EscapeString(title) + `</h1>
<p class="login-sub">` + html.EscapeString(msg) + `</p>
<p class="login-recover"><a href="/mail/recover">Start again</a> · ` +
		`<a href="/os/login">Back to sign in</a></p>`
}

// recoveryAskFormPage asks an administrator for help.
func recoveryAskFormPage(addr, errMsg string) string {
	return `<h1 class="login-title">Ask your administrator</h1>
<p class="login-sub">If you have no recovery codes and no recovery address, an administrator has to help you
back in. This tells them you are locked out.</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover/ask" novalidate>
  <div class="field">
    <label class="field-label" for="rc-email">Your mail address</label>
    <input class="input" id="rc-email" type="email" name="email" required autocomplete="username"
      value="` + html.EscapeString(addr) + `" aria-label="Your mail address">
  </div>
  <div class="field">
    <label class="field-label" for="rc-note">Anything that helps them recognise you (optional)</label>
    <input class="input" id="rc-note" type="text" name="note" maxlength="500"
      placeholder="e.g. which team you are on" aria-label="Note for the administrator">
  </div>
  <button class="btn btn--primary login-submit" type="submit">Send the request</button>
</form>
<p class="login-recover"><a href="/mail/recover">Send a reset link instead</a> · ` +
		`<a href="/mail/recover/code">Use a recovery code</a></p>`
}

// recoveryAskedPage is shown for every accepted ask — real address or not.
func recoveryAskedPage() string {
	return `<h1 class="login-title">Your administrator has been told</h1>
<p class="login-sub">If that mailbox exists, whoever runs this server can now see that you are locked out.
They will contact you directly — nothing is sent to the mailbox you cannot open.</p>
<p class="login-recover">Once you are back in, set up recovery codes so you never need this again.</p>
<p class="login-recover"><a href="/os/login">Back to sign in</a></p>`
}
