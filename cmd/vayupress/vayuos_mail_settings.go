// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_settings.go — the routed, per-mailbox settings page.
//
// The accounts list used to carry EVERY per-mailbox control inside one collapsing
// card: forwarding, vacation, aliases, recovery, handover, PGP and filters nested
// seven deep, with profile pictures and cartoon pickers after them. On a
// many-mailbox install that is a very tall page whose every inline action
// re-renders the whole list — and there is nowhere to put an explanation that does
// not fit in a select label.
//
// This page is that missing room. The accounts list keeps what an operator scans
// across mailboxes (identity, quota, role, retention, enable/delete) plus one link
// here; everything that is genuinely a *setting* lives on its own URL, which is
// also bookmarkable and linkable from a ticket.
//
// Surfaces: the section builders are shared with the accounts list, so the swap
// target is adapted in exactly one place (see vayuMailboxSettingsSections) and the
// response surface is chosen from HTMX's HX-Target header (see acctRefresh). A test
// asserts the settings page contains no list-surface targets, so a future control
// added to a shared builder cannot silently swap the wrong thing.

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/auth"
	avatarpkg "github.com/johalputt/vayupress/internal/avatar"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailboxSettingsSections builds every per-mailbox setting as one fragment. The
// bool reports whether the mailbox exists.
//
// forHolder builds the holder's own page (3cl): the personal settings only.
// The address's aliases and its handover are the operator's, and recovery is
// on the holder's Connect page already.
func (a *App) mailboxSettingsSections(ctx context.Context, email string, forHolder bool) (string, bool) {
	ac, ok := a.mailAccountByEmail(ctx, email)
	if !ok {
		return "", false
	}
	// The settings in the sections a person looks for them under, each item
	// named for the page's search (mailSettingsLayout). An item whose control
	// carries its own heading is not given a second one.
	var b strings.Builder
	b.WriteString(mailSettingGroup("general", "General",
		mailSetting{Name: "Name on mail you send", HTML: autosaveForm(vayuCardName(ac))},
		mailSetting{Name: "Picture", Find: "avatar photo", HTML: a.mailboxAvatarSettings(ac), Bare: true}))
	addresses := []mailSetting{{Name: "Forward a copy to", Find: "forwarding auto-forward", HTML: autosaveForm(vayuCardForwarding(ac))}}
	if !forHolder {
		addresses = append(addresses, mailSetting{Name: "Aliases", Find: "other addresses", HTML: a.vayuCardAliases(ctx, ac), Bare: true})
	}
	b.WriteString(mailSettingGroup("addresses", "Addresses", addresses...))
	b.WriteString(mailSettingGroup("rules", "Rules and replies",
		mailSetting{Name: "Vacation reply", Find: "autoresponder out of office away", HTML: autosaveForm(openSection(a.vayuCardVacation(ctx, ac))), Bare: true},
		mailSetting{Name: "Rules", Find: "rules filters sort move forward reject label", HTML: a.vayuCardFilters(ctx, ac), Bare: true}))
	privacy := []mailSetting{
		{Name: "Sign-in attempts", Find: "sign-in name login security", HTML: a.vayuCardSignins(ctx, ac), Bare: true},
		{Name: "Encryption keys", Find: "pgp openpgp key", HTML: a.vayuCardPGP(ac), Bare: true},
		{Name: "S/MIME certificate", Find: "smime certificate signing", HTML: a.vayuCardSMIME(ctx, ac), Bare: true},
	}
	if !forHolder {
		privacy = append(privacy,
			mailSetting{Name: "Recovery", Find: "recovery codes", HTML: a.vayuCardRecovery(ctx, ac.Email), Bare: true},
			mailSetting{Name: "Handover", Find: "ownership transfer", HTML: a.vayuCardHandover(ctx, ac), Bare: true})
	}
	if mh := a.modelHelpSetting(ctx, ac, forHolder); mh != "" {
		privacy = append(privacy, mailSetting{Name: "Help from a model", Find: "ai summarise draft assistant", HTML: mh})
	}
	b.WriteString(mailSettingGroup("privacy", "Privacy and security", privacy...))

	// One adaptation point: the shared builders mark their controls with the list
	// surface. On this page every control must come back to #vm-mbox-settings, so
	// the target is re-pointed here rather than by adding a parameter to seven
	// builders and every call site. The test below keeps this honest.
	sec := b.String()
	sec = strings.ReplaceAll(sec, `hx-target="#vm-accounts-list"`, `hx-target="#vm-mbox-settings"`)
	return sec, true
}

