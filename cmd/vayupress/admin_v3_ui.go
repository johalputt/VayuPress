package main

// admin_v3_ui.go — VayuPress Admin v3, mounted under /os.
//
// Design goals (ADR-0068): surpass Ghost/WordPress/Substack in UI beauty,
// feature depth, and security while remaining a sovereign single-binary with
// zero CDN dependencies and strict-CSP compliance.
//
// CSP posture (inherited from middleware.go — non-negotiable):
//   default-src 'self'; style-src 'self'; script-src 'self' 'nonce-<N>';
//   font-src 'self'; img-src 'self' data:; form-action 'self'
//
// Rules honoured:
//   - No inline <style> or style="" attributes. All CSS lives in admin-v3.css.
//   - The only inline <script> block carries the per-request CSP nonce.
//   - No external CDNs. All assets served same-origin under /os/static/.
//   - All user-originated strings escaped with html.EscapeString before HTML emit.
//   - DOM mutations in admin-v3.js use textContent / createElement; no innerHTML
//     with untrusted data.
//
// Phase 1 implements: login page redesign, new grouped sidebar, stat-card
// dashboard, posts table, editor wrapper, settings page, SEO page.
// Phases 2-7 add block editor, media library, members, TOTP security, i18n,
// GraphQL admin, command palette, and all remaining intelligence features.

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// ── Static asset path ────────────────────────────────────────────────────────

func adminV3StaticDir() string {
	return config.EnvOr("STATIC_DIR", "/var/www/vayupress/static")
}

// ── Route registration ───────────────────────────────────────────────────────

