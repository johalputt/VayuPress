package main

// admin_os_ui.go — VayuPress VayuOS, mounted under /os.
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
//   - No inline <style> or style="" attributes. All CSS lives in admin-os.css.
//   - The only inline <script> block carries the per-request CSP nonce.
//   - No external CDNs. All assets served same-origin under /os/static/.
//   - All user-originated strings escaped with html.EscapeString before HTML emit.
//   - DOM mutations in admin-os.js use textContent / createElement; no innerHTML
//     with untrusted data.
//
// Phase 1 implements: login page redesign, new grouped sidebar, stat-card
// dashboard, posts table, editor wrapper, settings page, SEO page.
// Phases 2-7 add block editor, media library, members, TOTP security, i18n,
// GraphQL admin, command palette, and all remaining intelligence features.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	htmpl "html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

// ── Static asset path ────────────────────────────────────────────────────────

func adminOSStaticDir() string {
	return config.EnvOr("STATIC_DIR", "/var/www/vayupress/static")
}

// ── Route registration ───────────────────────────────────────────────────────

// registerAdminOSUIRoutes mounts VayuOS under /os.
// Follows the same auth/CSP/CSRF patterns as Admin v2 (admin_ui.go).
func (a *App) registerAdminOSUIRoutes(r chi.Router) {
	// VayuOS is now a legacy surface (ADR-0069 Stage 3 in progress): the
	// canonical admin is VayuOS at /os. Old /admin/v3[/...] URLs 302-redirect
	// into the /os equivalent, joining /admin and /admin/v2.
	osRedirect := legacyRedirect()
	r.Get("/admin/v3", osRedirect)
	r.Handle("/admin/v3/*", osRedirect)

	// Public static assets (served same-origin so CSP 'self' covers them).
	r.Get("/os/static/css/admin-os.css", serveAdminOSAsset("css/admin-os.css", "text/css; charset=utf-8"))
	r.Get("/os/static/js/admin-os.js", serveAdminOSAsset("js/admin-os.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-editor.js", serveAdminOSAsset("js/admin-os-editor.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-security.js", serveAdminOSAsset("js/admin-os-security.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-members.js", serveAdminOSAsset("js/admin-os-members.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-newsletter.js", serveAdminOSAsset("js/admin-os-newsletter.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-profile.js", serveAdminOSAsset("js/admin-os-profile.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-intel.js", serveAdminOSAsset("js/admin-os-intel.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-pages.js", serveAdminOSAsset("js/admin-os-pages.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-tools.js", serveAdminOSAsset("js/admin-os-tools.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-monitoring.js", serveAdminOSAsset("js/admin-os-monitoring.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-theme.js", serveAdminOSAsset("js/admin-os-theme.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/theme-preview-frame.js", serveAdminOSAsset("js/theme-preview-frame.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-theme-store.js", serveAdminOSAsset("js/admin-os-theme-store.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-mail.js", serveAdminOSAsset("js/admin-os-mail.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-update.js", serveAdminOSAsset("js/admin-os-update.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-storage.js", serveAdminOSAsset("js/admin-os-storage.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/purify.min.js", serveAdminOSAsset("js/purify.min.js", "application/javascript; charset=utf-8"))

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
		http.ServeFile(w, req, filepath.Join(adminOSStaticDir(), "fonts", canon))
	})

	// Country flag SVGs (flag-icons, MIT) compiled into the binary and served
	// on demand from /os/static/flags/<cc>.svg. Path-traversal is impossible:
	// the filename is validated to be exactly a two-letter lowercase ISO code,
	// and the bytes come from the embedded FS — never the live filesystem.
	r.Get("/os/static/flags/{file}", func(w http.ResponseWriter, req *http.Request) {
		file := chi.URLParam(req, "file")
		if !isFlagFile(file) {
			http.NotFound(w, req)
			return
		}
		data, err := flagFS.ReadFile("flags/" + file)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})

	// Public: login page and credential forms.
	r.Get("/os/login", a.handleOSLogin)
	r.Post("/os/login", a.handleOSLoginSubmit)
	r.Post("/os/logout", a.handleOSLogout)

	// Protected pages and APIs — require session or API key.
	r.Group(func(pr chi.Router) {
		pr.Use(a.requireSessionOrAPIKey)

		// Pages
		pr.Get("/os", a.handleOSDashboard)
		pr.Get("/os/posts", a.handleOSPosts)
		pr.Get("/os/comments", a.handleOSComments)
		// Session-friendly comment moderation. The /api/v1/admin/comments originals
		// require an API key; VayuOS operators hold a session cookie.
		pr.With(auth.CSRFTokenMiddleware).Put("/os/api/comments/{id}/status", a.handleCommentModerate)
		// Custom pages — standalone articles flagged is_page (no post chrome),
		// managed separately from the blog feed (Tumblr-style "Add a page").
		pr.Get("/os/pages", a.handleOSPages)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/pages/quick-create", a.handleOSQuickCreatePage)
		// Contact-form inbox — durable record of public contact submissions.
		pr.Get("/os/messages", a.handleOSMessages)
		pr.Get("/os/messages/{id}", a.handleOSMessageDetail)
		pr.With(auth.CSRFTokenMiddleware).Put("/os/api/messages/{id}/read", a.handleOSMessageRead)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/messages/{id}", a.handleOSMessageDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/messages/read-all", a.handleOSMessagesReadAll)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/messages/delete-read", a.handleOSMessagesDeleteRead)
		pr.Get("/os/api/messages/export.csv", a.handleOSMessagesExportCSV)
		pr.Get("/os/media", a.handleOSMedia)
		pr.Get("/os/api/media", a.handleOSMediaList)
		// Session-friendly media upload + import. The /api/v1/admin/media originals
		// require an API key; VayuOS operators hold a session cookie, so the browser
		// Media library must POST here instead (same handlers, CSRF-protected).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/media/upload", a.handleMediaUpload)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/media/delete", a.handleOSMediaDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/media/alt", a.handleOSMediaAlt)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/media/import", a.handleMediaImport)
		pr.Get("/os/members", a.handleOSMembers)
		// Session-friendly membership management APIs (the /api/v1/admin/* originals
		// require an API key; VayuOS operators hold a session cookie).
		pr.Get("/os/api/members/stats", a.handleMemberStats)
		pr.Get("/os/api/members/export.csv", a.handleMembersExportCSV)
		pr.Get("/os/api/members/{email}", a.handleMemberDetail)
		pr.Get("/os/api/members/tiers", a.handleTierListAdmin)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/members/tiers", a.handleTierCreate)
		pr.With(auth.CSRFTokenMiddleware).Put("/os/api/members/tiers/{id}", a.handleTierUpdate)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/members/tiers/{id}", a.handleTierDelete)
		pr.With(auth.CSRFTokenMiddleware).Put("/os/api/members/{email}/tier", a.handleMemberSetTier)
		pr.With(auth.CSRFTokenMiddleware).Put("/os/api/members/{email}/cancel", a.handleMemberCancel)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/members/{email}/labels", a.handleMemberLabelAdd)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/members/{email}/labels/{label}", a.handleMemberLabelRemove)
		// Newsletter console — the operator page plus session-friendly management
		// APIs (the /api/v1/admin/newsletter/* originals require an API key; os
		// operators hold a session cookie). Writes are CSRF-protected.
		pr.Get("/os/newsletter", a.handleOSNewsletter)
		pr.Get("/os/api/newsletter/stats", a.handleOSNewsletterStats)
		pr.Get("/os/api/newsletter/subscribers", a.handleOSNewsletterSubscribers)
		pr.Get("/os/api/newsletter/broadcasts", a.handleOSNewsletterBroadcasts)
		pr.Get("/os/api/newsletter/export.csv", a.handleOSNewsletterExport)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/newsletter/subscribers/{id}", a.handleOSNewsletterDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/newsletter/test", a.handleOSNewsletterSendTest)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/newsletter/broadcast", a.handleOSNewsletterBroadcastSend)
		// Self-service author profile + admin team/role management (session mirrors).
		pr.Get("/os/profile", a.handleOSProfile)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/profile", a.handleProfileSave)
		pr.Get("/os/api/users", a.handleUserList)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/users", a.handleUserCreate)
		pr.With(auth.CSRFTokenMiddleware).Put("/os/api/users/{email}/role", a.handleUserSetRole)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/users/{email}/mailbox", a.handleAssignMailbox)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/users/{email}", a.handleUserDelete)
		pr.Get("/os/security", a.handleOSSecurity)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/totp/begin", a.handleOSTOTPBegin)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/totp/verify", a.handleOSTOTPVerify)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/totp/disable", a.handleOSTOTPDisable)
		pr.Get("/os/editor", a.handleOSEditor)
		pr.Get("/os/editor/{slug}", a.handleOSEditor)
		pr.Get("/os/monitoring", a.handleOSMonitoring)
		pr.Get("/os/governance", a.handleOSGovernance)
		pr.Get("/os/theme", a.handleOSTheme)
		pr.Get("/os/theme/store", a.handleOSThemeStore)
		pr.Get("/os/theme/preview", a.handleOSThemePreview)
		pr.Get("/os/theme/preview.css", a.handleOSThemePreviewCSS)
		// Session-friendly mirrors of the Theme Studio JSON API (the /api/v1/admin
		// originals require an API key; os operators hold a session cookie).
		pr.Get("/os/api/theme/presets", a.handleThemePresets)
		pr.Get("/os/api/theme/tokens", a.handleThemeTokens)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/preview", a.handleThemePreview)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/preview-draft", a.handleOSThemePreviewDraft)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/apply", a.handleThemeApply)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/code", a.handleOSThemeCode)
		pr.Get("/os/api/theme/export", a.handleOSThemeExport)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/import", a.handleOSThemeImport)
		// Session-friendly read-only mirrors of the operator JSON APIs (the
		// /api/v1/admin/* originals require an API key; os operators hold a
		// session cookie). Same handlers, no CSRF needed for GETs.
		pr.Get("/os/api/mode", a.handleModeStatus)
		pr.Get("/os/api/budgets", a.handleGovernanceBudgets)
		pr.Get("/os/tools", a.handleOSTools)
		pr.Get("/os/api/tools", a.handleOSToolsList)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/tools/toggle", a.handleOSToolToggle)

		// Monetization — payment order ledger + gateway config, and the
		// activation-gated advertising surface.
		pr.Get("/os/monetization", a.handleOSMonetization)
		pr.Get("/os/api/orders", a.handleOSOrdersList)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/orders/{id}/paid", a.handleOSOrderMarkPaid)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/orders/{id}/cancel", a.handleOSOrderCancel)
		pr.Get("/os/ads", a.handleOSAds)
		pr.Get("/os/api/ads", a.handleOSAdsList)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/ads", a.handleOSAdCreate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/ads/{id}/toggle", a.handleOSAdToggle)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/ads/{id}", a.handleOSAdDelete)

		// Update & Backup — one-click signature-verified self-update plus full
		// site (database + settings) export/import. Writes are CSRF-protected and
		// admin-role gated inside each handler; export/import lift the server
		// read/write deadlines so transfers have no size limit.
		pr.Get("/os/update", a.handleOSUpdate)
		pr.Get("/os/api/update/check", a.handleOSUpdateCheck)
		pr.Get("/os/api/update/history", a.handleOSUpdateHistory)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/update/apply", a.handleOSUpdateApply)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/update/restart", a.handleOSUpdateRestart)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/update/rollback", a.handleOSUpdateRollback)
		pr.Get("/os/api/backup/export", a.handleOSBackupExport)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/backup/import", a.handleOSBackupImport)

		// Storage & System — admin-only resource usage (RAM/disk) plus managed
		// files (backups/logs/temp) with per-file download + delete. The
		// download/delete validate the path against the live managed-file set, so
		// path traversal is impossible and the live DB can never be touched.
		pr.Get("/os/storage", a.handleOSStorage)
		pr.Get("/os/api/storage/download", a.handleOSStorageDownload)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/storage/delete", a.handleOSStorageDelete)
		pr.Get("/os/seo", a.handleOSSEONative)
		pr.Get("/os/analytics", a.handleOSAnalytics)
		// VayuAnalytics: export downloads + goal management (session-authed).
		pr.Get("/os/api/analytics/export", a.handleAnalyticsExport)
		pr.Get("/os/api/analytics/realtime", a.handleAnalyticsRealtime)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/analytics/goals", a.handleAnalyticsCreateGoal)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/analytics/goals/{id}", a.handleAnalyticsDeleteGoal)
		// VayuOS — native control layer (Phase 2): Publishing · Mail · PGP.
		// GET pages are wrapped in CSRFTokenMiddleware so each load (re)issues the
		// vp_csrf cookie the panel's POSTs read back; without this the token
		// expires (1h) and Send / Save-as-draft / message actions start 403ing.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos", a.handleVayuOSDashboard)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/pgp", a.handleVayuOSPGP)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail", a.handleVayuOSMail)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail/inbox", a.handleVayuOSInbox)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail/search", a.handleVayuOSSearch)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail/message", a.handleVayuOSMessage)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail/sent", a.handleVayuOSSent)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail/compose", a.handleVayuOSCompose)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail/accounts", a.handleVayuOSAccounts)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuos/mail/connect", a.handleVayuOSConnect)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayuos/mail/send", a.handleVayuOSSend)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayuos/mail/draft", a.handleVayuOSDraft)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayuos/mail/message/action", a.handleVayuOSMessageAction)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayuos/mail/accounts/create", a.handleVayuOSAccountCreate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayuos/mail/accounts/delete", a.handleVayuOSAccountDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayuos/mail/accounts/update", a.handleVayuOSAccountUpdate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayuos/mail/accounts/totp", a.handleVayuOSAccountTOTP)
		pr.Get("/os/vayuos/security", a.handleVayuOSSecurity)
		pr.Get("/os/api/vayuos/health", a.handleVayuOSHealthJSON)
		pr.Get("/os/settings", a.handleOSSettings)
		pr.Get("/os/settings/{group}", a.handleOSSettings)

		// API Keys console — VayuPress's own rotatable bearer tokens plus
		// encrypted third-party service credentials (IndexNow, OpenRouter,
		// Ollama, n8n, custom).
		pr.Get("/os/apikeys", a.handleOSAPIKeys)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/apikeys/create", a.handleOSAPIKeyCreate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/apikeys/rotate", a.handleOSAPIKeyRotate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/apikeys/revoke", a.handleOSAPIKeyRevoke)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/apikeys/delete", a.handleOSAPIKeyDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/credentials/save", a.handleOSCredentialSave)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/credentials/reveal", a.handleOSCredentialReveal)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/credentials/delete", a.handleOSCredentialDelete)

		// CSRF-protected writes
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/seo/regenerate", a.handleSEORegenerate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/settings", a.handleOSSettingsAPI)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/quick-create", a.handleOSQuickCreatePost)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/status", a.handleOSPostStatus)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/posts/{slug}", a.handleOSPostDelete)
		// Session-friendly branding (favicon) upload — the /admin/theme/favicon
		// original is in the API-key-only group, so a browser operator can't reach
		// it. This mirror is gated by requireSessionOrAPIKey + CSRF.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/branding/favicon", a.handleFaviconUpload)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/branding/hero", a.handleHeroUpload)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/branding/og", a.handleOGUpload)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/save", a.handleOSEditorSave)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/preview", a.handleOSEditorPreview)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/import", a.handleOSEditorImport)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/slug", a.handleOSEditorSlug)
		// Session-friendly mirrors of the editor's block tools (the /api/v1/admin
		// originals require an API key; os operators hold a session cookie).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/embed/unfurl", a.handleEmbedUnfurl)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/diagram/preview", a.handleDiagramPreview)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/ai", a.handleOSEditorAI)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/convert", a.handleOSEditorConvert)
		pr.Get("/os/api/editor/versions/{slug}", a.handleOSEditorVersionList)
		pr.Get("/os/api/editor/versions/{slug}/{id}", a.handleOSEditorVersionGet)

		// Read-only APIs (no CSRF needed)
		pr.Get("/os/api/activity", a.handleOSActivity)
		pr.Get("/os/api/cmd-index", a.handleOSCmdIndex)
		pr.Get("/os/api/search/drift", a.handleSearchDrift)

		// Interactive operator consoles — rendered in the VayuOS shell.
		// These were previously in the RequireAPIKey group in routes.go, which
		// caused a 401 JSON error when visited from a browser (no API key header).
		// They belong here under requireSessionOrAPIKey so a browser session works.
		pr.Get("/os/modes", a.handleModesPage)
		pr.Get("/os/faults", a.handleFaultPage)
		pr.Get("/os/topology", a.handleTopologyPage)
		pr.Get("/os/replay", a.handleReplayPage)
		pr.Get("/os/policy", a.handlePolicyPage)
		pr.Get("/os/adr", a.handleAdminADR)

		// Operator-initiated actions — API key callers don't hold a browser session
		// so they have no CSRF cookie. These are gated by requireSessionOrAPIKey;
		// browser callers arriving via the panel always carry a session and are
		// protected by the SameSite=Strict session cookie which prevents CSRF itself.
		pr.Post("/os/api/search/reindex", a.handleOSSearchReindex)
		pr.Post("/os/api/feed/regenerate", a.handleOSFeedRegenerate)
	})

	// Redirect bare /os/* to dashboard if hitting unknown paths
	r.Get("/os/*", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/os", http.StatusSeeOther)
	})
}

