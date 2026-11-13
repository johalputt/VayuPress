// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_signin.go — a mailbox's own sign-in attempts on its settings
// page, for its holder and for an administrator: what was tried, from where
// and through which door, "It was me" on each source, and the countries the
// mailbox may be signed in from (its fence, internal/signin).

import (
	"context"
	"errors"
	"github.com/johalputt/vayupress/internal/i18n"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/signin"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailboxSigninRows is how many of a mailbox's attempts its card lists.
const mailboxSigninRows = 12

// vayuCardSignins is the mailbox's sign-in card: its attempts and its fence
// when attempts are recorded on this install, and its sign-in name always.
func (a *App) vayuCardSignins(ctx context.Context, ac vmail.Account) string {
	if a.signin == nil {
		return a.signinNameBlock(ctx, ac)
	}
	lang := i18n.Lang(ctx)
	account := auth.AccountKey(ac.Email)
	rows, _ := a.signin.Attempts(ctx, "", account, mailboxSigninRows)
	var b strings.Builder
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">` + esc(i18n.T(lang, "Sign-in attempts")) + `</span>`)
	cutoff := time.Now().Add(-signin.Retention)
	for _, x := range rows {
		if x.Stage == signin.StageCode && x.At.After(cutoff) {
			b.WriteString(saCallout("warn", esc(i18n.T(lang, "Your password was right but the code was wrong on {when}, from {where}. If that was not you, someone knows your password: change it.",
				"when", i18n.Date(lang, config.InSite(x.At), i18n.DayMonthAt), "where", signinWhere(x.Masked, x.Country)))))
			break
		}
	}
	if len(rows) == 0 {
		b.WriteString(`<p class="text-sm muted">` + esc(i18n.T(lang, "No attempt in the last {n} days.", "n", int(signin.Retention.Hours()/24))) + `</p>`)
	} else {
		b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>` + esc(i18n.T(lang, "When")) + `</th><th>` + esc(i18n.T(lang, "From")) + `</th><th>` + esc(i18n.T(lang, "Door")) + `</th><th>` + esc(i18n.T(lang, "What happened")) + `</th><th></th></tr></thead><tbody>`)
		done := map[string]bool{}
		for _, x := range rows {
			what := signinStageWord(lang, x.Stage)
			if x.N > 1 {
				what += " ×" + itoaSafe(x.N)
			}
			door := signinDoorName(lang, x.Door)
			if door == "" {
				door = x.Door
			}
			mine := ""
			if x.Stage != signin.StageOK && !done[x.FP] {
				done[x.FP] = true
				mine = `<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/vayumail/accounts/signin"` +
					hxVals("email", ac.Email, "op", "mine", "fp", x.FP) + ` hx-target="#vm-accounts-list" hx-swap="innerHTML">` + esc(i18n.T(lang, "It was me")) + `</button>`
			}
			b.WriteString(`<tr><td>` + html.EscapeString(i18n.Date(lang, config.InSite(x.At), i18n.WeekdayDayMonthAt)) + `</td><td>` + html.EscapeString(signinWhere(x.Masked, x.Country)) +
				`</td><td>` + html.EscapeString(door) + `</td><td>` + html.EscapeString(what) + `</td><td>` + mine + `</td></tr>`)
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`<span class="text-xs muted">` + esc(i18n.T(lang, "Addresses are shown with their last part hidden.")) + `</span></div>`)

	b.WriteString(a.signinNameBlock(ctx, ac))

	fence := strings.Join(a.signin.Fence(account), ", ")
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">` + esc(i18n.T(lang, "Sign in only from these countries")) + `</span>` +
		`<form class="vm-row" hx-post="/os/vayumail/accounts/signin" hx-target="#vm-accounts-list" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + html.EscapeString(ac.Email) + `"><input type="hidden" name="op" value="fence">` +
		`<input class="input input--sm" name="countries" value="` + html.EscapeString(fence) + `" placeholder="` + esc(i18n.T(lang, "Anywhere")) + `" aria-label="` + esc(i18n.T(lang, "Country codes, such as IN, DE")) + `">` +
		`<button class="btn btn--sm" type="submit">` + esc(i18n.T(lang, "Save")) + `</button></form>` +
		`<span class="text-xs muted">` + esc(i18n.T(lang, "Two-letter codes, such as IN, DE. Empty means anywhere. A sign-in from elsewhere is refused before its password is checked; an address whose country is not known is let through.")) + `</span></div>`)
	return b.String()
}