// registerAdminV3UIRoutes mounts Admin v3 under /os.
// Follows the same auth/CSP/CSRF patterns as Admin v2 (admin_ui.go).
func (a *App) registerAdminV3UIRoutes(r chi.Router) {
	// Admin v3 is now a legacy surface (ADR-0069 Stage 3 in progress): the
	// canonical admin is VayuOS at /os. Old /admin/v3[/...] URLs 302-redirect
	// into the /os equivalent, joining /admin and /admin/v2.
	v3Redirect := legacyRedirect()
	r.Get("/admin/v3", v3Redirect)
	r.Handle("/admin/v3/*", v3Redirect)

	// Public static assets (served same-origin so CSP 'self' covers them).
	r.Get("/os/static/css/admin-v3.css", serveAdminV3Asset("css/admin-v3.css", "text/css; charset=utf-8"))
	r.Get("/os/static/js/admin-v3.js", serveAdminV3Asset("js/admin-v3.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-v3-editor.js", serveAdminV3Asset("js/admin-v3-editor.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-v3-security.js", serveAdminV3Asset("js/admin-v3-security.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-v3-intel.js", serveAdminV3Asset("js/admin-v3-intel.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-v3-tools.js", serveAdminV3Asset("js/admin-v3-tools.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-v3-monitoring.js", serveAdminV3Asset("js/admin-v3-monitoring.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-v3-theme.js", serveAdminV3Asset("js/admin-v3-theme.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/purify.min.js", serveAdminV3Asset("js/purify.min.js", "application/javascript; charset=utf-8"))

	// Fonts — path-traversal prevented by switch allowlist (same pattern as v2).
	r.Get("/os/static/fonts/{file}", func(w http.ResponseWriter, req *http.Request) {
		var canon string
		switch chi.URLParam(req, "file") {
		case "space-grotesk.woff2":
			canon = "space-grotesk.woff2"
		case "inter.woff2":
			canon = "inter.woff2"
		case "jetbrains-mono.woff2":
			canon = "jetbrains-mono.woff2"
		default:
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFile(w, req, filepath.Join(adminV3StaticDir(), "fonts", canon))
	})

	// Public: login page and credential forms.
	r.Get("/os/login", a.handleV3Login)
	r.Post("/os/login", a.handleV3LoginSubmit)
	r.Post("/os/logout", a.handleV3Logout)

	// Protected pages and APIs — require session or API key.
	r.Group(func(pr chi.Router) {
		pr.Use(a.requireSessionOrAPIKey)

		// Pages
		pr.Get("/os", a.handleV3Dashboard)
		pr.Get("/os/posts", a.handleV3Posts)
		pr.Get("/os/media", a.handleV3Media)
		pr.Get("/os/api/media", a.handleV3MediaList)
		pr.Get("/os/members", a.handleV3Members)
		pr.Get("/os/security", a.handleV3Security)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/totp/begin", a.handleV3TOTPBegin)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/totp/verify", a.handleV3TOTPVerify)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/totp/disable", a.handleV3TOTPDisable)
		pr.Get("/os/editor", a.handleV3Editor)
		pr.Get("/os/editor/{slug}", a.handleV3Editor)
		pr.Get("/os/monitoring", a.handleV3Monitoring)
		pr.Get("/os/governance", a.handleV3Governance)
		pr.Get("/os/theme", a.handleV3Theme)
		// Session-friendly mirrors of the Theme Studio JSON API (the /api/v1/admin
		// originals require an API key; v3 operators hold a session cookie).
		pr.Get("/os/api/theme/presets", a.handleThemePresets)
		pr.Get("/os/api/theme/tokens", a.handleThemeTokens)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/preview", a.handleThemePreview)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/apply", a.handleThemeApply)
		// Session-friendly read-only mirrors of the operator JSON APIs (the
		// /api/v1/admin/* originals require an API key; v3 operators hold a
		// session cookie). Same handlers, no CSRF needed for GETs.
		pr.Get("/os/api/mode", a.handleModeStatus)
		pr.Get("/os/api/budgets", a.handleGovernanceBudgets)
		pr.Get("/os/tools", a.handleV3Tools)
		pr.Get("/os/api/tools", a.handleV3ToolsList)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/tools/toggle", a.handleV3ToolToggle)
		pr.Get("/os/seo", a.handleV3SEONative)
		pr.Get("/os/analytics", a.handleV3Analytics)
		pr.Get("/os/settings", a.handleV3Settings)
		pr.Get("/os/settings/{group}", a.handleV3Settings)

		// CSRF-protected writes
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/seo/regenerate", a.handleSEORegenerate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/settings", a.handleV3SettingsAPI)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/quick-create", a.handleV3QuickCreatePost)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/save", a.handleV3EditorSave)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/preview", a.handleV3EditorPreview)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/ai", a.handleV3EditorAI)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/convert", a.handleV3EditorConvert)
		pr.Get("/os/api/editor/versions/{slug}", a.handleV3EditorVersionList)
		pr.Get("/os/api/editor/versions/{slug}/{id}", a.handleV3EditorVersionGet)

		// Read-only APIs (no CSRF needed)
		pr.Get("/os/api/activity", a.handleV3Activity)
		pr.Get("/os/api/cmd-index", a.handleV3CmdIndex)
	})

	// Redirect bare /os/* to dashboard if hitting unknown paths
	r.Get("/os/*", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/os", http.StatusSeeOther)
	})
}

func serveAdminV3Asset(rel, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeFile(w, req, filepath.Join(adminV3StaticDir(), filepath.FromSlash(rel)))
	}
}

// ── Shared layout ────────────────────────────────────────────────────────────

// navItem builds a sidebar nav link with an inline SVG icon.
func navItem(href, label, key, active, iconSVG string) string {
	cls := "nav-link"
	if key == active {
		cls += " active"
	}
	return `<a class="` + cls + `" href="` + href + `">` +
		iconSVG +
		html.EscapeString(label) +
		`</a>`
}

// svgIcon returns a minimal inline SVG for the sidebar.
// Using path data keeps us CDN-free and avoids an extra HTTP round-trip.
func svgIcon(path string) string {
	return `<svg viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d="` + path + `" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`
}

var (
	iconDashboard  = svgIcon("M3 10.5L10 3l7 7.5M5 8.5V17h3.5v-4h3v4H15V8.5")
	iconPosts      = svgIcon("M4 4h12v2H4V4zm0 4h12v2H4V8zm0 4h8v2H4v-2z")
	iconNewPost    = svgIcon("M10 4v12m-6-6h12")
	iconMedia      = svgIcon("M3 5a2 2 0 012-2h10a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V5zm0 8l4-4 3 3 2-2 4 4")
	iconMembers    = svgIcon("M13 6a3 3 0 11-6 0 3 3 0 016 0zm-9 10a6 6 0 1112 0H4z")
	iconNewsletter = svgIcon("M3 8l7-4 7 4v8a1 1 0 01-1 1H4a1 1 0 01-1-1V8zm7-1v9m-4-6h8")
	iconSEO        = svgIcon("M8 15A7 7 0 108 1a7 7 0 000 14zm5-1l4 4")
	iconAnalytics  = svgIcon("M3 17l4-8 4 4 4-6 4 4")
	iconSettings   = svgIcon("M10 13a3 3 0 100-6 3 3 0 000 6zm0 0v1m0-8V5M4.2 4.2l.7.7m10-.7l-.7.7M3 10H2m16 0h-1M4.9 15.8l.7-.7m9.5.7l-.7-.7")
	iconSecurity   = svgIcon("M10 2l6 3v5c0 3.5-2.5 6.8-6 8-3.5-1.2-6-4.5-6-8V5l6-3z")
	iconTools      = svgIcon("M12.5 3.5a3 3 0 00-3.9 3.9l-5.1 5.1 2 2 5.1-5.1a3 3 0 003.9-3.9l-2 2-2-2 2-2z")
	iconMonitoring = svgIcon("M2 10h3l2-5 3 11 3-8 2 2h3")
	iconGovernance = svgIcon("M10 2l7 3v5c0 3.5-2.8 6.8-7 8-4.2-1.2-7-4.5-7-8V5l7-3zm0 5v6m-3-3h6")
	iconTheme      = svgIcon("M10 2a8 8 0 100 16c1 0 1.5-.7 1.5-1.5 0-.4-.2-.8-.4-1-.3-.3-.4-.6-.4-1 0-.8.7-1.5 1.5-1.5H14a4 4 0 004-4c0-3.6-3.6-6.5-8-6.5zM5.5 10a1 1 0 110-2 1 1 0 010 2zm3-3a1 1 0 110-2 1 1 0 010 2zm5 0a1 1 0 110-2 1 1 0 010 2z")
)

// adminV3Layout renders the shared chrome for admin v3.
// The nonce is injected into the single inline bootstrap <script> block.
// All CSS/JS are external same-origin files. No inline styles.
func adminV3Layout(nonce, title, active string, settings *v3Settings, bodyHTML string) string {
	et := html.EscapeString(title)
	theme := "dark"
	if settings != nil && settings.AdminTheme != "" {
		theme = settings.AdminTheme
	}
	siteName := "VayuPress"
	if settings != nil && settings.SiteName != "" {
		siteName = html.EscapeString(settings.SiteName)
	}

	cmdHint := `<button class="topbar-cmd" aria-label="Command palette" title="Open command palette">
      <svg viewBox="0 0 20 20" fill="none" width="14" height="14" aria-hidden="true"><path d="M8 15A7 7 0 108 1a7 7 0 000 14zm5-1l4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
      <span class="topbar-cmd-text">Search or jump…</span>
      <kbd>⌘K</kbd>
    </button>`

	return `<!DOCTYPE html>
<html lang="en" data-theme="` + html.EscapeString(theme) + `">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + et + ` — ` + siteName + ` · VayuOS</title>
<meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/os/static/css/admin-v3.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="vp-v3" data-admin-theme="` + html.EscapeString(theme) + `">
<a href="#main-content" class="skip-link">Skip to main content</a>

<!-- Sidebar overlay for mobile tap-to-close -->
<div class="sidebar-overlay" aria-hidden="true"></div>

<div class="shell">
<!-- ── Sidebar ──────────────────────────────────────────────── -->
<aside class="sidebar" aria-label="Admin navigation">
  <div class="sidebar-brand">
    <img src="/static/favicon-light.png" alt="" width="28" height="28">
    <span class="sidebar-brand-name">` + siteName + `</span>
    <span class="sidebar-brand-os">VayuOS</span>
  </div>
  <nav class="sidebar-nav" aria-label="Primary">
    <div class="sidebar-section-label">Content</div>
    ` + navItem("/os", "Dashboard", "dashboard", active, iconDashboard) + `
    ` + navItem("/os/posts", "Posts", "posts", active, iconPosts) + `
    ` + navItem("/os/editor", "New Post", "editor", active, iconNewPost) + `
    ` + navItem("/os/media", "Media", "media", active, iconMedia) + `

    <div class="sidebar-section-label">Audience</div>
    ` + navItem("/os/members", "Members", "members", active, iconMembers) + `
    ` + navItem("/os/newsletter", "Newsletter", "newsletter", active, iconNewsletter) + `

    <div class="sidebar-section-label">Optimize</div>
    ` + navItem("/os/seo", "SEO", "seo", active, iconSEO) + `
    ` + navItem("/os/analytics", "Analytics", "analytics", active, iconAnalytics) + `
    ` + navItem("/os/theme", "Theme Studio", "theme", active, iconTheme) + `

    <div class="sidebar-section-label">System</div>
    ` + navItem("/os/monitoring", "Monitoring", "monitoring", active, iconMonitoring) + `
    ` + navItem("/os/governance", "Governance", "governance", active, iconGovernance) + `
    ` + navItem("/os/tools", "Tools & Plugins", "tools", active, iconTools) + `
    ` + navItem("/os/settings", "Settings", "settings", active, iconSettings) + `
    ` + navItem("/os/security", "Security", "security", active, iconSecurity) + `
    <div class="sidebar-spacer"></div>
    <a class="nav-link" href="/admin/v2">
      <svg viewBox="0 0 20 20" fill="none" width="16" height="16" aria-hidden="true"><path d="M9 4H5a2 2 0 00-2 2v8a2 2 0 002 2h10a2 2 0 002-2v-4M13 4h4m0 0v4m0-4L9 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
      Admin v2
    </a>
  </nav>
  <div class="sidebar-footer">
    <div class="sidebar-user">
      <div class="avatar avatar--sm avatar--brand">A</div>
      <div class="sidebar-user-info">
        <div class="sidebar-user-name">Admin</div>
        <div class="sidebar-user-role">Administrator</div>
      </div>
    </div>
  </div>
</aside>

<!-- ── Main ─────────────────────────────────────────────────── -->
<div class="main">
  <header class="topbar" role="banner">
    <button type="button" class="menu-toggle btn--icon" data-action="toggle-sidebar" aria-label="Toggle sidebar">
      <svg viewBox="0 0 20 20" fill="none" width="20" height="20" aria-hidden="true"><path d="M3 5h14M3 10h14M3 15h14" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>
    </button>
    <span class="topbar-title">` + et + `</span>
    <span class="topbar-spacer"></span>
    ` + cmdHint + `
    <a class="btn btn--primary btn--sm" href="/os/editor">New Post</a>
    <form method="POST" action="/os/logout">
      <button type="submit" class="btn btn--ghost btn--sm">Sign out</button>
    </form>
  </header>

  <main id="main-content" class="content">
` + bodyHTML + `
  </main>
</div><!-- .main -->
</div><!-- .shell -->

<!-- Bottom nav for mobile -->
<nav class="bottom-nav" aria-label="Mobile navigation">
  <a class="bottom-nav-item` + activeCls("dashboard", active) + `" href="/os">
    ` + iconDashboard + `<span>Home</span>
  </a>
  <a class="bottom-nav-item` + activeCls("posts", active) + `" href="/os/posts">
    ` + iconPosts + `<span>Posts</span>
  </a>
  <a class="bottom-nav-item` + activeCls("editor", active) + `" href="/os/editor">
    ` + iconNewPost + `<span>Write</span>
  </a>
  <a class="bottom-nav-item` + activeCls("members", active) + `" href="/os/members">
    ` + iconMembers + `<span>Members</span>
  </a>
  <a class="bottom-nav-item` + activeCls("settings", active) + `" href="/os/settings">
    ` + iconSettings + `<span>Settings</span>
  </a>
</nav>

<!-- Command palette -->
<div id="cmd-backdrop" class="cmd-backdrop" hidden role="dialog" aria-modal="true" aria-label="Command palette">
  <div class="cmd-panel">
    <div class="cmd-input-wrap">
      <svg class="cmd-search-icon" viewBox="0 0 20 20" fill="none" width="18" height="18" aria-hidden="true">
        <path d="M8 15A7 7 0 108 1a7 7 0 000 14zm5-1l4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
      </svg>
      <input id="cmd-input" class="cmd-input" type="text" placeholder="Search posts, members, settings…" autocomplete="off" aria-label="Search">
    </div>
    <div id="cmd-results" class="cmd-results" role="listbox"></div>
    <div class="cmd-footer">
      <span class="cmd-footer-hint"><kbd>↑↓</kbd> navigate</span>
      <span class="cmd-footer-hint"><kbd>↵</kbd> select</span>
      <span class="cmd-footer-hint"><kbd>Esc</kbd> close</span>
    </div>
  </div>
</div>

<!-- Toast container -->
<div class="toast-container" aria-live="polite" aria-atomic="true"></div>

<!-- Bootstrap (nonce-gated, reads data-admin-theme from body) -->
<script src="/os/static/js/purify.min.js"></script>
<script nonce="` + nonce + `" src="/os/static/js/admin-v3.js"></script>
</body></html>`
}

// activeCls returns " active" when the key matches the active page.
func activeCls(key, active string) string {
	if key == active {
		return " active"
	}
	return ""
}

// v3Settings holds the subset of site settings needed to render every page.
type v3Settings struct {
	SiteName   string
	AdminTheme string
}

// getV3Settings loads settings needed for layout rendering.
func (a *App) getV3Settings(ctx context.Context) *v3Settings {
	s := &v3Settings{}
	if a.siteSettings != nil {
		s.SiteName = a.siteSettings.Get(ctx, settings.KeySiteName)
		s.AdminTheme = a.siteSettings.Get(ctx, "admin.theme")
	}
	return s
}

// writeV3HTML writes HTML with the standard v3 response headers and CSRF cookie.
func writeV3HTML(w http.ResponseWriter, body string) {
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{
			Name: "vp_csrf", Value: token, Path: "/",
			SameSite: http.SameSiteStrictMode, HttpOnly: false,
			Secure: csrfCookieSecure(), MaxAge: 3600,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// ── Login page ───────────────────────────────────────────────────────────────

func (a *App) handleV3Login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(v3LoginPage("", "")))
}

func (a *App) handleV3LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	pass := r.FormValue("password")
	if email == "" || pass == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(v3LoginPage(email, "Email and password are required.")))
		return
	}
	if a.userStore == nil || a.sessions == nil {
		http.Error(w, "accounts not initialised", http.StatusServiceUnavailable)
		return
	}
	// Brute-force guard — shared lockout state with the v2 surface and the
	// API-key path so attempts cannot be split across surfaces.
	ip := loginClientIP(r)
	if locked, until := auth.CheckAuthLockout(ip); locked {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(v3LoginPage(email, loginLockoutMessage(until))))
		return
	}
	u, err := a.userStore.Authenticate(r.Context(), email, pass)
	if err != nil {
		auth.RecordAuthFailure(ip)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(v3LoginPage(email, "Invalid email or password.")))
		return
	}
	// Second factor: if the account has 2FA enabled, a valid TOTP code is required.
	// On failure the password must be re-entered (it is never echoed back).
	if ok, required := a.verifyTOTPForLogin(r.Context(), email, r.FormValue("totp")); required && !ok {
		auth.RecordAuthFailure(ip)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(v3LoginPage(email, "Enter the 6-digit code from your authenticator app, then re-enter your password.")))
		return
	}
	token, err := a.sessions.Create(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	auth.RecordAuthSuccess(ip)
	a.userStore.TouchLastLogin(r.Context(), u.ID)
	auth.SetSessionCookie(w, token)
	http.Redirect(w, r, "/os", http.StatusSeeOther)
}

func (a *App) handleV3Logout(w http.ResponseWriter, r *http.Request) {
	if a.sessions != nil {
		if token := auth.SessionTokenFromRequest(r); token != "" {
			_ = a.sessions.Destroy(r.Context(), token)
		}
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/os/login", http.StatusSeeOther)
}

// v3LoginPage builds the full login page HTML. It uses a split-viewport layout:
// left hero panel (animated gradient mesh) + right form panel.
func v3LoginPage(prefillEmail, errMsg string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div class="login-error" role="alert">` + html.EscapeString(errMsg) + `</div>`
	}
	return `<!DOCTYPE html>
<html lang="en" data-theme="dark">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in — VayuPress Admin</title>
<meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/os/static/css/admin-v3.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="vp-v3 login-page">

<!-- Hero panel -->
<div class="login-hero" aria-hidden="true">
  <div class="login-hero-bg"></div>
  <div class="login-hero-grid"></div>
  <div class="login-hero-content">
    <div class="login-hero-brand">
      <img src="/static/favicon-light.png" alt="" width="36" height="36">
      <span>VayuPress</span>
    </div>
  </div>
  <div class="login-hero-tagline-wrap">
    <div class="login-hero-headline">Publishing that belongs to you.</div>
    <div class="login-hero-sub">Sovereign, single-binary, zero third-party dependencies. More powerful than Ghost. More private than Substack. Yours completely.</div>
  </div>
</div>

<!-- Form panel -->
<div class="login-panel">
  <div class="login-card-title">Welcome back</div>
  <div class="login-card-sub">Sign in to your VayuPress dashboard</div>
  ` + errHTML + `
  <form class="login-form" method="POST" action="/os/login" novalidate>
    <div class="field">
      <label class="field-label" for="login-email">Email</label>
      <input id="login-email" class="input" type="email" name="email"
        value="` + html.EscapeString(prefillEmail) + `"
        placeholder="you@example.com" autocomplete="username"
        required autofocus>
    </div>
    <div class="field">
      <label class="field-label" for="login-password">Password</label>
      <input id="login-password" class="input" type="password" name="password"
        placeholder="••••••••" autocomplete="current-password" required>
    </div>
    <div class="field">
      <label class="field-label" for="login-totp">Two-factor code <span class="muted text-xs">(if enabled)</span></label>
      <input id="login-totp" class="input" type="text" name="totp"
        inputmode="numeric" autocomplete="one-time-code" maxlength="6"
        placeholder="000000">
    </div>
    <button type="submit" class="btn btn--primary">Sign in</button>
  </form>
  <div class="login-footer">VayuPress Admin v3 · CSP-strict · Zero-telemetry</div>
</div>

<script src="/os/static/js/admin-v3.js"></script>
</body></html>`
}

