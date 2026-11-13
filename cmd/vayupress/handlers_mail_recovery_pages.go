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
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
)

// renderRecoveryPage wraps a card body in the console sign-in's own shell, in
// lang, with the menu that changes it. Recovery pages are per-request and must
// never be cached: a reset form sitting in a shared cache with a token in it is
// a credential left on a shelf.
func (a *App) renderRecoveryPage(w http.ResponseWriter, r *http.Request, lang, card string) {
	brand := render.GetActiveSettings().Name
	if brand == "" {
		brand = config.Cfg.Domain
	}
	if brand == "" {
		brand = "VayuMail"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", lang)
	w.Header().Add("Vary", "Accept-Language, Cookie")
	w.Header().Set("Cache-Control", "private, no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	// A reset link is a bearer credential in the URL, so it must not travel in a
	// Referer header to any third party the page happens to reach.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")

	_, _ = w.Write([]byte(authPageShellIn(lang, i18n.T(lang, "Mailbox recovery · {brand}", "brand", brand), `
  <div class="login-brandline">`+saMark()+`<span>VayuPress</span></div>
  <div class="login-card">
`+card+`
  </div>
  <div class="login-footer">`+saEdition()+`<span>`+esc(i18n.T(lang, "Recovery links expire after 30 minutes and can be used once."))+`</span></div>`+
		languageMenu(lang, r))))
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

// recoveryField is one labelled input of a recovery form.
func recoveryField(id, label, input string) string {
	return `<div class="field"><label class="field-label" for="` + id + `">` + esc(label) + `</label>` + input + `</div>`
}

// recoveryAddressField asks for the mailbox's address, filled with addr.
func recoveryAddressField(lang, addr string) string {
	label := i18n.T(lang, "Your mail address")
	return recoveryField("rc-email", label, `<input class="input" id="rc-email" type="email" name="email" required autocomplete="username" value="`+
		esc(addr)+`" placeholder="you@`+esc(config.Cfg.Domain)+`" aria-label="`+esc(label)+`">`)
}

// recoveryPasswordFields asks for the new password, twice.
func recoveryPasswordFields(lang string) string {
	pass, confirm := i18n.T(lang, "New password"), i18n.T(lang, "Confirm password")
	return recoveryField("rc-pass", pass, `<input class="input" id="rc-pass" type="password" name="password" required minlength="8" autocomplete="new-password" aria-label="`+esc(pass)+`">`) +
		recoveryField("rc-confirm", confirm, `<input class="input" id="rc-confirm" type="password" name="confirm" required minlength="8" autocomplete="new-password" aria-label="`+esc(confirm)+`">`)
}

// recoveryLink is a link of the recovery pages, its words in lang.
func recoveryLink(href, words string) string {
	return `<a href="` + href + `">` + esc(words) + `</a>`
}

// recoveryFormPage asks for the address to send a link to.
func recoveryFormPage(lang, errMsg string) string {
	return `<h1 class="login-title">` + esc(i18n.T(lang, "Recover your mailbox")) + `</h1>
<p class="login-sub">` + esc(i18n.T(lang, "Enter your mail address and we will send a reset link to the recovery address on file for it.")) + `</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover" novalidate>` + recoveryAddressField(lang, "") + `
  <button class="btn btn--primary login-submit" type="submit">` + esc(i18n.T(lang, "Send a reset link")) + `</button>
</form>
<p class="login-recover">` + esc(i18n.T(lang, "Have a recovery code instead?")) + ` ` + recoveryLink("/mail/recover/code", i18n.T(lang, "Use a recovery code")) + `</p>
<p class="login-recover">` + esc(i18n.T(lang, "Neither?")) + ` ` + recoveryLink("/mail/recover/ask", i18n.T(lang, "Ask your administrator")) + `</p>
<p class="login-recover">` + recoveryLink("/os/login", i18n.T(lang, "Back to sign in")) + `</p>`
}

// recoverySentPage is shown for EVERY accepted request — existing mailbox or
// not, rate-limited or not. It is the enumeration guarantee made visible: there
// is one page, so there is nothing to compare.
func recoverySentPage(lang string) string {
	return `<h1 class="login-title">` + esc(i18n.T(lang, "Check your recovery address")) + `</h1>
<p class="login-sub">` + esc(recoverySameAnswer(lang)) + `</p>
<p class="login-recover">` + esc(i18n.T(lang, "Nothing arrived? The mailbox may have no recovery address on file.")) + ` ` +
		recoveryLink("/mail/recover/code", i18n.T(lang, "Use a recovery code")) + `</p>
<p class="login-recover">` + recoveryLink("/os/login", i18n.T(lang, "Back to sign in")) + `</p>`
}

// recoveryPasswordFormPage collects the new password for a link-based reset.
func recoveryPasswordFormPage(lang, token, errMsg string) string {
	return `<h1 class="login-title">` + esc(i18n.T(lang, "Choose a new password")) + `</h1>
<p class="login-sub">` + esc(i18n.T(lang, "This link can be used once. Pick something you have not used elsewhere.")) + `</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover/reset" novalidate>
  <input type="hidden" name="token" value="` + esc(token) + `">` + recoveryPasswordFields(lang) + `
  <button class="btn btn--primary login-submit" type="submit">` + esc(i18n.T(lang, "Set my new password")) + `</button>
</form>`
}

// recoveryCodeFormPage collects address, code and new password in one step. One
// step is deliberate: a two-step flow would have to confirm the code before
// asking for a password, and that confirmation is exactly the oracle that tells
// an attacker which addresses and codes are real.
func recoveryCodeFormPage(lang, addr, errMsg string) string {
	code := i18n.T(lang, "Recovery code")
	return `<h1 class="login-title">` + esc(i18n.T(lang, "Use a recovery code")) + `</h1>
<p class="login-sub">` + esc(i18n.T(lang, "Enter one of the codes you saved when you set up recovery. Each code works once.")) + `</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover/code" novalidate>` + recoveryAddressField(lang, addr) +
		recoveryField("rc-code", code, `<input class="input" id="rc-code" type="text" name="code" required autocomplete="one-time-code" placeholder="XXXX-XXXX-XXXX" spellcheck="false" aria-label="`+esc(code)+`">`) +
		recoveryPasswordFields(lang) + `
  <button class="btn btn--primary login-submit" type="submit">` + esc(i18n.T(lang, "Set my new password")) + `</button>
</form>
<p class="login-recover">` + recoveryLink("/mail/recover", i18n.T(lang, "Send a reset link instead")) + ` · ` +
		recoveryLink("/mail/recover/ask", i18n.T(lang, "Ask your administrator")) + `</p>`
}

// recoveryDonePage reports exactly what the reset destroyed.
//
// Listing it is not decoration. The holder needs to know their other devices
// were signed out (so they are not surprised), and if the numbers are higher
// than they expect — devices they do not recognise — that is the signal that
// someone else was in the account.
func recoveryDonePage(lang, addr string, out mailResetOutcome) string {
	var b strings.Builder
	b.WriteString(`<h1 class="login-title">` + esc(i18n.T(lang, "Your password is set")) + `</h1>`)
	b.WriteString(`<p class="login-sub">` + esc(i18n.T(lang, "You can sign in to {address} now. For your security, everything else was disconnected:", "address", addr)) + `</p><ul class="login-list">`)
	b.WriteString(`<li>` + esc(i18n.T(lang, "{n, plural, one {# connected app signed out} other {# connected apps signed out}}: set your mail app up again", "n", out.AppPasswordsRevoked)) + `</li>`)
	b.WriteString(`<li>` + esc(i18n.T(lang, "{n, plural, one {# web session ended} other {# web sessions ended}}", "n", out.SessionsRevoked)) + `</li>`)
	if out.QueueHeld > 0 {
		b.WriteString(`<li>` + esc(i18n.T(lang, "{n, plural, one {# unsent message held for review} other {# unsent messages held for review}}", "n", out.QueueHeld)) + `</li>`)
	}
	b.WriteString(`</ul>`)
	if out.NotifiedContact != "" {
		b.WriteString(`<p class="login-recover">` + esc(i18n.T(lang, "A confirmation went to your recovery address.")) + `</p>`)
	}
	if len(out.Problems) > 0 {
		// Never hide a partial failure behind a success page: the holder must know
		// if something that should have been disconnected might not have been.
		b.WriteString(`<p class="login-error" role="alert">` + esc(i18n.T(lang, "Some cleanup steps did not complete. Tell your administrator, and check your connected devices.")) + `</p>`)
	}
	b.WriteString(`<p class="login-recover">` + recoveryLink("/os/login", i18n.T(lang, "Go to sign in")) + `</p>`)
	return b.String()
}

// recoveryNoticePage is a bare message card: title and message already in
// the page's language.
func recoveryNoticePage(lang, title, msg string) string {
	return `<h1 class="login-title">` + esc(title) + `</h1>
<p class="login-sub">` + esc(msg) + `</p>
<p class="login-recover">` + recoveryLink("/mail/recover", i18n.T(lang, "Start again")) + ` · ` +
		recoveryLink("/os/login", i18n.T(lang, "Back to sign in")) + `</p>`
}

// recoveryAskFormPage asks an administrator for help.
func recoveryAskFormPage(lang, addr, errMsg string) string {
	note := i18n.T(lang, "Anything that helps them recognise you (optional)")
	return `<h1 class="login-title">` + esc(i18n.T(lang, "Ask your administrator")) + `</h1>
<p class="login-sub">` + esc(i18n.T(lang, "If you have no recovery codes and no recovery address, an administrator has to help you back in. This tells them you are locked out.")) + `</p>
` + recoveryNotice(errMsg) + `
<form class="login-form" method="POST" action="/mail/recover/ask" novalidate>` + recoveryAddressField(lang, addr) +
		recoveryField("rc-note", note, `<input class="input" id="rc-note" type="text" name="note" maxlength="500" placeholder="`+
			esc(i18n.T(lang, "For example, which team you are on"))+`" aria-label="`+esc(note)+`">`) + `
  <button class="btn btn--primary login-submit" type="submit">` + esc(i18n.T(lang, "Send the request")) + `</button>
</form>
<p class="login-recover">` + recoveryLink("/mail/recover", i18n.T(lang, "Send a reset link instead")) + ` · ` +
		recoveryLink("/mail/recover/code", i18n.T(lang, "Use a recovery code")) + `</p>`
}

// recoveryAskedPage is shown for every accepted ask — real address or not.
func recoveryAskedPage(lang string) string {
	return `<h1 class="login-title">` + esc(i18n.T(lang, "Your administrator has been told")) + `</h1>
<p class="login-sub">` + esc(i18n.T(lang, "If that mailbox exists, whoever runs this server can now see that you are locked out. They will contact you directly; nothing is sent to the mailbox you cannot open.")) + `</p>
<p class="login-recover">` + esc(i18n.T(lang, "Once you are back in, set up recovery codes so you never need this again.")) + `</p>
<p class="login-recover">` + recoveryLink("/os/login", i18n.T(lang, "Back to sign in")) + `</p>`
}
