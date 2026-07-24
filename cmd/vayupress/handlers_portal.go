package main

// handlers_portal.go — the VayuPortal membership overlay backend.
//
// VayuPortal is the reader-facing, Ghost-style membership widget (a floating
// button + slide-in panel) rendered on every public page. It is purely
// client-side (static/js/portal.js) and talks to three small endpoints:
//
//   - GET  /api/v1/members/me            current auth + capability snapshot
//   - POST /api/v1/members/vayumail-login sign in with a VayuMail mailbox
//                                          credential (+ TOTP when 2FA is on)
//   - GET  /static/js/portal.js          the widget script (same-origin, no nonce)
//
// The passwordless magic-link flow (handleMemberLogin) and sign-out
// (handleMemberLogout) are reused unchanged; this file only adds the snapshot
// endpoint and the credential ("Sign in with VayuMail") path.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/totp"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// setMemberSessionCookie writes the member session cookie with the same
// security attributes used elsewhere, so portal logins and magic-link logins
// produce interchangeable sessions.
func (a *App) setMemberSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: memberCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: csrfCookieSecure(), SameSite: http.SameSiteLaxMode,
		MaxAge: int(members.SessionTTL.Seconds()),
	})
}

// vayuMailLoginEnabled reports whether the "Sign in with VayuMail" option can be
// offered: the mail engine must be active and have an account store.
func (a *App) vayuMailLoginEnabled() bool {
	return a.vayuMail != nil && a.vayuMail.Config().Enabled && a.vayuMail.Accounts() != nil
}

// membershipEnabled mirrors the operator's "show membership" setting that also
// gates the nav Sign in / Sign up buttons.
func (a *App) membershipEnabled(r *http.Request) bool {
	if a.siteSettings == nil {
		return false
	}
	return a.siteSettings.Get(r.Context(), settings.KeyMembershipButtons) == "true"
}

// handleMemberMe returns a small JSON snapshot the portal uses to decide what to
// render: whether membership is enabled at all, whether the VayuMail credential
// option is available, and — when a member session cookie is present — the
// signed-in member's public profile. It never requires the operator API key.
func (a *App) handleMemberMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	resp := map[string]interface{}{
		"enabled":          a.membershipEnabled(r),
		"vayumail_enabled": a.vayuMailLoginEnabled(),
		"authenticated":    false,
	}
	if m := a.resolveMember(r); m != nil {
		resp["authenticated"] = true
		resp["member"] = a.memberSnapshot(r, m)
	} else if u := a.resolveConsoleUser(r); u != nil {
		// A VayuOS operator/staff user is signed in (console session) even though
		// they hold no reader "member" session. Reflect that on the public site so
		// the nav shows their identity + a shortcut to the dashboard instead of
		// "Sign in / Sign up" — "recognise me wherever I am on my own site".
		resp["authenticated"] = true
		resp["member"] = operatorSnapshot(u)
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// operatorSnapshot builds the public account chip for a signed-in VayuOS
// operator/staff user. The console_url flag makes the nav chip link straight to
// the dashboard (instead of the reader account panel); avatar, when set, lets
// the chip show their profile picture.
func operatorSnapshot(u *users.User) map[string]interface{} {
	name := u.Name
	if name == "" {
		name = u.Email
	}
	m := map[string]interface{}{
		"email":       u.Email,
		"name":        name,
		"operator":    true,
		"console_url": "/os",
	}
	if u.AvatarURL != "" {
		m["avatar"] = u.AvatarURL
	}
	return m
}

// memberSnapshot builds the public member object the portal renders. When the
// member also holds a VayuMail mailbox it advertises the role so the portal can
// offer an "Open VayuMail" (or full VayuOS console) shortcut — used by both the
// /me snapshot and the VayuMail login response so the console button appears
// immediately after signing in, not only after a page reload.
func (a *App) memberSnapshot(r *http.Request, m *members.Member) map[string]interface{} {
	mem := map[string]interface{}{
		"email": m.Email,
		"name":  m.DisplayName(),
		"tier":  m.Tier,
		"paid":  m.IsPaid(),
	}
	// Public profile picture: when the member is also a CMS user (typically the
	// owner/staff who signed into the portal), surface their avatar — the same
	// public URL shown on their /author page — so the account panel and nav chip
	// show their photo instead of only an initial.
	if a.userStore != nil {
		if cu, err := a.userStore.GetByEmail(r.Context(), m.Email); err == nil && cu != nil && cu.AvatarURL != "" {
			mem["avatar"] = cu.AvatarURL
		}
	}
	if a.vayuMailLoginEnabled() {
		if role := a.vayuMail.Accounts().RoleFor(r.Context(), m.Email); role != "" {
			_, console := mailConsoleAccess(role)
			mem["mail"] = map[string]interface{}{
				"role":    role,
				"admin":   role == vmail.RoleAdministrator,
				"console": console,
			}
		}
	}
	return mem
}

// handleMemberComments returns the signed-in member's own comments (any status),
// newest first, so the portal's Activity tab can show them where they commented
// and whether each is still pending review or live. Reads via the read pool.
func (a *App) handleMemberComments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Resolve the signed-in principal the same way commenting does — a reader
	// member OR a console operator — so an operator sees their own comments here
	// too (their comments are stored under their console email, which resolveMember
	// alone never matched, so the tab always read "you haven't commented yet").
	who := a.resolveCommenter(r)
	if who == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "not-signed-in", "Sign in to view your activity", "")
		return
	}
	if a.commentStore == nil {
		writeJSON(w, r, http.StatusOK, map[string]interface{}{"comments": []interface{}{}})
		return
	}
	list, err := a.commentStore.ListByEmail(r.Context(), dbpkg.Reader(), who.Email, 100)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", "Could not load your activity", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"comments": list, "count": len(list)})
}