// ── Dashboard ────────────────────────────────────────────────────────────────

// v3PublishTrend returns the count of articles created on each of the last n
// days (oldest first). Used to render the dashboard sparkline. Counts come
// straight from the articles table grouped by calendar day (UTC).
func v3PublishTrend(ctx context.Context, n int) []int {
	out := make([]int, n)
	if dbpkg.DB == nil {
		return out
	}
	// Bucket per day for the window. SQLite date() truncates to YYYY-MM-DD.
	rows, err := dbpkg.DB.QueryContext(ctx,
		`SELECT date(created_at) d, COUNT(1) c
		 FROM articles
		 WHERE created_at >= date('now', ?)
		 GROUP BY d`, "-"+strconv.Itoa(n-1)+" days")
	if err != nil {
		return out
	}
	defer rows.Close()
	byDay := map[string]int{}
	for rows.Next() {
		var d string
		var c int
		if rows.Scan(&d, &c) == nil {
			byDay[d] = c
		}
	}
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		day := now.AddDate(0, 0, -(n - 1 - i)).Format("2006-01-02")
		out[i] = byDay[day]
	}
	return out
}

// v3Sparkline renders a compact inline SVG line chart from a series of values.
// It emits no inline styles (CSP-safe); all colour comes from CSS via
// currentColor on the .sparkline class. width/height are SVG viewBox units.
func v3Sparkline(vals []int) string {
	const w, h = 240, 48
	if len(vals) == 0 {
		return ""
	}
	maxV := 1
	for _, v := range vals {
		if v > maxV {
			maxV = v
		}
	}
	n := len(vals)
	stepX := float64(w) / float64(n-1)
	if n == 1 {
		stepX = 0
	}
	pts := make([]string, 0, n)
	for i, v := range vals {
		x := float64(i) * stepX
		// Leave 4px top/bottom padding so the stroke isn't clipped.
		y := float64(h-4) - (float64(v)/float64(maxV))*float64(h-8)
		pts = append(pts, strconv.FormatFloat(x, 'f', 1, 64)+","+strconv.FormatFloat(y, 'f', 1, 64))
	}
	poly := strings.Join(pts, " ")
	// Area fill path (down to baseline) + the line on top.
	area := "0," + strconv.Itoa(h) + " " + poly + " " + strconv.Itoa(w) + "," + strconv.Itoa(h)
	return `<svg class="sparkline" viewBox="0 0 ` + strconv.Itoa(w) + ` ` + strconv.Itoa(h) +
		`" preserveAspectRatio="none" role="img" aria-label="Publishing activity, last ` + strconv.Itoa(n) + ` days">` +
		`<polyline class="sparkline__area" points="` + area + `"/>` +
		`<polyline class="sparkline__line" points="` + poly + `"/>` +
		`</svg>`
}