// signinNameBlock is the mailbox's sign-in name, and whether mail apps take
// its own password.
func (a *App) signinNameBlock(ctx context.Context, ac vmail.Account) string {
	lang := i18n.Lang(ctx)
	var b strings.Builder
	name := a.vayuMail.Accounts().SigninName(ctx, ac.Email)
	nameNote := i18n.T(lang, "Empty: the address signs in, as it always has.")
	if name != "" {
		nameNote = i18n.T(lang, "Sign in with {name}, not the address, everywhere: the site, the console, the app and mail apps' own passwords. App passwords keep working with the address. An administrator can clear it.", "name", name)
	}
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">` + esc(i18n.T(lang, "Sign-in name")) + `</span>` +
		`<form class="vm-row" hx-post="/os/vayumail/accounts/signin" hx-target="#vm-accounts-list" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + html.EscapeString(ac.Email) + `"><input type="hidden" name="op" value="name">` +
		`<input class="input input--sm" name="name" value="` + html.EscapeString(name) + `" placeholder="` + esc(i18n.T(lang, "None")) + `" autocomplete="off" aria-label="` + esc(i18n.T(lang, "Sign-in name")) + `">` +
		`<button class="btn btn--sm" type="submit">` + esc(i18n.T(lang, "Save")) + `</button></form>` +
		`<span class="text-xs muted">` + esc(i18n.T(lang, "The address people write to is public, so it is guessed at. With a sign-in name it no longer signs in, and a guesser who has it has nothing.")) + ` ` +
		html.EscapeString(nameNote) + `</span></div>`)
	mailApps := i18n.T(lang, "Mail apps sign in with app passwords only; this mailbox's own password is refused over IMAP, POP3 and SMTP.")
	if !ac.RequireDeviceApproval {
		mailApps = i18n.T(lang, "Mail apps may sign in with this mailbox's own password. An administrator can require app passwords only.")
	}
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">` + esc(i18n.T(lang, "Mail apps")) + `</span><span class="text-sm">` + html.EscapeString(mailApps) + `</span></div>`)

	return b.String()
}

// handleVayuOSAccountSignin applies the card's two controls for a mailbox's
// holder or an administrator: "It was me" on a source that tried it, and its
// fence. A fence the holder sets that would shut out the address they set it
// from is refused.
func (a *App) handleVayuOSAccountSignin(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	_ = r.ParseForm()
	lang := i18n.Lang(r.Context())
	email := strings.TrimSpace(r.FormValue("email"))
	if !a.mayChangeMailboxSettings(r, email) {
		code, msg := a.settingsRefusal(lang, email)
		writeAPIError(w, r, http.StatusForbidden, code, msg, "")
		return
	}
	account := auth.AccountKey(email)
	var opErr error
	op := r.FormValue("op")
	if a.signin == nil && op != "name" {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", i18n.T(lang, "Sign-in attempts are not recorded on this install."), "")
		return
	}
	switch op {
	case "mine":
		fp := r.FormValue("fp")
		// Only a source that tried this mailbox: "It was me" teaches the
		// mailbox a place it was actually signed in from.
		if rows, err := a.signin.Attempts(r.Context(), fp, account, 1); err != nil || len(rows) == 0 || fp == "" {
			opErr = errors.New(i18n.T(lang, "That source has not tried this mailbox."))
		} else if opErr = a.signin.ItWasMe(r.Context(), account, fp); opErr == nil {
			dbpkg.AuditLog("signin.mine", dbpkg.AuditActor(r), account, rows[0].Masked)
		}
	case "fence":
		var list []string
		for _, c := range strings.FieldsFunc(r.FormValue("countries"), func(r rune) bool { return r == ',' || r == ' ' }) {
			if c = strings.ToUpper(strings.TrimSpace(c)); len(c) != 2 {
				opErr = errors.New(i18n.T(lang, "“{code}” is not a two-letter country code.", "code", c))
				break
			}
			list = append(list, c)
		}
		if opErr == nil {
			// The address the holder sets it from must stay inside it; an
			// administrator setting another's fence is not shut out by it.
			self := ""
			if a.isMailboxHolder(r, email) {
				self = auth.ClientIP(r)
			}
			if opErr = a.signin.SetFence(r.Context(), account, list, self); errors.Is(opErr, signin.ErrSelf) {
				opErr = errors.New(i18n.T(lang, "You are signing in from a country that is not on the list, so it was not saved."))
			} else if opErr == nil {
				dbpkg.AuditLog("signin.fence", dbpkg.AuditActor(r), account, strings.Join(list, ","))
			}
		}
	case "name":
		name := r.FormValue("name")
		if opErr = a.vayuMail.Accounts().SetSigninName(r.Context(), email, name); opErr == nil {
			dbpkg.AuditLog("signin.name", dbpkg.AuditActor(r), account, map[bool]string{true: "cleared", false: "set"}[strings.TrimSpace(name) == ""])
		}
	default:
		opErr = errors.New(i18n.T(lang, "That is not something this page does."))
	}
	card := a.acctRefresh(r, email)
	if opErr != nil {
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
	}
	writeOSHTML(w, r, card)
}
