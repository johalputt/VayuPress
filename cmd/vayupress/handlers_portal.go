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

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/totp"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// setMemberSessionCookie writes the member session cookie with the same
// security attributes used elsewhere, so portal logins and magic-link logins
// produce interchangeable sessions.
func (a *App) setMemberSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: memberCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: config.Cfg.Domain != "localhost", SameSite: http.SameSiteLaxMode,
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
	}
	writeJSON(w, r, http.StatusOK, resp)
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
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "not-signed-in", "Sign in to view your activity", "")
		return
	}
	if a.commentStore == nil {
		writeJSON(w, r, http.StatusOK, map[string]interface{}{"comments": []interface{}{}})
		return
	}
	list, err := a.commentStore.ListByEmail(r.Context(), dbpkg.Reader(), m.Email, 100)
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
	m, err := a.members.Upsert(r.Context(), emailAddr)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", "Could not sign you in", "")
		return
	}
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

// handleMemberPortalJS serves the VayuPortal widget script. Same-origin static
// asset → satisfies the strict `script-src 'self'` CSP without a nonce, so it
// works on disk-cached public pages just like the other public scripts.
func (a *App) handleMemberPortalJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(render.PortalJS))
}
