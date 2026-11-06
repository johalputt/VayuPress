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
// It is NOT encryption, and the card says so in those words: the messages
// are files on a server someone runs. ADR-0152 D4 records why sealing is not
// built yet; nothing here licenses a stronger sentence.

import (
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// privateMailboxBlock is the holder's Private mailbox control, or its state
// once it is on.
func (a *App) privateMailboxBlock(r *http.Request, email string) string {
	limits := `<p class="text-xs muted">Your mail is not encrypted: whoever runs the server can still read the files on its disk. ` +
		`An administrator can still suspend or delete the mailbox and change its storage allowance.</p>`
	if a.vayuMail.IsHandedOver(email) {
		return `<div id="vm-private"><p class="text-sm"><strong>Private.</strong> Administrators of this site cannot open this mailbox, ` +
			`set its password, clear its two-step sign-in, give it an app password, approve its devices or change its settings. ` +
			`Each attempt is written to its access record.</p>` + limits + `</div>`
	}
	contact := a.vayuMail.Accounts().RecoveryContact(r.Context(), email)
	need := ""
	if strings.TrimSpace(contact) == "" {
		need = `<p class="text-sm"><strong>First set a recovery address</strong> on <a href="/os/vayumail/connect">Connect a device</a>: with the mailbox private, ` +
			`it is the only way back in if you lose your password, and where a notice goes if the emergency override is ever used.</p>`
	}
	return `<div id="vm-private"><details class="vm-ooo vm-acct__sub"><summary><span class="field-label">Make this mailbox private</span></summary>` +
		`<p class="text-sm">Once private, administrators of this site cannot open this mailbox, set its password, clear its two-step sign-in, ` +
		`give it an app password, approve its devices or change its settings, and each attempt is written to its access record. ` +
		`<strong>It cannot be undone</strong>, by you or by an administrator.</p>` + limits + need +
		`<form class="vm-ooo-form" hx-post="/os/vayumail/settings/private" hx-target="#vm-private" hx-swap="outerHTML">` +
		`<label class="field"><span class="field-label">Your password</span><input class="input input--sm" type="password" name="current" autocomplete="current-password" required></label>` +
		`<label class="field"><span class="field-label">Type ` + html.EscapeString(email) + ` to confirm</span><input class="input input--sm" type="text" name="confirm" autocomplete="off" required></label>` +
		`<button class="btn btn--primary btn--sm" type="submit">Make it private</button></form>` +
		`<div id="vm-private-result" aria-live="polite"></div></details></div>`
}

// handleVayuOSMakePrivate makes the session's own mailbox private.
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
	if a.vayuMail.IsHandedOver(own) {
		writeOSHTML(w, r, a.privateMailboxBlock(r, own))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(r.FormValue("confirm")), own) {
		refuse("Type the mailbox address exactly: this cannot be undone.")
		return
	}
	// The password is a guess like any sign-in's: the same per-mailbox
	// throttle, so a session left open cannot be used to try passwords.
	if d := mailAuthThrottle.Delay(own); d > 0 {
		time.Sleep(d)
	}
	if !auth.VerifySecretArgon2id(r.FormValue("current"), a.vayuMail.Accounts().HashFor(r.Context(), own)) {
		mailAuthThrottle.Fail(own)
		refuse("That is not your password.")
		return
	}
	mailAuthThrottle.Success(own)
	contact := a.vayuMail.Accounts().RecoveryContact(r.Context(), own)
	if strings.TrimSpace(contact) == "" {
		refuse("Set a recovery address first.")
		return
	}
	if err := a.vayuMail.Accounts().MakePrivate(r.Context(), own, contact); err != nil {
		refuse("Not changed: " + err.Error() + ".")
		return
	}
	dbpkg.AuditLog("vayumail.account.private", own, own, "the holder made this mailbox private")
	writeOSHTML(w, r, a.privateMailboxBlock(r, own))
}
