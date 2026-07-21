package main

// handlers_portal_totp.go — member self-serve two-factor authentication.
//
// A paid member who holds a real VayuMail mailbox (a "mail ID") signs in to the
// site with that address + password via handleMemberVayuMailLogin. This file
// lets them turn on TOTP 2FA for that mailbox themselves, from the member
// dashboard, WITH a scannable QR — the same enrolment the operator can drive
// from VayuOS, but member-authenticated and only ever acting on the caller's own
// mailbox. The QR + otpauth:// label (issuer = domain, account = email) let an
// authenticator app add the account by scanning, with the name pre-filled.
//
// 2FA here is the SAME second factor the mailbox-credential login already
// enforces (handleMemberVayuMailLogin) — enrolling makes "Sign in with VayuMail"
// ask for a 6-digit code. The passwordless magic-link path is unaffected.
//
// CSRF: these are member-session (SameSite=Lax) JSON endpoints reached only by
// the same-origin dashboard fetch, matching the sibling member POST routes
// (avatar, ads, mailbox claim). Disabling requires the current code so a stolen
// session cannot silently strip the second factor; a member who has lost their
// authenticator recovers via the operator (VayuOS → mailbox → disable 2FA).

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/totp"
)

// memberOwnMailbox returns the signed-in member's own mailbox address when they
// actually hold a VayuMail mailbox. Both conditions gate self-serve 2FA: the
// second factor protects the mailbox-credential login, so only a member who owns
// that credential may enrol, and only ever for their own address.
func (a *App) memberOwnMailbox(r *http.Request) (email string, ok bool) {
	m := a.resolveMember(r)
	if m == nil || !a.vayuMailLoginEnabled() {
		return "", false
	}
	// Prefer the mailbox explicitly linked to this member (a claimed / purchased
	// address, which can differ from the login email); fall back to the login
	// address itself when the member signed in with a VayuMail credential — then
	// the login email IS the mailbox.
	addr := ""
	if a.members != nil {
		addr = strings.ToLower(strings.TrimSpace(a.members.MailAddressFor(r.Context(), m.Email)))
	}
	if addr == "" {
		addr = strings.ToLower(strings.TrimSpace(m.Email))
	}
	if addr == "" || a.vayuMail.Accounts().RoleFor(r.Context(), addr) == "" {
		return "", false
	}
	return addr, true
}

// handleMemberTOTPBegin generates a fresh, still-disabled secret for the member's
// own mailbox and returns the manual key, the otpauth:// URI and a CSP-safe QR
// data: image so an authenticator app can add the account by scanning.
func (a *App) handleMemberTOTPBegin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	email, ok := a.memberOwnMailbox(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "no-mailbox", "Two-factor is available once you have a VayuMail address", "")
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "secret-error", "Could not start 2FA setup", "")
		return
	}
	if err := a.vayuMail.Accounts().SetTOTPSecret(r.Context(), email, secret); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "store-error", "Could not start 2FA setup", "")
		return
	}
	uri := totp.ProvisioningURI(secret, a.vayuMail.Config().Domain, email)
	writeJSON(w, r, http.StatusOK, map[string]string{"secret": secret, "uri": uri, "qr": qrDataURI(uri)})
}

// handleMemberTOTPVerify validates the submitted code against the pending secret
// and, on success, enables 2FA for the member's mailbox.
func (a *App) handleMemberTOTPVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	email, ok := a.memberOwnMailbox(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "no-mailbox", "Two-factor is available once you have a VayuMail address", "")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	accts := a.vayuMail.Accounts()
	secret, _ := accts.TOTPStatus(r.Context(), email)
	if secret == "" {
		writeAPIError(w, r, http.StatusBadRequest, "no-pending", "Start 2FA setup first", "")
		return
	}
	if !totp.Validate(secret, strings.TrimSpace(body.Code)) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-code", "That code is not valid — try the current one", "")
		return
	}
	if err := accts.EnableTOTP(r.Context(), email); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "enable-error", "Could not enable 2FA", "")
		return
	}
	logging.LogInfo("members", "member enabled mailbox 2FA: "+email)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "enabled"})
}

// handleMemberTOTPDisable turns 2FA off for the member's own mailbox. It requires
// the current 6-digit code so a hijacked session cannot silently remove the
// second factor; a member who lost their authenticator disables it via the
// operator instead.
func (a *App) handleMemberTOTPDisable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	email, ok := a.memberOwnMailbox(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "no-mailbox", "Two-factor is available once you have a VayuMail address", "")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	accts := a.vayuMail.Accounts()
	secret, enabled := accts.TOTPStatus(r.Context(), email)
	if !enabled || secret == "" {
		// Nothing to do — already off. Report success so the UI settles correctly.
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "disabled"})
		return
	}
	if !totp.Validate(secret, strings.TrimSpace(body.Code)) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-code", "Enter your current 6-digit code to turn 2FA off", "")
		return
	}
	if err := accts.DisableTOTP(r.Context(), email); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "disable-error", "Could not disable 2FA", "")
		return
	}
	logging.LogInfo("members", "member disabled mailbox 2FA: "+email)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "disabled"})
}