// mailboxAvatarSettings is the profile-picture editor (upload, remove, or pick a
// prebuilt cartoon). It lives on the settings page so the accounts list is not
// carrying an image picker for every mailbox on the server.
func (a *App) mailboxAvatarSettings(ac vmail.Account) string {
	email := html.EscapeString(ac.Email)
	hasAvatar := strings.TrimSpace(ac.AvatarType) != ""
	removeBtn := ""
	if hasAvatar {
		removeBtn = `<button class="btn btn--sm btn--ghost" type="button" hx-post="/os/vayumail/accounts/avatar/remove" ` +
			hxVals("email", ac.Email) + ` hx-target="#vm-mbox-settings" hx-swap="innerHTML">Remove picture</button>`
	}
	var b strings.Builder
	b.WriteString(`<div class="vm-acct__avatar-edit"><span class="field-label">Profile picture</span>` +
		`<form class="vm-row vm-avatar-form" hx-post="/os/vayumail/accounts/avatar" hx-encoding="multipart/form-data" hx-target="#vm-mbox-settings" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + email + `">` +
		`<input class="input input--sm" type="file" name="avatar" accept="image/png,image/jpeg,image/gif,image/webp" aria-label="Profile picture">` +
		`<button class="btn btn--sm" type="submit">Upload</button></form>` + removeBtn +
		`<span class="text-xs muted">PNG, JPEG, GIF or WebP · up to 500 KB.</span></div>`)
	// Cartoons: same-origin previews, one click to set (no external asset fetched).
	qEmail := qparam(ac.Email)
	b.WriteString(`<div class="vm-acct__avatar-pick"><span class="field-label">Or choose an avatar</span><div class="vm-cartoon-row">`)
	for n := 0; n < avatarpkg.CartoonCount; n++ {
		ns := itoaSafe(n)
		b.WriteString(`<button type="button" class="vm-cartoon" title="Use this cartoon" ` +
			`hx-post="/os/vayumail/accounts/avatar/cartoon"` + hxVals("email", ac.Email, "n", ns) +
			` hx-target="#vm-mbox-settings" hx-swap="innerHTML">` +
			`<img class="vm-cartoon__img" src="/os/vayumail/accounts/avatar/cartoon?email=` + qEmail + `&amp;n=` + ns +
			`" alt="Cartoon ` + ns + `" width="40" height="40" loading="lazy"></button>`)
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// mailAccountByEmail finds one mailbox among the configured accounts.
func (a *App) mailAccountByEmail(ctx context.Context, email string) (vmail.Account, bool) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return vmail.Account{}, false
	}
	want := strings.ToLower(strings.TrimSpace(email))
	if want == "" {
		return vmail.Account{}, false
	}
	accs, err := a.vayuMail.Accounts().List(ctx)
	if err != nil {
		return vmail.Account{}, false
	}
	for _, ac := range accs {
		if strings.ToLower(strings.TrimSpace(ac.Email)) == want {
			return ac, true
		}
	}
	return vmail.Account{}, false
}

// acctRefresh returns the markup a per-mailbox control should swap back.
//
// HTMX reports the id of the element it is about to replace in HX-Target, so a
// control inside the settings page returns that page's sections and everything
// else (including a direct POST from a test) returns the accounts list — the
// original behaviour, unchanged. Both surfaces render the same mailbox, so a
// spoofed header can only choose *which* admin-only fragment comes back.
//
// A holder only ever gets their own page's sections back. The accounts list
// names every mailbox on the server, and a control a holder may now use must
// not answer with it (3cl).
func (a *App) acctRefresh(r *http.Request, email string) string {
	if !a.isAdminRequest(r) {
		sec, _ := a.mailboxSettingsSections(r.Context(), email, true)
		return `<div id="vm-mbox-settings">` + sec + `</div>`
	}
	if r.Header.Get("HX-Target") == "vm-mbox-settings" && strings.TrimSpace(email) != "" {
		if sec, ok := a.mailboxSettingsSections(r.Context(), email, false); ok {
			return `<div id="vm-mbox-settings">` + sec + `</div>`
		}
	}
	return a.vayuAccountsList(r.Context())
}