func serveAdminOSAsset(rel, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		diskPath := filepath.Join(adminOSStaticDir(), filepath.FromSlash(rel))
		if _, err := os.Stat(diskPath); err == nil {
			http.ServeFile(w, req, diskPath)
			return
		}
		// The on-disk copy is missing — e.g. STATIC_DIR was never provisioned, or
		// is read-only under a hardened service sandbox so syncEmbeddedStatic
		// could not write it. Serve the copy compiled into the binary so the
		// panel always works, even immediately after a one-click self-update.
		if data, err := fs.ReadFile(embeddedStaticFS, rel); err == nil {
			http.ServeContent(w, req, filepath.Base(rel), time.Time{}, bytes.NewReader(data))
			return
		}
		http.NotFound(w, req)
	}
}

// assetVerCache memoises per-asset cache-busting tokens.
var assetVerCache sync.Map // rel -> string

// assetVer returns a cache-busting query value for a VayuOS static asset that
// combines the release Version with a short content hash of the file. Because
// it tracks file content, browsers refetch CSS/JS as soon as it actually
// changes — even between builds that share the same release Version — while
// still caching aggressively when nothing changed.
func assetVer(rel string) string {
	if v, ok := assetVerCache.Load(rel); ok {
		return v.(string)
	}
	v := Version
	b, err := os.ReadFile(filepath.Join(adminOSStaticDir(), filepath.FromSlash(rel)))
	if err != nil {
		// Fall back to the embedded copy so the cache-buster still tracks the
		// shipped asset content when STATIC_DIR is unavailable (ADR-0099).
		b, err = fs.ReadFile(embeddedStaticFS, rel)
	}
	if err == nil {
		sum := sha256.Sum256(b)
		v = Version + "-" + hex.EncodeToString(sum[:4])
	}
	assetVerCache.Store(rel, v)
	return v
}

// ── Shared layout ────────────────────────────────────────────────────────────

// navItem builds a sidebar nav link with an inline SVG icon.
func navItem(href, label, key, active, iconSVG string) string {
	return navItemBadge(href, label, key, active, iconSVG, 0)
}

// navItemBadge is navItem with an optional unread-count pill. A count <= 0
// renders no badge; counts over 99 cap at "99+" so the pill stays compact.
func navItemBadge(href, label, key, active, iconSVG string, count int) string {
	cls := "nav-link"
	if key == active {
		cls += " active"
	}
	badge := ""
	if count > 0 {
		txt := intToStr(count)
		if count > 99 {
			txt = "99+"
		}
		badge = `<span class="nav-badge" aria-label="` + txt + ` unread">` + txt + `</span>`
	}
	return `<a class="` + cls + `" href="` + href + `">` +
		iconSVG +
		html.EscapeString(label) +
		badge +
		`</a>`
}

// unreadMessagesLabel is the dashboard Messages-card footer link text.
func unreadMessagesLabel(unread int) string {
	if unread > 0 {
		return "Unread — open inbox →"
	}
	return "Open inbox →"
}

// osUnread safely reads the unread-message count for the sidebar badge.
func osUnread(s *osSettings) int {
	if s == nil {
		return 0
	}
	return s.UnreadMessages
}

// osSidebarNav builds the role-scoped sidebar. A mail-only session (mailbox /
// reviewer role) sees only its Mailbox and Profile; console sessions see only
// the sections their access level permits. The visibility rule is exactly the
// route guard (osPathMinLevel) so what is shown is precisely what is reachable —
// hidden items are also blocked server-side.
func osSidebarNav(active string, s *osSettings) string {
	lvl := accessAdmin
	mailOnly := false
	if s != nil {
		lvl = s.AccessLevel
		mailOnly = s.MailOnly
	}
	if mailOnly {
		return `<div class="sidebar-section-label">Mail</div>` +
			navItem("/os/vayuos/mail/inbox", "Mailbox", "vayuos", active, iconSecurity) +
			navItem("/os/profile", "My Profile", "profile", active, iconMembers)
	}

	var b strings.Builder
	// gate returns the item only when this access level can reach its href.
	gate := func(item, href string) string {
		if lvl < osPathMinLevel(href) {
			return ""
		}
		return item
	}
	section := func(label string, items ...string) {
		shown := make([]string, 0, len(items))
		for _, it := range items {
			if it != "" {
				shown = append(shown, it)
			}
		}
		if len(shown) == 0 {
			return
		}
		b.WriteString(`<div class="sidebar-section-label">` + label + `</div>`)
		for _, it := range shown {
			b.WriteString(it)
		}
	}

	section("Content",
		gate(navItem("/os", "Dashboard", "dashboard", active, iconDashboard), "/os"),
		gate(navItem("/os/posts", "Posts", "posts", active, iconPosts), "/os/posts"),
		gate(navItem("/os/comments", "Comments", "comments", active, iconComments), "/os/comments"),
		gate(navItem("/os/pages", "Pages", "pages", active, iconPages), "/os/pages"),
		gate(navItemBadge("/os/messages", "Messages", "messages", active, iconMessages, osUnread(s)), "/os/messages"),
		gate(navItem("/os/editor", "New Post", "editor", active, iconNewPost), "/os/editor"),
		gate(navItem("/os/media", "Media", "media", active, iconMedia), "/os/media"),
	)
	section("Audience",
		gate(navItem("/os/members", "Members", "members", active, iconMembers), "/os/members"),
		gate(navItem("/os/newsletter", "Newsletter", "newsletter", active, iconNewsletter), "/os/newsletter"),
		navItem("/os/profile", "My Profile", "profile", active, iconMembers),
	)
	section("Monetization",
		gate(navItem("/os/monetization", "Monetization", "monetization", active, iconMoney), "/os/monetization"),
		gate(navItem("/os/ads", "Advertising", "ads", active, iconAds), "/os/ads"),
	)
	section("Optimize",
		gate(navItem("/os/seo", "SEO", "seo", active, iconSEO), "/os/seo"),
		gate(navItem("/os/analytics", "Analytics", "analytics", active, iconAnalytics), "/os/analytics"),
		gate(navItem("/os/theme", "Theme Studio", "theme", active, iconTheme), "/os/theme"),
		gate(navItem("/os/theme/store", "Theme Store", "theme-store", active, iconThemeStore), "/os/theme/store"),
		navItem("/os/vayuos", "VayuMail", "vayuos", active, iconSecurity),
	)
	section("System",
		gate(navItem("/os/monitoring", "Monitoring", "monitoring", active, iconMonitoring), "/os/monitoring"),
		gate(navItem("/os/governance", "Governance", "governance", active, iconGovernance), "/os/governance"),
		gate(navItem("/os/tools", "Tools & Plugins", "tools", active, iconTools), "/os/tools"),
		gate(navItem("/os/update", "Update & Backup", "update", active, iconUpdate), "/os/update"),
		gate(navItem("/os/storage", "Storage & System", "storage", active, iconStorage), "/os/storage"),
		gate(navItem("/os/settings", "Settings", "settings", active, iconSettings), "/os/settings"),
		gate(navItem("/os/apikeys", "API Keys", "apikeys", active, iconKey), "/os/apikeys"),
		gate(navItem("/os/security", "Security", "security", active, iconSecurity), "/os/security"),
	)
	section("Operations",
		gate(navItem("/os/modes", "System Modes", "modes", active, iconModes), "/os/modes"),
		gate(navItem("/os/policy", "Policy Inspector", "policy", active, iconPolicy), "/os/policy"),
		gate(navItem("/os/topology", "Topology", "topology", active, iconTopology), "/os/topology"),
		gate(navItem("/os/replay", "Replay Explorer", "replay", active, iconReplay), "/os/replay"),
		gate(navItem("/os/faults", "Fault Engine", "faults", active, iconFaults), "/os/faults"),
		gate(navItem("/os/adr", "ADR Registry", "adrs", active, iconADR), "/os/adr"),
	)
	return b.String()
}

