// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_smime.go — a mailbox's S/MIME certificate on its settings page
// (internal/vayuos/mail/smime.go): uploaded as the .p12 or .pfx its authority
// or browser exports, shown by its issuer and expiry, removed. A mailbox that
// keeps one signs with it whenever Sign is ticked in compose.

import (
	"context"
	"github.com/johalputt/vayupress/internal/i18n"
	"io"
	"net/http"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// vayuCardSMIME is the certificate's section on the mailbox's settings page.
func (a *App) vayuCardSMIME(ctx context.Context, ac vmail.Account) string {
	lang := i18n.Lang(ctx)
	head := `<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">` + esc(i18n.T(lang, "S/MIME certificate")) + `</span> `
	form := `<form class="vm-row" hx-post="/os/vayumail/accounts/smime" hx-encoding="multipart/form-data" hx-target="#vm-mbox-settings" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + esc(ac.Email) + `">` +
		`<input class="input input--sm" type="file" name="p12" accept=".p12,.pfx,application/x-pkcs12" aria-label="` + esc(i18n.T(lang, "Certificate file (.p12 or .pfx)")) + `" required>` +
		`<input class="input input--sm" type="password" name="password" placeholder="` + esc(i18n.T(lang, "The file's password")) + `" aria-label="` + esc(i18n.T(lang, "The file's password")) + `" autocomplete="off">` +
		`<button class="btn btn--sm" type="submit">` + esc(i18n.T(lang, "Upload")) + `</button></form>`
	cert, ok := a.vayuMail.Accounts().SMIMECertFor(ctx, ac.Email)
	if !ok {
		return head + `<span class="muted text-sm">` + esc(i18n.T(lang, "None")) + `</span></summary>` +
			`<p class="text-sm muted">` + esc(i18n.T(lang, "For correspondents whose organisations use S/MIME: a certificate for {address} from a certificate authority, as the .p12 or .pfx file it exports. Once it is here, Sign in compose signs with it.", "address", ac.Email)) + `</p>` + form + `</details>`
	}
	return head + `<span class="muted text-sm">` + esc(i18n.T(lang, "From {issuer}", "issuer", cert.Issuer)) + `</span></summary>` +
		`<p class="text-sm">` + esc(i18n.T(lang, "Issued by {issuer}, valid until {date}. Sign in compose signs with it rather than the OpenPGP key.", "issuer", cert.Issuer, "date", i18n.Date(lang, cert.NotAfter, i18n.DayMonthYearShort))) + `</p>` +
		`<div class="vm-row"><button type="button" class="btn btn--sm btn--ghost" hx-post="/os/vayumail/accounts/smime/remove"` + hxVals("email", ac.Email) +
		` hx-target="#vm-mbox-settings" hx-swap="innerHTML">` + esc(i18n.T(lang, "Remove certificate")) + `</button></div>` +
		`<p class="text-xs muted">` + esc(i18n.T(lang, "Replace it with a new file:")) + `</p>` + form + `</details>`
}

// smimeAccount is the mailbox a certificate request names, if this session
// may change it: its holder (3cl), or an administrator, except on a mailbox
// handed to its holder, which signs only with what its holder gives it.
func (a *App) smimeAccount(w http.ResponseWriter, r *http.Request) string {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return ""
	}
	email := a.avatarAccountEmail(w, r, r.FormValue("email"))
	if email == "" {
		return ""
	}
	if a.isMailboxHolder(r, email) {
		return email
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Only this mailbox's holder or an administrator can change its settings."), "")
		return ""
	}
	if a.vayuMail.IsHandedOver(email) {
		writeAPIError(w, r, http.StatusForbidden, "handed-over", i18n.T(i18n.Lang(r.Context()), "This mailbox has been handed to its holder; its certificate is theirs to give."), "")
		return ""
	}
	return email
}

// handleVayuOSSMIMEUpload keeps the uploaded certificate as the mailbox's.
func (a *App) handleVayuOSSMIMEUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if err := r.ParseMultipartForm(128 << 10); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "too-large", i18n.T(i18n.Lang(r.Context()), "That file is too large to be a certificate."), "")
		return
	}
	email := a.smimeAccount(w, r)
	if email == "" {
		return
	}
	file, _, err := r.FormFile("p12")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "no-file", i18n.T(i18n.Lang(r.Context()), "No certificate file was given."), "")
		return
	}
	defer file.Close()
	p12, _ := io.ReadAll(file)
	if _, err := a.vayuMail.Accounts().ImportSMIME(r.Context(), email, p12, r.FormValue("password"), time.Now()); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "smime-refused", i18n.T(i18n.Lang(r.Context()), "Not kept: {reason}.", "reason", err.Error()), "")
		return
	}
	writeOSHTML(w, r, a.acctRefresh(r, email))
}

// handleVayuOSSMIMERemove forgets the mailbox's certificate and key.
func (a *App) handleVayuOSSMIMERemove(w http.ResponseWriter, r *http.Request) {
	email := a.smimeAccount(w, r)
	if email == "" {
		return
	}
	if err := a.vayuMail.Accounts().DeleteSMIME(r.Context(), email); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "remove-failed", i18n.T(i18n.Lang(r.Context()), "The certificate could not be removed."), "")
		return
	}
	writeOSHTML(w, r, a.acctRefresh(r, email))
}