// isMailboxHolder reports whether email is this session's own mailbox: the
// boundary app passwords and signatures already use (canManageAppPassword).
// The holder of a mailbox manages its personal settings (the operator's
// decision of 2026-10-17, 3cl); only an administrator manages the address
// itself.
func (a *App) isMailboxHolder(r *http.Request, email string) bool {
	_, own := a.ownMailbox(r)
	return own != "" && strings.EqualFold(own, strings.TrimSpace(email))
}

// isOwnMailbox reports whether email is the signed-in account's own
// mailbox: the one assigned to it, or the one at the address it signs in
// with, which on most installs is the owner's and is never "assigned".
func (a *App) isOwnMailbox(r *http.Request, email string) bool {
	if a.isMailboxHolder(r, email) {
		return true
	}
	u := currentUser(r)
	return u != nil && u.Email != "" && strings.EqualFold(u.Email, strings.TrimSpace(email))
}

// mayChangeMailboxSettings is who may change a mailbox's personal settings: its
// holder, or an administrator while the mailbox is not private. A private
// (handed-over) mailbox's settings are its holder's alone; an administrator's
// attempt is refused here and written to the holder's record.
func (a *App) mayChangeMailboxSettings(r *http.Request, email string) bool {
	if a.isOwnMailbox(r, email) {
		return true
	}
	if !a.isAdminRequest(r) {
		return false
	}
	return !a.refusedAsPrivate(r, email, "change its settings")
}

// refusedAsPrivate reports whether email is a private mailbox someone other
// than its holder is acting on, and if so writes the attempt to the holder's
// record ("what" completes "An administrator tried to …").
func (a *App) refusedAsPrivate(r *http.Request, email, what string) bool {
	if a.vayuMail == nil || a.isOwnMailbox(r, email) || !a.vayuMail.IsHandedOver(email) {
		return false
	}
	_ = a.vayuMail.AppendLedger(r.Context(), email, dbpkg.AuditActor(r), "refused", "an administrator tried to "+what)
	return true
}

// settingsRefusal is the reason a settings change was refused, in the
// words of whichever rule refused it.
func (a *App) settingsRefusal(email string) (code, msg string) {
	if a.vayuMail != nil && a.vayuMail.IsHandedOver(email) {
		return "private-mailbox", "This mailbox is private: only its holder can change its settings."
	}
	return "forbidden", "only this mailbox's holder or an administrator"
}