// svgIcon returns a minimal inline SVG for the sidebar.
// Using path data keeps us CDN-free and avoids an extra HTTP round-trip.
func svgIcon(path string) string {
	return `<svg viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d="` + path + `" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`
}

var (
	iconDashboard  = svgIcon("M3 10.5L10 3l7 7.5M5 8.5V17h3.5v-4h3v4H15V8.5")
	iconPosts      = svgIcon("M4 4h12v2H4V4zm0 4h12v2H4V8zm0 4h8v2H4v-2z")
	iconComments   = svgIcon("M3 4h14v9H7l-4 3V4zm3 3h8M6 10h5")
	iconPages      = svgIcon("M5 2h7l3 3v13H5V2zm7 0v3h3M7 9h6M7 12h6M7 15h4")
	iconMessages   = svgIcon("M2 4h16v10H6l-4 3V4zm3 4h10M5 11h7")
	iconNewPost    = svgIcon("M10 4v12m-6-6h12")
	iconMedia      = svgIcon("M3 5a2 2 0 012-2h10a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2V5zm0 8l4-4 3 3 2-2 4 4")
	iconMembers    = svgIcon("M13 6a3 3 0 11-6 0 3 3 0 016 0zm-9 10a6 6 0 1112 0H4z")
	iconNewsletter = svgIcon("M3 8l7-4 7 4v8a1 1 0 01-1 1H4a1 1 0 01-1-1V8zm7-1v9m-4-6h8")
	iconSEO        = svgIcon("M8 15A7 7 0 108 1a7 7 0 000 14zm5-1l4 4")
	iconAnalytics  = svgIcon("M3 17l4-8 4 4 4-6 4 4")
	iconSettings   = svgIcon("M10 13a3 3 0 100-6 3 3 0 000 6zm0 0v1m0-8V5M4.2 4.2l.7.7m10-.7l-.7.7M3 10H2m16 0h-1M4.9 15.8l.7-.7m9.5.7l-.7-.7")
	iconSecurity   = svgIcon("M10 2l6 3v5c0 3.5-2.5 6.8-6 8-3.5-1.2-6-4.5-6-8V5l6-3z")
	iconTools      = svgIcon("M12.5 3.5a3 3 0 00-3.9 3.9l-5.1 5.1 2 2 5.1-5.1a3 3 0 003.9-3.9l-2 2-2-2 2-2z")
	iconUpdate     = svgIcon("M3 10a7 7 0 0112-4.9L17 7m0 0V3m0 4h-4M17 10a7 7 0 01-12 4.9L3 13m0 0v4m0-4h4")
	iconStorage    = svgIcon("M3 5a2 2 0 012-2h10a2 2 0 012 2v2H3V5zm0 4h14v6a2 2 0 01-2 2H5a2 2 0 01-2-2V9zm3 3h2")
	iconMonitoring = svgIcon("M2 10h3l2-5 3 11 3-8 2 2h3")
	iconGovernance = svgIcon("M10 2l7 3v5c0 3.5-2.8 6.8-7 8-4.2-1.2-7-4.5-7-8V5l7-3zm0 5v6m-3-3h6")
	iconTheme      = svgIcon("M10 2a8 8 0 100 16c1 0 1.5-.7 1.5-1.5 0-.4-.2-.8-.4-1-.3-.3-.4-.6-.4-1 0-.8.7-1.5 1.5-1.5H14a4 4 0 004-4c0-3.6-3.6-6.5-8-6.5zM5.5 10a1 1 0 110-2 1 1 0 010 2zm3-3a1 1 0 110-2 1 1 0 010 2zm5 0a1 1 0 110-2 1 1 0 010 2z")
	iconThemeStore = svgIcon("M3 7l1.5-3h11L17 7M3 7h14M3 7v9a1 1 0 001 1h12a1 1 0 001-1V7M8 7v3a2 2 0 004 0V7")
	iconModes      = svgIcon("M10 2l7 4v8l-7 4-7-4V6l7-4zm0 2.3L5 7v6l5 2.7L15 13V7l-5-2.7z")
	iconPolicy     = svgIcon("M10 2l6 3v5c0 3.5-2.5 6.8-6 8-3.5-1.2-6-4.5-6-8V5l6-3zm-1 9l4-4-1.4-1.4L9 8.2 7.4 6.6 6 8l3 3z")
	iconTopology   = svgIcon("M10 3a2 2 0 100 4 2 2 0 000-4zM4 13a2 2 0 100 4 2 2 0 000-4zm12 0a2 2 0 100 4 2 2 0 000-4zM10 7v3m0 0l-4 3m4-3l4 3")
	iconReplay     = svgIcon("M4 10a6 6 0 116 6m-6-6l-2-2m2 2l2-2m-2 8v-2")
	iconFaults     = svgIcon("M10 2l8 14H2L10 2zm0 5v4m0 3h.01")
	iconADR        = svgIcon("M5 3h7l3 3v11H5V3zm7 0v3h3M7 9h6m-6 3h6m-6 3h4")
	iconMoney      = svgIcon("M10 2v16M6.5 6.5h5a2 2 0 010 4h-3a2 2 0 000 4h5")
	iconAds        = svgIcon("M3 5h14v8H3V5zm2 11h6M6 8h6m-6 2.5h4")
)

// renderTrustedHTML emits a pre-constructed, server-side HTML fragment verbatim.
// The page body is assembled from fixed templates with every interpolated user
// value escaped via html.EscapeString at construction, so it is already safe.
//
// It is intentionally a plain string conversion — NOT an html/template
// execution. Passing a template.HTML value into html/template's Execute is what
// CodeQL flags as an "escaping bypass" (go/html-template-escaping-bypass), and
// since the passthrough emits the bytes unchanged either way, the direct
// conversion is equivalent and keeps the data off the html/template sink.
func renderTrustedHTML(h htmpl.HTML) string {
	return string(h)
}

// adminOSLayout renders the shared chrome for VayuOS.
// The nonce is injected into the single inline bootstrap <script> block.
// All CSS/JS are external same-origin files. No inline styles.
//
// It is composed from adminOSShellHead + the body + adminOSShellFoot so that
// streaming operator pages (System Modes, Policy, Topology, Replay, Faults,
// ADRs) can share the exact same VayuOS chrome without buffering their whole
// body — they call the head/foot helpers directly.
func adminOSLayout(nonce, title, active string, settings *osSettings, bodyHTML htmpl.HTML) string {
	return adminOSShellHead(nonce, title, active, settings) +
		renderTrustedHTML(bodyHTML) +
		adminOSShellFoot(nonce, "")
}

// adminOSShellHead emits the VayuOS document head, sidebar, topbar and the
// opening <main class="content"> tag. The caller appends body content and then
// adminOSShellFoot.
func adminOSShellHead(nonce, title, active string, settings *osSettings) string {
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

	// Mail-only sessions cannot create posts; hide the topbar shortcut for them.
	newPostBtn := `<a class="btn btn--primary btn--sm" href="/os/editor">New Post</a>`
	if settings != nil && settings.MailOnly {
		newPostBtn = ""
	}

	return `<!DOCTYPE html>
<html lang="en" data-theme="` + html.EscapeString(theme) + `">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + et + ` — ` + siteName + ` · VayuOS</title>
<meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/os/static/css/admin-os.css?v=` + assetVer("css/admin-os.css") + `">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="vp-os" data-admin-theme="` + html.EscapeString(theme) + `">
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
    ` + osSidebarNav(active, settings) + `
    <div class="sidebar-spacer"></div>
  </nav>
  <div class="sidebar-footer">
    ` + osSidebarUser(settings) + `
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
    ` + newPostBtn + `
    <form method="POST" action="/os/logout">
      <button type="submit" class="btn btn--ghost btn--sm">Sign out</button>
    </form>
  </header>

  <main id="main-content" class="content">
`
}

// adminOSShellFoot closes the content/main/shell, renders the mobile bottom nav,
// command palette and toast container, then the nonce-gated bootstrap script.
// When pageScript is non-empty it is emitted as an additional nonce-gated inline
// script alongside the shared operator-control helpers (csrf/vpPost/show) and a
// live status region, so streaming operator pages keep their POST controls.
func adminOSShellFoot(nonce, pageScript string) string {
	ops := ""
	if pageScript != "" {
		ops = `<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
var msg=document.getElementById('action-msg');
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
function show(text,isErr){if(!msg)return;msg.textContent=text;msg.classList.toggle('is-error',!!isErr);msg.classList.add('visible');}
window.vpPost=function(url,onok){fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}}).then(function(r){return r.json().then(function(d){return {ok:r.ok,d:d};});}).then(function(res){show(res.ok?(onok?onok(res.d):'ok'):(res.d.detail||res.d.title||'error'),!res.ok);if(res.ok)setTimeout(function(){location.reload();},650);}).catch(function(e){show('Error: '+e,true);});};
` + pageScript + `
})();
</script>`
	}
	return `  </main>
</div><!-- .main -->
</div><!-- .shell -->
` + ops + `
<!-- Bottom nav for mobile -->
<nav class="bottom-nav" aria-label="Mobile navigation">
  <a class="bottom-nav-item" href="/os">
    ` + iconDashboard + `<span>Home</span>
  </a>
  <a class="bottom-nav-item" href="/os/posts">
    ` + iconPosts + `<span>Posts</span>
  </a>
  <a class="bottom-nav-item" href="/os/editor">
    ` + iconNewPost + `<span>Write</span>
  </a>
  <a class="bottom-nav-item" href="/os/members">
    ` + iconMembers + `<span>Members</span>
  </a>
  <a class="bottom-nav-item" href="/os/settings">
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
<script nonce="` + nonce + `" src="/os/static/js/admin-os.js"></script>
</body></html>`
}

// osSettings holds the subset of site settings needed to render every page.
type osSettings struct {
	SiteName   string
	AdminTheme string
	// Signed-in user, surfaced in the sidebar footer card.
	UserID     string
	UserName   string
	UserRole   string
	UserAvatar string
	// MailOnly / AccessLevel drive role-scoped sidebar visibility and match the
	// route guard in requireSessionOrAPIKey (hidden == unreachable).
	MailOnly    bool
	AccessLevel int
	// UnreadMessages drives the sidebar badge on the Messages item.
	UnreadMessages int
}

// getOSSettings loads settings needed for layout rendering.
func (a *App) getOSSettings(ctx context.Context) *osSettings {
	s := &osSettings{}
	if a.siteSettings != nil {
		s.SiteName = a.siteSettings.Get(ctx, settings.KeySiteName)
		s.AdminTheme = a.siteSettings.Get(ctx, "admin.theme")
	}
	// Unread contact messages drive the sidebar badge. Best-effort: any error
	// (nil DB / missing table on a pre-046 schema) just leaves the badge off.
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM contact_messages WHERE is_read=0`).Scan(&s.UnreadMessages)
	}
	// Surface the authenticated user (if any) so the shell can show their
	// avatar/name/role. The user is attached to the context by
	// requireSessionOrAPIKey and already carries the profile fields.
	s.AccessLevel = accessAdmin // legacy API-key / no-session callers are admin-equivalent
	if v := ctx.Value(ctxUserKey); v != nil {
		if u, ok := v.(*users.User); ok && u != nil {
			s.UserID = u.ID
			s.UserName = u.Name
			if s.UserName == "" {
				s.UserName = authorFallbackName(u.Email)
			}
			s.UserRole = u.Role
			s.UserAvatar = u.AvatarURL
			if mo, ok := ctx.Value(ctxMailOnlyKey).(bool); ok && mo {
				s.MailOnly = true
			}
			s.AccessLevel = accessLevelFor(u.Role, s.MailOnly)
		}
	}
	return s
}

// roleDisplay returns a human label for a role slug.
func roleDisplay(role string) string {
	switch role {
	case users.RoleAdmin:
		return "Administrator"
	case users.RoleEditor:
		return "Editor"
	case users.RoleAuthor:
		return "Author"
	case "":
		return "Administrator"
	default:
		return strings.ToUpper(role[:1]) + role[1:]
	}
}