// handleMemberVayuMailLogin authenticates a reader against a VayuMail mailbox
// (email + password), enforcing TOTP when that account has 2FA enabled, and on
// success starts a member session. This lets people who already hold a VayuMail
// address sign in directly instead of waiting for a magic link.
//
// Responses are deliberately uniform on bad credentials so the endpoint cannot
// be used to enumerate which addresses exist. The one exception is the
// "totp-required" signal, which is only ever reached *after* the password has
// already been verified — so it leaks nothing to an attacker without the
// password.
func (a *App) handleMemberVayuMailLogin(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	if !a.vayuMailLoginEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayumail-disabled", "VayuMail sign-in is not available", "")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	emailAddr := strings.TrimSpace(strings.ToLower(body.Email))
	if emailAddr == "" || body.Password == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Email and password are required", "")
		return
	}

	accts := a.vayuMail.Accounts()
	hash := accts.HashFor(r.Context(), emailAddr)
	// Verify even when the account is unknown (hash == "") to keep the timing and
	// the response identical regardless of whether the address exists.
	if !auth.VerifySecretArgon2id(body.Password, hash) || hash == "" {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-credentials", "That email and password don't match", "")
		return
	}

	// Second factor, when the mailbox has 2FA enabled.
	if secret, enabled := accts.TOTPStatus(r.Context(), emailAddr); enabled {
		code := strings.TrimSpace(body.Code)
		if code == "" {
			writeAPIError(w, r, http.StatusUnauthorized, "totp-required", "This account uses two-factor authentication — enter your 6-digit code", "")
			return
		}
		if !totp.Validate(secret, code) {
			writeAPIError(w, r, http.StatusUnauthorized, "totp-invalid", "That code is not valid — try the current one", "")
			return
		}
	}

	// Credentials good: ensure a member record exists for this address and start
	// a session. The member's tier is whatever it already is (free by default);
	// holding a mailbox does not itself grant a paid plan.
	m, err := a.members.UpsertScoped(r.Context(), a.memberScope(r), emailAddr)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", "Could not sign you in", "")
		return
	}
	// Record coarse, GDPR-safe join location once (country/region/city; no IP).
	geo := geoFromHeaders(r)
	a.members.SetGeoIfEmpty(r.Context(), m.ID, geo.Country, geo.Region, geo.City)
	token, err := a.members.CreateSession(r.Context(), m.ID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "session-error", "Could not start your session", "")
		return
	}
	a.setMemberSessionCookie(w, token)
	logging.LogInfo("members", "member signed in via VayuMail: "+m.Email)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"member":        a.memberSnapshot(r, m),
	})
}

