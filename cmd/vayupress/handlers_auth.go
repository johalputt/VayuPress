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
	"github.com/johalputt/vayupress/internal/totp"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

type ctxKey string

const ctxUserKey ctxKey = "vp_user"

// ctxMailOnlyKey marks a request whose VayuOS access was granted via a
// VayuMail mailbox login that is NOT an administrator — such sessions are
// confined to the VayuMail surface (see requireSessionOrAPIKey).
const ctxMailOnlyKey ctxKey = "vp_mail_only"

// ctxAccessKey carries the resolved console access level (see access* below).
const ctxAccessKey ctxKey = "vp_access"

// Console access levels, in ascending capability. Every authenticated /os
// request is assigned one; the sidebar nav and the route guard both consult it
// so "what you can see" exactly matches "what you can reach".
//
//   - accessMailOnly: mailbox / reviewer roles — confined to the VayuMail surface.
//   - accessAuthor  : author — own content (Posts, New Post, Media), Profile, Mail.
//   - accessEditor  : editor — + Comments, Pages, SEO, Analytics, Theme, Messages.
//   - accessAdmin   : administrator — the full console (Members, Newsletter,
//     Monetization, System, Operations, Settings, Security, API Keys, Update…).
const (
	accessMailOnly = iota
	accessAuthor
	accessEditor
	accessAdmin
)

// accessLevelFor maps a (CMS) role + mail-only flag to a console access level.
func accessLevelFor(role string, mailOnly bool) int {
	if mailOnly {
		return accessMailOnly
	}
	switch role {
	case users.RoleAdmin:
		return accessAdmin
	case users.RoleEditor:
		return accessEditor
	default:
		return accessAuthor
	}
}

// osPathInArea reports whether an /os path belongs to a feature area, matching
// both the page (`/os/<area>`) and its API actions (`/os/api/<area>`).
func osPathInArea(path, area string) bool {
	for _, base := range []string{"/os/" + area, "/os/api/" + area} {
		if path == base || strings.HasPrefix(path, base+"/") {
			return true
		}
	}
	return false
}

// osPathMinLevel returns the minimum console access level required to open an
// /os path. Content pages (Dashboard, Posts, editor, Media, Profile, VayuMail)
// are the permissive author-level default; only the editor- and admin-sensitive
// areas are gated, so adding a benign page never accidentally locks it out.
func osPathMinLevel(path string) int {
	adminAreas := []string{
		"settings", "security", "apikeys", "update", "storage", "monitoring", "governance",
		"tools", "modes", "policy", "topology", "replay", "faults", "adr",
		"members", "newsletter", "monetization", "ads",
	}
	editorAreas := []string{"comments", "pages", "seo", "analytics", "theme", "messages"}
	for _, a := range adminAreas {
		if osPathInArea(path, a) {
			return accessAdmin
		}
	}
	for _, a := range editorAreas {
		if osPathInArea(path, a) {
			return accessEditor
		}
	}
	return accessAuthor
}

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
		if a.sessions != nil {
			if token := auth.SessionTokenFromRequest(r); token != "" {
				if uid, err := a.sessions.Validate(r.Context(), token); err == nil {
					// A VayuMail account session carries a "vmail:" id; resolve it to a
					// synthesized, role-scoped identity. A real CMS user session resolves
					// against the user store.
					if email, isMail := strings.CutPrefix(uid, "vmail:"); isMail {
						if u, mailOnly, ok := a.resolveMailSessionUser(r.Context(), email); ok {
							a.serveWithAccess(w, r, next, u, mailOnly)
							return
						}
					} else if a.userStore != nil {
						if u, err := a.userStore.GetByID(r.Context(), uid); err == nil {
							a.serveWithAccess(w, r, next, u, false)
							return
						}
					}
				}
			}
		}
		// Fallback: a reader who signed in with their VayuMail mailbox (via the
		// membership portal) may open VayuMail according to that account's role.
		if u, mailOnly, ok := a.resolveMailMember(r); ok {
			a.serveWithAccess(w, r, next, u, mailOnly)
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

// serveWithAccess enforces the role-scoped access policy for an authenticated
// request, then forwards it with the user + access level attached to the
// context. A mail-only session is confined to the VayuMail surface; a console
// session is blocked from areas above its level. Denials redirect a browser to
// its allowed home and return 403 JSON to API/XHR callers — so a record/area a
// role cannot use is both hidden (nav) and unreachable (here).
func (a *App) serveWithAccess(w http.ResponseWriter, r *http.Request, next http.Handler, u *users.User, mailOnly bool) {
	// Forced password change: a bootstrapped default admin must set a new password
	// before reaching anything else. Allow only the change-password page itself,
	// logout and static assets; redirect everything else there. Browser nav gets a
	// redirect; API/XHR gets 403 so a stale tab can't keep mutating.
	if u.MustChangePassword && !forcedChangePathAllowed(r.URL.Path) {
		if strings.Contains(r.Header.Get("Accept"), "application/json") ||
			r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			writeAPIError(w, r, http.StatusForbidden, "password-change-required", "Set a new password to continue.", "")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserKey, u)
		if r.URL.Path != "/os/change-password" {
			http.Redirect(w, r, "/os/change-password", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
		return
	}
	level := accessLevelFor(u.Role, mailOnly)
	if mailOnly {
		if !mailOnlyPathAllowed(r.URL.Path) {
			a.denyAccess(w, r, "/os/vayumail/inbox")
			return
		}
	} else if level < osPathMinLevel(r.URL.Path) {
		a.denyAccess(w, r, "/os")
		return
	}
	ctx := context.WithValue(r.Context(), ctxUserKey, u)
	ctx = context.WithValue(ctx, ctxAccessKey, level)
	if mailOnly {
		ctx = context.WithValue(ctx, ctxMailOnlyKey, true)
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}

// forcedChangePathAllowed lists the paths reachable while a user must change
// their password — the change page itself, logout, and static assets — so the
// forced-change redirect can never lock the operator out of the very page that
// clears the flag.
func forcedChangePathAllowed(path string) bool {
	switch path {
	case "/os/change-password", "/os/logout":
		return true
	}
	return strings.HasPrefix(path, "/os/static/")
}

// denyAccess refuses an in-policy-but-out-of-scope request: 403 JSON for
// API/XHR callers, otherwise a redirect to the caller's allowed home.
func (a *App) denyAccess(w http.ResponseWriter, r *http.Request, home string) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") ||
		r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "your role does not have access to this area", "")
		return
	}
	http.Redirect(w, r, home, http.StatusSeeOther)
}

