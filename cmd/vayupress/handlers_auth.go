package main

// handlers_auth.go — password login, logout, session middleware, and user
// management for the multi-author accounts feature (Tier 1).
//
// Auth model: admin pages accept EITHER the configured API key (header/cookie,
// unchanged legacy path) OR a valid login session cookie issued after an
// email+password sign-in. This keeps existing single-key deployments working
// while enabling real per-author logins.

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/users"
)

type ctxKey string

const ctxUserKey ctxKey = "vp_user"

// currentUser returns the authenticated user attached to the request, if any.
func currentUser(r *http.Request) *users.User {
	if v := r.Context().Value(ctxUserKey); v != nil {
		if u, ok := v.(*users.User); ok {
			return u
		}
	}
	return nil
}

// requireSessionOrAPIKey gates admin pages. A valid API key passes through
// unchanged. Otherwise a valid session cookie resolves the user and attaches it
// to the request context. On failure, browser navigations are redirected to the
// login page; API/XHR callers receive 401 JSON.
func (a *App) requireSessionOrAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.HasValidAPIKey(r) {
			next.ServeHTTP(w, r)
			return
		}
		if a.sessions != nil && a.userStore != nil {
			if token := auth.SessionTokenFromRequest(r); token != "" {
				if uid, err := a.sessions.Validate(r.Context(), token); err == nil {
					if u, err := a.userStore.GetByID(r.Context(), uid); err == nil {
						ctx := context.WithValue(r.Context(), ctxUserKey, u)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
			}
		}
		// Unauthenticated.
		if strings.Contains(r.Header.Get("Accept"), "application/json") ||
			r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			writeAPIError(w, r, http.StatusUnauthorized, "unauthorized", "login required", "")
			return
		}
		http.Redirect(w, r, "/admin/v2/login", http.StatusSeeOther)
	})
}

// handleV2LoginSubmit authenticates email+password and starts a session.
func (a *App) handleV2LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if a.userStore == nil || a.sessions == nil {
		http.Error(w, "accounts not initialised", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := r.PostFormValue("email")
	password := r.PostFormValue("password")
	u, err := a.userStore.Authenticate(r.Context(), email, password)
	if err != nil {
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "auth", Severity: "notice", Msg: "login failed"})
		a.renderLoginPage(w, r, "Invalid email or password.")
		return
	}
	// Second factor: enforce TOTP when the account has 2FA enabled. This closes
	// the older surface so an enrolled account cannot bypass 2FA via /admin/v2.
	if ok, required := a.verifyTOTPForLogin(r.Context(), email, r.PostFormValue("totp")); required && !ok {
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "auth", Severity: "notice", Msg: "login 2fa failed"})
		a.renderLoginPage(w, r, "Enter the 6-digit code from your authenticator app, then re-enter your password.")
		return
	}
	token, err := a.sessions.Create(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	a.userStore.TouchLastLogin(r.Context(), u.ID)
	auth.SetSessionCookie(w, token)
	logging.LogInfo("auth", "login: "+u.Email+" ("+u.Role+")")
	http.Redirect(w, r, "/admin/v2", http.StatusSeeOther)
}

// handleV2Logout destroys the current session.
func (a *App) handleV2Logout(w http.ResponseWriter, r *http.Request) {
	if a.sessions != nil {
		if token := auth.SessionTokenFromRequest(r); token != "" {
			_ = a.sessions.Destroy(r.Context(), token)
		}
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/admin/v2/login", http.StatusSeeOther)
}

// renderLoginPage renders the email/password sign-in form. errMsg, when set, is
// shown as an inline error. The form posts to /admin/v2/login.
func (a *App) renderLoginPage(w http.ResponseWriter, r *http.Request, errMsg string) {
	nonce := render.CSPNonce(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	errBlock := ""
	if errMsg != "" {
		errBlock = `<p class="login-error" role="alert">` + html.EscapeString(errMsg) + `</p>`
	}
	body := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in — VayuPress Admin</title><meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/admin/v2/static/css/admin-v2.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head><body>
<div class="login-wrap"><div class="card login-card">
  <div class="login-brand">VayuPress</div>
  ` + errBlock + `
  <form method="POST" action="/admin/v2/login">
    <div class="field"><label for="lg-email">Email</label>
      <input id="lg-email" name="email" class="input" type="email" autocomplete="username" required autofocus></div>
    <div class="field"><label for="lg-pass">Password</label>
      <input id="lg-pass" name="password" class="input" type="password" autocomplete="current-password" required></div>
    <div class="field"><label for="lg-totp">Two-factor code (if enabled)</label>
      <input id="lg-totp" name="totp" class="input" type="text" inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000"></div>
    <div class="btn-row mt-2"><button class="btn btn-primary" type="submit">Sign in</button></div>
    <p class="hint">Or supply the API key via the configured proxy header to bypass password login.</p>
  </form>
</div></div>
<script src="/admin/v2/static/js/purify.min.js"></script>
<script nonce="` + nonce + `" src="/admin/v2/static/js/admin-v2.js"></script>
</body></html>`
	_, _ = w.Write([]byte(body))
}

// =============================================================================
// User management API (admin-role guarded)
// =============================================================================

// requireAdminRole ensures the session user is an admin. API-key callers (no
// session user) are treated as admin-equivalent for backward compatibility.
func (a *App) isAdminRequest(r *http.Request) bool {
	if u := currentUser(r); u != nil {
		return u.Role == users.RoleAdmin
	}
	return auth.HasValidAPIKey(r)
}

// POST /api/v1/admin/users  {email, name, password, role}
func (a *App) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if a.userStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "users-disabled", "Accounts not initialised", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	var body struct {
		Email, Name, Password, Role string
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	u, err := a.userStore.Create(r.Context(), body.Email, body.Name, body.Password, body.Role)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "create-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{"user": u})
}

// GET /api/v1/admin/users
func (a *App) handleUserList(w http.ResponseWriter, r *http.Request) {
	if a.userStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "users-disabled", "Accounts not initialised", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	list, err := a.userStore.List(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"users": list, "count": len(list)})
}

// DELETE /api/v1/admin/users/{email}
func (a *App) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if a.userStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "users-disabled", "Accounts not initialised", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	email := chi.URLParam(r, "email")
	if err := a.userStore.Delete(r.Context(), email); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "delete-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"deleted": email})
}