// handleMemberVayuMailDeviceRegister enrols a NEW device for a mailbox
// (ADR-0129). The caller authenticates with the raw mailbox credential —
// web-bootstrap scope, the one place the password must keep working — and
// receives a device identity: a random device_id plus a freshly generated
// device password (same generator/format as app passwords, stored only as an
// Argon2id hash). The device starts life 'pending' and cannot sync any mail
// until it is approved from the 2FA-protected web console; when the mailbox
// has device approval switched off it is approved immediately.
//
// Same defences as vayumail-login/vayumail-privkey: the shared brute-force
// throttle, a uniform 401 on ANY auth failure (anti-enumeration), a no-store
// response (it carries a secret shown exactly once), and an audit-log entry.
func (a *App) handleMemberVayuMailDeviceRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuMailLoginEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayumail-disabled", "VayuMail sign-in is not available", "")
		return
	}
	var body struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		DeviceName string `json:"device_name"`
		Platform   string `json:"platform"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	emailAddr := strings.TrimSpace(strings.ToLower(body.Email))
	if emailAddr == "" || body.Password == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Email and password are required", "")
		return
	}

	// Authenticate in web-bootstrap scope: the raw mailbox password is accepted
	// here even when the mailbox requires device approval — this endpoint IS the
	// bootstrap that turns a password into an approvable device credential.
	bridge := &vayuMailBridge{app: a}
	if d := mailAuthThrottle.Delay(emailAddr); d > 0 {
		time.Sleep(d)
	}
	if !bridge.verifyCredentialWeb(r.Context(), emailAddr, body.Password) {
		mailAuthThrottle.Fail(emailAddr)
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-credentials", "That email and password don't match", "")
		return
	}
	mailAuthThrottle.Success(emailAddr)

	accts := a.vayuMail.Accounts()
	// The auth path reads back at most appPasswordMaxPerMailbox rows, so a row
	// beyond the cap would be a credential that can never authenticate.
	if len(accts.AppPasswordCredentials(r.Context(), emailAddr)) >= appPasswordMaxPerMailbox {
		writeAPIError(w, r, http.StatusConflict, "device-limit", "Device limit reached for this mailbox — remove an old device in the console first", "")
		return
	}
	label := strings.TrimSpace(body.DeviceName)
	if label == "" {
		label = "VayuMail Mobile"
	}
	if len(label) > 64 {
		label = label[:64]
	}
	platform := strings.TrimSpace(body.Platform)
	if len(platform) > 32 {
		platform = platform[:32]
	}
	deviceID, err := generateDeviceID()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "device-error", "Could not register the device", "")
		return
	}
	secret, err := generateAppPasswordSecret()
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "device-error", "Could not register the device", "")
		return
	}
	hash, err := auth.HashSecretArgon2id(secret)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "device-error", "Could not register the device", "")
		return
	}
	// New devices are pending until approved from the 2FA-protected console —
	// unless the operator switched approval off for this mailbox.
	status := vmail.DeviceStatusPending
	if !accts.RequireDeviceApproval(r.Context(), emailAddr) {
		status = vmail.DeviceStatusApproved
	}
	if _, err := accts.CreateDevice(r.Context(), emailAddr, label, platform, deviceID, hash, status); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "device-error", "Could not register the device", "")
		return
	}
	dbpkg.AuditLog("vayumail.device.register", emailAddr, emailAddr, label+" ["+platform+"] "+status)
	logging.LogInfo("members", "VayuMail device registered ("+status+") for "+emailAddr)
	// The device password is shown exactly once, dash-grouped like the console
	// reveal; the auth path accepts it with or without the dashes.
	writeJSON(w, r, http.StatusOK, map[string]string{
		"device_id":       deviceID,
		"device_password": groupAppPasswordSecret(secret),
		"status":          status,
	})
}

// handleMemberVayuMailDeviceStatus lets a registered device poll its own
// approval state ("pending" | "approved" | "blocked") so the app can tell the
// user to go approve it in the console — and flip to syncing the moment it is.
// The device proves possession of ITS OWN credential (verified against that
// one row's Argon2id hash); any failure — unknown mailbox, unknown device,
// wrong password — returns the same uniform 401 as the other member endpoints.
func (a *App) handleMemberVayuMailDeviceStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.vayuMailLoginEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayumail-disabled", "VayuMail sign-in is not available", "")
		return
	}
	var body struct {
		Email          string `json:"email"`
		DeviceID       string `json:"device_id"`
		DevicePassword string `json:"device_password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	emailAddr := strings.TrimSpace(strings.ToLower(body.Email))
	if emailAddr == "" || strings.TrimSpace(body.DeviceID) == "" || body.DevicePassword == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Email, device id and device password are required", "")
		return
	}

	if d := mailAuthThrottle.Delay(emailAddr); d > 0 {
		time.Sleep(d)
	}
	id, hash, status, ok := a.vayuMail.Accounts().DeviceCredential(r.Context(), emailAddr, strings.TrimSpace(body.DeviceID))
	// Verify even when the device is unknown (hash == "") to keep the timing
	// and the response identical regardless of whether the device exists. The
	// hash is computed over the dashless secret; the raw form is a fallback.
	pw := strings.ReplaceAll(body.DevicePassword, "-", "")
	match := auth.VerifySecretArgon2id(pw, hash) ||
		(pw != body.DevicePassword && auth.VerifySecretArgon2id(body.DevicePassword, hash))
	if !ok || !match {
		mailAuthThrottle.Fail(emailAddr)
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-credentials", "That email and password don't match", "")
		return
	}
	mailAuthThrottle.Success(emailAddr)
	a.vayuMail.Accounts().TouchAppPassword(r.Context(), id)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": status})
}

