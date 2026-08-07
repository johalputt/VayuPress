// SPDX-License-Identifier: Apache-2.0

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
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
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
	// A client sits at the FLOOR of the ladder as well as behind its own
	// allowlist. Belt and braces on purpose: if the confinement branch in
	// serveWithAccess were ever bypassed, every `level < osPathMinLevel(path)`
	// comparison in the tree still denies, so the failure mode is a client who
	// can reach nothing rather than a client who can reach everything.
	if mailOnly || role == users.RoleClient {
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
		// "buzz" and "claudecode" are the per-client connector consoles: both mint
		// API keys, so they sit with "connector" and "apikeys" rather than being
		// readable by an author.
		"settings", "security", "apikeys", "connector", "buzz", "claudecode", "update", "storage", "monitoring", "governance",
		"tools", "modes", "mode", "policy", "topology", "replay", "faults", "adr", "budgets",
		"members", "newsletter", "monetization", "ads", "website", "branding", "shield",
		// Money & fulfilment: payment-gateway secrets, the order ledger, and the
		// premium mail-ID marketplace mutate revenue and expose customer PII —
		// admin-only, never author/editor.
		"payments", "credentials", "orders", "mailids",
		// Infrastructure & operator controls: domain registration + TLS
		// provisioning, encrypted backup export, power (restart/shutdown), and
		// staff-user management each control the machine or its identities.
		// "dns" is the Domains & DNS page: it reveals the install's own hostnames
		// and resolved addresses and offers the privileged provisioning control,
		// so it sits with the other infrastructure surfaces rather than being
		// readable by an author.
		// "vayukeep" is the Backup & Recovery console: it names the backup target
		// on disk, lists every restore point, and offers controls that read those
		// archives back. Same class as "backup" and "power" — admin-only.
		"domains", "dns", "backup", "vayukeep", "power", "users",
		// "vayuflow" arms automations that execute with the OWNER's authority.
		// An author who could reach the flow editor could arm work that runs as
		// themselves on a schedule — a privilege-escalation bug wearing a routing
		// mistake's clothes, which is why the ADR calls this out by name.
		"vayuflow",
		// "world" switches the console between the clearnet and Tor views. It
		// matched no area and fell to the permissive author default, and it is not
		// under /os/api/ so the fail-closed API rule never saw it either.
		"world",
		// Growth is the hub that fronts Members / Newsletter / Monetization /
		// Advertising; Operations fronts Modes / Policy / Topology / Replay / Faults
		// / ADR — both inherit their fronted pages' admin gate.
		"growth", "operations",
		// Infrastructure controls: VayuTor onion services, the Anonymous Tor Space
		// toggle, and the Tor-world site registry each supervise network-facing
		// services / a second server process — admin-only (ADR-0141 review).
		"tor", "spaces", "torworld",
		// VayuVeil enumerates this host's device nodes, display sockets and kernel
		// tunables. That is a map of the machine, so it sits with the other
		// infrastructure surfaces rather than being readable by an author.
		"vayuveil",
	}
	// "optimize" is the hub that fronts SEO / Analytics / VayuShield / Theme Studio
	// / Theme Store; it opens at editor level (its editor-safe cards) and hides the
	// admin-only VayuShield card from non-admins in the grid itself.
	editorAreas := []string{"comments", "pages", "seo", "analytics", "theme", "messages", "optimize"}
	// Author-safe API areas: the self-service and content-authoring endpoints an
	// author legitimately calls. Every OTHER /os/api/* path is fail-CLOSED to
	// admin below — so a newly-added sensitive endpoint can never silently
	// inherit author access (the systemic gap behind the credential/payment/
	// orders/domains/power/users escalations).
	authorAPIAreas := []string{
		"posts", "editor", "media", "embed", "diagram", "profile",
		"vayumail", "talk", "activity", "cmd-index", "feed", "search", "totp", "vayuos",
	}
	// The per-domain console (/os/d/{id}/…) administers a HOSTED CUSTOMER'S site:
	// its identity, its content, what it serves at "/", its theme code and its
	// uploaded bundle. It is reached from the Domains registry, which is admin-
	// only, and every page in it is admin-only at its own primary address.
	//
	// Stated here explicitly because this namespace fell between both gates below.
	// osPathInArea matches `/os/<area>` and `/os/api/<area>`, so `/os/d/x/settings`
	// matched no area and took the permissive author default; and the fail-closed
	// API rule that exists to catch precisely that only fires on paths beginning
	// `/os/api/`, which no per-domain endpoint does. The effect was that mounting a
	// page under a domain LOWERED its gate — `/os/api/theme/code` required editor
	// while `/os/d/{id}/api/theme/code` required author, and that handler carries
	// no role check of its own, so an author could write site-wide custom CSS onto
	// any hosted customer's live domain.
	//
	// Matched on the path SEGMENT, not the bare prefix: "/os/d" as a string prefix
	// would also swallow /os/dashboard, /os/domains and /os/dns.
	if path == "/os/d" || strings.HasPrefix(path, "/os/d/") {
		return accessAdmin
	}

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
	// Fail-closed API default: an /os/api/* path that matched no admin/editor area
	// requires admin UNLESS it is an explicit author-safe area. Non-API /os pages
	// keep the permissive author default (they are navigational, and the sensitive
	// ones are already admin-gated above).
	if strings.HasPrefix(path, "/os/api/") {
		for _, a := range authorAPIAreas {
			if osPathInArea(path, a) {
				return accessAuthor
			}
		}
		return accessAdmin
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

// resolveConsoleUser resolves the operator/staff identity from a VayuOS console
// session cookie (vp_session), or nil if the request carries no valid console
// session. It mirrors requireSessionOrAPIKey's resolution but is read-only and
// never mutates the request. Two callers rely on it: the public login page (to
// forward an already-signed-in operator to the console) and the public site nav
// (to recognise a signed-in operator and show their account, not Sign in).
func (a *App) resolveConsoleUser(r *http.Request) *users.User {
	if a.sessions == nil {
		return nil
	}
	token := auth.SessionTokenFromRequest(r)
	if token == "" {
		return nil
	}
	uid, err := a.sessions.Validate(r.Context(), token)
	if err != nil {
		return nil
	}
	if email, isMail := strings.CutPrefix(uid, "vmail:"); isMail {
		if u, _, ok := a.resolveMailSessionUser(r.Context(), email); ok {
			return u
		}
		return nil
	}
	if a.userStore != nil {
		if u, err := a.userStore.GetByID(r.Context(), uid); err == nil {
			return u
		}
	}
	return nil
}

// hasValidConsoleSession reports whether the request already carries a session
// cookie that resolves to a real user (a CMS account or a VayuMail account). The
// public login page uses it to forward an already-signed-in operator straight to
// the console instead of re-showing the form.
func (a *App) hasValidConsoleSession(r *http.Request) bool {
	return a.resolveConsoleUser(r) != nil
}

// requireSessionOrAPIKey gates admin pages. A valid API key passes through
// unchanged. Otherwise a valid session cookie resolves the user and attaches it
// to the request context. On failure, browser navigations are redirected to the
// login page; API/XHR callers receive 401 JSON.
func (a *App) requireSessionOrAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ki, ok := auth.ResolveValidAPIKey(r); ok {
			// Key-authenticated automation entering the /os surface: enforce the
			// same fine-grained capability table as /api/v1 (ADR-0134), so a grant
			// cannot be bypassed by switching prefixes. Superuser keys (bootstrap,
			// internal, pre-migration backfilled) pass everything, exactly as
			// before. Session-authenticated operators never reach this branch and
			// are governed by session RBAC below, unchanged.
			if !keyMayCall(ki, r.Method, r.URL.Path) {
				writeAPIError(w, r, http.StatusForbidden, "insufficient_permissions",
					"this API key does not hold the capability required for this route", "/docs/compatibility/vayuapi")
				return
			}
			// Same per-key budget + WORM audit as the /api surface, so a key's
			// usage trail and rate limit cannot be escaped by switching prefixes.
			a.serveWithKeyUsage(w, auth.RequestWithKeyInfo(r, ki), ki, next)
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
	// Classify the session, then switch with an explicit default that DENIES. An
	// unclassified session is refused rather than served — the same reason
	// osAudience's zero value is not a valid answer.
	switch classifySession(u, mailOnly) {
	case confineClient:
		// A client with no valid binding is an invalid identity, not a client
		// bound to the primary. Refuse outright: '' is the primary's sentinel
		// everywhere else, and defaulting there would hand a customer the
		// agency's own install.
		if _, ok := clientScopeFor(u.Role, u.ClientDomainID); !ok {
			a.denyAccess(w, r, "/os/logout")
			return
		}
		if !clientPathAllowed(r.URL.Path) {
			a.denyClient(w, r)
			return
		}
	case confineMailOnly:
		if !mailOnlyPathAllowed(r.URL.Path) {
			a.denyAccess(w, r, "/os/vayumail/inbox")
			return
		}
	case confineNone:
		if level < osPathMinLevel(r.URL.Path) {
			a.denyAccess(w, r, "/os")
			return
		}
	default:
		a.denyAccess(w, r, "/os/logout")
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
		// Narrowed from the whole /os/api/vayuos prefix to the mail endpoints a
		// confined mailbox session actually uses (the ADR-0144 recovery flow).
		//
		// The broad prefix also admitted /os/api/vayuos/health — the operator's
		// component-by-component health snapshot, detail strings included — and
		// /os/api/vayuos/security/check, whose page is admin-only in the VayuMail
		// nav. A mail-confined principal is a READER who claimed a mailbox, not
		// staff; infrastructure state is not theirs to read, and this is precisely
		// what an untrusted agency client would have inherited.
		strings.HasPrefix(path, "/os/api/vayuos/mail/"):
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
		config.FormatSite(until, "15:04 MST") + "."
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

// classifySession maps a resolved session to its confinement. It never returns
// confineUnset for a non-nil user, but the caller still handles that case with a
// refusal — a classifier that cannot be wrong is a classifier nobody checks.
func classifySession(u *users.User, mailOnly bool) confinement {
	if u == nil {
		return confineUnset
	}
	if u.Role == users.RoleClient {
		return confineClient
	}
	if mailOnly {
		return confineMailOnly
	}
	return confineNone
}

// isAdminSession reports whether the caller is an administrator by way of a real
// SESSION. A valid API key — of any scope, superuser included — is never enough.
//
// isAdminRequest above accepts any valid key as admin, which is right for the
// operations keyMayCall has already gated by capability. It is NOT right for
// MINTING OR PROMOTING AN IDENTITY THAT CAN LOG IN, and that gap was a complete
// privilege escalation:
//
//	/os/vayumail is mapped to SectionMail (api_capabilities.go), so a key holding
//	only mail:write passes keyMayCall for POST /os/vayumail/accounts/create. It
//	then satisfies isAdminRequest, creates a mailbox with role "administrator",
//	and mailConsoleAccess maps that role to users.RoleAdmin with console=true.
//	Signing in with that mailbox credential yields full console administration.
//	The same applies to accounts/update, which accepts a Role field and can
//	promote an existing mailbox the same way.
//
// A scoped key promoting itself to install owner is exactly the outcome the
// capability system exists to prevent, so the rule is: a credential that grants
// console access may only be created or promoted by a human session. This
// mirrors keyLifecycleAuthorized below, which already refuses to let a scoped
// key mint a superuser token.
func (a *App) isAdminSession(r *http.Request) bool {
	u := currentUser(r)
	return u != nil && u.Role == users.RoleAdmin
}

// mailRoleGrantsConsole reports whether a VayuMail role, once assigned, produces
// a credential that can sign in to the VayuOS console. It is the single place
// that question is asked, so create and update cannot drift apart.
func mailRoleGrantsConsole(role string) bool {
	_, console := mailConsoleAccess(strings.TrimSpace(role))
	return console
}

// mailTargetGrantsConsole reports whether the mailbox at email CURRENTLY holds a
// role that grants console access — read from storage, not from the request.
func (a *App) mailTargetGrantsConsole(ctx context.Context, email string) bool {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return false
	}
	return mailRoleGrantsConsole(a.vayuMail.Accounts().RoleFor(ctx, strings.TrimSpace(email)))
}

// mailCredentialActionAuthorized gates every operation that can TAKE OVER, or
// lock out, an existing console-capable mailbox: resetting its password,
// removing its second factor, disabling it, re-roling it, deleting it.
//
// isAdminSession above closed the CREATE and PROMOTE doors by inspecting the
// SUBMITTED role. That is only half the surface, because a mailbox that already
// holds a console-capable role does not need promoting — it needs taking. A key
// granted only mail:write satisfied isAdminRequest and could:
//
//	POST /os/vayumail/accounts/update {"email":"boss@…","pass":"…"}
//	  => the install owner's mailbox now has a password I chose. No Role field is
//	     submitted, so the promote guard never fires.
//	POST /os/vayumail/accounts/totp   {"email":"boss@…","action":"disable"}
//	  => and now there is no second factor between me and the console.
//	POST /os/vayumail/accounts/delete {"email":"boss@…"}
//	  => or I simply delete every administrator and the operator is locked out.
//
// The rule is symmetric with creation: a credential that grants console access
// may only be minted, promoted, reset, stripped or destroyed by a human session.
//
// Deliberately narrow. It reads the target's CURRENT role, so the ordinary
// mailboxes automation manages (role "mailbox", "reviewer", a custom role) are
// untouched and every existing API-key script against them keeps working. Only
// the handful of mailboxes that can sign in to the console are fenced.
func (a *App) mailCredentialActionAuthorized(r *http.Request, email string) bool {
	if !a.mailTargetGrantsConsole(r.Context(), email) {
		return true
	}
	return a.isAdminSession(r)
}

// writeMailSessionRequired is the single refusal used by the guard above, so the
// message an operator's automation receives explains the boundary rather than
// reading as a generic 403.
func writeMailSessionRequired(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, http.StatusForbidden, "session-admin-required",
		"this mailbox can sign in to the console, so changing its credentials, its second factor, "+
			"its role or its existence requires an administrator session; an API key cannot do it", "")
}

// keyLifecycleAuthorized reports whether the caller may perform API-key lifecycle
// mutations (rotate / revoke / delete / activate / deactivate). Managing the key
// fleet is an operator-admin / superuser operation: a session administrator is
// allowed, and an API-key caller must hold a SUPERUSER key. A scoped key — even
// one with settings:write — must never rotate/revoke/delete another key or mint a
// superuser token by rotating the internal key (audit C2/M10). This mirrors the
// fail-closed superuser check keyMayCall applies to unmapped routes.
func (a *App) keyLifecycleAuthorized(r *http.Request) bool {
	if u := currentUser(r); u != nil {
		return u.Role == users.RoleAdmin
	}
	if ki, ok := auth.KeyInfoFromContext(r.Context()); ok {
		return ki.IsSuperuser()
	}
	return false
}

// POST /api/v1/admin/users  {email, name, password, role}
func (a *App) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if a.userStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "users-disabled", "Accounts not initialised", "")
		return
	}
	// keyLifecycleAuthorized, not isAdminRequest. This handler MINTS AN IDENTITY
	// THAT CAN LOG IN, and isAdminRequest accepts any valid API key as admin —
	// so a key granted only settings:write reached here (the capability table
	// maps /api/v1/admin/users to SectionSettings) and created itself an account
	// with role "admin". Signing in with the password it chose then yielded full
	// console administration, outliving revocation of the key that did it.
	//
	// The predicate eleven lines above already draws the right line and is the
	// one the key-minting path uses: a human admin session, or a SUPERUSER key.
	// That keeps the documented "admin pages accept either a valid API key or a
	// login session" true for a real admin key, and denies it to a scoped one.
	// The reasoning is spelled out on isAdminSession, which named this exact
	// escalation for /os/vayumail and closed it there; this is the same hole in
	// the CMS user API, which was never migrated.
	if !a.keyLifecycleAuthorized(r) {
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
	if err == nil {
		// Neither create nor role-change wrote an audit entry, so an account
		// appearing in the staff list was the only evidence it had happened.
		dbpkg.AuditLog("user.create", dbpkg.AuditActor(r), body.Email, "role="+body.Role)
	}
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
	// Destroying a login is the same trust class as minting one: a scoped key
	// that can delete accounts can remove every administrator and lock the
	// operator out of their own install.
	if !a.keyLifecycleAuthorized(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	email := chi.URLParam(r, "email")
	// Resolve the id BEFORE the delete: sessions are keyed on it, and after the
	// row is gone there is nothing left to look it up by.
	var deletedID string
	if u, err := a.userStore.GetByEmail(r.Context(), email); err == nil && u != nil {
		deletedID = u.ID
	}
	if err := a.userStore.Delete(r.Context(), email); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "delete-error", err.Error(), "")
		return
	}
	// Migration 088 removed the users foreign key from sessions, and with it the
	// ON DELETE CASCADE that used to sign a removed account out. Doing it here
	// restores that and improves on it: the cascade was invisible and untested,
	// and it never covered the "vmail:" half of the column at all.
	if a.sessions != nil && deletedID != "" {
		if n, err := a.sessions.DestroyForUser(r.Context(), deletedID); err != nil {
			logging.LogError("auth", "could not end sessions for deleted account "+email, err.Error())
		} else if n > 0 {
			dbpkg.AuditLog("user.sessions.revoked", dbpkg.AuditActor(r), email, strconv.Itoa(n)+" ended")
		}
	}
	dbpkg.AuditLog("user.delete", dbpkg.AuditActor(r), email, "")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"deleted": email})
}