// handleVayuOSMySettings is the holder's own settings page: forwarding,
// vacation, filters, keys and picture for the mailbox this session holds. An
// administrator's per-mailbox page is /os/vayumail/accounts/settings.
func (a *App) handleVayuOSMySettings(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		a.writeMailSetup(w, r, "Mailbox settings")
		return
	}
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	_, own := a.ownMailbox(r)
	ac, ok := a.mailAccountByEmail(r.Context(), own)
	if a.isAdminRequest(r) && !ok {
		// An administrator with no mailbox of their own (ownMailbox) is given
		// the mailboxes to choose from; each opens its administrator's page.
		// One with a mailbox of their own has the holder's page for it, as
		// every holder does: its Signing in, Private mailbox and Throwaway
		// addresses are a holder's, and the administrator's page for the
		// same mailbox is under Accounts.
		writeOSHTML(w, r, adminOSLayout(nonce, "Mailbox settings", "vayumail", cfg, htmpl.HTML(a.mailboxSettingsPicker(r.Context()))))
		return
	}
	if !ok {
		writeOSHTML(w, r, adminOSLayout(nonce, "Mailbox settings", "vayumail", cfg, htmpl.HTML(
			`<div class="page-header"><h1>Mailbox settings</h1></div>`+
				`<div class="empty-state">No mailbox has been given to your account yet. An administrator gives one under <strong>Members → Team &amp; roles</strong>.</div>`)))
		return
	}
	sec, _ := a.mailboxSettingsSections(r.Context(), ac.Email, true)
	head := `<div class="page-header"><h1>Settings</h1></div>` +
		`<div class="card vm-mbox-head">` + mailAvatarImg(ac.Email, a.mailboxAvatarSet()) +
		`<div class="vm-mbox-head__meta"><div class="vm-mbox-head__email">` + esc(ac.Email) + `</div></div></div>`
	signinItems := []mailSetting{
		{Name: "Security checklist", Find: "score security check", HTML: a.securityChecklist(r, ac)},
		{Name: "Where you are signed in", Find: "sessions devices sign out everywhere", HTML: a.sessionsBlock(r)},
		{Name: "Password", Find: "change password", HTML: holderPasswordForm(), Bare: true},
		// Two-factor stays on the site's account page (handlers_portal_totp.go),
		// where turning it off asks for a current code; a second way to change
		// the factor from here would be the weaker of the two.
		{Name: "Two-factor", Find: "2fa code authenticator totp", HTML: `<p class="text-sm">` + map[bool]string{true: "On.", false: "Off."}[ac.TOTPEnabled] +
			` It is set on <a href="/members/account">your account on the site</a>, where turning it off asks for a current code.</p>`},
		{Name: "Phones and mail apps", Find: "app passwords recovery connect device imap", HTML: `<p class="text-sm">App passwords, recovery and setting up a phone are on <a href="/os/vayumail/connect">Connect a device</a>.</p>`},
	}
	script := ""
	if pk := a.passkeysBlock(r); pk != "" {
		signinItems = append(signinItems, mailSetting{Name: "Passkeys", Find: "passkey only security key fingerprint face", HTML: pk, Bare: true})
		script = `<script nonce="` + nonce + `" src="/os/static/js/passkey.js?v=` + assetVer("js/passkey.js") + `"></script>`
	}
	after := mailSettingGroup("signin", "Signing in", signinItems...) +
		mailSettingGroup("private", "Privacy",
			mailSetting{Name: "Private mailbox", Find: "private privacy administrator admin access handover", HTML: a.privateMailboxBlock(r, ac.Email), Bare: true}) +
		mailSettingGroup("throwaway", "Throwaway addresses",
			mailSetting{Name: "Throwaway addresses", Find: "alias disposable website shop sign up", HTML: a.throwawayBlock(a.mailReader(r, "")), Bare: true}) +
		mailSettingGroup("tools", "Connected tools",
			mailSetting{Name: "Connected tools", Find: "ai assistant mcp agent connected apps oauth disconnect", HTML: a.connectedToolsHere(r), Bare: true}) +
		mailSettingGroup("export", "Import and export",
			mailSetting{Name: "Bring mail in", Find: "import imap mbox", HTML: `<p class="text-sm"><a href="/os/vayumail/import?user=` + qparam(ac.Email) + `">Bring mail in</a> from another mail account or a file.</p>`},
			mailSetting{Name: "Download all mail", Find: "export backup mbox zip", HTML: `<p class="text-sm"><a href="/os/vayumail/download?user=` + qparam(ac.Email) + `" download>Download all mail</a>, every folder as an mbox file in one zip.</p>`})
	nav := append(append([][2]string{}, mailSettingsNav...), [2]string{"signin", "Signing in"}, [2]string{"private", "Privacy"}, [2]string{"throwaway", "Throwaway addresses"}, [2]string{"tools", "Connected tools"}, [2]string{"export", "Import and export"})
	body := mailSettingsLayout(head, sec, after, nav) + script +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Settings", "vayumail", cfg, htmpl.HTML(body)))
}