// osSidebarUser renders the signed-in user card (avatar + name + role) shown at
// the foot of the sidebar. It links to the self-service profile editor.
func osSidebarUser(s *osSettings) string {
	name, role, avatarURL := "Admin", "Administrator", ""
	if s != nil {
		if s.UserName != "" {
			name = s.UserName
		}
		role = roleDisplay(s.UserRole)
		avatarURL = s.UserAvatar
	}
	avatar := `<div class="avatar avatar--sm avatar--brand">` + html.EscapeString(initials(name, "")) + `</div>`
	if avatarURL != "" {
		avatar = `<img class="avatar avatar--sm" src="` + html.EscapeString(avatarURL) + `" alt="" width="28" height="28">`
	}
	return `<a class="sidebar-user" href="/os/profile" title="Edit your profile">
      ` + avatar + `
      <div class="sidebar-user-info">
        <div class="sidebar-user-name">` + html.EscapeString(name) + `</div>
        <div class="sidebar-user-role">` + html.EscapeString(role) + `</div>
      </div>
    </a>`
}

// writeOSHTML writes HTML with the standard os response headers and CSRF cookie.
func writeOSHTML(w http.ResponseWriter, body string) {
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{
			Name: "vp_csrf", Value: token, Path: "/",
			SameSite: http.SameSiteStrictMode, HttpOnly: false,
			Secure: csrfCookieSecure(), MaxAge: 3600,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	// Admin pages must never be cached by the browser or any proxy/CDN —
	// otherwise a stale panel (e.g. an old Analytics page) keeps showing after a
	// deploy. These pages are dynamic and cheap to render, so always serve fresh.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// ── Login page ───────────────────────────────────────────────────────────────

func (a *App) handleOSLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(osLoginPage("", "")))
}

func (a *App) handleOSLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	pass := r.FormValue("password")
	if email == "" || pass == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osLoginPage(email, "Email and password are required.")))
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
		_, _ = w.Write([]byte(osLoginPage(email, loginLockoutMessage(until))))
		return
	}
	u, err := a.userStore.Authenticate(r.Context(), email, pass)
	if err != nil {
		// Fall back to a VayuMail account login (mailbox / author / editor / etc.),
		// so those email accounts can sign in from the same website login button.
		if addr, mok, totpMissing := a.authMailAccount(r.Context(), email, pass, r.FormValue("totp")); mok {
			token, terr := a.sessions.Create(r.Context(), "vmail:"+addr)
			if terr != nil {
				http.Error(w, "could not start session", http.StatusInternalServerError)
				return
			}
			auth.RecordAuthSuccess(ip)
			auth.SetSessionCookie(w, token)
			http.Redirect(w, r, "/os", http.StatusSeeOther)
			return
		} else if totpMissing {
			auth.RecordAuthFailure(ip)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(osLoginPage(email, "Enter the 6-digit code from your authenticator app, then re-enter your password.")))
			return
		}
		auth.RecordAuthFailure(ip)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osLoginPage(email, "Invalid email or password.")))
		return
	}
	// Second factor: if the account has 2FA enabled, a valid TOTP code is required.
	// On failure the password must be re-entered (it is never echoed back).
	if ok, required := a.verifyTOTPForLogin(r.Context(), email, r.FormValue("totp")); required && !ok {
		auth.RecordAuthFailure(ip)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osLoginPage(email, "Enter the 6-digit code from your authenticator app, then re-enter your password.")))
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

func (a *App) handleOSLogout(w http.ResponseWriter, r *http.Request) {
	if a.sessions != nil {
		if token := auth.SessionTokenFromRequest(r); token != "" {
			_ = a.sessions.Destroy(r.Context(), token)
		}
	}
	auth.ClearSessionCookie(w)
	// Also end any membership session, so a reader who reached VayuMail via the
	// portal (VayuMail mailbox login) is signed out completely from one action.
	if a.members != nil {
		if c, err := r.Cookie(memberCookie); err == nil && c.Value != "" {
			_ = a.members.DestroySession(r.Context(), c.Value)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: memberCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: config.Cfg.Domain != "localhost", SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	http.Redirect(w, r, "/os/login", http.StatusSeeOther)
}

// osLoginPage builds the full login page HTML. It uses a split-viewport layout:
// left hero panel (animated gradient mesh) + right form panel.
func osLoginPage(prefillEmail, errMsg string) string {
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
<link rel="stylesheet" href="/os/static/css/admin-os.css?v=` + assetVer("css/admin-os.css") + `">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
</head>
<body class="vp-os login-page">

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
  <div class="login-footer">VayuPress VayuOS · CSP-strict · Zero-telemetry</div>
</div>

<script src="/os/static/js/admin-os.js"></script>
</body></html>`
}

// ── Dashboard ────────────────────────────────────────────────────────────────

// osPublishTrend returns the count of articles created on each of the last n
// days (oldest first). Used to render the dashboard sparkline. Counts come
// straight from the articles table grouped by calendar day (UTC).
func osPublishTrend(ctx context.Context, n int) []int {
	out := make([]int, n)
	if dbpkg.DB == nil {
		return out
	}
	// Bucket per day for the window. SQLite date() truncates to YYYY-MM-DD.
	rows, err := dbpkg.Reader().QueryContext(ctx,
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
	if rows.Err() != nil {
		return out
	}
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		day := now.AddDate(0, 0, -(n - 1 - i)).Format("2006-01-02")
		out[i] = byDay[day]
	}
	return out
}

// osSparkline renders a compact inline SVG line chart from a series of values.
// It emits no inline styles (CSP-safe); all colour comes from CSS via
// currentColor on the .sparkline class. width/height are SVG viewBox units.
func osSparkline(vals []int) string {
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

func (a *App) handleOSDashboard(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	snap := a.getAdminSnapshot()

	// 14-day publishing trend sparkline.
	trend := osPublishTrend(r.Context(), 14)
	trendTotal := 0
	for _, v := range trend {
		trendTotal += v
	}
	sparkSVG := osSparkline(trend)

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
    <div class="stat-card__value">` + strconv.Itoa(snap.TotalArticles-snap.TotalPages) + `</div>
    <div class="stat-card__bottom">
      <span class="muted text-xs">Blog posts</span>
    </div>
  </div>
  <div class="stat-card">
    <div class="stat-card__top">
      <div class="stat-card__label">Pages</div>
      <div class="stat-card__icon stat-card__icon--brand">
        <svg viewBox="0 0 16 16" fill="none" width="16" height="16" aria-hidden="true"><path d="M4 2h6l3 3v9H4V2zm6 0v3h3M6 8h5M6 11h5" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/></svg>
      </div>
    </div>
    <div class="stat-card__value">` + strconv.Itoa(snap.TotalPages) + `</div>
    <div class="stat-card__bottom">
      <a class="text-xs" href="/os/pages">Manage pages →</a>
    </div>
  </div>
  <div class="stat-card">
    <div class="stat-card__top">
      <div class="stat-card__label">Messages</div>
      <div class="stat-card__icon stat-card__icon--accent">
        <svg viewBox="0 0 16 16" fill="none" width="16" height="16" aria-hidden="true"><path d="M2 4h12v8H5l-3 2V4zm2 3h8M4 9h5" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/></svg>
      </div>
    </div>
    <div class="stat-card__value">` + strconv.Itoa(snap.UnreadMessages) + `</div>
    <div class="stat-card__bottom">
      <a class="text-xs" href="/os/messages">` + unreadMessagesLabel(snap.UnreadMessages) + `</a>
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
      <!-- Populated by admin-os.js via GET /os/api/activity -->
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

	writeOSHTML(w, adminOSLayout(nonce, "Dashboard", "dashboard", cfg, htmpl.HTML(body)))
}

// ── Posts ────────────────────────────────────────────────────────────────────

// osPostsPageSize is how many posts the manager shows per page. The newest
// page (page 1) lists the latest osPostsPageSize posts; older posts live on
// subsequent pages reachable via the pager. This replaces the old hard
// `LIMIT 500` cap so every post is reachable regardless of archive size.
const osPostsPageSize = 100

func (a *App) handleOSPosts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// A CSRF token cookie so the inline publish/unpublish control can POST.
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

	// ── Parse filters from the query string ──────────────────────────────────
	qv := r.URL.Query()
	q := strings.TrimSpace(qv.Get("q"))
	if len(q) > 120 {
		q = q[:120]
	}
	status := qv.Get("status")
	if status != "published" && status != "draft" {
		status = "all"
	}
	period := qv.Get("period")
	from := normalizeDateParam(qv.Get("from"))
	to := normalizeDateParam(qv.Get("to"))
	// A period preset is a shortcut for a created-at window. An explicit custom
	// from/to range always wins; the preset only applies when no range is set.
	if from == "" && to == "" {
		if since := periodSince(period); since != "" {
			from = since
		} else {
			period = "all"
		}
	} else {
		period = "" // a custom range overrides the preset selector
	}

	// ── Shared filter predicate (search + date range), independent of the
	// status tab so the tab counts reflect the active search/date filter. ──
	// Pages (is_page=1) are managed on /os/pages, not in the blog feed, so the
	// Posts manager only ever lists real posts.
	where := []string{"is_page=0"}
	args := []any{}
	if q != "" {
		where = append(where, "(title LIKE ? OR COALESCE(tags,'') LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	if from != "" {
		where = append(where, "date(created_at) >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "date(created_at) <= ?")
		args = append(args, to)
	}
	filterClause := ""
	if len(where) > 0 {
		filterClause = " WHERE " + strings.Join(where, " AND ")
	}

	// ── Status counts within the active filter ───────────────────────────────
	allCount, published, drafts := 0, 0, 0
	if dbpkg.DB != nil {
		// status is NOT NULL DEFAULT 'published' (migration 030), so we group by
		// the bare column — `COALESCE(status,'published')` would defeat
		// idx_articles_status and force a full-table scan (a 502-class stall on a
		// large catalog). With no search/date filter this is an index-only count.
		if rows, err := dbpkg.Reader().QueryContext(r.Context(),
			`SELECT status s, COUNT(1) c FROM articles`+filterClause+` GROUP BY status`, args...); err == nil {
			for rows.Next() {
				var s string
				var c int
				if rows.Scan(&s, &c) == nil {
					allCount += c
					if s == "draft" {
						drafts += c
					} else {
						published += c
					}
				}
			}
			_ = rows.Err() // best-effort admin status counts
			rows.Close()
		}
	}
	total := allCount
	switch status {
	case "published":
		total = published
	case "draft":
		total = drafts
	}

	// ── Pagination maths (100 per page; page clamped to a valid range) ────────
	totalPages := (total + osPostsPageSize - 1) / osPostsPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	page := 1
	if p, err := strconv.Atoi(qv.Get("page")); err == nil && p > 1 {
		page = p
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * osPostsPageSize

	// ── Fetch the current page of posts (drafts included; the public site never
	// surfaces drafts but the manager must). ──────────────────────────────────
	type postRow struct {
		Title, Slug, Status string
		Tags                []string
		Updated             time.Time
	}
	var posts []postRow
	listWhere := append([]string{}, where...)
	listArgs := append([]any{}, args...)
	switch status {
	case "published":
		listWhere = append(listWhere, "status='published'")
	case "draft":
		listWhere = append(listWhere, "status='draft'")
	}
	listClause := ""
	if len(listWhere) > 0 {
		listClause = " WHERE " + strings.Join(listWhere, " AND ")
	}
	listArgs = append(listArgs, osPostsPageSize, offset)
	if dbpkg.DB != nil {
		if rows, err := dbpkg.Reader().QueryContext(r.Context(),
			`SELECT title,slug,COALESCE(tags,''),updated_at,COALESCE(status,'published') FROM articles`+listClause+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, listArgs...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var p postRow
				var tagsCSV string
				if rows.Scan(&p.Title, &p.Slug, &tagsCSV, &p.Updated, &p.Status) == nil {
					p.Tags = splitCSVTags(tagsCSV)
					posts = append(posts, p)
				}
			}
			_ = rows.Err() // best-effort admin post list
		}
	}

	filtersActive := q != "" || from != "" || to != "" || status != "all"

	var body string
	if allCount == 0 && !filtersActive {
		body = `<div class="page-header"><h1>Posts</h1></div>
<div class="card empty-state">
  <div class="empty-icon">✍️</div>
  <div class="empty-title">No posts yet</div>
  <div class="empty-sub">Your articles will appear here. Write your first one — it only takes a minute.</div>
  <a class="btn btn--primary mt-4" href="/os/editor">Write your first post</a>
</div>`
	} else {
		rows := ""
		for _, p := range posts {
			tags := ""
			for _, t := range p.Tags {
				tags += `<span class="chip chip--brand">#` + html.EscapeString(t) + `</span> `
			}
			esc := html.EscapeString(p.Slug)
			isDraft := p.Status == "draft"
			statusPill := `<span class="status-pill status-pill--live">● Published</span>`
			toggleLabel, toggleTo := "Unpublish", "draft"
			viewBtn := `<a class="btn btn--ghost btn--sm" href="/` + esc + `" target="_blank" rel="noopener">View ↗</a>`
			if isDraft {
				statusPill = `<span class="status-pill status-pill--draft">● Draft</span>`
				toggleLabel, toggleTo = "Publish", "published"
				// A draft is hidden from the public site (previewed in the editor).
				viewBtn = ""
			}
			rows += `<tr data-post-row data-status="` + p.Status + `">
  <td><input type="checkbox" data-post-select value="` + esc + `" aria-label="Select ` + html.EscapeString(p.Title) + `"></td>
  <td class="row-title">
    <a href="/os/editor/` + esc + `">` + html.EscapeString(p.Title) + `</a>
    <div class="row-meta">/` + esc + `</div>
  </td>
  <td>` + statusPill + `</td>
  <td>` + tags + `</td>
  <td class="muted text-sm">` + p.Updated.UTC().Format("2 Jan 2006") + `</td>
  <td class="row-actions">
    <a class="btn btn--ghost btn--sm" href="/os/editor/` + esc + `">Edit</a>
    ` + viewBtn + `
    <button type="button" class="btn btn--ghost btn--sm" data-post-toggle data-slug="` + esc + `" data-to="` + toggleTo + `">` + toggleLabel + `</button>
    <button type="button" class="btn btn--ghost btn--sm" data-post-delete data-slug="` + esc + `" data-title="` + html.EscapeString(p.Title) + `">Delete</button>
  </td>
</tr>`
		}

		tableBlock := `<div class="table-wrap">
    <table class="table">
      <thead><tr><th><input type="checkbox" data-post-select-all aria-label="Select all posts on this page"></th><th>Title</th><th>Status</th><th>Tags</th><th>Updated</th><th></th></tr></thead>
      <tbody>` + rows + `</tbody>
    </table>
  </div>`
		if len(posts) == 0 {
			tableBlock = `<div class="table-empty">No posts match your filter. <a href="/os/posts">Clear filters</a>.</div>`
		}

		shownFrom, shownTo := 0, 0
		if len(posts) > 0 {
			shownFrom = offset + 1
			shownTo = offset + len(posts)
		}

		body = `<div class="page-header">
  <h1>Posts <span class="count-pill">` + strconv.Itoa(allCount) + `</span></h1>
  <div class="page-actions">
    <a class="btn btn--primary" href="/os/editor">New Post</a>
  </div>
</div>
<div class="card">
  <div class="toolbar-row">
    <form class="posts-filter" method="GET" action="/os/posts" role="search">
      <input type="hidden" name="status" value="` + html.EscapeString(status) + `">
      <input class="input search-input" type="search" name="q" value="` + html.EscapeString(q) + `" placeholder="Search by title or tag…" aria-label="Search posts">
      ` + osPostsPeriodSelect(period) + `
      <label class="posts-filter-date">From <input class="input input--sm" type="date" name="from" value="` + html.EscapeString(from) + `" aria-label="From date"></label>
      <label class="posts-filter-date">To <input class="input input--sm" type="date" name="to" value="` + html.EscapeString(to) + `" aria-label="To date"></label>
      <button class="btn btn--ghost btn--sm" type="submit">Filter</button>
      <a class="btn btn--ghost btn--sm" href="/os/posts">Clear</a>
    </form>
    <div class="seg-filter" role="tablist" aria-label="Filter by status">
      <a class="seg-btn` + osActiveCls(status == "all") + `" href="` + osPostsHref("all", q, from, to, period, 1) + `">All <span class="muted">` + strconv.Itoa(allCount) + `</span></a>
      <a class="seg-btn` + osActiveCls(status == "published") + `" href="` + osPostsHref("published", q, from, to, period, 1) + `">Published <span class="muted">` + strconv.Itoa(published) + `</span></a>
      <a class="seg-btn` + osActiveCls(status == "draft") + `" href="` + osPostsHref("draft", q, from, to, period, 1) + `">Drafts <span class="muted">` + strconv.Itoa(drafts) + `</span></a>
    </div>
  </div>
  <div class="bulk-bar" data-post-bulkbar hidden>
    <span class="text-sm"><span data-post-bulk-count>0</span> selected</span>
    <button type="button" class="btn btn--ghost btn--sm" data-post-bulk="published">Publish</button>
    <button type="button" class="btn btn--ghost btn--sm" data-post-bulk="draft">Unpublish</button>
    <button type="button" class="btn btn--ghost btn--sm" data-post-bulk="delete">Delete</button>
  </div>
  ` + tableBlock + `
  ` + osPostsPager(status, q, from, to, period, page, totalPages, total, shownFrom, shownTo) + `
</div>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('action-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
document.querySelectorAll('[data-post-toggle]').forEach(function(b){
  b.addEventListener('click',function(){
    b.disabled=true;
    fetch('/os/api/posts/status',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({slug:b.getAttribute('data-slug'),status:b.getAttribute('data-to')})})
      .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
      .then(function(res){if(res.ok){show(res.d.status==='published'?'Published':'Moved to draft',false);setTimeout(function(){location.reload();},500);}else{b.disabled=false;show(res.d.detail||res.d.title||'Error',true);}})
      .catch(function(e){b.disabled=false;show('Error: '+e,true);});
  });
});
document.querySelectorAll('[data-post-delete]').forEach(function(b){
  b.addEventListener('click',function(){
    var t=b.getAttribute('data-title')||'this post';
    if(!window.confirm('Delete "'+t+'"? This permanently removes the post and its comments and cannot be undone.'))return;
    b.disabled=true;
    fetch('/os/api/posts/'+encodeURIComponent(b.getAttribute('data-slug')),{method:'DELETE',headers:{'X-CSRF-Token':csrf()}})
      .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
      .then(function(res){if(res.ok){show('Deleted',false);var row=b.closest('[data-post-row]');if(row)row.remove();}else{b.disabled=false;show(res.d.detail||res.d.title||'Error',true);}})
      .catch(function(e){b.disabled=false;show('Error: '+e,true);});
  });
});
// ── Bulk selection + actions ──────────────────────────────────────────────────
var bulkBar=document.querySelector('[data-post-bulkbar]');
var bulkCount=document.querySelector('[data-post-bulk-count]');
var selectAll=document.querySelector('[data-post-select-all]');
function selectedSlugs(){return Array.prototype.slice.call(document.querySelectorAll('[data-post-select]:checked')).map(function(c){return c.value;});}
function refreshBulk(){var n=selectedSlugs().length;if(bulkCount)bulkCount.textContent=String(n);if(bulkBar)bulkBar.hidden=n===0;}
document.querySelectorAll('[data-post-select]').forEach(function(c){c.addEventListener('change',refreshBulk);});
if(selectAll)selectAll.addEventListener('change',function(){document.querySelectorAll('[data-post-select]').forEach(function(c){c.checked=selectAll.checked;});refreshBulk();});
document.querySelectorAll('[data-post-bulk]').forEach(function(b){
  b.addEventListener('click',function(){
    var slugs=selectedSlugs();if(!slugs.length)return;
    var act=b.getAttribute('data-post-bulk');
    if(act==='delete'&&!window.confirm('Delete '+slugs.length+' post'+(slugs.length>1?'s':'')+'? This cannot be undone.'))return;
    b.disabled=true;show(act==='delete'?'Deleting…':'Updating…',false);
    var jobs=slugs.map(function(s){
      if(act==='delete')return fetch('/os/api/posts/'+encodeURIComponent(s),{method:'DELETE',headers:{'X-CSRF-Token':csrf()}});
      return fetch('/os/api/posts/status',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({slug:s,status:act})});
    });
    Promise.all(jobs).then(function(){show('Done — '+slugs.length+' updated',false);setTimeout(function(){location.reload();},500);}).catch(function(e){b.disabled=false;show('Error: '+e,true);});
  });
});
})();
</script>`
	}
	writeOSHTML(w, adminOSLayout(nonce, "Posts", "posts", cfg, htmpl.HTML(body)))
}

