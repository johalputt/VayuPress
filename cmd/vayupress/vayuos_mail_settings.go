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
	"html"
	"net/http"
	"strings"

	htmpl "html/template"

	avatarpkg "github.com/johalputt/vayupress/internal/avatar"
	"github.com/johalputt/vayupress/internal/render"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailboxSettingsSections builds every per-mailbox setting as one fragment. The
// bool reports whether the mailbox exists.
func (a *App) mailboxSettingsSections(ctx context.Context, email string) (string, bool) {
	ac, ok := a.mailAccountByEmail(ctx, email)
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString(`<div class="vm-acct__sub"><span class="field-label">Auto-forward a copy to</span>` + vayuCardForwarding(ac) + `</div>`)
	b.WriteString(a.vayuCardVacation(ctx, ac))
	b.WriteString(a.vayuCardAliases(ctx, ac))
	b.WriteString(a.vayuCardRecovery(ctx, ac.Email))
	b.WriteString(a.vayuCardHandover(ctx, ac))
	b.WriteString(a.vayuCardPGP(ac))
	b.WriteString(a.vayuCardFilters(ctx, ac))
	b.WriteString(a.mailboxAvatarSettings(ac))

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
			`hx-post="/os/vayumail/accounts/avatar/cartoon" ` + hxVals("email", ac.Email, "n", ns) +
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
func (a *App) acctRefresh(r *http.Request, email string) string {
	if r.Header.Get("HX-Target") == "vm-mbox-settings" && strings.TrimSpace(email) != "" {
		if sec, ok := a.mailboxSettingsSections(r.Context(), email); ok {
			return `<div id="vm-mbox-settings">` + sec + `</div>`
		}
	}
	return a.vayuAccountsList(r.Context())
}

// handleVayuOSMailboxSettings renders one mailbox's full settings page.
func (a *App) handleVayuOSMailboxSettings(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Mailbox settings</h1></div>`)
	body.WriteString(`<p class="page-sub">Everything that belongs to one address — forwarding, vacation, aliases, filters, recovery, handover, PGP and its picture.</p>`)
	body.WriteString(a.vayuosNav(r, "accounts"))

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		body.WriteString(`<div class="empty-state">VayuMail is inactive.</div>`)
		writeOSHTML(w, r, adminOSLayout(nonce, "Mailbox settings", "vayuos", cfg, htmpl.HTML(body.String())))
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
	esc := html.EscapeString
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

	sec, _ := a.mailboxSettingsSections(r.Context(), ac.Email)
	body.WriteString(`<span id="vm-mbox-spin" class="htmx-indicator vm-spin" aria-hidden="true">saving…</span>`)
	body.WriteString(`<div id="vm-mbox-settings">` + sec + `</div>`)

	// This page hosts the recovery card too, whose controls are driven by
	// admin-os-mail-recovery.js. Loading only admin-os-mail.js would leave them as
	// dead buttons — the exact kind of "it renders, so it must work" trap.
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail-recovery.js?v=` + assetVer("js/admin-os-mail-recovery.js") + `"></script>`)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`)
	writeOSHTML(w, r, adminOSLayout(nonce, "Mailbox settings", "vayuos", cfg, htmpl.HTML(body.String())))
}