// handleMemberVayuMailPrivKey returns the CALLER'S OWN mailbox PGP private key
// (armored) so the VayuMail-Mobile app can import it and decrypt received mail
// on-device — WKD only serves public keys, leaving the app unable to read
// encrypted-at-rest inbound mail without the private half.
//
// Security posture: this hands the owner their own private key over TLS to an
// authenticated caller. It is consistent with VayuPGP's existing trust model —
// the server already stores every mailbox's private key (AES-256-GCM at rest)
// and can already decrypt everything at rest and in transit (see the IMAP
// decrypt hook). The endpoint therefore reveals nothing the server does not
// already hold on the owner's behalf. It is defended by:
//   - the MAIL-SYNC credential scope (verifyCredential): when the mailbox
//     requires device approval (ADR-0129, the default) only an APPROVED
//     device credential unlocks the key — the raw CMS/mailbox password is
//     refused here just like on IMAP/POP3/submission, because the private key
//     decrypts the same mail those protocols serve;
//   - the shared brute-force throttle (mailAuthThrottle) — a decaying per-mailbox
//     delay on every failed attempt, identical to IMAP/SMTP/POP3;
//   - a generic 401 on ANY failure that never reveals whether the address
//     exists (anti-enumeration — same status and body for an unknown mailbox as
//     for a wrong password);
//   - an audit-log entry on every successful retrieval;
//   - a no-store response so the key is never cached by proxies or the client.
func (a *App) handleMemberVayuMailPrivKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.vayuPGP == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayupgp-disabled", "PGP is not available", "")
		return
	}
	if !a.vayuMailLoginEnabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "vayumail-disabled", "VayuMail sign-in is not available", "")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	emailAddr := strings.TrimSpace(strings.ToLower(body.Email))
	if emailAddr == "" || body.Password == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Email and password are required", "")
		return
	}

	// Authenticate exactly as the mail protocol servers do: the shared
	// brute-force throttle first (a mailbox under attack accrues a growing delay
	// before every attempt), then the canonical credential check that accepts the
	// mailbox password OR a device app password. On failure the throttle records
	// it and we return a uniform 401 — identical for an unknown mailbox and a
	// wrong password, so the endpoint cannot enumerate which addresses exist.
	bridge := &vayuMailBridge{app: a}
	if d := mailAuthThrottle.Delay(emailAddr); d > 0 {
		time.Sleep(d)
	}
	if !bridge.verifyCredential(r.Context(), emailAddr, body.Password) {
		mailAuthThrottle.Fail(emailAddr)
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-credentials", "That email and password don't match", "")
		return
	}
	mailAuthThrottle.Success(emailAddr)

	// Credentials good. Load the caller's armored private key; if this mailbox
	// pre-dates auto-keygen and has none yet, mint one idempotently first so a key
	// always exists to return.
	armored, err := a.vayuPGP.ArmoredPrivateKey(emailAddr)
	if err != nil {
		name := emailAddr
		if i := strings.Index(name, "@"); i > 0 {
			name = name[:i]
		}
		if _, gerr := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: emailAddr, Name: name, Email: emailAddr}); gerr != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "keygen-error", "Could not provision your key", "")
			return
		}
		armored, err = a.vayuPGP.ArmoredPrivateKey(emailAddr)
		if err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "key-error", "Could not load your key", "")
			return
		}
	}

	dbpkg.AuditLog("vayumail.privkey.fetch", emailAddr, emailAddr, "")
	logging.LogInfo("members", "VayuMail private key exported for on-device decryption: "+emailAddr)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"email":               emailAddr,
		"armored_private_key": armored,
	})
}

// handleMemberPortalJS serves the VayuPortal widget script. Same-origin static
// asset → satisfies the strict `script-src 'self'` CSP without a nonce, so it
// works on disk-cached public pages just like the other public scripts.
func (a *App) handleMemberPortalJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(render.PortalJS))
}