// ── Comments moderation ──────────────────────────────────────────────────────

func (a *App) handleOSComments(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// CSRF token cookie so the inline approve/reject controls can POST.
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

	var body string
	if a.commentStore == nil {
		body = `<div class="page-header"><h1>Comments</h1></div>
<div class="card empty-state"><div class="empty-icon">💬</div>
<div class="empty-title">Comments unavailable</div>
<div class="empty-sub">The comment store is not initialised.</div></div>`
		writeOSHTML(w, adminOSLayout(nonce, "Comments", "comments", cfg, htmpl.HTML(body)))
		return
	}

	// Resolve slugs only for the articles referenced by the comments shown
	// (≤500), via the read pool. At scale the catalog can hold hundreds of
	// thousands of posts, so loading the entire id→slug map per page view — and
	// on the single writer connection — does not scale.
	all, _ := a.commentStore.ListAll(r.Context(), "all", 500)
	slugByID := map[string]string{}
	seenID := map[string]bool{}
	ids := make([]any, 0, len(all))
	for _, c := range all {
		if c.ArticleID != "" && !seenID[c.ArticleID] {
			seenID[c.ArticleID] = true
			ids = append(ids, c.ArticleID)
		}
	}
	if len(ids) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		if rows, err := dbpkg.Reader().QueryContext(r.Context(), `SELECT id, slug FROM articles WHERE id IN (`+ph+`)`, ids...); err == nil {
			defer rows.Close() //nolint:errcheck
			for rows.Next() {
				var id, slug string
				if rows.Scan(&id, &slug) == nil {
					slugByID[id] = slug
				}
			}
			_ = rows.Err() // best-effort id→slug map for comment links
		}
	}

	var pending, approved int
	rowsHTML := ""
	for _, c := range all {
		switch c.Status {
		case "pending":
			pending++
		case "approved":
			approved++
		}
		pill := `<span class="status-pill">` + html.EscapeString(c.Status) + `</span>`
		switch c.Status {
		case "approved":
			pill = `<span class="status-pill status-pill--live">● approved</span>`
		case "pending":
			pill = `<span class="status-pill status-pill--draft">● pending</span>`
		case "rejected", "spam":
			pill = `<span class="status-pill">● ` + html.EscapeString(c.Status) + `</span>`
		}
		slug := slugByID[c.ArticleID]
		postCell := html.EscapeString(slug)
		if slug != "" {
			postCell = `<a href="/` + html.EscapeString(slug) + `" target="_blank" rel="noopener">/` + html.EscapeString(slug) + `</a>`
		}
		// Action buttons depend on current status.
		actions := ""
		if c.Status != "approved" {
			actions += `<button type="button" class="btn btn--primary btn--sm" data-comment-action data-id="` + html.EscapeString(c.ID) + `" data-to="approved">Approve</button> `
		}
		if c.Status != "rejected" {
			actions += `<button type="button" class="btn btn--ghost btn--sm" data-comment-action data-id="` + html.EscapeString(c.ID) + `" data-to="rejected">Reject</button> `
		}
		if c.Status != "spam" {
			actions += `<button type="button" class="btn btn--ghost btn--sm" data-comment-action data-id="` + html.EscapeString(c.ID) + `" data-to="spam">Spam</button>`
		}
		rowsHTML += `<tr data-comment-row data-status="` + c.Status + `">
  <td class="row-title"><strong>` + html.EscapeString(c.Author) + `</strong>
    <div class="row-meta">` + html.EscapeString(c.Email) + `</div></td>
  <td>` + html.EscapeString(c.Body) + `</td>
  <td>` + postCell + `</td>
  <td>` + pill + `</td>
  <td class="muted text-sm">` + c.CreatedAt.UTC().Format("2 Jan 2006 15:04") + `</td>
  <td class="row-actions">` + actions + `</td>
</tr>`
	}

	if len(all) == 0 {
		body = `<div class="page-header"><h1>Comments</h1></div>
<div class="card empty-state"><div class="empty-icon">💬</div>
<div class="empty-title">No comments yet</div>
<div class="empty-sub">When readers comment on your articles, they appear here for moderation before going public.</div></div>`
	} else {
		body = `<div class="page-header">
  <h1>Comments <span class="count-pill">` + strconv.Itoa(len(all)) + `</span></h1>
  <div class="page-actions"><span class="text-sm muted">` + strconv.Itoa(pending) + ` pending · ` + strconv.Itoa(approved) + ` approved</span></div>
</div>
<div class="card">
  <div class="toolbar-row">
    <div class="seg-filter" role="tablist" aria-label="Filter by status">
      <button type="button" class="seg-btn is-active" data-comment-filter="all">All <span class="muted">` + strconv.Itoa(len(all)) + `</span></button>
      <button type="button" class="seg-btn" data-comment-filter="pending">Pending <span class="muted">` + strconv.Itoa(pending) + `</span></button>
      <button type="button" class="seg-btn" data-comment-filter="approved">Approved <span class="muted">` + strconv.Itoa(approved) + `</span></button>
    </div>
  </div>
  <div class="table-wrap">
    <table class="table">
      <thead><tr><th>Author</th><th>Comment</th><th>Post</th><th>Status</th><th>When</th><th></th></tr></thead>
      <tbody>` + rowsHTML + `</tbody>
    </table>
  </div>
  <div class="table-empty" data-filter-empty hidden>No comments match this filter.</div>
</div>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('action-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
document.querySelectorAll('[data-comment-action]').forEach(function(b){
  b.addEventListener('click',function(){
    b.disabled=true;
    fetch('/os/api/comments/'+encodeURIComponent(b.getAttribute('data-id'))+'/status',{method:'PUT',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({status:b.getAttribute('data-to')})})
      .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
      .then(function(res){if(res.ok){show('Comment '+b.getAttribute('data-to'),false);setTimeout(function(){location.reload();},400);}else{b.disabled=false;show(res.d.detail||res.d.title||res.d.error||'Error',true);}})
      .catch(function(e){b.disabled=false;show('Error: '+e,true);});
  });
});
var rowsEl=document.querySelectorAll('[data-comment-row]');
document.querySelectorAll('[data-comment-filter]').forEach(function(s){
  s.addEventListener('click',function(){
    document.querySelectorAll('[data-comment-filter]').forEach(function(x){x.classList.remove('is-active');});
    s.classList.add('is-active');
    var f=s.getAttribute('data-comment-filter');
    rowsEl.forEach(function(row){row.hidden=(f!=='all'&&row.getAttribute('data-status')!==f);});
  });
});
})();
</script>`
	}
	writeOSHTML(w, adminOSLayout(nonce, "Comments", "comments", cfg, htmpl.HTML(body)))
}