// mailboxSettingsPicker is Mailbox settings for an administrator with no
// mailbox of their own: every mailbox, each opening its settings.
func (a *App) mailboxSettingsPicker(ctx context.Context) string {
	var b strings.Builder
	b.WriteString(`<div class="page-header"><h1>Mailbox settings</h1></div>`)
	b.WriteString(`<p class="page-sub">Choose a mailbox: forwarding, vacation reply, aliases, filters, recovery, keys and its picture.</p>`)
	accs, err := a.vayuMail.Accounts().List(ctx)
	if err != nil {
		b.WriteString(saCallout("danger", html.EscapeString("Could not read the mailboxes: "+err.Error())))
		return b.String()
	}
	if len(accs) == 0 {
		b.WriteString(`<div class="empty-state">No mailbox exists yet. <a href="/os/vayumail/accounts">Create one under Administration</a>.</div>`)
		return b.String()
	}
	b.WriteString(`<div class="card"><ul class="vm-mbox-pick">`)
	for _, ac := range accs {
		b.WriteString(`<li><a href="/os/vayumail/accounts/settings?user=` + qparam(ac.Email) + `">` +
			mailAvatarImg(ac.Email, a.mailboxAvatarSet()) + `<span class="mono">` + esc(ac.Email) + `</span></a></li>`)
	}
	b.WriteString(`</ul></div>`)
	return b.String()
}

// holderPasswordForm is the holder's own password change. It answers into
// #vm-pass-result, because the change signs every session out, this one too.
func holderPasswordForm() string {
	return `<details class="vm-ooo vm-acct__sub" id="vm-pass"><summary><span class="field-label">Password</span></summary>` +
		`<form class="vm-ooo-form" hx-post="/os/vayumail/settings/password" hx-target="#vm-pass-result" hx-swap="innerHTML">` +
		`<label class="field"><span class="field-label">Current password</span><input class="input input--sm" type="password" name="current" autocomplete="current-password" required></label>` +
		`<label class="field"><span class="field-label">New password</span><input class="input input--sm" type="password" name="new" autocomplete="new-password" minlength="8" required></label>` +
		`<label class="field"><span class="field-label">New password again</span><input class="input input--sm" type="password" name="again" autocomplete="new-password" minlength="8" required></label>` +
		`<p class="text-xs muted">Changing it signs out every app, device and web session on this mailbox, this one too, and sends you a notice.</p>` +
		`<button class="btn btn--primary btn--sm" type="submit">Change password</button></form>` +
		`<div id="vm-pass-result" aria-live="polite"></div></details>`
}

// handleVayuOSMyPassword changes the holder's own password, proven by the
// current one, through applyMailPasswordReset: the one path every mailbox
// password change takes, so app passwords, sessions and reset links are
// revoked and the holder is told, as for any reset (3cl).
func (a *App) handleVayuOSMyPassword(w http.ResponseWriter, r *http.Request) {
	_, own := a.ownMailbox(r)
	if own == "" || !a.mailRunning() || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "no mailbox of your own here", "")
		return
	}
	_ = r.ParseForm()
	answer := func(tone, msg string) { writeOSHTML(w, r, saCallout(tone, html.EscapeString(msg))) }
	if r.FormValue("new") != r.FormValue("again") {
		answer("danger", "The two new passwords differ.")
		return
	}
	// The current password is a guess like any sign-in's: the same per-mailbox
	// throttle, so a session left open is not an unmetered oracle for it.
	if d := mailAuthThrottle.Delay(own); d > 0 {
		time.Sleep(d)
	}
	if !auth.VerifySecretArgon2id(r.FormValue("current"), a.vayuMail.Accounts().HashFor(r.Context(), own)) {
		mailAuthThrottle.Fail(own)
		answer("danger", "That is not the current password.")
		return
	}
	mailAuthThrottle.Success(own)
	deps, ok := a.mailResetDepsFor()
	if !ok {
		answer("danger", "Mail is not running.")
		return
	}
	// A sealed mailbox's key is locked with this password: it moves to the new
	// one first, and back if the change itself is refused, so the key and the
	// password never part.
	holder := a.vayuPGP != nil && a.vayuPGP.HolderKeeps(own)
	if holder {
		if err := a.vayuPGP.ChangeHolderPassword(own, r.FormValue("current"), r.FormValue("new")); err != nil {
			answer("danger", "Not changed: your mail is locked under an earlier password. Open it with your recovery code first.")
			return
		}
	}
	out, err := applyMailPasswordReset(r.Context(), deps, own, r.FormValue("new"), mailResetByHolder, own)
	if err != nil {
		if holder {
			_ = a.vayuPGP.ChangeHolderPassword(own, r.FormValue("new"), r.FormValue("current"))
		}
		answer("danger", "Not changed: "+err.Error()+".")
		return
	}
	writeOSHTML(w, r, saCallout("ok", html.EscapeString(fmt.Sprintf(
		"Password changed. %d app%s and %d session%s were signed out, this one too.",
		out.AppPasswordsRevoked, plural(out.AppPasswordsRevoked), out.SessionsRevoked, plural(out.SessionsRevoked))))+
		` <a class="btn btn--sm" href="/os/login">Sign in again</a>`)
}

