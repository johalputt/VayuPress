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
	"html"
	"net/http"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

const privateNoBackDoor = `<p class="text-sm"><strong>If you forget your password and lose your recovery code, your mail cannot be recovered by anyone, including the site's owner. There is no back door.</strong></p>`

// privateMailboxBlock is the holder's Private mailbox control, or its state
// once it is on.
func (a *App) privateMailboxBlock(r *http.Request, email string) string {
	sealing := a.vayuPGP != nil
	sealed := sealing && a.vayuPGP.HolderKeeps(email)
	limits := `<p class="text-xs muted">Your mail is not encrypted: whoever runs the server can still read the files on its disk. ` +
		`An administrator can still suspend or delete the mailbox and change its storage allowance.</p>`
	if sealing {
		limits = `<p class="text-xs muted">Who wrote to you, to whom and when stay readable to the server, which needs them to deliver mail; ` +
			`mail from outside is sealed the moment it arrives. Whoever controls the server's software could change it to catch your password ` +
			`the next time you sign in. An administrator can still suspend or delete the mailbox and change its storage allowance.</p>`
	}
	refusals := `administrators of this site cannot open this mailbox, set its password, clear its two-step sign-in, ` +
		`give it an app password, approve its devices or change its settings, and each attempt is written to its access record.`
	if a.vayuMail.IsHandedOver(email) {
		state := `<p class="text-sm"><strong>Private.</strong> Since it was made private, ` + refusals + `</p>`
		switch {
		case sealed:
			state = `<p class="text-sm"><strong>Private and sealed.</strong> Your mail is encrypted on this server to a key locked with your password; ` +
				`no one can read it from the disk, the site's owner included. Since it was made private, ` + refusals + `</p>` +
				`<form class="vm-ooo-form" hx-post="/os/vayumail/lock" hx-swap="none"><button class="btn btn--sm" type="submit">Close my mail now</button></form>`
		case sealing:
			state += `<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">Seal my mail</span></summary>` +
				`<p class="text-sm">Encrypt this mailbox's mail on the server to a key locked with your password, so it cannot be read from the disk.</p>` +
				privateNoBackDoor + a.privateForm(email, "Seal my mail") + `</details>`
		}
		return `<div id="vm-private">` + state + limits + `</div>`
	}
	contact := a.vayuMail.Accounts().RecoveryContact(r.Context(), email)
	need := ""
	if strings.TrimSpace(contact) == "" {
		need = `<p class="text-sm"><strong>First set a recovery address</strong> on <a href="/os/vayumail/connect">Connect a device</a>: with the mailbox private, ` +
			`it is how you set a new password if you lose yours, and where a notice goes if the emergency override is ever used.</p>`
	}
	what := `<p class="text-sm">Once private, ` + refusals + ` <strong>It cannot be undone</strong>, by you or by an administrator.</p>`
	if sealing {
		what = `<p class="text-sm">Once private, your mail is encrypted on this server to a key locked with your password, and ` + refusals +
			` <strong>It cannot be undone</strong>, by you or by an administrator.</p>` + privateNoBackDoor
	}
	return `<div id="vm-private"><details class="vm-ooo vm-acct__sub"><summary><span class="field-label">Make this mailbox private</span></summary>` +
		what + limits + need + a.privateForm(email, "Make it private") + `</details></div>`
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

func (a *App) privateForm(email, button string) string {
	return `<form class="vm-ooo-form" hx-post="/os/vayumail/settings/private" hx-target="#vm-private" hx-swap="outerHTML">` +
		`<label class="field"><span class="field-label">Your password</span><input class="input input--sm" type="password" name="current" autocomplete="current-password" required></label>` +
		`<label class="field"><span class="field-label">Type ` + html.EscapeString(email) + ` to confirm</span><input class="input input--sm" type="text" name="confirm" autocomplete="off" required></label>` +
		`<button class="btn btn--primary btn--sm" type="submit">` + button + `</button></form>` +
		`<div id="vm-private-result" aria-live="polite"></div>`
}

// handleVayuOSMakePrivate makes the session's own mailbox private, and seals
// it, or seals a mailbox made private before sealing existed.
func (a *App) handleVayuOSMakePrivate(w http.ResponseWriter, r *http.Request) {
	_, own := a.ownMailbox(r)
	if own == "" || !a.mailRunning() || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no mailbox of your own here", "")
		return
	}
	_ = r.ParseForm()
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
		refuse("Type the mailbox address exactly: this cannot be undone.")
		return
	}
	pw := r.FormValue("current")
	if !a.ownMailPasswordOK(r, own, pw) {
		refuse("That is not your password.")
		return
	}
	// A mailbox several people hold (sales@, info@) is not one holder's to
	// put out of the others' reach: ADR-0152 D4's shared-address condition.
	if n := a.mailboxHolders(r.Context(), own); n > 1 {
		refuse("This mailbox is shared by " + itoaSafe(n) + " people: one of them cannot make it private for the others.")
		return
	}
	contact := a.vayuMail.Accounts().RecoveryContact(r.Context(), own)
	if !private && strings.TrimSpace(contact) == "" {
		refuse("Set a recovery address first.")
		return
	}
	// The key first: a mailbox refused its administrators but still open on
	// the disk would be a claim, and running this again finishes a seal that
	// stopped part-way.
	code := ""
	if sealing {
		c, err := a.sealMailbox(own, pw)
		if err != nil {
			refuse("Not sealed: " + err.Error() + ".")
			return
		}
		code = c
		dbpkg.AuditLog("vayumail.account.sealed", own, own, "the holder sealed this mailbox's mail to their key")
	}
	if !private {
		if err := a.vayuMail.Accounts().MakePrivate(r.Context(), own, contact); err != nil {
			refuse("Not changed: " + err.Error() + ".")
			return
		}
		dbpkg.AuditLog("vayumail.account.private", own, own, "the holder made this mailbox private")
	}
	out := a.privateMailboxBlock(r, own)
	if code != "" {
		out = strings.Replace(out, `<div id="vm-private">`, `<div id="vm-private">`+recoveryCodeBlock(code,
			`Your mailbox is private and its mail is sealed. The mail already here is being sealed now.`), 1)
	}
	writeOSHTML(w, r, out)
}
