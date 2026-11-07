// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_privatekey.go — a private mailbox whose mail is sealed to its
// holder's key.
//
// Making a mailbox private hands its key to the holder (VayuPGP holder.go)
// and seals its mail on disk (VayuMail sealing.go). The server can then read
// that mail only while a sign-in that proved the mailbox password holds the
// key open: an IMAP, POP3 or console sign-in with the password does it
// (holdMailOpen), and so does typing the password into the sealed notice at
// the top of the inbox. Signing out, a password reset, or eight hours close
// it again. A device credential or app password does not open it: those are
// not the password the key is locked with. The VayuMail app does not need the
// server to: it takes the locked key and unlocks it on the phone.
//
// Lose the password and the recovery code and the mail is gone. That is the
// operator's decision (2026-10-18), and the page says it before the switch.

import (
	"context"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// mailSealedShut reports whether email's mail is sealed and its key is not
// held open now.
func (a *App) mailSealedShut(email string) bool {
	return a.vayuPGP != nil && email != "" && a.vayuPGP.HolderKeeps(email) && !a.vayuPGP.HeldOpen(email)
}

// holdMailOpen is called after a sign-in that proved email's own password:
// for a sealed mailbox it holds the key open. A password that is right but
// does not open the key (it was reset since the key was locked) is logged;
// the holder's recovery code is what opens it then.
func (a *App) holdMailOpen(email, password string) {
	if a.vayuPGP == nil {
		return
	}
	if err := a.vayuPGP.HoldOpen(email, password); err != nil {
		logging.LogWarn("vayumail", "a sign-in to "+email+" did not open its sealed mail: "+err.Error())
	}
}

// ownMailPasswordOK checks the holder's mailbox password as any sign-in
// does: the per-mailbox throttle first, so a session left open is not an
// unmetered oracle for it.
func (a *App) ownMailPasswordOK(r *http.Request, own, password string) bool {
	if d := mailAuthThrottle.Delay(own); d > 0 {
		time.Sleep(d)
	}
	if !auth.VerifySecretArgon2id(password, a.vayuMail.Accounts().HashFor(r.Context(), own)) {
		mailAuthThrottle.Fail(own)
		return false
	}
	mailAuthThrottle.Success(own)
	return true
}

// sealMailbox hands own's key to its holder under password and seals the
// mail already stored, in the background. It returns the recovery code, to
// be shown once.
func (a *App) sealMailbox(own, password string) (string, error) {
	name := own
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: own, Name: name, Email: own}); err != nil {
		return "", err
	}
	code, err := a.vayuPGP.LockToHolder(own, password)
	if err != nil {
		return "", err
	}
	go a.sealStoredMail(own)
	return code, nil
}

func (a *App) sealStoredMail(own string) {
	sealed, left, err := a.vayuMail.SealStored(own)
	switch {
	case err != nil:
		logging.LogError("vayumail", "sealing the stored mail of "+own, err.Error())
	case left > 0:
		logging.LogWarn("vayumail", "sealed "+itoaSafe(sealed)+" stored messages of "+own+"; "+itoaSafe(left)+" are left for the next run")
	default:
		logging.LogInfo("vayumail", "sealed "+itoaSafe(sealed)+" stored messages of "+own)
	}
}

// recoveryCodeBlock shows a recovery code, once.
func recoveryCodeBlock(code, lead string) string {
	c := html.EscapeString(code)
	return saCallout("warning", `<p class="text-sm">`+lead+`</p>`+
		`<p class="text-sm"><strong>Your recovery code: <code>`+c+`</code></strong></p>`+
		`<p class="text-sm">Write it down or save it somewhere only you can reach. It is shown this once and kept nowhere on this server. `+
		`If you forget your password and lose this code, your mail cannot be recovered by anyone, including the site's owner.</p>`+
		`<p class="text-sm"><a class="btn btn--sm" download="mailbox-recovery-code.txt" href="data:text/plain;charset=utf-8,`+c+`">Save it as a file</a></p>`)
}

// sealedNotice is what the holder of a sealed mailbox sees at the top of
// their inbox while its key is not held open.
func (a *App) sealedNotice(own, result string) string {
	if !a.mailSealedShut(own) {
		return ""
	}
	return `<div id="vm-sealed">` + saCallout("info", `<p class="text-sm"><strong>Your mail is sealed.</strong> `+
		`Only your mailbox password opens it: type it here to read your mail on this site for the next eight hours. `+
		`Senders and dates show while it is sealed; subjects and messages do not.</p>`+
		`<form class="vm-ooo-form" hx-post="/os/vayumail/unlock" hx-target="#vm-sealed" hx-swap="outerHTML">`+
		`<label class="field"><span class="field-label">Mailbox password</span><input class="input input--sm" type="password" name="current" autocomplete="current-password" required></label>`+
		`<button class="btn btn--primary btn--sm" type="submit">Open my mail</button></form>`+
		`<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">Use your recovery code</span></summary>`+
		`<p class="text-sm">If your password was reset since you made your mailbox private, your recovery code opens your mail and puts it under the password you use now.</p>`+
		`<form class="vm-ooo-form" hx-post="/os/vayumail/unlock/recover" hx-target="#vm-sealed" hx-swap="outerHTML">`+
		`<label class="field"><span class="field-label">Recovery code</span><input class="input input--sm" type="text" name="code" autocomplete="off" required></label>`+
		`<label class="field"><span class="field-label">Mailbox password</span><input class="input input--sm" type="password" name="current" autocomplete="current-password" required></label>`+
		`<button class="btn btn--sm" type="submit">Open with the code</button></form></details>`+
		result) + `</div>`
}