func (a *App) handleV3Dashboard(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())
	snap := a.getAdminSnapshot()

	// 14-day publishing trend sparkline.
	trend := v3PublishTrend(r.Context(), 14)
	trendTotal := 0
	for _, v := range trend {
		trendTotal += v
	}
	sparkSVG := v3Sparkline(trend)

	pct := int(snap.StoragePct)
	storBar := "progress__bar"
	storWidth := storageWidthClass(pct)
	if pct >= 90 {
		storBar += " progress__bar--danger"
	} else if pct >= 75 {
		storBar += " progress__bar--warn"
	} else {
		storBar += " progress__bar--ok"
	}

	// Recent articles table
	recentHTML := `<div class="table-empty">No articles yet — <a href="/os/editor">write your first post</a>.</div>`
	if len(snap.RecentArticles) > 0 {
		rows := ""
		for _, ra := range snap.RecentArticles {
			rows += `<tr>
  <td class="row-title"><a href="/os/editor/` + html.EscapeString(ra.Slug) + `">` + html.EscapeString(ra.Title) + `</a>
    <div class="row-meta">/` + html.EscapeString(ra.Slug) + `</div></td>
  <td class="muted text-sm">` + ra.CreatedAt.UTC().Format("2 Jan 2006") + `</td>
  <td class="row-actions">
    <a class="btn btn--ghost btn--sm" href="/os/editor/` + html.EscapeString(ra.Slug) + `">Edit</a>
    <a class="btn btn--ghost btn--sm" href="/` + html.EscapeString(ra.Slug) + `" target="_blank" rel="noopener">View ↗</a>
  </td>
</tr>`
		}
		recentHTML = `<div class="table-wrap"><table class="table">
  <thead><tr><th>Title</th><th>Created</th><th></th></tr></thead>
  <tbody>` + rows + `</tbody>
</table></div>`
	}

	body := `<!-- Quick compose -->
<div class="quick-compose" role="search">
  <span class="quick-compose-icon" aria-hidden="true">✍</span>
  <input id="quick-compose-input" class="quick-compose-input"
    type="text" placeholder="Start a new post… (press Enter)" autocomplete="off"
    aria-label="Quick compose: type a title and press Enter">
</div>

<!-- Stat cards -->
<div class="stat-grid">
  <div class="stat-card">
    <div class="stat-card__top">
      <div class="stat-card__label">Articles</div>
      <div class="stat-card__icon stat-card__icon--brand">
        <svg viewBox="0 0 16 16" fill="none" width="16" height="16" aria-hidden="true"><path d="M2 3h12v2H2V3zm0 4h12v2H2V7zm0 4h8v2H2v-2z" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/></svg>
      </div>
    </div>
    <div class="stat-card__value">` + strconv.Itoa(snap.TotalArticles) + `</div>
    <div class="stat-card__bottom">
      <span class="muted text-xs">Published posts</span>
    </div>
  </div>
  <div class="stat-card">
    <div class="stat-card__top">
      <div class="stat-card__label">Pending jobs</div>
      <div class="stat-card__icon stat-card__icon--accent">
        <svg viewBox="0 0 16 16" fill="none" width="16" height="16" aria-hidden="true"><path d="M8 3v5l3 3" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2"/></svg>
      </div>
    </div>
    <div class="stat-card__value">` + strconv.Itoa(snap.PendingJobs) + `</div>
    <div class="stat-card__bottom">
      <span class="muted text-xs">In queue</span>
    </div>
  </div>
  <div class="stat-card">
    <div class="stat-card__top">
      <div class="stat-card__label">Failed jobs</div>
      <div class="stat-card__icon stat-card__icon--warn">
        <svg viewBox="0 0 16 16" fill="none" width="16" height="16" aria-hidden="true"><path d="M8 5v4m0 2.5v.5M3 13h10L8 3 3 13z" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </div>
    </div>
    <div class="stat-card__value">` + strconv.Itoa(snap.FailedJobs) + `</div>
    <div class="stat-card__bottom">
      <span class="muted text-xs">Needs attention</span>
    </div>
  </div>
  <div class="stat-card">
    <div class="stat-card__top">
      <div class="stat-card__label">Completed</div>
      <div class="stat-card__icon stat-card__icon--ok">
        <svg viewBox="0 0 16 16" fill="none" width="16" height="16" aria-hidden="true"><path d="M4 8l3 3 5-5" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/><circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="1.2"/></svg>
      </div>
    </div>
    <div class="stat-card__value">` + strconv.Itoa(snap.CompletedJobs) + `</div>
    <div class="stat-card__bottom">
      <span class="muted text-xs">All time</span>
    </div>
  </div>
</div>

<!-- Publishing trend -->
<div class="card mb-6">
  <div class="flex justify-between items-center">
    <div class="card-title">Publishing trend</div>
    <span class="text-xs muted">` + strconv.Itoa(trendTotal) + ` in last 14 days</span>
  </div>
  <div class="sparkline-wrap">` + sparkSVG + `</div>
</div>

<div class="grid grid-2 mb-6">
  <!-- Storage -->
  <div class="card">
    <div class="card-title">Storage</div>
    <div class="progress"><div class="` + storBar + ` ` + storWidth + `"></div></div>
    <div class="flex justify-between mt-3">
      <span class="text-xs muted">` + strconv.Itoa(pct) + `% used</span>
      <span class="text-xs muted">Cache hit ` + strconv.Itoa(int(snap.CacheHitRatio*100)) + `%</span>
    </div>
  </div>

  <!-- Activity feed -->
  <div class="card">
    <div class="card-title">Recent activity</div>
    <div id="activity-feed" class="activity-list">
      <!-- Populated by admin-v3.js via GET /os/api/activity -->
      <div class="skeleton skeleton--text mb-3"></div>
      <div class="skeleton skeleton--text mb-3 w-80"></div>
      <div class="skeleton skeleton--text w-65"></div>
    </div>
  </div>
</div>

<!-- Recent articles -->
<div class="card">
  <div class="card-title">Recent articles</div>
  ` + recentHTML + `
</div>`

	writeV3HTML(w, adminV3Layout(nonce, "Dashboard", "dashboard", cfg, body))
}

