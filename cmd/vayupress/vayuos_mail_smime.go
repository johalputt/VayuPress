// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_smime.go — a mailbox's S/MIME certificate on its settings page
// (internal/vayuos/mail/smime.go): uploaded as the .p12 or .pfx its authority
// or browser exports, shown by its issuer and expiry, removed. A mailbox that
// keeps one signs with it whenever Sign is ticked in compose.

import (
	"context"
	"io"
	"net/http"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// vayuCardSMIME is the certificate's section on the mailbox's settings page.
func (a *App) vayuCardSMIME(ctx context.Context, ac vmail.Account) string {
	head := `<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">S/MIME certificate</span> `
	form := `<form class="vm-row" hx-post="/os/vayumail/accounts/smime" hx-encoding="multipart/form-data" hx-target="#vm-mbox-settings" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + esc(ac.Email) + `">` +
		`<input class="input input--sm" type="file" name="p12" accept=".p12,.pfx,application/x-pkcs12" aria-label="Certificate file (.p12 or .pfx)" required>` +
		`<input class="input input--sm" type="password" name="password" placeholder="The file's password" aria-label="The file's password" autocomplete="off">` +
		`<button class="btn btn--sm" type="submit">Upload</button></form>`
	cert, ok := a.vayuMail.Accounts().SMIMECertFor(ctx, ac.Email)
	if !ok {
		return head + `<span class="muted text-sm">None</span></summary>` +
			`<p class="text-sm muted">For correspondents whose organisations use S/MIME: a certificate for ` + esc(ac.Email) +
			` from a certificate authority, as the .p12 or .pfx file it exports. Once it is here, Sign in compose signs with it.</p>` + form + `</details>`
	}
	return head + `<span class="muted text-sm">From ` + esc(cert.Issuer) + `</span></summary>` +
		`<p class="text-sm">Issued by ` + esc(cert.Issuer) + `, valid until ` + esc(cert.NotAfter.Format("2 January 2006")) +
		`. Sign in compose signs with it rather than the OpenPGP key.</p>` +
		`<div class="vm-row"><button type="button" class="btn btn--sm btn--ghost" hx-post="/os/vayumail/accounts/smime/remove"` + hxVals("email", ac.Email) +
		` hx-target="#vm-mbox-settings" hx-swap="innerHTML">Remove certificate</button></div>` +
		`<p class="text-xs muted">Replace it with a new file:</p>` + form + `</details>`
}

// smimeAccount is the mailbox a certificate request names, if an
// administrator may change it: a mailbox handed to its holder signs only
// with what its holder gives it.
func (a *App) smimeAccount(w http.ResponseWriter, r *http.Request) string {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return ""
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return ""
	}
	email := a.avatarAccountEmail(w, r, r.FormValue("email"))
	if email == "" {
		return ""
	}
	if a.vayuMail.IsHandedOver(email) {
		writeAPIError(w, r, http.StatusForbidden, "handed-over", "This mailbox has been handed to its holder; its certificate is theirs to give.", "")
		return ""
	}
	return email
}

// handleVayuOSSMIMEUpload keeps the uploaded certificate as the mailbox's.
func (a *App) handleVayuOSSMIMEUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	if err := r.ParseMultipartForm(128 << 10); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "too-large", "That file is too large to be a certificate.", "")
		return
	}
	email := a.smimeAccount(w, r)
	if email == "" {
		return
	}
	file, _, err := r.FormFile("p12")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "no-file", "No certificate file was given.", "")
		return
	}
	defer file.Close()
	p12, _ := io.ReadAll(file)
	if _, err := a.vayuMail.Accounts().ImportSMIME(r.Context(), email, p12, r.FormValue("password"), time.Now()); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "smime-refused", "Not kept: "+err.Error()+".", "")
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
		writeAPIError(w, r, http.StatusInternalServerError, "remove-failed", "The certificate could not be removed.", "")
		return
	}
	writeOSHTML(w, r, a.acctRefresh(r, email))
}