// sealedRefusal answers a refused unlock or recovery: in the notice while the
// mail is shut, and on its own while it is open, so it is never lost.
func (a *App) sealedRefusal(own, msg string) string {
	if a.mailSealedShut(own) {
		return a.sealedNotice(own, saCallout("danger", msg))
	}
	return `<div id="vm-sealed">` + saCallout("danger", msg) + `</div>`
}

// ownSealedMailbox is the session's own mailbox, when the request may act on
// its seal.
func (a *App) ownSealedMailbox(w http.ResponseWriter, r *http.Request) (string, bool) {
	_, own := a.ownMailbox(r)
	if own == "" || !a.mailRunning() || a.vayuMail.Accounts() == nil || a.vayuPGP == nil || !a.vayuPGP.HolderKeeps(own) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no sealed mailbox of your own here", "")
		return "", false
	}
	_ = r.ParseForm()
	return own, true
}

// handleVayuOSMailUnlock holds the session's own sealed mail open with the
// password typed.
func (a *App) handleVayuOSMailUnlock(w http.ResponseWriter, r *http.Request) {
	own, ok := a.ownSealedMailbox(w, r)
	if !ok {
		return
	}
	pw := r.FormValue("current")
	if !a.ownMailPasswordOK(r, own, pw) {
		writeOSHTML(w, r, a.sealedRefusal(own, "That is not your mailbox password."))
		return
	}
	if err := a.vayuPGP.HoldOpen(own, pw); err != nil {
		writeOSHTML(w, r, a.sealedRefusal(own,
			"That is your password, but your mail is locked under an earlier one: open it with your recovery code."))
		return
	}
	w.Header().Set("HX-Redirect", "/os/vayumail/inbox")
	w.WriteHeader(http.StatusOK)
}

// handleVayuOSMailRecover opens the session's own sealed mail with its
// recovery code, puts it under the mailbox password as it is now, and shows
// the new code that replaces the one used.
func (a *App) handleVayuOSMailRecover(w http.ResponseWriter, r *http.Request) {
	own, ok := a.ownSealedMailbox(w, r)
	if !ok {
		return
	}
	pw := r.FormValue("current")
	if !a.ownMailPasswordOK(r, own, pw) {
		writeOSHTML(w, r, a.sealedRefusal(own, "That is not your mailbox password."))
		return
	}
	next, err := a.vayuPGP.RecoverHolder(own, r.FormValue("code"), pw)
	if err != nil {
		// A code is a guess like a password: it counts against the same throttle.
		mailAuthThrottle.Fail(own)
		writeOSHTML(w, r, a.sealedRefusal(own, "That recovery code does not open your mail."))
		return
	}
	dbpkg.AuditLog("vayumail.account.recovery-code", own, own, "the holder opened their sealed mail with a recovery code")
	writeOSHTML(w, r, `<div id="vm-sealed">`+recoveryCodeBlock(next,
		`Your mail is open, under the password you use now. The code you used no longer works; this is your new one.`)+
		`<p class="text-sm"><a class="btn btn--sm" href="/os/vayumail/inbox">Read my mail</a></p></div>`)
}

// handleVayuOSMailLock closes the session's own sealed mail now.
func (a *App) handleVayuOSMailLock(w http.ResponseWriter, r *http.Request) {
	own, ok := a.ownSealedMailbox(w, r)
	if !ok {
		return
	}
	a.vayuPGP.CloseHeld(own)
	w.Header().Set("HX-Redirect", "/os/vayumail/inbox")
	w.WriteHeader(http.StatusOK)
}

// closeMailOnSignOut closes the signing-out session's sealed mail. It reads
// the session before it is destroyed: the sign-out route runs outside the
// middleware that names the user.
func (a *App) closeMailOnSignOut(ctx context.Context, token string) {
	if a.vayuPGP == nil || a.sessions == nil || token == "" {
		return
	}
	uid, err := a.sessions.Validate(ctx, token)
	if err != nil {
		return
	}
	email := strings.TrimPrefix(uid, "vmail:")
	if email == uid {
		email = ""
		if a.userStore != nil {
			if u, uerr := a.userStore.GetByID(ctx, uid); uerr == nil && u != nil {
				email = u.MailAddress
			}
		}
	}
	if email != "" {
		a.vayuPGP.CloseHeld(strings.ToLower(email))
	}
}