// osActiveCls returns the " is-active" class suffix when active is true.
func osActiveCls(active bool) string {
	if active {
		return " is-active"
	}
	return ""
}

// normalizeDateParam validates a YYYY-MM-DD date string, returning "" if it is
// empty or malformed so an invalid value never reaches the SQL query.
func normalizeDateParam(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return ""
	}
	return s
}

// periodSince maps a preset time-window key to an inclusive lower-bound date
// (YYYY-MM-DD, UTC). An empty return means "all time" / unrecognised key.
func periodSince(period string) string {
	var days int
	switch period {
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "90d":
		days = 90
	case "365d":
		days = 365
	default:
		return ""
	}
	return time.Now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
}

// osPostsPeriodSelect renders the time-range preset dropdown with the active
// option preselected.
func osPostsPeriodSelect(period string) string {
	opts := []struct{ Val, Label string }{
		{"all", "Any time"},
		{"7d", "Last 7 days"},
		{"30d", "Last 30 days"},
		{"90d", "Last 90 days"},
		{"365d", "Last 12 months"},
	}
	cur := period
	if cur == "" {
		cur = "all"
	}
	out := `<select class="select select--inline" name="period" aria-label="Time range">`
	for _, o := range opts {
		sel := ""
		if o.Val == cur {
			sel = " selected"
		}
		out += `<option value="` + o.Val + `"` + sel + `>` + o.Label + `</option>`
	}
	out += `</select>`
	return out
}

// osPostsHref builds a /os/posts URL that preserves the active filters while
// overriding the status tab and target page. Default values are omitted so the
// query string stays clean and shareable.
func osPostsHref(status, q, from, to, period string, page int) string {
	v := url.Values{}
	if status != "" && status != "all" {
		v.Set("status", status)
	}
	if q != "" {
		v.Set("q", q)
	}
	if from != "" {
		v.Set("from", from)
	}
	if to != "" {
		v.Set("to", to)
	}
	if period != "" && period != "all" {
		v.Set("period", period)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if enc := v.Encode(); enc != "" {
		return "/os/posts?" + enc
	}
	return "/os/posts"
}

// osPostsPager renders the premium pagination control: a "showing X–Y of Z"
// summary, first/last + prev/next + ±10-page jump buttons, a windowed run of
// page numbers, and a "go to page" form. All navigation is plain GET links so
// it works without JavaScript and respects the strict CSP.
func osPostsPager(status, q, from, to, period string, page, totalPages, total, shownFrom, shownTo int) string {
	info := `<div class="pager-info">Showing <strong>` + strconv.Itoa(shownFrom) + `–` + strconv.Itoa(shownTo) +
		`</strong> of <strong>` + strconv.Itoa(total) + `</strong> posts</div>`
	if totalPages <= 1 {
		return `<nav class="pager" aria-label="Posts pagination">` + info + `</nav>`
	}

	btn := func(label string, target int, disabled bool, extraCls string) string {
		cls := "pager-btn"
		if extraCls != "" {
			cls += " " + extraCls
		}
		if disabled {
			return `<span class="` + cls + ` is-disabled" aria-disabled="true">` + label + `</span>`
		}
		return `<a class="` + cls + `" href="` + osPostsHref(status, q, from, to, period, target) + `">` + label + `</a>`
	}
	num := func(i int) string {
		if i == page {
			return `<span class="pager-btn is-current" aria-current="page">` + strconv.Itoa(i) + `</span>`
		}
		return `<a class="pager-btn" href="` + osPostsHref(status, q, from, to, period, i) + `">` + strconv.Itoa(i) + `</a>`
	}

	prev10 := page - 10
	if prev10 < 1 {
		prev10 = 1
	}
	next10 := page + 10
	if next10 > totalPages {
		next10 = totalPages
	}

	controls := btn("« First", 1, page == 1, "")
	if totalPages > 10 {
		controls += btn("‹‹ 10", prev10, page == 1, "")
	}
	controls += btn("‹ Prev", page-1, page == 1, "")

	start := page - 2
	if start < 1 {
		start = 1
	}
	end := page + 2
	if end > totalPages {
		end = totalPages
	}
	if start > 1 {
		controls += num(1)
		if start > 2 {
			controls += `<span class="pager-gap">…</span>`
		}
	}
	for i := start; i <= end; i++ {
		controls += num(i)
	}
	if end < totalPages {
		if end < totalPages-1 {
			controls += `<span class="pager-gap">…</span>`
		}
		controls += num(totalPages)
	}

	controls += btn("Next ›", page+1, page == totalPages, "")
	if totalPages > 10 {
		controls += btn("10 ››", next10, page == totalPages, "")
	}
	controls += btn("Last »", totalPages, page == totalPages, "")

	jump := `<form class="pager-jump" method="GET" action="/os/posts">`
	if status != "all" {
		jump += `<input type="hidden" name="status" value="` + html.EscapeString(status) + `">`
	}
	if q != "" {
		jump += `<input type="hidden" name="q" value="` + html.EscapeString(q) + `">`
	}
	if from != "" {
		jump += `<input type="hidden" name="from" value="` + html.EscapeString(from) + `">`
	}
	if to != "" {
		jump += `<input type="hidden" name="to" value="` + html.EscapeString(to) + `">`
	}
	if period != "" && period != "all" {
		jump += `<input type="hidden" name="period" value="` + html.EscapeString(period) + `">`
	}
	jump += `<label class="pager-jump-label">Go to page
      <input class="input input--sm pager-jump-input" type="number" name="page" min="1" max="` + strconv.Itoa(totalPages) + `" value="` + strconv.Itoa(page) + `" aria-label="Page number">
    </label>
    <span class="pager-jump-total">of ` + strconv.Itoa(totalPages) + `</span>
    <button class="btn btn--ghost btn--sm" type="submit">Go</button>
  </form>`

	return `<nav class="pager" aria-label="Posts pagination">
  ` + info + `
  <div class="pager-controls">` + controls + `</div>
  ` + jump + `
</nav>`
}

// ── Editor ───────────────────────────────────────────────────────────────────

// handleOSEditor serves the post editor. To avoid any data loss during the
// gradual migration it picks the editor by article state:
//   - existing article with a block document      → native os block editor
//   - existing empty draft (no content, no blocks) → native os block editor
//   - existing article with legacy HTML/Markdown   → v2 editor (lossless)
//   - brand-new post (no slug)                     → v2 editor (handles create)
func (a *App) handleOSEditor(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	slug := chi.URLParam(r, "slug")

	if slug != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		art, err := a.articles.Get(ctx, slug)
		if err == nil {
			meta := loadPostMeta(r.Context(), slug)
			metaScript := osEditorMetaScript(slug, art.Status, art.CreatedAt, art.Tags, meta)
			blocksJSON := loadBlocksJSON(r.Context(), slug)
			hasBlocks := strings.TrimSpace(blocksJSON) != "" && strings.TrimSpace(blocksJSON) != "[]"
			emptyDraft := strings.TrimSpace(art.Content) == ""
			if hasBlocks || emptyDraft {
				body := osEditorBody(slug, art.Title, blocksJSON) + metaScript
				body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-editor.js"></script>`
				writeOSHTML(w, adminOSLayout(nonce, "Edit Post", "editor", cfg, htmpl.HTML(body)))
				return
			}
			// Legacy (non-block) content: open it in the native block editor,
			// pre-seeded with an in-memory import of the article HTML. The block
			// side-car is NOT persisted and articles.content is left untouched, so
			// this is non-destructive — navigating away leaves the post exactly as
			// it was. The first Save commits the imported blocks (HTML→blocks via
			// the conservative importer + bluemonday on render).
			blocks := blockrender.ImportHTML(art.Content)
			raw, err := json.Marshal(blocks)
			if err != nil {
				raw = []byte("[]")
			}
			body := osEditorBody(slug, art.Title, string(raw)) + metaScript
			body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-editor.js"></script>`
			writeOSHTML(w, adminOSLayout(nonce, "Edit Post", "editor", cfg, htmpl.HTML(body)))
			return
		}
	}

	// Brand-new post: the native block editor owns the create path (v1.6.0).
	// It hydrates with an empty document and an empty slug; the first Save POSTs
	// to /os/api/editor/save, which creates the article and returns its slug.
	body := osEditorBody("", "", "[]") + osEditorMetaScript("", "", time.Time{}, nil, PostMeta{})
	body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-editor.js"></script>`
	writeOSHTML(w, adminOSLayout(nonce, "New Post", "editor", cfg, htmpl.HTML(body)))
}

// ── SEO ──────────────────────────────────────────────────────────────────────
// The native SEO dashboard now lives in admin_os_intel.go (handleOSSEONative).

// ── Settings ─────────────────────────────────────────────────────────────────