// handleVayuOSMailboxSettings renders one mailbox's full settings page.
func (a *App) handleVayuOSMailboxSettings(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Mailbox settings</h1></div>`)
	body.WriteString(`<p class="page-sub">Everything that belongs to one address — forwarding, vacation, aliases, filters, recovery, handover, PGP and its picture.</p>`)

	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		a.writeMailSetup(w, r, "Mailbox settings")
		return
	}
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os/vayumail/accounts")
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("user"))
	ac, ok := a.mailAccountByEmail(r.Context(), email)
	if !ok {
		body.WriteString(`<div class="empty-state">No mailbox with that address. <a href="/os/vayumail/accounts">Back to mail accounts</a></div>`)
		writeOSHTML(w, r, adminOSLayout(nonce, "Mailbox settings", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	// Identity header: who this is, whether it is active, and its storage.
	badge := `<span class="badge badge--ok">Active</span>`
	if !ac.Active {
		badge = `<span class="badge badge--warn">Disabled</span>`
	}
	if ac.TOTPEnabled {
		badge += `<span class="badge badge--info">2FA</span>`
	}
	used := a.vayuMail.MailboxUsage(ac.Email)
	quota := a.vayuMail.MailboxQuota(ac.Email)
	storage := humanBytes(used)
	if quota > 0 {
		storage += " of " + humanBytes(quota)
	} else {
		storage += " (no limit)"
	}
	body.WriteString(`<div class="card vm-mbox-head">` +
		mailAvatarImg(ac.Email, a.mailboxAvatarSet()) +
		`<div class="vm-mbox-head__meta"><div class="vm-mbox-head__email mono">` + esc(ac.Email) + `</div>` +
		`<div class="vm-mbox-head__badges">` + badge + `</div>` +
		`<div class="muted text-sm">Storage: ` + esc(storage) + `</div></div>` +
		`<a class="btn btn--sm" href="/os/vayumail/accounts">← All mailboxes</a>` +
		`</div>`)

	sec, _ := a.mailboxSettingsSections(r.Context(), ac.Email, false)
	head := body.String()
	body.Reset()
	// The administrator's own mailbox carries their passkeys, as a holder's
	// page does: an administrator opening Mail › Settings lands here, and
	// had no way to add one from Mail. Another person's mailbox never shows
	// them: a passkey is the signed-in account's, and the block lists only
	// the session's own.
	after := ""
	if a.isOwnMailbox(r, ac.Email) {
		if pk := a.passkeysBlock(r); pk != "" {
			after = mailSettingGroup("signin", "Signing in", mailSetting{Name: "Passkeys", Find: "passkey only security key fingerprint face", HTML: pk, Bare: true})
			after += `<script nonce="` + nonce + `" src="/os/static/js/passkey.js?v=` + assetVer("js/passkey.js") + `"></script>`
		}
	}
	body.WriteString(mailSettingsLayout(head, sec, after, mailSettingsNav))

	// This page hosts the recovery card too, whose controls are driven by
	// admin-os-mail-recovery.js. Loading only admin-os-mail.js would leave them as
	// dead buttons — the exact kind of "it renders, so it must work" trap.
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail-recovery.js?v=` + assetVer("js/admin-os-mail-recovery.js") + `"></script>`)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Mailbox settings", "vayuos", cfg, htmpl.HTML(body.String())))
}