// ── Posts ────────────────────────────────────────────────────────────────────

func (a *App) handleV3Posts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())

	res, err := a.articles.List(r.Context(), 1, 200, "")
	count := 0
	rows := ""
	if err == nil {
		for _, p := range res.Articles {
			count++
			tags := ""
			searchTags := ""
			for _, t := range p.Tags {
				tags += `<span class="chip chip--brand">#` + html.EscapeString(t) + `</span> `
				searchTags += " " + t
			}
			rows += `<tr data-post-row data-search="` + html.EscapeString(strings.ToLower(p.Title+searchTags)) + `">
  <td class="row-title">
    <a href="/os/editor/` + html.EscapeString(p.Slug) + `">` + html.EscapeString(p.Title) + `</a>
    <div class="row-meta">/` + html.EscapeString(p.Slug) + `</div>
  </td>
  <td>` + tags + `</td>
  <td class="muted text-sm">` + p.UpdatedAt.UTC().Format("2 Jan 2006") + `</td>
  <td class="row-actions">
    <a class="btn btn--ghost btn--sm" href="/os/editor/` + html.EscapeString(p.Slug) + `">Edit</a>
    <a class="btn btn--ghost btn--sm" href="/` + html.EscapeString(p.Slug) + `" target="_blank" rel="noopener">View ↗</a>
  </td>
</tr>`
		}
	}

	var body string
	if count == 0 {
		body = `<div class="page-header"><h1>Posts</h1></div>
<div class="card empty-state">
  <div class="empty-icon">✍️</div>
  <div class="empty-title">No posts yet</div>
  <div class="empty-sub">Your published articles will appear here. Write your first one — it only takes a minute.</div>
  <a class="btn btn--primary mt-4" href="/os/editor">Write your first post</a>
</div>`
	} else {
		body = `<div class="page-header">
  <h1>Posts <span class="count-pill">` + strconv.Itoa(count) + `</span></h1>
  <div class="page-actions">
    <a class="btn btn--primary" href="/os/editor">New Post</a>
  </div>
</div>
<div class="card">
  <div class="toolbar-row">
    <input class="input search-input" type="search"
      data-posts-search placeholder="Search by title or tag…" aria-label="Search posts">
  </div>
  <div class="table-wrap">
    <table class="table">
      <thead><tr><th>Title</th><th>Tags</th><th>Updated</th><th></th></tr></thead>
      <tbody>` + rows + `</tbody>
    </table>
  </div>
  <div class="table-empty" data-search-empty hidden>No posts match your search.</div>
</div>`
	}
	writeV3HTML(w, adminV3Layout(nonce, "Posts", "posts", cfg, body))
}