func (a *App) handleOSSettings(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	group := chi.URLParam(r, "group")
	if group == "" {
		group = "general"
	}

	tabs := []struct{ Key, Label, Href string }{
		{"general", "General", "/os/settings/general"},
		{"navigation", "Navigation", "/os/settings/navigation"},
		{"footer", "Footer", "/os/settings/footer"},
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
	case "navigation":
		groupBody = osSettingsNavigation(r.Context(), ss)
	case "footer":
		groupBody = osSettingsFooter(r.Context(), ss)
	case "design":
		groupBody = osSettingsDesign(r.Context(), ss)
	case "members":
		groupBody = osSettingsMembers(r.Context(), ss)
	case "email":
		groupBody = osSettingsEmail(r.Context(), ss)
	case "security":
		groupBody = osSettingsSecurity(r.Context(), ss)
	case "advanced":
		groupBody = osSettingsAdvanced(r.Context(), ss)
	default:
		groupBody = osSettingsGeneral(r.Context(), ss)
	}

	body := `<div class="page-header">
  <h1>Settings</h1>
  <div class="page-actions">
    <span id="settings-status" role="status" aria-live="polite" class="text-xs muted"></span>
    <button type="button" class="btn btn--primary btn--sm" id="settings-save-btn">Save changes</button>
  </div>
</div>
<nav class="tab-list" aria-label="Settings sections">` + tabHTML + `</nav>
<div class="card">
  ` + groupBody + `
  <div class="settings-save-bar">
    <span id="settings-status-bar" role="status" aria-live="polite" class="text-xs muted"></span>
    <button type="button" class="btn btn--primary btn--sm" id="settings-save-bar-btn">Save changes</button>
  </div>
</div>`

	saveScript := `var saveBtn=document.getElementById('settings-save-btn');
var saveBtnBar=document.getElementById('settings-save-bar-btn');
var statusEl=document.getElementById('settings-status');
var statusBar=document.getElementById('settings-status-bar');
function setStatus(t,isErr){
  var c=isErr?'var(--color-danger,#ef4444)':'var(--color-success,#22c55e)';
  if(statusEl){statusEl.textContent=t;statusEl.style.color=c;}
  if(statusBar){statusBar.textContent=t;statusBar.style.color=c;}
}
function doSave(){
  var fields=document.querySelectorAll('[data-setting-key]');
  var pairs=[];
  fields.forEach(function(el){
    var key=el.dataset.settingKey;
    var val=el.type==='checkbox'?(el.checked?'true':'false'):el.value;
    pairs.push({key:key,value:val});
  });
  if(!pairs.length){setStatus('Nothing to save',false);return;}
  if(saveBtn)saveBtn.disabled=true;
  if(saveBtnBar)saveBtnBar.disabled=true;
  setStatus('Saving…',false);
  var c=csrf();
  // Send sequentially to avoid SQLite write contention (WAL allows one writer).
  pairs.reduce(function(chain,p){
    return chain.then(function(){
      return fetch('/os/api/settings',{
        method:'POST',
        headers:{'Content-Type':'application/json','X-CSRF-Token':c},
        body:JSON.stringify(p)
      }).then(function(r){
        if(r.ok)return;
        return r.json().then(function(e){
          throw new Error(p.key+': '+(e.detail||e.message||e.error||r.status));
        }).catch(function(){
          throw new Error(p.key+': HTTP '+r.status);
        });
      });
    });
  },Promise.resolve()).then(function(){
    setStatus('Saved',false);
    if(saveBtn)saveBtn.disabled=false;
    if(saveBtnBar)saveBtnBar.disabled=false;
    if(window.vpToast)window.vpToast('Settings saved','ok');
  }).catch(function(e){
    setStatus('Failed — '+e.message,true);
    if(saveBtn)saveBtn.disabled=false;
    if(saveBtnBar)saveBtnBar.disabled=false;
  });
}
if(saveBtn)saveBtn.addEventListener('click',doSave);
if(saveBtnBar)saveBtnBar.addEventListener('click',doSave);
// Reorder a row among its same-type siblings (dir<0 = up, dir>0 = down), then
// resync the hidden JSON so the new order persists on Save. Shared by the nav
// and footer link editors. CSP-safe: pure DOM, no inline handlers.
function moveRow(row,dir,sync){
  if(dir<0){var p=row.previousElementSibling;if(p)row.parentNode.insertBefore(row,p);}
  else{var n=row.nextElementSibling;if(n)row.parentNode.insertBefore(n,row);}
  if(sync)sync();
}
function reorderBtns(row,sync){
  var up=document.createElement('button');up.type='button';up.className='btn btn--sm';up.textContent='↑';up.title='Move up';up.setAttribute('aria-label','Move up');
  up.addEventListener('click',function(){moveRow(row,-1,sync);});
  var dn=document.createElement('button');dn.type='button';dn.className='btn btn--sm';dn.textContent='↓';dn.title='Move down';dn.setAttribute('aria-label','Move down');
  dn.addEventListener('click',function(){moveRow(row,1,sync);});
  return[up,dn];
}
// Navigation menu editor (Navigation tab). Builds rows from nav.items JSON and
// keeps a hidden input in sync so the generic Save picks it up.
var navEditor=document.getElementById('nav-editor');
var navHidden=document.getElementById('nav-json-input');
var navAdd=document.getElementById('nav-add-btn');
if(navEditor&&navHidden){
  function navSync(){
    var rows=navEditor.querySelectorAll('[data-nav-row]');var out=[];
    rows.forEach(function(row){
      var l=row.querySelector('[data-nav-label]').value.trim();
      var h=row.querySelector('[data-nav-href]').value.trim();
      if(l&&h)out.push({label:l,href:h});
    });
    navHidden.value=JSON.stringify(out);
  }
  function navRow(label,href){
    var row=document.createElement('div');row.setAttribute('data-nav-row','');
    row.style.cssText='display:flex;gap:.5rem;align-items:center;margin-bottom:.5rem';
    var li=document.createElement('input');li.className='input';li.type='text';li.placeholder='Label';li.value=label||'';li.setAttribute('data-nav-label','');li.style.flex='1';
    var hi=document.createElement('input');hi.className='input';hi.type='text';hi.placeholder='/path or https://…';hi.value=href||'';hi.setAttribute('data-nav-href','');hi.style.flex='2';
    var rm=document.createElement('button');rm.type='button';rm.className='btn btn--sm';rm.textContent='✕';
    rm.addEventListener('click',function(){row.remove();navSync();});
    li.addEventListener('input',navSync);hi.addEventListener('input',navSync);
    row.appendChild(li);row.appendChild(hi);
    var nb=reorderBtns(row,navSync);row.appendChild(nb[0]);row.appendChild(nb[1]);
    row.appendChild(rm);
    return row;
  }
  (function(){
    var seed=[];try{seed=JSON.parse(navEditor.getAttribute('data-nav-json')||'[]');}catch(e){seed=[];}
    seed.forEach(function(it){navEditor.appendChild(navRow(it.label,it.href));});
  })();
  if(navAdd)navAdd.addEventListener('click',function(){navEditor.appendChild(navRow('',''));});
}
// Branding: favicon/logo upload (Design tab). Elements only exist there.
var favFile=document.getElementById('brand-favicon-file');
var favUp=document.getElementById('brand-favicon-upload');
var favRm=document.getElementById('brand-favicon-remove');
var favStatus=document.getElementById('brand-favicon-status');
var favImg=document.getElementById('brand-favicon-img');
var favState=document.getElementById('brand-favicon-state');
function favSet(t,isErr){if(favStatus){favStatus.textContent=t;favStatus.style.color=isErr?'var(--color-danger,#ef4444)':'var(--color-success,#22c55e)';}}
function favBust(){if(favImg)favImg.src='/favicon.ico?t='+Date.now();}
if(favUp)favUp.addEventListener('click',function(){
  var f=favFile&&favFile.files&&favFile.files[0];
  if(!f){favSet('Choose a PNG or ICO first',true);return;}
  favUp.disabled=true;favSet('Uploading…',false);
  var fd=new FormData();fd.append('favicon',f);
  fetch('/os/api/branding/favicon',{method:'POST',headers:{'X-CSRF-Token':csrf()},body:fd})
    .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
    .then(function(res){favUp.disabled=false;if(res.ok){favSet('Favicon updated',false);favBust();if(favState)favState.textContent='Custom favicon active — stored in the database.';}else{favSet(res.d.error||'Upload failed',true);}})
    .catch(function(e){favUp.disabled=false;favSet('Error: '+e,true);});
});
if(favRm)favRm.addEventListener('click',function(){
  favRm.disabled=true;favSet('Removing…',false);
  var fd=new FormData();fd.append('remove','1');
  fetch('/os/api/branding/favicon',{method:'POST',headers:{'X-CSRF-Token':csrf()},body:fd})
    .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
    .then(function(res){favRm.disabled=false;if(res.ok){favSet('Default restored',false);favBust();if(favState)favState.textContent='Using the default mark.';}else{favSet(res.d.error||'Remove failed',true);}})
    .catch(function(e){favRm.disabled=false;favSet('Error: '+e,true);});
});
// Footer editor (Footer tab). Builds tagline/copyright/columns/social/legal and
// keeps a hidden JSON input (footer.config) in sync for the generic Save.
var footerInput=document.getElementById('footer-json-input');
if(footerInput){
  var fTagline=document.getElementById('footer-tagline');
  var fCopyright=document.getElementById('footer-copyright');
  var fCols=document.getElementById('footer-cols');
  var fSocial=document.getElementById('footer-social');
  var fLegal=document.getElementById('footer-legal');
  function fLinkRow(label,href){
    var row=document.createElement('div');row.setAttribute('data-f-link','');
    row.style.cssText='display:flex;gap:.5rem;align-items:center;margin-bottom:.4rem';
    var li=document.createElement('input');li.className='input';li.type='text';li.placeholder='Label';li.value=label||'';li.setAttribute('data-f-label','');li.style.flex='1';
    var hi=document.createElement('input');hi.className='input';hi.type='text';hi.placeholder='/path, mailto: or https://…';hi.value=href||'';hi.setAttribute('data-f-href','');hi.style.flex='2';
    var rm=document.createElement('button');rm.type='button';rm.className='btn btn--sm';rm.textContent='✕';
    rm.addEventListener('click',function(){row.remove();footerSync();});
    li.addEventListener('input',footerSync);hi.addEventListener('input',footerSync);
    row.appendChild(li);row.appendChild(hi);
    var fb=reorderBtns(row,footerSync);row.appendChild(fb[0]);row.appendChild(fb[1]);
    row.appendChild(rm);
    return row;
  }
  function fColCard(title,links){
    var card=document.createElement('div');card.setAttribute('data-f-col','');
    card.style.cssText='border:1px solid var(--border,#2a2a2a);border-radius:8px;padding:.75rem;margin-bottom:.75rem';
    var head=document.createElement('div');head.style.cssText='display:flex;gap:.5rem;align-items:center;margin-bottom:.5rem';
    var ti=document.createElement('input');ti.className='input';ti.type='text';ti.placeholder='Column title (e.g. Company)';ti.value=title||'';ti.setAttribute('data-f-col-title','');ti.style.flex='1';
    ti.addEventListener('input',footerSync);
    var rmc=document.createElement('button');rmc.type='button';rmc.className='btn btn--sm';rmc.textContent='Remove column';
    rmc.addEventListener('click',function(){card.remove();footerSync();});
    head.appendChild(ti);
    var cb=reorderBtns(card,footerSync);head.appendChild(cb[0]);head.appendChild(cb[1]);
    head.appendChild(rmc);
    var linksWrap=document.createElement('div');linksWrap.setAttribute('data-f-col-links','');
    (links||[]).forEach(function(l){linksWrap.appendChild(fLinkRow(l.label,l.href));});
    var addL=document.createElement('button');addL.type='button';addL.className='btn btn--sm';addL.textContent='+ Add link';
    addL.addEventListener('click',function(){linksWrap.appendChild(fLinkRow('',''));footerSync();});
    card.appendChild(head);card.appendChild(linksWrap);card.appendChild(addL);
    return card;
  }
  function fCollect(wrap){
    var out=[];if(!wrap)return out;
    wrap.querySelectorAll('[data-f-link]').forEach(function(row){
      var l=row.querySelector('[data-f-label]').value.trim();
      var h=row.querySelector('[data-f-href]').value.trim();
      if(l&&h)out.push({label:l,href:h});
    });
    return out;
  }
  function footerSync(){
    var cols=[];
    if(fCols)fCols.querySelectorAll('[data-f-col]').forEach(function(card){
      var t=card.querySelector('[data-f-col-title]').value.trim();
      var links=fCollect(card.querySelector('[data-f-col-links]'));
      if(t||links.length)cols.push({title:t,links:links});
    });
    footerInput.value=JSON.stringify({
      tagline:fTagline?fTagline.value.trim():'',
      copyright:fCopyright?fCopyright.value.trim():'',
      columns:cols,
      social:fCollect(fSocial),
      legal:fCollect(fLegal)
    });
  }
  (function(){
    var seed={};try{seed=JSON.parse(footerInput.getAttribute('data-footer-seed')||'{}');}catch(e){seed={};}
    if(fTagline)fTagline.value=seed.tagline||'';
    if(fCopyright)fCopyright.value=seed.copyright||'';
    if(fCols)(seed.columns||[]).forEach(function(c){fCols.appendChild(fColCard(c.title,c.links));});
    if(fSocial)(seed.social||[]).forEach(function(l){fSocial.appendChild(fLinkRow(l.label,l.href));});
    if(fLegal)(seed.legal||[]).forEach(function(l){fLegal.appendChild(fLinkRow(l.label,l.href));});
    if(fTagline)fTagline.addEventListener('input',footerSync);
    if(fCopyright)fCopyright.addEventListener('input',footerSync);
    footerSync();
  })();
  var addCol=document.getElementById('footer-add-col');
  if(addCol)addCol.addEventListener('click',function(){fCols.appendChild(fColCard('',[]));footerSync();});
  var addSocial=document.getElementById('footer-add-social');
  if(addSocial)addSocial.addEventListener('click',function(){fSocial.appendChild(fLinkRow('',''));footerSync();});
  var addLegal=document.getElementById('footer-add-legal');
  if(addLegal)addLegal.addEventListener('click',function(){fLegal.appendChild(fLinkRow('',''));footerSync();});
}`

	fullHTML := adminOSShellHead(nonce, "Settings", "settings", cfg) +
		renderTrustedHTML(htmpl.HTML(body)) +
		adminOSShellFoot(nonce, saveScript)
	writeOSHTML(w, fullHTML)
}

