// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_private.go — a holder makes their own mailbox private.
//
// Private is the handover state (ADR-0152 Phase 5), started by the holder
// rather than given by the operator: administrators can no longer open the
// mailbox, sign in to it with a password they set, clear its two-step
// sign-in, mint an app password for it, approve its devices or change its
// settings (mayChangeMailboxSettings, refusedAsPrivate), and each attempt is
// written to the holder's record. It is one-way, as handover is.
//
// With VayuPGP running, making it private also seals it: the mailbox key is
// handed to the holder under their password and a recovery code, and its
// mail is encrypted to that key on disk (vayuos_mail_privatekey.go). A
// mailbox made private before sealing existed is offered the seal on the same
// card. Without VayuPGP the card says plainly that the files are not
// encrypted.

import (
	"context"
	"github.com/johalputt/vayupress/internal/i18n"
	"html"
	"net/http"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// privateNoBackDoor says, before a mailbox is sealed, that nothing can open
// it without its password or its recovery code.
func privateNoBackDoor(lang string) string {
	return `<p class="text-sm"><strong>` + esc(i18n.T(lang, "If you forget your password and lose your recovery code, your mail cannot be recovered by anyone, including the site's owner. There is no back door.")) + `</strong></p>`
}

// privateMailboxBlock is the holder's Private mailbox setting: what it does,
// what it does not, and the form that does it. Each state is said in whole
// sentences, so what an administrator can no longer do reads the same in
// every language as it does here.
func (a *App) privateMailboxBlock(r *http.Request, email string) string {
	lang := i18n.Lang(r.Context())
	strong := func(s string) string { return "<strong>" + esc(s) + "</strong>" }
	sealing := a.vayuPGP != nil
	sealed := sealing && a.vayuPGP.HolderKeeps(email)
	limits := `<p class="text-xs muted">` + esc(i18n.T(lang, "Your mail is not encrypted: whoever runs the server can still read the files on its disk. An administrator can still suspend or delete the mailbox and change its storage allowance.")) + `</p>`
	if sealing {
		limits = `<p class="text-xs muted">` + esc(i18n.T(lang, "Who wrote to you, to whom and when stay readable to the server, which needs them to deliver mail; mail from outside is sealed the moment it arrives. Your calendar and contacts are not sealed: reminders, and your phone and computer, need them while you are away. Files in Drive are sealed, whatever their size. Whoever controls the server's software could change it to catch your password the next time you sign in. An administrator can still suspend or delete the mailbox and change its storage allowance.")) + `</p>`
	}
	since := esc(i18n.T(lang, "Since it was made private, administrators of this site cannot open this mailbox, set its password, clear its two-step sign-in, give it an app password, approve its devices or change its settings, and each attempt is written to its access record."))
	if a.vayuMail.IsHandedOver(email) {
		state := `<p class="text-sm">` + strong(i18n.T(lang, "Private.")) + ` ` + since + `</p>`
		switch {
		case sealed:
			state = `<p class="text-sm">` + strong(i18n.T(lang, "Private and sealed.")) + ` ` +
				esc(i18n.T(lang, "Your mail is encrypted on this server to a key locked with your password; no one can read it from the disk, the site's owner included.")) + ` ` + since + `</p>` +
				`<form class="vm-ooo-form" hx-post="/os/vayumail/lock" hx-swap="none"><button class="btn btn--sm" type="submit">` + esc(i18n.T(lang, "Close my mail now")) + `</button></form>`
		case sealing:
			state += `<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">` + esc(i18n.T(lang, "Seal my mail")) + `</span></summary>` +
				`<p class="text-sm">` + esc(i18n.T(lang, "Encrypt this mailbox's mail on the server to a key locked with your password, so it cannot be read from the disk.")) + `</p>` +
				privateNoBackDoor(lang) + a.privateForm(lang, email, i18n.T(lang, "Seal my mail")) + `</details>`
		}
		return `<div id="vm-private">` + state + limits + `</div>`
	}
	contact := a.vayuMail.Accounts().RecoveryContact(r.Context(), email)
	need := ""
	if strings.TrimSpace(contact) == "" {
		need = `<p class="text-sm">` + escFill(i18n.T(lang, "{first} on {link}: with the mailbox private, it is how you set a new password if you lose yours, and where a notice goes if the emergency override is ever used.", "first", htmlMark(0), "link", htmlMark(1)),
			strong(i18n.T(lang, "First set a recovery address")), `<a href="/os/vayumail/connect">`+esc(i18n.T(lang, "Connect a device"))+`</a>`) + `</p>`
	}
	undone := escFill(i18n.T(lang, "{strong}, by you or by an administrator.", "strong", htmlMark(0)), strong(i18n.T(lang, "It cannot be undone")))
	what := `<p class="text-sm">` + esc(i18n.T(lang, "Once private, administrators of this site cannot open this mailbox, set its password, clear its two-step sign-in, give it an app password, approve its devices or change its settings, and each attempt is written to its access record.")) + ` ` + undone + `</p>`
	if sealing {
		what = `<p class="text-sm">` + esc(i18n.T(lang, "Once private, your mail is encrypted on this server to a key locked with your password, and administrators of this site cannot open this mailbox, set its password, clear its two-step sign-in, give it an app password, approve its devices or change its settings, and each attempt is written to its access record.")) + ` ` + undone + `</p>` + privateNoBackDoor(lang)
	}
	return `<div id="vm-private"><details class="vm-ooo vm-acct__sub"><summary><span class="field-label">` + esc(i18n.T(lang, "Make this mailbox private")) + `</span></summary>` +
		what + limits + need + a.privateForm(lang, email, i18n.T(lang, "Make it private")) + `</details></div>`
}

// mailboxHolders counts the people the address is assigned to. A list that
// cannot be read counts as none known: this rule spares co-holders a lockout,
// and the mailbox's own password has already been proved.
func (a *App) mailboxHolders(ctx context.Context, email string) int {
	if a.userStore == nil {
		return 0
	}
	list, err := a.userStore.List(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for _, u := range list {
		if strings.EqualFold(strings.TrimSpace(u.MailAddress), email) {
			n++
		}
	}
	return n
}

func (a *App) privateForm(lang, email, button string) string {
	return `<form class="vm-ooo-form" hx-post="/os/vayumail/settings/private" hx-target="#vm-private" hx-swap="outerHTML">` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Your password")) + `</span><input class="input input--sm" type="password" name="current" autocomplete="current-password" required></label>` +
		`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Type {address} to confirm", "address", email)) + `</span><input class="input input--sm" type="text" name="confirm" autocomplete="off" required></label>` +
		`<button class="btn btn--primary btn--sm" type="submit">` + esc(button) + `</button></form>` +
		`<div id="vm-private-result" aria-live="polite"></div>`
}

// handleVayuOSMakePrivate makes the session's own mailbox private, and seals
// it, or seals a mailbox made private before sealing existed.
func (a *App) handleVayuOSMakePrivate(w http.ResponseWriter, r *http.Request) {
	_, own := a.ownMailbox(r)
	if own == "" || !a.mailRunning() || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "There is no mailbox of your own here."), "")
		return
	}
	_ = r.ParseForm()
	lang := i18n.Lang(r.Context())
	refuse := func(msg string) {
		b := strings.Replace(a.privateMailboxBlock(r, own), `<div id="vm-private-result" aria-live="polite"></div>`,
			`<div id="vm-private-result" aria-live="polite">`+saCallout("danger", html.EscapeString(msg))+`</div>`, 1)
		writeOSHTML(w, r, strings.Replace(b, `<details class="vm-ooo vm-acct__sub">`, `<details class="vm-ooo vm-acct__sub" open>`, 1))
	}
	private := a.vayuMail.IsHandedOver(own)
	sealing := a.vayuPGP != nil
	if private && (!sealing || a.vayuPGP.HolderKeeps(own)) {
		writeOSHTML(w, r, a.privateMailboxBlock(r, own))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.FormValue("confirm")), own) {
		refuse(i18n.T(lang, "Type the mailbox address exactly: this cannot be undone."))
		return
	}
	pw := r.FormValue("current")
	if !a.ownMailPasswordOK(r, own, pw) {
		refuse(i18n.T(lang, "That is not your password."))
		return
	}
	// A mailbox several people hold (sales@, info@) is not one holder's to
	// put out of the others' reach: ADR-0152 D4's shared-address condition.
	if n := a.mailboxHolders(r.Context(), own); n > 1 {
		refuse(i18n.T(lang, "This mailbox is shared by {n} people: one of them cannot make it private for the others.", "n", n))
		return
	}
	contact := a.vayuMail.Accounts().RecoveryContact(r.Context(), own)
	if !private && strings.TrimSpace(contact) == "" {
		refuse(i18n.T(lang, "Set a recovery address first."))
		return
	}
	// The key first: a mailbox refused its administrators but still open on
	// the disk would be a claim, and running this again finishes a seal that
	// stopped part-way.
	code := ""
	if sealing {
		c, err := a.sealMailbox(own, pw)
		if err != nil {
			refuse(i18n.T(lang, "Not sealed: {reason}.", "reason", err.Error()))
			return
		}
		code = c
		dbpkg.AuditLog("vayumail.account.sealed", own, own, "the holder sealed this mailbox's mail to their key")
	}
	if !private {
		if err := a.vayuMail.Accounts().MakePrivate(r.Context(), own, contact); err != nil {
			refuse(i18n.T(lang, "Not changed: {reason}.", "reason", err.Error()))
			return
		}
		dbpkg.AuditLog("vayumail.account.private", own, own, "the holder made this mailbox private")
	}
	out := a.privateMailboxBlock(r, own)
	if code != "" {
		out = strings.Replace(out, `<div id="vm-private">`, `<div id="vm-private">`+recoveryCodeBlock(code,
			i18n.T(lang, "Your mailbox is private and its mail is sealed. The mail already here is being sealed now.")), 1)
	}
	writeOSHTML(w, r, out)
}