// ── Editor ───────────────────────────────────────────────────────────────────

// handleV3Editor serves the post editor. To avoid any data loss during the
// gradual migration it picks the editor by article state:
//   - existing article with a block document      → native v3 block editor
//   - existing empty draft (no content, no blocks) → native v3 block editor
//   - existing article with legacy HTML/Markdown   → v2 editor (lossless)
//   - brand-new post (no slug)                     → v2 editor (handles create)
func (a *App) handleV3Editor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())
	slug := chi.URLParam(r, "slug")

	if slug != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		art, err := a.articles.Get(ctx, slug)
		if err == nil {
			blocksJSON := loadBlocksJSON(r.Context(), slug)
			hasBlocks := strings.TrimSpace(blocksJSON) != "" && strings.TrimSpace(blocksJSON) != "[]"
			emptyDraft := strings.TrimSpace(art.Content) == ""
			if hasBlocks || emptyDraft {
				body := v3EditorBody(slug, art.Title, blocksJSON)
				body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-v3-editor.js"></script>`
				writeV3HTML(w, adminV3Layout(nonce, "Edit Post", "editor", cfg, body))
				return
			}
			// Legacy content: fall through to the lossless v2 editor.
			a.serveV3LegacyEditor(w, r, nonce, cfg, slug, art.Title, art.Content)
			return
		}
	}

	// Brand-new post: the native block editor owns the create path (v1.6.0).
	// It hydrates with an empty document and an empty slug; the first Save POSTs
	// to /os/api/editor/save, which creates the article and returns its slug.
	body := v3EditorBody("", "", "[]")
	body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-v3-editor.js"></script>`
	writeV3HTML(w, adminV3Layout(nonce, "New Post", "editor", cfg, body))
}

// serveV3LegacyEditor renders the v2 editor body wrapped in v3 chrome. Used for
// legacy (non-block) articles and brand-new posts so no existing content path
// regresses while the block editor matures.
func (a *App) serveV3LegacyEditor(w http.ResponseWriter, r *http.Request, nonce string, cfg *v3Settings, slug, title, content string) {
	heading := "New Post"
	format, source := "markdown", ""
	if slug != "" {
		heading = "Edit Post"
		var f, s string
		if err := dbpkg.DB.QueryRowContext(r.Context(),
			`SELECT format, source FROM article_sources WHERE slug=?`, slug).Scan(&f, &s); err == nil && s != "" {
			format, source = f, s
		} else {
			format, source = "html", content
		}
	}
	edBody := editorBodyHTML(slug, heading, title, format, source)

	// For existing legacy posts, offer a non-destructive "Convert to blocks"
	// action (ADR-0069 Stage 1). It imports the HTML into a block document and
	// reopens this URL in the native block editor; the rendered article content
	// is untouched until the operator re-saves, so the conversion is reversible
	// by simply navigating away without saving.
	convertBanner := ""
	if slug != "" {
		convertBanner = `<div class="convert-banner" data-convert-banner>
  <div class="convert-banner__text">
    <strong>New block editor available.</strong>
    <span class="muted">Convert this post to editable blocks — your published content stays unchanged until you save.</span>
  </div>
  <button type="button" class="btn btn--primary btn--sm" data-convert-slug="` + html.EscapeString(slug) + `">Convert to blocks</button>
</div>
<script nonce="` + nonce + `">
(function(){
  var btn = document.querySelector('[data-convert-slug]');
  if(!btn) return;
  btn.addEventListener('click', function(){
    if(!window.confirm('Import this post into the block editor? Your live content will not change until you save in the new editor.')) return;
    var slug = btn.getAttribute('data-convert-slug');
    var m = document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
    var csrf = m ? decodeURIComponent(m[1]) : '';
    btn.disabled = true;
    btn.textContent = 'Converting…';
    fetch('/os/api/editor/convert', {
      method:'POST',
      headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},
      body: JSON.stringify({slug: slug})
    }).then(function(r){
      if(!r.ok) throw new Error('convert failed ('+r.status+')');
      return r.json();
    }).then(function(){
      window.location.href = '/os/editor/' + encodeURIComponent(slug);
    }).catch(function(err){
      btn.disabled = false;
      btn.textContent = 'Convert to blocks';
      window.alert(String(err.message||err));
    });
  });
})();
</script>`
	}

	body := convertBanner + edBody + `
<script nonce="` + nonce + `" src="/admin/v2/static/js/admin-v2.js"></script>`
	writeV3HTML(w, adminV3Layout(nonce, heading, "editor", cfg, body))
}