// mailSetting is one setting on the Settings page: the name it is shown and
// found under, more words it is found by, and its control. Bare: the control
// carries its own heading.
type mailSetting struct {
	Name, Find, HTML string
	Bare             bool
}

// mailSettingGroup is one section of the Settings page.
func mailSettingGroup(id, title string, items ...mailSetting) string {
	var b strings.Builder
	b.WriteString(`<section class="mx-set" id="set-` + id + `" aria-labelledby="set-` + id + `-t"><h2 class="mx-set__title" id="set-` + id + `-t">` + esc(title) + `</h2>`)
	for _, it := range items {
		if it.HTML == "" {
			continue
		}
		b.WriteString(`<div class="mx-set__item" data-mx-set="` + esc(strings.ToLower(it.Name+" "+it.Find)) + `">`)
		if !it.Bare {
			b.WriteString(`<h3 class="mx-set__name">` + esc(it.Name) + `</h3>`)
		}
		b.WriteString(it.HTML + `</div>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

// autosaveForm marks a one-value setting's form to save as it changes on
// the Settings page (admin-os-mail.js), where its Save button is not shown;
// the accounts list, which shares the builder, keeps the button.
//
// Such a form says so (autosaveHeader), and a save of it that succeeds is
// not redrawn (quietWhenSaved): a redraw replaced the form while the person
// was still in it, taking their place and what they had typed since. Its
// saves queue on the form, which therefore stays; an error is still drawn.
func autosaveForm(form string) string {
	return strings.Replace(form, "<form ", autosaveAttrs+" ", 1)
}

// autosaveAttrs marks a form that saves as it changes.
const autosaveAttrs = `<form data-mx-autosave hx-sync="this:queue last" hx-headers='{"` + autosaveHeader + `":"1"}'`

// autosaveHeader is sent by a form that saves as it changes.
const autosaveHeader = "X-Mail-Autosave"

// quietWhenSaved tells htmx to leave the page as it is after a save sent as a
// field changed that succeeded; a failed one is redrawn with its message.
func quietWhenSaved(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil && r.Header.Get(autosaveHeader) == "1" {
		w.Header().Set("HX-Reswap", "none")
	}
}

// openSection draws a setting's collapsible section open: on the Settings
// page it is what the person came for, and every save redraws the page's
// settings, which folded a closed-by-default section shut under them.
func openSection(card string) string {
	return strings.Replace(card, "<details ", "<details open ", 1)
}

// mailSettingsLayout is the Settings page: the sections listed on the left
// under a field that finds any setting by name, and the settings beside
// them. head comes first in the column; swapped is the mailbox's own
// sections, which every save redraws (#vm-mbox-settings); after holds the
// sections the page adds; nav names every section, in order.
func mailSettingsLayout(head, swapped, after string, nav [][2]string) string {
	var b strings.Builder
	b.WriteString(`<div class="mx-settings"><nav class="mx-settings__nav" aria-label="Settings sections">` +
		`<label class="mx-settings__find">` + saIcon("search") + `<input type="search" placeholder="Find a setting" aria-label="Find a setting" autocomplete="off" data-mx-set-find></label>`)
	for _, n := range nav {
		b.WriteString(`<a class="sa-appside__item" href="#set-` + n[0] + `"><span class="sa-appside__label">` + esc(n[1]) + `</span></a>`)
	}
	b.WriteString(`</nav><div class="mx-settings__body">` + head +
		`<p class="mx-settings__none" data-mx-set-none hidden>No setting is called that.</p>` +
		`<span id="vm-mbox-spin" class="htmx-indicator vm-spin" aria-hidden="true">saving…</span>` +
		`<div id="vm-mbox-settings">` + swapped + `</div>` + after +
		`<p class="mx-saved" role="status" data-mx-saved hidden><span>Saved.</span> <button type="button" class="mx-head__link" data-mx-undo>Undo</button></p>` +
		`</div></div>`)
	return b.String()
}

// mailSettingsNav is the sections every mailbox's Settings has.
var mailSettingsNav = [][2]string{{"general", "General"}, {"addresses", "Addresses"}, {"rules", "Rules and replies"}, {"privacy", "Privacy and security"}}
