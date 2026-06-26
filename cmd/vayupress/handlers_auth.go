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
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

type ctxKey string

const ctxUserKey ctxKey = "vp_user"

// ctxMailOnlyKey marks a request whose VayuOS access was granted via a
// VayuMail mailbox login that is NOT an administrator — such sessions are
// confined to the VayuMail surface (see requireSessionOrAPIKey).
const ctxMailOnlyKey ctxKey = "vp_mail_only"

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
		// Fallback: a reader who signed in with their VayuMail mailbox (via the
		// membership portal) may open VayuMail according to that account's role.
		// Administrators get the full console; every other mail role is confined
		// to the VayuMail surface ("only mail → only VayuMail").
		if u, mailOnly, ok := a.resolveMailMember(r); ok {
			if mailOnly && !mailOnlyPathAllowed(r.URL.Path) {
				http.Redirect(w, r, "/os/vayuos/mail/inbox", http.StatusSeeOther)
				return
			}
			ctx := context.WithValue(r.Context(), ctxUserKey, u)
			if mailOnly {
				ctx = context.WithValue(ctx, ctxMailOnlyKey, true)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
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

// resolveMailMember attempts to authenticate the request as a VayuMail mailbox
// holder who signed in through the membership portal. It returns a synthesized
// admin-context user plus a mailOnly flag:
//
//   - administrator / editor / author → console access (mailOnly = false)
//   - reviewer / mailbox / custom      → VayuMail surface only (mailOnly = true)
//
// The exact CMS capabilities of a console identity (admin vs editor vs author)
// are enforced downstream by the existing isAdminRequest / role checks, exactly
// as they are for a real CMS user of the same role.
//
// The synthesized user is never persisted; its ID is prefixed "vmail:" so it
// can never collide with a real CMS user, and its MailAddress is set so the
// existing per-mailbox scoping (ownMailbox) resolves to the holder's own inbox.
func (a *App) resolveMailMember(r *http.Request) (u *users.User, mailOnly bool, ok bool) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		return nil, false, false
	}
	m := a.resolveMember(r)
	if m == nil {
		return nil, false, false
	}
	role := a.vayuMail.Accounts().RoleFor(r.Context(), m.Email)
	if role == "" {
		return nil, false, false // not a VayuMail account
	}
	cmsRole, console := mailConsoleAccess(role)
	su := &users.User{
		ID:          "vmail:" + m.Email,
		Email:       m.Email,
		Name:        m.DisplayName(),
		MailAddress: m.Email,
		Role:        cmsRole,
	}
	// console == false means the holder is confined to the VayuMail surface.
	return su, !console, true
}

// mailConsoleAccess maps a VayuMail account role to the CMS console role it
// stands in for, and reports whether that role may use the wider VayuOS console
// (true) or is confined to the VayuMail surface only (false).
//
//   - administrator → admin   : full console
//   - editor        → editor  : console with editor capabilities
//   - author        → author  : console with author capabilities
//   - reviewer      → author  : VayuMail only (read-only role, no console write)
//   - mailbox       → author  : VayuMail only (mail-only identity)
//   - any custom    → author  : VayuMail only (conservative default)
//
// The CMS role assigned to a confined identity is irrelevant to what it can
// reach (it is path-restricted to the mail surface), but a sensible default is
// kept for the mailbox scoping that runs there.
func mailConsoleAccess(mailRole string) (cmsRole string, console bool) {
	switch mailRole {
	case vmail.RoleAdministrator:
		return users.RoleAdmin, true
	case vmail.RoleEditor:
		return users.RoleEditor, true
	case vmail.RoleAuthor:
		return users.RoleAuthor, true
	default: // reviewer, mailbox, and any custom role
		return users.RoleAuthor, false
	}
}

// mailOnlyPathAllowed reports whether a mail-confined VayuMail session (a
// reviewer / mailbox / custom role with no console access) may reach the given
// VayuOS path. Such sessions are restricted to the VayuMail pages and the
// static assets those pages need; everything else is redirected to the inbox.
func mailOnlyPathAllowed(path string) bool {
	switch {
	case strings.HasPrefix(path, "/os/vayuos/mail"),
		strings.HasPrefix(path, "/os/static"),
		strings.HasPrefix(path, "/os/api/vayuos"):
		return true
	}
	return false
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
	// VayuOS: auto-provision PGP keypair + mailbox for the new account.
	a.publishUserCreated(r.Context(), u.ID, u.Name, u.Email)
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