// ── SEO ──────────────────────────────────────────────────────────────────────
// The native SEO dashboard now lives in admin_v3_intel.go (handleV3SEONative).

// ── Settings ─────────────────────────────────────────────────────────────────

func (a *App) handleV3Settings(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getV3Settings(r.Context())
	group := chi.URLParam(r, "group")
	if group == "" {
		group = "general"
	}

	tabs := []struct{ Key, Label, Href string }{
		{"general", "General", "/os/settings/general"},
		{"design", "Design", "/os/settings/design"},
		{"members", "Members", "/os/settings/members"},
		{"email", "Email", "/os/settings/email"},
		{"security", "Security", "/os/settings/security"},
		{"advanced", "Advanced", "/os/settings/advanced"},
	}

	tabHTML := ""
	for _, t := range tabs {
		cls := "tab"
		if t.Key == group {
			cls += " tab--active"
		}
		tabHTML += `<a class="` + cls + `" href="` + t.Href + `">` + html.EscapeString(t.Label) + `</a>`
	}

	var groupBody string
	ss := a.siteSettings
	switch group {
	case "design":
		groupBody = v3SettingsDesign(r.Context(), ss)
	case "members":
		groupBody = v3SettingsMembers(r.Context(), ss)
	case "email":
		groupBody = v3SettingsEmail(r.Context(), ss)
	case "security":
		groupBody = v3SettingsSecurity(r.Context(), ss)
	case "advanced":
		groupBody = v3SettingsAdvanced(r.Context(), ss)
	default:
		groupBody = v3SettingsGeneral(r.Context(), ss)
	}

	body := `<div class="page-header">
  <h1>Settings</h1>
</div>
<nav class="tab-list" aria-label="Settings sections">` + tabHTML + `</nav>
<div class="card">` + groupBody + `</div>
<!-- Also show the update checker from v2 -->
` + buildV2SettingsBody(r.Context())

	writeV3HTML(w, adminV3Layout(nonce, "Settings", "settings", cfg, body))
}

func v3SettingsGeneral(ctx context.Context, ss *settings.Store) string {
	var siteName, tagline, desc, author string
	if ss != nil {
		siteName = ss.Get(ctx, settings.KeySiteName)
		tagline = ss.Get(ctx, settings.KeySiteTagline)
		desc = ss.Get(ctx, settings.KeySiteDescription)
		author = ss.Get(ctx, settings.KeySiteAuthor)
	}

	return `<div class="settings-section">
  <div class="settings-block-title">Site identity</div>
  <div class="field"><label class="field-label" for="s-name">Site name</label>
    <input id="s-name" class="input" type="text"
      data-setting-key="` + settings.KeySiteName + `"
      value="` + html.EscapeString(siteName) + `" placeholder="My Publication"></div>
  <div class="field"><label class="field-label" for="s-tagline">Tagline</label>
    <input id="s-tagline" class="input" type="text"
      data-setting-key="` + settings.KeySiteTagline + `"
      value="` + html.EscapeString(tagline) + `" placeholder="A short description"></div>
  <div class="field"><label class="field-label" for="s-desc">Description</label>
    <textarea id="s-desc" class="textarea"
      data-setting-key="` + settings.KeySiteDescription + `"
      placeholder="Used in RSS, sitemaps, and SEO meta">` + html.EscapeString(desc) + `</textarea></div>
  <div class="field"><label class="field-label" for="s-author">Author name</label>
    <input id="s-author" class="input" type="text"
      data-setting-key="` + settings.KeySiteAuthor + `"
      value="` + html.EscapeString(author) + `" placeholder="Your name"></div>
</div>`
}

func v3SettingsDesign(ctx context.Context, ss *settings.Store) string {
	primaryLight, primaryDark, customCSS := "#0f766e", "#2dd4bf", ""
	if ss != nil {
		if v := ss.Get(ctx, settings.KeyThemePrimaryLight); v != "" {
			primaryLight = v
		}
		if v := ss.Get(ctx, settings.KeyThemePrimaryDark); v != "" {
			primaryDark = v
		}
		customCSS = ss.Get(ctx, settings.KeyThemeCustomCSS)
	}

	return `<div class="settings-section">
  <div class="settings-block-title">Theme colours</div>
  <div class="settings-row">
    <div class="settings-row-info">
      <div class="settings-row-label">Primary colour (light mode)</div>
      <div class="settings-row-hint">Main brand colour used on the public site</div>
    </div>
    <input type="color" data-setting-key="` + settings.KeyThemePrimaryLight + `" value="` + html.EscapeString(primaryLight) + `">
  </div>
  <div class="settings-row">
    <div class="settings-row-info">
      <div class="settings-row-label">Primary colour (dark mode)</div>
    </div>
    <input type="color" data-setting-key="` + settings.KeyThemePrimaryDark + `" value="` + html.EscapeString(primaryDark) + `">
  </div>
  <div class="settings-block-title mt-4">Custom CSS</div>
  <div class="field">
    <label class="field-label" for="s-custom-css">Custom stylesheet (injected on public pages only)</label>
    <textarea id="s-custom-css" class="textarea font-mono" rows="8"
      data-setting-key="` + settings.KeyThemeCustomCSS + `"
      placeholder="/* Your custom CSS here */">` + html.EscapeString(customCSS) + `</textarea>
    <span class="field-hint">Applied to every public page. Never loaded in the admin panel.</span>
  </div>
</div>`
}

func v3SettingsMembers(_ context.Context, _ *settings.Store) string {
	return `<div class="settings-section">
  <div class="settings-block-title">Memberships</div>
  <div class="settings-row">
    <div class="settings-row-info">
      <div class="settings-row-label">Enable memberships</div>
      <div class="settings-row-hint">Allow readers to create free or paid accounts</div>
    </div>
    <input type="checkbox" class="toggle" data-setting-key="members.enabled" checked>
  </div>
  <div class="settings-row">
    <div class="settings-row-info">
      <div class="settings-row-label">Magic-link sign-in</div>
      <div class="settings-row-hint">Passwordless email links (no password required for members)</div>
    </div>
    <input type="checkbox" class="toggle" data-setting-key="members.magic_link" checked>
  </div>
  <p class="text-sm muted mt-4">Stripe webhook secret and paid tier configuration are set via environment variables. See documentation for details.</p>
</div>`
}

