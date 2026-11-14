// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_sealed.go — mail to anyone, sealed (internal/vayuos/mail/
// sealed.go keeps it): the holder's page that seals a message and lists the
// ones still open, the notice the person outside is sent, and the public
// page where they open it with the passphrase and answer it.

import (
	"html"
	"net/http"
	"strings"
	"time"

	htmpl "html/template"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// sealedSlots bounds the passphrase derivations under way at once: each is
// 64 MB of Argon2id, and the page that asks for one is public.
var sealedSlots = make(chan struct{}, 4)

// sealedTTLs are how long a sealed message may stay open, in days.
var sealedTTLs = []int{1, 7, 30}

// sealedTTLLabel names a length from sealedTTLs in lang.
func sealedTTLLabel(lang string, days int) string {
	switch days {
	case 1:
		return i18n.T(lang, "A day")
	case 7:
		return i18n.T(lang, "A week")
	}
	return i18n.T(lang, "Thirty days")
}

// sealedBlock is the sealing form and the mailbox's sealed messages still
// open.
func (a *App) sealedBlock(rd vmail.Reader) string {
	lang := rd.Lang()
	post := ` hx-post="/os/vayumail/sealed" hx-target="#vm-sealed" hx-swap="outerHTML"`
	var b strings.Builder
	b.WriteString(`<div id="vm-sealed"><form class="mx-sealform"` + post + `><input type="hidden" name="op" value="new">` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "To")) + `</span><input class="input" type="email" name="to" required autocomplete="off" placeholder="someone@elsewhere.example"></label>` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Subject")) + `</span><input class="input" name="subject" maxlength="200" autocomplete="off"></label>` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Message")) + `</span><textarea class="input" name="body" rows="8" required></textarea></label>` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Passphrase")) + `</span><input class="input" type="password" name="passphrase" minlength="` + itoaSafe(vmail.SealedMinPassphrase) + `" required autocomplete="new-password">` +
		`<span class="field-hint">` + esc(i18n.T(lang, "At least {n} characters. Tell it to them another way: in person or by phone, never in an email.", "n", vmail.SealedMinPassphrase)) + `</span></label>` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "It can be opened for")) + `</span><select class="input" name="days">`)
	for _, days := range sealedTTLs {
		sel := ""
		if days == 7 {
			sel = " selected"
		}
		b.WriteString(`<option value="` + itoaSafe(days) + `"` + sel + `>` + esc(sealedTTLLabel(lang, days)) + `</option>`)
	}
	b.WriteString(`</select></label><button class="btn btn--primary" type="submit">` + esc(i18n.T(lang, "Seal and send the link")) + `</button></form>`)
	if list, _ := a.vayuMail.SealedSent(rd); len(list) > 0 {
		b.WriteString(`<h2 class="mx-set__title">` + esc(i18n.T(lang, "Still open")) + `</h2><ul class="mx-throwaways">`)
		for _, m := range list {
			until := i18n.Date(lang, config.InSite(m.Expires), i18n.DayMonthAt)
			var meta string
			switch {
			case m.Opened == 0:
				meta = i18n.T(lang, "Not opened yet · until {when}", "when", until)
			case m.Replies == 0:
				meta = i18n.T(lang, "Opened · until {when}", "when", until)
			default:
				meta = i18n.T(lang, "{n, plural, one {Opened · # answer · until {when}} other {Opened · # answers · until {when}}}", "n", m.Replies, "when", until)
			}
			b.WriteString(`<li><span class="mx-throwaways__what"><span class="mx-throwaways__addr">` + esc(m.Recipient) + `</span><span class="mx-throwaways__meta">` + esc(meta) + `</span></span>` +
				`<button type="button" class="btn btn--ghost btn--sm"` + post + hxVals("op", "withdraw", "id", m.ID) + ` hx-confirm="` + esc(i18n.T(lang, "Close this sealed message now? Its link will open nothing.")) + `">` + esc(i18n.T(lang, "Close it now")) + `</button></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// handleVayuOSSealed is the holder's Send a sealed message page.
func (a *App) handleVayuOSSealed(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		a.writeMailSetup(w, r, i18n.T(i18n.Lang(r.Context()), "Send a sealed message"))
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	rd := a.mailReader(r, "")
	lang := rd.Lang()
	title := i18n.T(lang, "Send a sealed message")
	body := `<div class="page-header"><h1>` + esc(title) + `</h1></div>` +
		`<p class="page-sub">` + esc(i18n.T(lang, "For someone with no key of their own. The message is not sent: it stays on this server, sealed with a passphrase you tell them another way. They are sent a link, open the message there with the passphrase, and can answer it there; the answer comes to your Inbox.")) + `</p>`
	if rd.Key() == "" || a.vayuMail.ReaderReadOnly(rd) {
		body += `<div class="empty-state">` + esc(i18n.T(lang, "Only a mailbox that can send can seal a message.")) + `</div>`
	} else {
		body += a.sealedBlock(rd)
	}
	writeOSHTML(w, r, adminOSLayout(nonce, title, "vayumail", cfg, htmpl.HTML(body)))
}

// handleVayuOSSealedAction seals a message and sends its link, or closes one.
func (a *App) handleVayuOSSealedAction(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-off", i18n.T(i18n.Lang(r.Context()), "VayuMail is not active."), "")
		return
	}
	rd := a.mailReader(r, "")
	lang := rd.Lang()
	_, from := a.ownMailbox(r)
	if rd.Key() == "" || from == "" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "This session has no mailbox of its own."), "")
		return
	}
	switch r.PostFormValue("op") {
	case "new":
		days := 7
		for _, d := range sealedTTLs {
			if r.PostFormValue("days") == itoaSafe(d) {
				days = d
			}
		}
		name := a.senderDisplayName(r.Context(), from)
		if name == "" {
			name = from
		}
		to := strings.TrimSpace(r.PostFormValue("to"))
		id, err := a.vayuMail.Seal(rd, name, to, r.PostFormValue("subject"), r.PostFormValue("body"), r.PostFormValue("passphrase"), time.Duration(days)*24*time.Hour)
		if err != nil {
			setMailSaid(w, i18n.T(lang, "Not sealed: {reason}.", "reason", strings.TrimPrefix(err.Error(), "vayumail: ")), true)
			break
		}
		// The notice is in the sender's language: they wrote the message
		// in it, and nothing is known of the recipient's.
		link := "https://" + config.Cfg.Domain + "/sealed/" + id
		until := i18n.Date(lang, config.InSite(time.Now().Add(time.Duration(days)*24*time.Hour)), i18n.DayMonthYearAt)
		notice := i18n.T(lang, "{name} ({address}) sent you a sealed message. Open it here, with the passphrase they gave you:", "name", name, "address", from) +
			"\n\n" + link + "\n\n" +
			i18n.T(lang, "It can be opened until {when}. The passphrase is not in this email; if you do not have it, ask them for it another way.", "when", until) + "\n"
		if _, err := a.vayuMail.Compose(r.Context(), from, []string{to}, i18n.T(lang, "A sealed message from {name}", "name", name), notice, ""); err != nil {
			_ = a.vayuMail.WithdrawSealed(rd, id)
			setMailSaid(w, i18n.T(lang, "Not sent: the link could not be mailed, so the message was not kept."), true)
			break
		}
		setMailSaid(w, i18n.T(lang, "Sealed. The link is on its way to {address}.", "address", to), false)
	case "withdraw":
		if err := a.vayuMail.WithdrawSealed(rd, r.PostFormValue("id")); err != nil {
			setMailSaid(w, i18n.T(lang, "That sealed message is not one of yours, or is already closed."), true)
		}
	}
	writeOSFragment(w, a.sealedBlock(rd))
}

// handleSealedPage is the public page a sealed message is opened on: the
// passphrase, then the message and a box to answer it. Every refusal reads
// the same, and wrong passphrases count against the address as wrong
// passwords do.
func (a *App) handleSealedPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	id, lang := chi.URLParam(r, "id"), i18n.Lang(r.Context())
	title := i18n.T(lang, "A sealed message")
	page := func(inner string) {
		_, _ = w.Write([]byte(authPageShell(title, `<div class="login-card">`+inner+`</div>`)))
	}
	ask := func(said string) string {
		if said != "" {
			said = `<div class="login-error" role="alert">` + html.EscapeString(said) + `</div>`
		}
		return `<h1 class="login-title">` + html.EscapeString(title) + `</h1><p class="login-sub">` + html.EscapeString(i18n.T(lang, "Its sender gave you a passphrase to open it.")) + `</p>` + said +
			`<form class="login-form" method="POST"><input type="hidden" name="op" value="open"><div class="field"><label class="field-label" for="sealed-pass">` + html.EscapeString(i18n.T(lang, "Passphrase")) + `</label>` +
			`<input id="sealed-pass" class="input" type="password" name="passphrase" required autofocus autocomplete="off"></div><button class="btn btn--primary" type="submit">` + html.EscapeString(i18n.T(lang, "Open")) + `</button></form>`
	}
	if r.Method != http.MethodPost || !a.mailRunning() {
		page(ask(""))
		return
	}
	ip := loginClientIP(r)
	if locked, _ := auth.CheckAuthLockout(ip); locked {
		page(ask(i18n.T(lang, "Too many wrong passphrases from here. Try again later.")))
		return
	}
	select {
	case sealedSlots <- struct{}{}:
		defer func() { <-sealedSlots }()
	case <-time.After(3 * time.Second):
		w.WriteHeader(http.StatusServiceUnavailable)
		page(ask(i18n.T(lang, "Busy. Try again in a moment.")))
		return
	}
	pass := r.PostFormValue("passphrase")
	if r.PostFormValue("op") == "reply" {
		if err := a.vayuMail.ReplySealed(id, pass, r.PostFormValue("text")); err != nil {
			if err == vmail.ErrSealedGone {
				auth.RecordAuthFailure(ip)
			}
			page(ask(strings.TrimPrefix(err.Error(), "vayumail: ")))
			return
		}
		page(`<h1 class="login-title">` + html.EscapeString(i18n.T(lang, "Your answer is on its way")) + `</h1><p class="login-sub">` + html.EscapeString(i18n.T(lang, "It is in its sender's Inbox now.")) + `</p>`)
		return
	}
	m, err := a.vayuMail.OpenSealed(id, pass)
	if err != nil {
		auth.RecordAuthFailure(ip)
		page(ask(strings.TrimPrefix(err.Error(), "vayumail: ")))
		return
	}
	page(`<h1 class="login-title">` + html.EscapeString(m.Subject) + `</h1><p class="login-sub">` +
		html.EscapeString(i18n.T(lang, "From {name}, open until {when}", "name", m.From, "when", i18n.Date(lang, config.InSite(m.Expires), i18n.DayMonthAt))) + `</p><div class="mx-sealed__body">` + html.EscapeString(m.Body) + `</div>` +
		`<form class="login-form" method="POST"><input type="hidden" name="op" value="reply"><input type="hidden" name="passphrase" value="` + html.EscapeString(pass) + `">` +
		`<div class="field"><label class="field-label" for="sealed-reply">` + html.EscapeString(i18n.T(lang, "Answer")) + `</label><textarea id="sealed-reply" class="input" name="text" rows="6" required></textarea></div>` +
		`<button class="btn btn--primary" type="submit">` + html.EscapeString(i18n.T(lang, "Send the answer")) + `</button></form>`)
}
