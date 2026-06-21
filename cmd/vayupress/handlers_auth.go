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
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/auth"
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
		http.Redirect(w, r, "/os/login", http.StatusSeeOther)
	})
}

// loginClientIP returns the client IP used to key login brute-force lockout.
// chi's RealIP middleware has already normalised r.RemoteAddr to the real
// client address (honouring X-Forwarded-For behind the trusted proxy); we strip
// any trailing port so direct and proxied connections key consistently.
func loginClientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	return ip
}

// loginLockoutMessage formats the operator-facing lockout notice.
func loginLockoutMessage(until time.Time) string {
	return "Too many failed sign-in attempts. Try again after " +
		until.UTC().Format("15:04 MST") + "."
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