func v3SettingsEmail(ctx context.Context, ss *settings.Store) string {
	from := ""
	if ss != nil {
		from = ss.Get(ctx, "smtp.from")
	}
	return `<div class="settings-section">
  <div class="settings-block-title">SMTP</div>
  <p class="text-sm muted mb-4">Configure via environment variables: <code>SMTP_HOST</code>, <code>SMTP_PORT</code>, <code>SMTP_USER</code>, <code>SMTP_PASS</code>, <code>SMTP_FROM</code>, <code>SMTP_TLS</code>.</p>
  <div class="field">
    <label class="field-label" for="s-smtp-from">From address (display only)</label>
    <input id="s-smtp-from" class="input" type="email" data-setting-key="smtp.from"
      value="` + html.EscapeString(from) + `" placeholder="VayuPress &lt;hello@example.com&gt;">
  </div>
</div>`
}

func v3SettingsSecurity(_ context.Context, _ *settings.Store) string {
	return `<div class="settings-section">
  <div class="settings-block-title">Security (Phase 5)</div>
  <p class="text-sm muted">Two-factor authentication (TOTP), session management, IP allowlist, and audit log will be available in Phase 5.</p>
  <div class="mt-4">
    <a class="btn btn--ghost btn--sm" href="/admin/v2/settings">Legacy settings →</a>
  </div>
</div>`
}

func v3SettingsAdvanced(_ context.Context, _ *settings.Store) string {
	return `<div class="settings-section">
  <div class="settings-block-title">Cache</div>
  <div class="settings-row">
    <div class="settings-row-info">
      <div class="settings-row-label">Cache directory</div>
      <div class="settings-row-hint">Set via <code>CACHE_DIR</code> environment variable</div>
    </div>
    <code class="font-mono text-xs muted">` + html.EscapeString(config.Cfg.CacheDir) + `</code>
  </div>
  <div class="section-divider"></div>
  <div class="settings-block-title">Danger zone</div>
  <p class="text-sm muted">Destructive actions and data export are available in the classic console.</p>
  <a class="btn btn--ghost btn--sm mt-3" href="/admin" target="_blank">Open classic console ↗</a>
</div>`
}

// ── JSON APIs ─────────────────────────────────────────────────────────────────

// handleV3Activity returns recent admin activity as JSON for the dashboard feed.
func (a *App) handleV3Activity(w http.ResponseWriter, r *http.Request) {
	type activityItem struct {
		Kind string `json:"kind"`
		Icon string `json:"icon"`
		Text string `json:"text"`
		Time string `json:"time"`
	}

	items := []activityItem{}

	// Recent articles
	res, err := a.articles.List(r.Context(), 1, 5, "")
	if err == nil {
		for _, p := range res.Articles {
			items = append(items, activityItem{
				Kind: "post",
				Icon: "✍",
				Text: "Article published: " + p.Title,
				Time: p.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
	}

	// Recent members (if members are enabled)
	if a.members != nil {
		list, err := a.members.List(r.Context(), 3)
		if err == nil {
			for _, m := range list {
				items = append(items, activityItem{
					Kind: "member",
					Icon: "👤",
					Text: "Member joined: " + m.Email,
					Time: m.CreatedAt.UTC().Format(time.RFC3339),
				})
			}
		}
	}

	// Sort by time descending (simple bubble — small list)
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].Time > items[i].Time {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	if len(items) > 8 {
		items = items[:8]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

// handleV3CmdIndex returns the command palette search index as JSON.
func (a *App) handleV3CmdIndex(w http.ResponseWriter, r *http.Request) {
	type cmdPost struct{ Label, Slug string }
	type cmdAction struct{ Label, Icon, Hint, Fn string }
	type cmdSetting struct{ Label, Icon, Href string }

	posts := []cmdPost{}
	if res, err := a.articles.List(r.Context(), 1, 50, ""); err == nil {
		for _, p := range res.Articles {
			posts = append(posts, cmdPost{Label: p.Title, Slug: p.Slug})
		}
	}

	actions := []cmdAction{
		{Label: "New Post", Icon: "✍", Hint: "Open the editor", Fn: ""},
		{Label: "SEO Dashboard", Icon: "🔍", Fn: ""},
		{Label: "Regenerate SEO artefacts", Icon: "⟳", Fn: ""},
	}

	sPages := []cmdSetting{
		{Label: "Theme Studio", Icon: "🎨", Href: "/os/theme"},
		{Label: "Monitoring", Icon: "📈", Href: "/os/monitoring"},
		{Label: "Governance", Icon: "🛡", Href: "/os/governance"},
		{Label: "Tools & Plugins", Icon: "🧩", Href: "/os/tools"},
		{Label: "General settings", Icon: "⚙", Href: "/os/settings/general"},
		{Label: "Design & theme", Icon: "🎨", Href: "/os/settings/design"},
		{Label: "Email settings", Icon: "✉", Href: "/os/settings/email"},
		{Label: "Members settings", Icon: "👥", Href: "/os/settings/members"},
		{Label: "Security settings", Icon: "🔒", Href: "/os/settings/security"},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"posts":    posts,
		"actions":  actions,
		"settings": sPages,
	})
}

// handleV3SettingsAPI persists a single settings key/value from the admin v3 UI.
func (a *App) handleV3SettingsAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{body.Key: body.Value}); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "settings-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleV3QuickCreatePost creates a draft post from the dashboard quick-compose.
func (a *App) handleV3QuickCreatePost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeAPIError(w, r, http.StatusBadRequest, "empty-title", "Title is required", "")
		return
	}
	// Generate a unique slug from the title (shared with the native editor).
	slug := a.uniqueArticleSlug(r.Context(), title)
	// Create the draft. Content must be non-empty to pass article validation, so
	// we seed a single space: it trims to empty, so handleV3Editor treats the
	// post as an empty draft and opens the block editor, and the placeholder is
	// replaced by the rendered blocks on the first save.
	if _, err := a.articles.Create(r.Context(), title, slug, " ", nil); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "create-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"slug": slug})
}

// ── Helpers shared with v2 ────────────────────────────────────────────────────

// buildV2SettingsBody returns the update-checker card from v2 settings.
// Phase 7 will have its own version; for now we reuse the v2 snapshot.
func buildV2SettingsBody(_ context.Context) string {
	// Return minimal body — the full update check logic runs in handleV2Settings.
	// v3 Phase 1 just links to the v2 settings update panel.
	return `<div class="card mt-4">
  <div class="card-title">Software updates</div>
  <p class="text-sm muted">Update history and version management are available in
  <a href="/admin/v2/settings">Admin v2 settings</a> while Phase 7 is in progress.</p>
</div>`
}