func osSettingsGeneral(ctx context.Context, ss *settings.Store) string {
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

func osSettingsNavigation(ctx context.Context, ss *settings.Store) string {
	navJSON := ""
	if ss != nil {
		navJSON = ss.Get(ctx, settings.KeyNavItems)
	}
	if strings.TrimSpace(navJSON) == "" {
		// Seed the editor with the built-in defaults so operators start from the
		// current visible menu rather than a blank slate.
		navJSON = `[{"label":"Home","href":"/"},{"label":"Feed","href":"/feed.xml"},{"label":"Console","href":"/admin"}]`
	}
	return `<div class="settings-section">
  <div class="settings-block-title">Public navigation menu</div>
  <p class="text-sm muted mb-4">These links appear in the top navigation bar on every public page. Point them at internal pages (e.g. <code>/about</code>), feeds, or external/redirect URLs (e.g. <code>https://example.com</code>). Drag-free, add or remove as many as you like.</p>
  <div id="nav-editor" data-nav-json="` + html.EscapeString(navJSON) + `"></div>
  <button type="button" class="btn btn--sm mt-2" id="nav-add-btn">+ Add link</button>
  <input type="hidden" id="nav-json-input" data-setting-key="` + settings.KeyNavItems + `" value="` + html.EscapeString(navJSON) + `">
  <p class="field-hint mt-2">Leave the list empty and Save to restore the default Home / Feed / Console menu.</p>
</div>`
}

// defaultFooterSeed pre-populates the footer editor for operators who have not
// configured a footer yet, so they start from a premium layout (a link column,
// Privacy/Terms legal links, copyright line) rather than a blank slate.
const defaultFooterSeed = `{"tagline":"","copyright":"© {year} {site}. All rights reserved.","columns":[{"title":"Explore","links":[{"label":"Home","href":"/"},{"label":"Feed","href":"/feed.xml"}]}],"social":[],"legal":[{"label":"Privacy","href":"/privacy"},{"label":"Terms","href":"/terms"}]}`

func osSettingsFooter(ctx context.Context, ss *settings.Store) string {
	footerJSON := ""
	if ss != nil {
		footerJSON = ss.Get(ctx, settings.KeyFooterConfig)
	}
	if strings.TrimSpace(footerJSON) == "" {
		footerJSON = defaultFooterSeed
	}
	esc := html.EscapeString(footerJSON)
	return `<div class="settings-section">
  <div class="settings-block-title">Premium footer</div>
  <p class="text-sm muted mb-4">Build a rich footer for every public page: a brand tagline, multiple link columns, social links, a legal-links bar (Privacy, Terms…) and a copyright line. Hrefs accept internal paths (e.g. <code>/privacy</code>), feeds, <code>mailto:</code> or external URLs. Leave everything empty to fall back to a clean default copyright bar.</p>

  <div class="field"><label class="field-label" for="footer-tagline">Footer tagline</label>
    <input id="footer-tagline" class="input" type="text" placeholder="A short line shown under your brand"></div>

  <div class="field"><label class="field-label" for="footer-copyright">Copyright line</label>
    <input id="footer-copyright" class="input" type="text" placeholder="© {year} {site}. All rights reserved.">
    <span class="field-hint">Use <code>{year}</code> for the current year and <code>{site}</code> for your site name.</span></div>

  <div class="settings-block-title mt-4">Link columns</div>
  <p class="text-sm muted mb-2">Grouped link lists (e.g. Explore, Company, Resources).</p>
  <div id="footer-cols"></div>
  <button type="button" class="btn btn--sm mt-2" id="footer-add-col">+ Add column</button>

  <div class="settings-block-title mt-4">Social links</div>
  <div id="footer-social"></div>
  <button type="button" class="btn btn--sm mt-2" id="footer-add-social">+ Add social link</button>

  <div class="settings-block-title mt-4">Legal links (bottom bar)</div>
  <p class="text-sm muted mb-2">Shown in the footer's bottom bar next to the copyright — e.g. Privacy, Terms, Cookies.</p>
  <div id="footer-legal"></div>
  <button type="button" class="btn btn--sm mt-2" id="footer-add-legal">+ Add legal link</button>

  <input type="hidden" id="footer-json-input" data-setting-key="` + settings.KeyFooterConfig + `" data-footer-seed="` + esc + `" value="` + esc + `">
</div>`
}

func osSettingsDesign(ctx context.Context, ss *settings.Store) string {
	primaryLight, primaryDark, customCSS := "#0f766e", "#2dd4bf", ""
	faviconState := "Using the default mark."
	if ss != nil {
		if v := ss.Get(ctx, settings.KeyThemePrimaryLight); v != "" {
			primaryLight = v
		}
		if v := ss.Get(ctx, settings.KeyThemePrimaryDark); v != "" {
			primaryDark = v
		}
		customCSS = ss.Get(ctx, settings.KeyThemeCustomCSS)
		if ss.Get(ctx, settings.KeyBrandFavicon) != "" {
			faviconState = "Custom favicon active — stored in the database."
		}
	}

	return `<div class="settings-section">
  <div class="settings-callout">
    <strong>Design now lives in the Theme Studio.</strong>
    <span class="text-sm muted">Logo, colours, layout, hero, fonts, navigation, article pages and the social share image are all edited there with a live preview.</span>
    <a class="btn btn--primary btn--sm mt-2" href="/os/theme">Open Theme Studio →</a>
  </div>
</div>
<div class="settings-section">
  <div class="settings-block-title">Branding</div>
  <div class="field">
    <label class="field-label">Logo &amp; favicon</label>
    <div class="settings-row" style="align-items:center;gap:1rem">
      <img id="brand-favicon-img" src="/favicon.ico?t=` + strconv.FormatInt(time.Now().Unix(), 10) + `" alt="Current favicon" width="40" height="40" style="border-radius:6px;background:var(--surface-2,#1a1a1a)">
      <div class="settings-row-info">
        <div class="settings-row-label">Site mark</div>
        <div class="settings-row-hint" id="brand-favicon-state">` + html.EscapeString(faviconState) + `</div>
      </div>
    </div>
    <span class="field-hint">PNG or ICO, square, ≤ 256 KB. Used as the favicon (browser tab) and the nav-bar logo on the public site. Applies immediately.</span>
    <div class="theme-actions" style="display:flex;gap:.5rem;align-items:center;margin-top:.5rem;flex-wrap:wrap">
      <input type="file" id="brand-favicon-file" accept="image/png,image/x-icon,.png,.ico" class="input" style="max-width:18rem">
      <button type="button" class="btn btn--primary btn--sm" id="brand-favicon-upload">Upload</button>
      <button type="button" class="btn btn--sm" id="brand-favicon-remove">Remove (use default)</button>
      <span id="brand-favicon-status" class="text-xs muted" role="status" aria-live="polite"></span>
    </div>
  </div>
</div>
<div class="settings-section">
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

func osSettingsMembers(ctx context.Context, ss *settings.Store) string {
	membershipBtns := ""
	if ss != nil && ss.Get(ctx, settings.KeyMembershipButtons) == "true" {
		membershipBtns = " checked"
	}
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
      <div class="settings-row-label">Show Sign in / Sign up on the site</div>
      <div class="settings-row-hint">Display public Sign in &amp; Sign up buttons in the homepage navigation (like Ghost)</div>
    </div>
    <input type="checkbox" class="toggle" data-setting-key="` + settings.KeyMembershipButtons + `"` + membershipBtns + `>
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

func osSettingsEmail(ctx context.Context, ss *settings.Store) string {
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

func osSettingsSecurity(_ context.Context, _ *settings.Store) string {
	return `<div class="settings-section">
  <div class="settings-block-title">Security</div>
  <p class="text-sm muted">Two-factor authentication (TOTP) and session management live in the dedicated <a href="/os/security">Security</a> panel.</p>
</div>`
}

func osSettingsAdvanced(_ context.Context, _ *settings.Store) string {
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

// handleOSActivity returns recent admin activity as JSON for the dashboard feed.
func (a *App) handleOSActivity(w http.ResponseWriter, r *http.Request) {
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

// handleOSCmdIndex returns the command palette search index as JSON.
func (a *App) handleOSCmdIndex(w http.ResponseWriter, r *http.Request) {
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
		{Label: "Update & Backup", Icon: "⬆", Href: "/os/update"},
		{Label: "General settings", Icon: "⚙", Href: "/os/settings/general"},
		{Label: "Design & theme", Icon: "🎨", Href: "/os/settings/design"},
		{Label: "Email settings", Icon: "✉", Href: "/os/settings/email"},
		{Label: "Members settings", Icon: "👥", Href: "/os/settings/members"},
		{Label: "Security settings", Icon: "🔒", Href: "/os/settings/security"},
		{Label: "API Keys", Icon: "🔑", Href: "/os/apikeys"},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"posts":    posts,
		"actions":  actions,
		"settings": sPages,
	})
}

// handleOSSettingsAPI persists a single settings key/value from the VayuOS UI.
func (a *App) handleOSSettingsAPI(w http.ResponseWriter, r *http.Request) {
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
	// Push the change into the live render pipeline and drop cached pages so
	// public-facing settings (site identity, membership buttons, SEO meta) take
	// effect immediately rather than on the next restart.
	if sv, err := a.siteSettings.GetAll(r.Context()); err == nil {
		render.SetActiveSettings(render.SiteSettings{
			Name:            sv[settings.KeySiteName],
			Tagline:         sv[settings.KeySiteTagline],
			Description:     sv[settings.KeySiteDescription],
			Author:          sv[settings.KeySiteAuthor],
			AuthorBio:       sv[settings.KeyAuthorBio],
			ShowMembership:  sv[settings.KeyMembershipButtons] == "true",
			PrimaryLight:    sv[settings.KeyThemePrimaryLight],
			PrimaryDark:     sv[settings.KeyThemePrimaryDark],
			AccentLight:     sv[settings.KeyThemeAccentLight],
			AccentDark:      sv[settings.KeyThemeAccentDark],
			CustomCSS:       sv[settings.KeyThemeCustomCSS],
			Keywords:        sv[settings.KeyHeadKeywords],
			ThemeColor:      sv[settings.KeyHeadThemeColor],
			Robots:          sv[settings.KeyHeadRobots],
			VerifyGoogle:    sv[settings.KeyHeadVerifyGoogle],
			VerifyBing:      sv[settings.KeyHeadVerifyBing],
			NavJSON:         sv[settings.KeyNavItems],
			FooterJSON:      sv[settings.KeyFooterConfig],
			OGImage:         render.OGImagePath(sv[settings.KeyThemeOGImage]),
			ShowHero:        sv[settings.KeyHomeHero] == "true",
			CommentsEnabled: sv[settings.KeyFeatureComments] != "off",
		})
	}
	render.CachePurgeAll()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSQuickCreatePost creates a draft post from the dashboard quick-compose.
func (a *App) handleOSQuickCreatePost(w http.ResponseWriter, r *http.Request) {
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
	// we seed a single space: it trims to empty, so handleOSEditor treats the
	// post as an empty draft and opens the block editor, and the placeholder is
	// replaced by the rendered blocks on the first save.
	if _, err := a.articles.Create(r.Context(), title, slug, " ", nil); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "create-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"slug": slug})
}

// handleOSSearchReindex triggers a full search index rebuild without requiring a
// CSRF token so that operators can call it with an API key from the shell.
// Example: curl -X POST https://yourdomain.com/os/api/search/reindex -H "X-API-Key: KEY"
func (a *App) handleOSSearchReindex(w http.ResponseWriter, r *http.Request) {
	a.handleSearchReindex(w, r)
}

// handleOSFeedRegenerate regenerates feed.xml (and sitemap.xml) from the
// current article store. Useful after a bulk migration that bypassed the
// normal write queue. Accessible with an API key so no browser session is needed.
// Example: curl -X POST https://yourdomain.com/os/api/feed/regenerate -H "X-API-Key: KEY"
func (a *App) handleOSFeedRegenerate(w http.ResponseWriter, r *http.Request) {
	go generateRSS()
	go generateSitemap()
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "regenerating", "note": "feed.xml and sitemap.xml are being rebuilt in the background"})
}