// resolveMailSessionUser resolves a VayuMail account (by email, from a "vmail:"
// session) to a synthesized, role-scoped identity. It returns ok=false if the
// account no longer exists or has been deactivated (HashFor only returns a hash
// for active accounts), so deleting/disabling an account immediately invalidates
// its web sessions.
func (a *App) resolveMailSessionUser(ctx context.Context, email string) (u *users.User, mailOnly bool, ok bool) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		return nil, false, false
	}
	if a.vayuMail.Accounts().HashFor(ctx, email) == "" {
		return nil, false, false // deleted or deactivated
	}
	role := a.vayuMail.Accounts().RoleFor(ctx, email)
	if role == "" {
		return nil, false, false
	}
	cmsRole, console := mailConsoleAccess(role)

	// Identity unification: if a real CMS account exists with this email, log in
	// as THAT persisted user — same profile, same stable /author/<id> URL,
	// editable profile — rather than a throwaway "vmail:" identity. The mailbox
	// and the CMS account are then one and the same person. The mailbox's role
	// still governs whether this session reaches the console (mailOnly).
	if console && a.userStore != nil {
		if cu, err := a.userStore.GetByEmail(ctx, email); err == nil && cu != nil {
			if cu.MailAddress == "" {
				cu.MailAddress = email
			}
			return cu, false, true
		}
	}

	su := &users.User{
		ID:          "vmail:" + email,
		Email:       email,
		Name:        authorFallbackName(email),
		MailAddress: email,
		Role:        cmsRole,
	}
	return su, !console, true
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
	// Identity unification (see resolveMailSessionUser): prefer the persisted CMS
	// account with this email for a console-capable holder, so it's one identity.
	if console && a.userStore != nil {
		if cu, err := a.userStore.GetByEmail(r.Context(), m.Email); err == nil && cu != nil {
			if cu.MailAddress == "" {
				cu.MailAddress = m.Email
			}
			return cu, false, true
		}
	}
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
	case path == "/os/profile" || strings.HasPrefix(path, "/os/profile/"),
		path == "/os/logout",
		strings.HasPrefix(path, "/os/vayumail"),
		strings.HasPrefix(path, "/os/static"),
		strings.HasPrefix(path, "/os/api/vayuos"):
		return true
	}
	return false
}

// authMailAccount verifies a VayuMail account's email+password (active accounts
// only) and, when the account has 2FA enabled, its TOTP code. It returns the
// normalized email, whether authentication fully succeeded, and whether a TOTP
// code was required but absent/invalid (so the form can prompt for it).
func (a *App) authMailAccount(ctx context.Context, email, pass, code string) (addr string, ok bool, totpMissing bool) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		return "", false, false
	}
	addr = strings.ToLower(strings.TrimSpace(email))
	if addr != "" && !strings.Contains(addr, "@") {
		addr += "@" + a.vayuMail.Config().Domain
	}
	hash := a.vayuMail.Accounts().HashFor(ctx, addr)
	if hash == "" || !auth.VerifySecretArgon2id(pass, hash) {
		return addr, false, false
	}
	if secret, enabled := a.vayuMail.Accounts().TOTPStatus(ctx, addr); enabled && secret != "" {
		if !totp.Validate(secret, code) {
			return addr, false, true
		}
	}
	return addr, true, false
}

// loginClientIP returns the client IP used to key login brute-force lockout.
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
