// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_signin.go — a mailbox's own sign-in attempts on its settings
// page, for its holder and for an administrator: what was tried, from where
// and through which door, "It was me" on each source, and the countries the
// mailbox may be signed in from (its fence, internal/signin).

import (
	"context"
	"errors"
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
	account := auth.AccountKey(ac.Email)
	rows, _ := a.signin.Attempts(ctx, "", account, mailboxSigninRows)
	var b strings.Builder
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">Sign-in attempts</span>`)
	cutoff := time.Now().Add(-signin.Retention)
	for _, x := range rows {
		if x.Stage == signin.StageCode && x.At.After(cutoff) {
			b.WriteString(saCallout("warn", `Your password was right but the code was wrong on `+
				html.EscapeString(config.FormatSite(x.At, "2 January at 15:04"))+`, from `+html.EscapeString(signinWhere(x.Masked, x.Country))+
				`. If that was not you, someone knows your password: change it.`))
			break
		}
	}
	if len(rows) == 0 {
		b.WriteString(`<p class="text-sm muted">No attempt in the last ` + itoaSafe(int(signin.Retention.Hours()/24)) + ` days.</p>`)
	} else {
		b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>When</th><th>From</th><th>Door</th><th>What happened</th><th></th></tr></thead><tbody>`)
		done := map[string]bool{}
		for _, x := range rows {
			what := signinStageWords[x.Stage]
			if x.N > 1 {
				what += " ×" + itoaSafe(x.N)
			}
			door := signinDoorNames[x.Door]
			if door == "" {
				door = x.Door
			}
			mine := ""
			if x.Stage != signin.StageOK && !done[x.FP] {
				done[x.FP] = true
				mine = `<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/vayumail/accounts/signin"` +
					hxVals("email", ac.Email, "op", "mine", "fp", x.FP) + ` hx-target="#vm-accounts-list" hx-swap="innerHTML">It was me</button>`
			}
			b.WriteString(`<tr><td>` + html.EscapeString(config.FormatSite(x.At, "2 Jan 15:04")) + `</td><td>` + html.EscapeString(signinWhere(x.Masked, x.Country)) +
				`</td><td>` + html.EscapeString(door) + `</td><td>` + html.EscapeString(what) + `</td><td>` + mine + `</td></tr>`)
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`<span class="text-xs muted">Addresses are shown with their last part hidden.</span></div>`)

	b.WriteString(a.signinNameBlock(ctx, ac))

	fence := strings.Join(a.signin.Fence(account), ", ")
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">Sign in only from these countries</span>` +
		`<form class="vm-row" hx-post="/os/vayumail/accounts/signin" hx-target="#vm-accounts-list" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + html.EscapeString(ac.Email) + `"><input type="hidden" name="op" value="fence">` +
		`<input class="input input--sm" name="countries" value="` + html.EscapeString(fence) + `" placeholder="Anywhere" aria-label="Country codes, such as IN, DE">` +
		`<button class="btn btn--sm" type="submit">Save</button></form>` +
		`<span class="text-xs muted">Two-letter codes, such as IN, DE. Empty means anywhere. A sign-in from elsewhere is refused before its password is checked; an address whose country is not known is let through.</span></div>`)
	return b.String()
}

// signinNameBlock is the mailbox's sign-in name, and whether mail apps take
// its own password.
func (a *App) signinNameBlock(ctx context.Context, ac vmail.Account) string {
	var b strings.Builder
	name := a.vayuMail.Accounts().SigninName(ctx, ac.Email)
	nameNote := "Empty: the address signs in, as it always has."
	if name != "" {
		nameNote = "Sign in with " + name + ", not the address, everywhere: the site, the console, the app and mail apps' own passwords. App passwords keep working with the address. An administrator can clear it."
	}
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">Sign-in name</span>` +
		`<form class="vm-row" hx-post="/os/vayumail/accounts/signin" hx-target="#vm-accounts-list" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + html.EscapeString(ac.Email) + `"><input type="hidden" name="op" value="name">` +
		`<input class="input input--sm" name="name" value="` + html.EscapeString(name) + `" placeholder="None" autocomplete="off" aria-label="Sign-in name">` +
		`<button class="btn btn--sm" type="submit">Save</button></form>` +
		`<span class="text-xs muted">The address people write to is public, so it is guessed at. With a sign-in name it no longer signs in, and a guesser who has it has nothing. ` +
		html.EscapeString(nameNote) + `</span></div>`)
	mailApps := "Mail apps sign in with app passwords only; this mailbox's own password is refused over IMAP, POP3 and SMTP."
	if !ac.RequireDeviceApproval {
		mailApps = "Mail apps may sign in with this mailbox's own password. An administrator can require app passwords only."
	}
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">Mail apps</span><span class="text-sm">` + html.EscapeString(mailApps) + `</span></div>`)

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
	email := strings.TrimSpace(r.FormValue("email"))
	if !a.mayChangeMailboxSettings(r, email) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "only this mailbox's holder or an administrator", "")
		return
	}
	account := auth.AccountKey(email)
	var opErr error
	op := r.FormValue("op")
	if a.signin == nil && op != "name" {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "Sign-in attempts are not recorded on this install.", "")
		return
	}
	switch op {
	case "mine":
		fp := r.FormValue("fp")
		// Only a source that tried this mailbox: "It was me" teaches the
		// mailbox a place it was actually signed in from.
		if rows, err := a.signin.Attempts(r.Context(), fp, account, 1); err != nil || len(rows) == 0 || fp == "" {
			opErr = errors.New("that source has not tried this mailbox")
		} else if opErr = a.signin.ItWasMe(r.Context(), account, fp); opErr == nil {
			dbpkg.AuditLog("signin.mine", dbpkg.AuditActor(r), account, rows[0].Masked)
		}
	case "fence":
		var list []string
		for _, c := range strings.FieldsFunc(r.FormValue("countries"), func(r rune) bool { return r == ',' || r == ' ' }) {
			if c = strings.ToUpper(strings.TrimSpace(c)); len(c) != 2 {
				opErr = errors.New(`"` + c + `" is not a two-letter country code`)
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
				opErr = errors.New("you are signing in from a country that is not on the list, so it was not saved")
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
		opErr = errors.New("unknown action")
	}
	card := a.acctRefresh(r, email)
	if opErr != nil {
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
	}
	writeOSHTML(w, r, card)
}
