// SPDX-License-Identifier: Apache-2.0

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
//   - No inline <style> or style="" attributes. All CSS lives in vayuos.css.
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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/mode"
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
	r.Get("/os/static/js/admin-os.js", serveAdminOSAsset("js/admin-os.js", "application/javascript; charset=utf-8"))
	// The console's one stylesheet, and the script for what the Still Air
	// shell adds.
	r.Get("/os/static/css/vayuos.css", serveAdminOSAsset("css/vayuos.css", "text/css; charset=utf-8"))
	r.Get("/os/static/js/vayuos.js", serveAdminOSAsset("js/vayuos.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/img/vayupress-mark-white.png", serveAdminOSAsset("img/vayupress-mark-white.png", "image/png"))
	r.Get("/os/static/img/vayupress-mark-black.png", serveAdminOSAsset("img/vayupress-mark-black.png", "image/png"))
	// VayuOS installable app (PWA): manifest, service worker (scoped /os/), and app
	// icons. Served here without auth — like the other /os/static assets — so the
	// browser can fetch them to offer + keep the install; they carry no user data.
	r.Get("/os/manifest.webmanifest", a.handleOSManifest)
	r.Get("/os/sw.js", a.handleOSServiceWorker)
	r.Get("/os/static/icons/vayuos-192.png", servePNG(osIcon192PNG))
	r.Get("/os/static/icons/vayuos-512.png", servePNG(osIcon512PNG))
	r.Get("/os/static/icons/vayuos-maskable-512.png", servePNG(osIconMaskablePNG))
	r.Get("/os/static/icons/vayuos-apple-180.png", servePNG(osIconApplePNG))
	// Alpine.js CSP build + the VayuOS island registry (ADR-0136). Self-hosted,
	// eval-free, served same-origin so they satisfy script-src 'self' with no
	// unsafe-eval.
	r.Get("/os/static/js/alpine-csp.min.js", serveAdminOSAsset("js/alpine-csp.min.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/vayu-islands.js", serveAdminOSAsset("js/vayu-islands.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-editor.js", serveAdminOSAsset("js/admin-os-editor.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-security.js", serveAdminOSAsset("js/admin-os-security.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-members.js", serveAdminOSAsset("js/admin-os-members.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-pwa.js", serveAdminOSAsset("js/admin-os-pwa.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-mail-recovery.js", serveAdminOSAsset("js/admin-os-mail-recovery.js", "application/javascript; charset=utf-8"))
	// The sign-in shell's light/dark switcher. It had no route at all, so the
	// three theme buttons on the login page have been inert since they shipped —
	// found by the guard in console_assets_test.go, not by anyone using it.
	r.Get("/os/static/js/os-theme.js", serveAdminOSAsset("js/os-theme.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-newsletter.js", serveAdminOSAsset("js/admin-os-newsletter.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-profile.js", serveAdminOSAsset("js/admin-os-profile.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-intel.js", serveAdminOSAsset("js/admin-os-intel.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-pages.js", serveAdminOSAsset("js/admin-os-pages.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-tools.js", serveAdminOSAsset("js/admin-os-tools.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-theme.js", serveAdminOSAsset("js/admin-os-theme.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/theme-preview-frame.js", serveAdminOSAsset("js/theme-preview-frame.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-theme-store.js", serveAdminOSAsset("js/admin-os-theme-store.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-mail.js", serveAdminOSAsset("js/admin-os-mail.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-talk.js", serveAdminOSAsset("js/admin-os-talk.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-tor.js", serveAdminOSAsset("js/admin-os-tor.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-update.js", serveAdminOSAsset("js/admin-os-update.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-storage.js", serveAdminOSAsset("js/admin-os-storage.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-website.js", serveAdminOSAsset("js/admin-os-website.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-bundle.js", serveAdminOSAsset("js/admin-os-bundle.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/admin-os-site-editor.js", serveAdminOSAsset("js/admin-os-site-editor.js", "application/javascript; charset=utf-8"))
	r.Get("/os/static/js/purify.min.js", serveAdminOSAsset("js/purify.min.js", "application/javascript; charset=utf-8"))

	// Fonts are NOT served from /os. This route used to allowlist three names —
	// space-grotesk.woff2, inter.woff2, jetbrains-mono.woff2 — and read them from
	// STATIC_DIR on disk. None of those files exists in this repository or in any
	// binary, so the route could only ever 404, and the console stylesheet
	// referenced all three: every console page load fired three failed requests and silently fell
	// back to the system font stack.
	//
	// Unlike serveAdminOSAsset it had no embedded fallback, which is why the CSS
	// and JS survived a binary-only update and the fonts did not. Rather than ship
	// ~200 KB of typefaces inside a single-binary product to fix cosmetics that
	// already looked right, the console stylesheet names only weights the binary
	// already embeds (/static/fonts, handleStaticFont) and leaves the rest to the
	// system stack.

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
		// Enter-the-Tor-world bridge (ADR-0141): while the operator's view is set to
		// Tor, this proxies /os/* into the separate Tor-world instance so they manage
		// its data (its own database), never clearnet's. Mounted after auth so only a
		// signed-in admin can reach the proxy; /os/world and /os/logout are exempt so
		// the operator can always flip back or sign out.
		pr.Use(a.torWorldMiddleware)
		// The world switch itself: a light GET that sets/clears the per-browser view
		// cookie (enabling the Tor world is the separate CSRF-checked space toggle).
		pr.Get("/os/world", a.handleWorldSwitch)

		// Pages
		//
		// "/os" (no slash) is outside the installed app's scope, "/os/", so an
		// installed app that navigated there left app mode and showed the address
		// bar. It redirects into scope for old bookmarks; everything the console
		// links to is osHome.
		pr.Get("/os", func(w http.ResponseWriter, r *http.Request) {
			dest := osHome
			if r.URL.RawQuery != "" {
				dest += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, dest, http.StatusFound)
		})
		// "/os/" serves the dashboard directly rather than redirecting to "/os".
		// It is the installed app's start_url, and it has to be inside the service
		// worker's "/os/" scope for the browser to mint a real app instead of a
		// shortcut (see handleOSManifest). A redirect here would not do: the
		// installability check follows the start URL, and answering it with a 30x
		// is what a shortcut-only install looks like from the browser's side.
		pr.Get("/os/", a.handleOSDashboard)
		pr.Get("/os/change-password", a.handleOSChangePassword)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/change-password", a.handleOSChangePasswordSubmit)
		pr.Get("/os/posts", a.handleOSPosts)
		pr.Get("/os/comments", a.handleOSComments)
		// Session-friendly comment moderation. The /api/v1/admin/comments originals
		// require an API key; VayuOS operators hold a session cookie.
		pr.With(auth.CSRFTokenMiddleware).Put("/os/api/comments/{id}/status", a.handleCommentModerate)
		// HTMX in-place moderation: returns an HTML fragment (new action buttons +
		// out-of-band status pill and pending/approved counts) instead of JSON, so
		// the Comments manager updates the row without a full-page reload.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/comments/{id}/status-fragment", a.handleOSCommentModerateFragment)
		// Custom pages — standalone articles flagged is_page (no post chrome),
		// managed separately from the blog feed (Tumblr-style "Add a page").
		pr.Get("/os/pages", a.handleOSPages)
		pr.Get("/os/website", a.handleOSWebsite)
		pr.Get("/os/website/editor", a.handleOSSiteEditor)
		// VayuDomains registry (migration 059) — manage every hostname this
		// install answers on. Writes are CSRF-protected session-friendly APIs.
		pr.Get("/os/domains", a.handleOSDomains)
		// Per-site manager — the "control every part of this site" surface reached
		// from a domain card and from the Optimize hub's "Your websites" row.
		pr.Get("/os/domains/{id}", a.handleOSDomainManage)

		// ── Per-domain console (ADR-0153 Phase 3) ──────────────────────────────
		//
		// The scope is the URL. /os/d/{id}/theme edits {id}; /os/theme edits the
		// primary. The middleware resolves {id} to a settings.Scope and refuses by
		// NOT ROUTING, so no handler downstream can run against a domain nobody
		// proved exists — and a write cannot be addressed by a stale switcher,
		// because there is no switcher.
		pr.Route("/os/d/{id}", func(dr chi.Router) {
			dr.Use(a.scopedDomainMiddleware)
			dr.Get("/", a.handleOSScopedHome)
			// Re-run the certificate checks on demand. A read, so no CSRF token
			// and no state change — see ADR-0160.
			dr.Get("/diagnose/live", a.handleOSScopedDiagnoseLive)
			dr.Get("/settings", a.handleOSScopedSettings)
			dr.With(auth.CSRFTokenMiddleware).Post("/api/settings", a.handleOSScopedSettingsSave)
			// Content (ADR-0154 D4). The move endpoint takes "site" or "primary"
			// and resolves it against the domain in the PATH, so a caller cannot
			// name a third site.
			dr.Get("/content", a.handleOSScopedContent)
			dr.With(auth.CSRFTokenMiddleware).Post("/api/content/move", a.handleOSScopedContentMove)
			dr.With(auth.CSRFTokenMiddleware).Post("/api/content/new", a.handleOSScopedContentNew)
			// Website (ADR-0154 D9): what this domain serves at "/", and the
			// content of that site when it serves a website.
			dr.Get("/website", a.handleOSScopedWebsite)
			dr.Get("/website/editor", a.handleOSScopedSiteEditor)
			dr.With(auth.CSRFTokenMiddleware).Post("/api/website", a.handleOSScopedWebsiteSave)
			dr.With(auth.CSRFTokenMiddleware).Post("/api/website/sample-demo", a.handleOSScopedSampleDemo)
			// A whole hand-built site for this domain (ADR-0154 D12). Both go
			// through customsite.Deploy, which confines every write to an
			// os.Root and refuses traversal in archive entries.
			dr.With(auth.CSRFTokenMiddleware).Post("/api/website/bundle/uploads", a.handleBundleUploadStart(scopedBundleSite))
			dr.With(auth.CSRFTokenMiddleware).Post("/api/website/bundle/uploads/{upload}", a.handleBundleUploadChunk(scopedBundleSite))
			dr.With(auth.CSRFTokenMiddleware).Post("/api/website/bundle/uploads/{upload}/deploy", a.handleBundleUploadDeploy(scopedBundleSite))
			dr.Get("/api/website/bundle/download", a.handleBundleDownload(scopedBundleSite))
			dr.With(auth.CSRFTokenMiddleware).Post("/api/website/bundle/generations/{gen}/restore", a.handleBundleRestore(scopedBundleSite))
			// The site as a document (ADR-0161): draft, publish, history.
			a.registerSiteDocRoutes(dr, auth.CSRFTokenMiddleware, "/api/site-doc", scopedSiteDocTarget)
			dr.Get("/api/website/preview", a.handleOSScopedWebsitePreview)
			dr.With(auth.CSRFTokenMiddleware).Post("/api/website/bundle/rollback", a.handleOSScopedBundleRollback)
			// THEME STUDIO IS NO LONGER MOUNTED PER SITE, and the comment that
			// used to sit here is why it took so long to notice: it said the
			// handler "reads its scope from the request, so one code path serves
			// both". The HANDLER does. The PAGE does not — its script posts to
			// absolute /os/api/theme/* and /os/api/settings paths, so every write
			// from /os/d/{id}/theme landed on the primary. Beneath that,
			// theme_tokens is CHECK(id=1) and applying a theme sets a process
			// global, so there is no per-site theme for a scoped route to write.
			//
			// The old address REDIRECTS rather than 404s — it is in bookmarks and
			// in this console's own history — and lands on the per-site settings
			// page, which is where a hosted site's identity genuinely lives
			// (ADR-0154 D3). Theme Studio is now named in sharedTools as
			// install-wide, alongside the media library and the newsletter.
			dr.Get("/theme", a.handleOSScopedThemeRetired)
			// This domain's own mark. The SAME upload handler as the operator's
			// branding page, mounted a second time — it takes its scope from the
			// request, so one code path stores both and a per-domain copy cannot
			// drift from the primary's.
			dr.With(auth.CSRFTokenMiddleware).Post("/api/branding/favicon", a.handleFaviconUpload)
			// Read side, for the console. A hosted domain's mark is already public
			// at that domain's own /favicon.ico; this exists because the panel is
			// served from a DIFFERENT origin and cannot ask another host for it.
			dr.Get("/branding/mark", a.handleOSScopedBrandMark)
			dr.Get("/seo", a.handleOSScopedSEO)
			dr.Get("/analytics", a.handleOSScopedAnalytics)
			dr.With(auth.CSRFTokenMiddleware).Post("/api/copy-from-primary", a.handleOSScopedCopyFromPrimary)
		})
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains", a.handleOSDomainCreate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/sync-all", a.handleOSDomainSyncAll)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/status", a.handleOSDomainStatus)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/allowance", a.handleOSDomainAllowance)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/release-mirror", a.handleOSDomainReleaseMirror)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/release-mirror/sync", a.handleOSDomainReleaseMirrorSync)
		pr.Get("/os/api/release-mirror", a.handleOSReleaseMirrorStatus)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/serves", a.handleOSDomainServes)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/client", a.handleOSDomainClient)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/sync", a.handleOSDomainSync)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/domains/{id}/brand", a.handleOSDomainBrand)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/domains/{id}", a.handleOSDomainDelete)
		// One-click "Add Tor site" (ADR-0141): Tor-world only. add-site registers a
		// placeholder site the operator can serve blog/mail on; sites/assign are the
		// parent↔child control channel that mints and hands back each site's .onion
		// (bearer-authed from the parent, so CSRF-exempt).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/torworld/add-site", a.handleTorWorldAddSite)
		pr.Get("/os/api/torworld/sites", a.handleTorWorldSites)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/torworld/assign", a.handleTorWorldAssign)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/website/save", a.handleOSWebsiteSave)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/website/custom-bundle/uploads", a.handleBundleUploadStart(primaryBundleSite(a)))
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/website/custom-bundle/uploads/{upload}", a.handleBundleUploadChunk(primaryBundleSite(a)))
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/website/custom-bundle/uploads/{upload}/deploy", a.handleBundleUploadDeploy(primaryBundleSite(a)))
		pr.Get("/os/api/website/custom-bundle/download", a.handleBundleDownload(primaryBundleSite(a)))
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/website/custom-bundle/generations/{gen}/restore", a.handleBundleRestore(primaryBundleSite(a)))
		a.registerSiteDocRoutes(pr, auth.CSRFTokenMiddleware, "/os/api/site-doc", primarySiteDocTarget)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/website/custom-rollback", a.handleOSWebsiteCustomRollback)
		pr.Get("/os/api/website/custom-guide", a.handleOSWebsiteCustomGuide)
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
		// Growth hub: consolidates Members / Newsletter / Monetization / Advertising
		// (+ My Profile) into one dashboard-style card page (admin-only).
		pr.Get("/os/growth", a.hubRedirect("audience"))
		// Operations hub: consolidates System Modes / Policy / Topology / Replay /
		// Fault Engine / ADR Registry into one dashboard-style card page (admin-only).
		pr.Get("/os/operations", a.hubRedirect("system"))
		// Power & Maintenance: take the public site offline behind a premium
		// maintenance page, restart, or shut down. Preview renders that page.
		pr.Get("/os/power", a.handleOSPower)
		pr.Get("/os/vayukeep", a.handleOSVayuKeep)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/backup", a.handleOSVayuKeepBackup)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/drill", a.handleOSVayuKeepDrill)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/verify", a.handleOSVayuKeepVerify)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/setup", a.handleOSVayuKeepSetup)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/disable", a.handleOSVayuKeepDisable)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/restore", a.handleOSVayuKeepRestore)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/delete", a.handleOSVayuKeepDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/retention", a.handleOSVayuKeepRetention)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/prune", a.handleOSVayuKeepPrune)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayukeep/clear-older", a.handleOSVayuKeepClearOlder)
		pr.Get("/os/power/preview", a.handleOSPowerPreview)
		// Optimize hub: consolidates SEO / Analytics / VayuShield / Theme Studio /
		// Theme Store + Tools / Domains / Settings / VayuAPI / VayuMCP into one
		// dashboard-style card page (editor+; admin-only cards hidden from editors).
		pr.Get("/os/optimize", a.hubRedirect("site"))
		// System hub: Storage & System / Settings / My Profile as a card page —
		// gives the Tor-world console the same minimal hub treatment (ADR-0141).
		pr.Get("/os/system", a.hubRedirect("system"))
		pr.Get("/os/members", a.handleOSMembers)
		// HTMX fragment: live-refresh the Members "Recent activity" feed.
		pr.Get("/os/members/activity", a.handleOSMembersActivityFragment)
		// Session-friendly membership management APIs (the /api/v1/admin/* originals
		// require an API key; VayuOS operators hold a session cookie).
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
		// Removing members who never confirmed their address. The store refuses to
		// delete a verified member, so neither route can remove a real account.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/members/unverified/purge", a.handleMembersPurgeUnverified)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/members/{email}", a.handleMemberDeleteAdmin)
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
		pr.Get("/os/monitoring/live", a.handleOSMonitoringLive)
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
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/harmony", handleThemeHarmony)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/nearest", handleThemeNearest)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/theme/draft", a.handleThemeDraftSave)
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
		// Power & Maintenance actions (admin-only, CSRF-protected).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/power/maintenance", a.handleOSPowerMaintenance)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/power/restart", a.handleOSPowerRestart)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/power/shutdown", a.handleOSPowerShutdown)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/power/crawlers", a.handleOSPowerCrawlers)

		// Monetization — payment order ledger + gateway config, and the
		// activation-gated advertising surface.
		pr.Get("/os/monetization", a.handleOSMonetization)
		pr.Get("/os/api/orders", a.handleOSOrdersList)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/orders/{id}/paid", a.handleOSOrderMarkPaid)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/orders/{id}/cancel", a.handleOSOrderCancel)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/orders/{id}/refund", a.handleOSOrderRefund)
		// One-click card gateways (Stripe now; PayPal in a later phase).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/stripe/connect", a.handleStripeConnect)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/stripe/test", a.handleStripeTest)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/stripe/disconnect", a.handleStripeDisconnect)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/paypal/connect", a.handlePayPalConnect)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/paypal/test", a.handlePayPalTest)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/paypal/disconnect", a.handlePayPalDisconnect)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/btcpay/connect", a.handleBTCPayConnect)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/btcpay/test", a.handleBTCPayTest)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/payments/btcpay/disconnect", a.handleBTCPayDisconnect)
		// Premium Mail-ID management console (see/approve/disapprove sales + the
		// operator's premium-name list).
		pr.Get("/os/monetization/mailids", a.handleOSMailIDs)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/mailids/{id}/approve", a.handleOSMailIDApprove)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/mailids/{id}/revoke", a.handleOSMailIDRevoke)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/mailids/premium-names/add", a.handleOSMailIDNameAdd)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/mailids/premium-names/remove", a.handleOSMailIDNameRemove)
		pr.Get("/os/ads", a.handleOSAds)
		pr.Get("/os/api/ads", a.handleOSAdsList)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/ads", a.handleOSAdCreate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/ads/{id}/toggle", a.handleOSAdToggle)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/ads/{id}/approve", a.handleOSAdReviewApprove)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/ads/{id}/reject", a.handleOSAdReviewReject)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/ads/{id}", a.handleOSAdDelete)

		// Update & Backup — one-click signature-verified self-update plus full
		// site (database + settings) export/import. Writes are CSRF-protected and
		// admin-role gated inside each handler; export/import lift the server
		// read/write deadlines so transfers have no size limit.
		pr.Get("/os/update", a.handleOSUpdate)
		pr.Get("/os/api/update/check", a.handleOSUpdateCheck)
		pr.Get("/os/api/update/history", a.handleOSUpdateHistory)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/update/apply", a.handleOSUpdateApply)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/update/rollback", a.handleOSUpdateRollback)
		// Subdomain provisioning: the console asks, a root-side systemd unit acts.
		// CSRF-protected because it makes the server run certbot on demand, and
		// Let's Encrypt rate limits are a finite resource worth not letting an
		// off-origin page burn.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/provision/run", a.handleOSProvisionRequest)
		pr.Get("/os/api/provision/status", a.handleOSProvisionStatus)
		// Domains & DNS — which records an install needs and whether each is
		// actually pointed here. Every subdomain fails quietly when its record is
		// missing, so this is the page that makes a half-configured install visible.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/dns", a.handleOSDNS)
		pr.Get("/os/api/backup/export", a.handleOSBackupExport)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/backup/import", a.handleOSBackupImport)

		// Storage & System — admin-only resource usage (RAM/disk) plus managed
		// files (backups/logs/temp) with per-file download + delete. The
		// download/delete validate the path against the live managed-file set, so
		// path traversal is impossible and the live DB can never be touched.
		pr.Get("/os/storage", a.handleOSStorage)
		pr.Get("/os/api/storage/download", a.handleOSStorageDownload)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/storage/delete", a.handleOSStorageDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/storage/clear-cache", a.handleOSStorageClearCache)
		pr.Get("/os/seo", a.handleOSSEONative)
		pr.Get("/os/analytics", a.handleOSAnalytics)
		// VayuAnalytics: export downloads + goal management (session-authed).
		pr.Get("/os/api/analytics/export", a.handleAnalyticsExport)
		pr.Get("/os/api/analytics/export-parquet", a.handleAnalyticsParquetExport)
		pr.Get("/os/api/analytics/realtime", a.handleAnalyticsRealtime)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/analytics/goals", a.handleAnalyticsCreateGoal)
		pr.With(auth.CSRFTokenMiddleware).Delete("/os/api/analytics/goals/{id}", a.handleAnalyticsDeleteGoal)
		// VayuShield operator panel (admin-gated).
		// Both the page load AND the section poll re-issue the vp_csrf cookie
		// (CSRFTokenMiddleware) so it never goes stale while the panel is open.
		// The hero polls the section route every 10s, so within 10s of a CSRF
		// secret rotation (every deploy/restart) or the 1h cookie lifetime, the
		// token is refreshed — without this, Save / Tier / Verify POSTs silently
		// 403 after a deploy until the operator hard-reloads (the "Save button
		// does nothing" report). Mirrors the VayuMail fix below.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/shield", a.handleOSShield)
		// Per-section HTMX fragment refresh (no whole-page reload).
		pr.With(auth.CSRFTokenMiddleware).Get("/os/shield/section/{name}", a.handleOSShieldSection)
		pr.Get("/os/api/shield/export", a.handleOSShieldExport)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/verify", a.handleOSShieldVerify)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/dismiss", a.handleOSShieldDismiss)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/release", a.handleOSShieldRelease)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/settings", a.handleOSShieldSettings)
		// Tier 2/3 in-panel toggle — records intent (a flag file); a root agent applies it.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/tier", a.handleOSShieldTier)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/cdn-allow", a.handleOSShieldCDNAllow)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/agent-upgrade", a.handleOSShieldAgentUpgrade)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/fix", a.handleOSShieldFix)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/shield/rescue", a.handleOSShieldRescue)
		// VayuOS — native control layer (Phase 2): Publishing · Mail · PGP.
		// GET pages are wrapped in CSRFTokenMiddleware so each load (re)issues the
		// vp_csrf cookie the panel's POSTs read back; without this the token
		// expires (1h) and Send / Save-as-draft / message actions start 403ing.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail", a.handleVayuOSDashboard)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/pgp", a.handleVayuOSPGP)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/dns", a.handleVayuOSMail)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/dns/verify", a.handleVayuOSMailDNSVerify)
		// Legacy /os/vayuos/* URLs (pre-2.8 layout) redirect permanently to the
		// clean /os/vayumail/* namespace so bookmarks and muscle memory keep working.
		pr.Get("/os/vayuos", redirectLegacyVayuOS)
		pr.Get("/os/vayuos/*", redirectLegacyVayuOS)
		pr.Post("/os/vayuos/*", redirectLegacyVayuOS)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/inbox", a.handleVayuOSInbox)
		pr.Get("/os/vayumail/inbox/fragment", a.handleVayuOSInboxFragment)
		pr.Get("/os/vayumail/unseen", a.handleVayuOSUnseen)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/inbox/action", a.handleVayuOSInboxAction)
		pr.Get("/os/vayumail/attachment", a.handleVayuOSAttachment)
		pr.Get("/os/vayumail/search/fragment", a.handleVayuOSSearchFragment)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/search", a.handleVayuOSSearch)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/message", a.handleVayuOSMessage)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/sent", a.handleVayuOSSent)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/compose", a.handleVayuOSCompose)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/accounts", a.handleVayuOSAccounts)
		// PUBLIC key download only. There is no private-key counterpart to this
		// route and there must never be one — an administrator has no business
		// holding another mailbox's private key. The owner's own device fetches it
		// via /api/v1/members/vayumail-privkey under the MAIL-SYNC device scope.
		pr.Get("/os/vayumail/accounts/pubkey", a.handleVayuOSAccountPubKey)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/connect", a.handleVayuOSConnect)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/send", a.handleVayuOSSend)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/draft", a.handleVayuOSDraft)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/message/action", a.handleVayuOSMessageAction)
		// Split reading pane: load a message beside the list + act on it in place.
		pr.Get("/os/vayumail/inbox/readpane", a.handleVayuOSReadpane)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/message/pane-action", a.handleVayuOSMessagePaneAction)
		// Aliases & auto-forwarding (admin-only; HTMX card on the Accounts page).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/aliases/action", a.handleVayuOSAliasAction)
		// Vacation autoresponder (admin-only; HTMX card on the Accounts page).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/autoreply/action", a.handleVayuOSAutoreplyAction)
		// Server-side filter rules (admin-only; HTMX card on the Accounts page).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/filters/action", a.handleVayuOSFilterAction)
		// Outbox: HTMX auto-refresh fragment + per-message Resend/Delete/Retry-all.
		pr.Get("/os/vayumail/outbox/fragment", a.handleVayuOSOutboxFragment)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/outbox/action", a.handleVayuOSOutboxAction)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/outbox/retention", a.handleVayuOSOutboxRetention)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/create", a.handleVayuOSAccountCreate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/delete", a.handleVayuOSAccountDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/update", a.handleVayuOSAccountUpdate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/totp", a.handleVayuOSAccountTOTP)
		// Account recovery enrolment + readiness (ADR-0144). Admin-only; the
		// generate endpoint issues credentials, so it is CSRF-protected like every
		// other console write.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/api/vayuos/mail/recovery/status", a.handleVayuOSRecoveryStatus)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuos/mail/recovery/codes", a.handleVayuOSRecoveryCodes)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuos/mail/recovery/contact", a.handleVayuOSRecoveryContact)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuos/mail/recovery/decide", a.handleVayuOSRecoveryDecide)
		// Accounts redesign: HTMX list fragment + inline action swap (enable/disable,
		// role, quota, retention, delete) so the page never full-reloads.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/accounts/fragment", a.handleVayuOSAccountsFragment)
		// One mailbox's own page: every per-mailbox setting, on its own URL. The
		// accounts list keeps a link to it instead of nesting seven sections.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/accounts/settings", a.handleVayuOSMailboxSettings)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/avatar", a.handleVayuOSAvatarUpload)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/avatar/remove", a.handleVayuOSAvatarRemove)
		// Prebuilt cartoon avatars: pick one instead of uploading (POST sets it),
		// with a GET preview endpoint that renders each option for this address.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/avatar/cartoon", a.handleVayuOSAvatarCartoon)
		pr.Get("/os/vayumail/accounts/avatar/cartoon", a.handleVayuOSAvatarCartoonPreview)
		pr.Get("/os/vayumail/accounts/avatar", a.handleVayuOSAvatarServe)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/action", a.handleVayuOSAccountsAction)
		// Per-mailbox address book: view panel + add/delete + one-click save from a
		// message. Every route resolves the owning mailbox (contactOwner), so a
		// non-admin can only ever touch their own contacts.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/contacts", a.handleVayuOSContacts)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/contacts/add", a.handleVayuOSContactAdd)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/contacts/delete", a.handleVayuOSContactDelete)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/contacts/save", a.handleVayuOSContactSave)
		// Devices: GET fragment backs the self-refresh poller so a newly-registered
		// pending device surfaces without a reload.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayumail/devices/fragment", a.handleVayuOSDevicesFragment)
		// App passwords — device credentials for VayuMail Mobile (and any
		// IMAP/SMTP/POP3 client). Created/revoked from the Connect tab; an admin
		// manages any mailbox, a mailbox holder only their own (enforced in the
		// handlers). Same session+CSRF chain as the TOTP route above.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/apppassword", a.handleVayuOSAppPasswordCreate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/accounts/apppassword/delete", a.handleVayuOSAppPasswordDelete)
		// Device approval (ADR-0129) — approve/block/remove registered devices
		// and toggle per-mailbox enforcement. Admin-only (enforced in the
		// handler): the 2FA-protected console IS the approval anchor, so mailbox
		// holders can never approve their own devices.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/vayumail/devices/action", a.handleVayuOSDeviceAction)

		// VayuTalk — its own top-level system (NOT a VayuMail sub-tab): ephemeral
		// E2E chat over the same relay the mobile app uses. The page + send POST
		// are CSRF/session guarded; the SSE stream is a GET authenticated by the
		// session cookie (EventSource can't set headers), server-side-decrypting
		// each envelope for the signed-in mailbox.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/talk", a.handleVayuOSTalk)
		pr.Get("/os/talk/stream", a.handleVayuOSTalkStream)
		pr.Get("/os/talk/peer", a.handleVayuOSTalkPeer)
		pr.Get("/os/talk/qr", a.handleVayuOSTalkQR)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/talk/send", a.handleVayuOSTalkSend)
		// Tor world: mint a fresh anonymous chat handle (ADR-0141).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/talk/rotate", a.handleVayuOSTalkRotate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/talk/federation", a.handleVayuOSTalkFederationToggle)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/talk/read", a.handleVayuOSTalkRead)

		// VayuTor — onion services control page + one-click toggle + count JSON.
		// VayuVeil (ADR-0150) — the endpoint observation-control console. Admin-only
		// via osPathMinLevel: it enumerates device nodes, display sockets and kernel
		// tunables on the host, which is operator information rather than author
		// information.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuveil", a.handleOSVayuVeil)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/vayuflow", a.handleOSVayuFlow)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuveil/toggle", a.handleOSVayuVeilToggle)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuveil/harden", a.handleOSVeilHardenRequest)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuflow/arm", a.handleOSVayuFlowArm)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuflow/run", a.handleOSVayuFlowRun)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuflow/save", a.handleOSVayuFlowSave)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuflow/enable", a.handleOSVayuFlowEnable)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuflow/delete", a.handleOSVayuFlowDelete)
		pr.With(auth.CSRFTokenMiddleware).Get("/os/tor", a.handleOSTor)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/tor/toggle", a.handleOSTorToggle)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/tor/bridges", a.handleOSTorBridges)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/tor/pagestats", a.handleOSTorPageStats)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/tor/hardening", a.handleOSTorHardening)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/tor/vanity", a.handleOSTorVanity)
		pr.Get("/os/tor/stats", a.handleOSTorStats)

		pr.Get("/os/vayumail/security", a.handleVayuOSSecurity)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/vayuos/security/check", a.handleVayuOSSecurityCheck)
		pr.Get("/os/api/vayuos/health", a.handleVayuOSHealthJSON)
		// "My site" — the agency client's own page (ADR-0152). Declared in
		// clientSurface; every other /os route is refused to a client by default.
		pr.Get("/os/mysite", a.handleOSMySite)
		pr.Get("/os/mysite/traffic", a.handleOSMySiteTraffic)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/mysite/brand", a.handleOSMySiteBrand)
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
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/apikeys/activate", a.handleOSAPIKeySetActive(true))
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/apikeys/deactivate", a.handleOSAPIKeySetActive(false))
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/credentials/save", a.handleOSCredentialSave)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/credentials/reveal", a.handleOSCredentialReveal)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/credentials/delete", a.handleOSCredentialDelete)

		// VayuMCP (ADR-0139, Stage 2): the one-click front door for
		// connecting Claude/MCP clients. GET-only — it mints and revokes through
		// the CSRF-protected API-key endpoints above, adding no new write surface.
		pr.Get("/os/connector", a.handleOSConnector)

		// Buzz connector (ADR-0146): the same MCP surface, walked through for a
		// Buzz agent. GET-only for the same reason — it mints through the
		// CSRF-protected API-key endpoints above and adds no write surface of
		// its own.
		pr.Get("/os/buzz", a.handleOSBuzz)

		// Claude Code connector (ADR-0147): the Claude-specific routes in, split
		// off /os/connector so the protocol page stops answering a question
		// nobody asked. GET-only, same reasoning as above.
		pr.Get("/os/claudecode", a.handleOSClaudeCode)

		// VayuOS Spaces (ADR-0141): the Clearnet/Tor worlds + the one-click
		// Anonymous Tor Space toggle. The GET is wrapped in CSRFTokenMiddleware so
		// loading the page (re)issues the vp_csrf cookie the toggle POST reads back
		// — without this the toggle 403s on a fresh session / after a restart or the
		// 1h cookie lifetime, and reloading never recovers it (mirrors /os/tor,
		// /os/shield). The toggle POST is CSRF-checked.
		pr.With(auth.CSRFTokenMiddleware).Get("/os/spaces", a.handleOSSpaces)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/spaces/toggle", a.handleOSSpaceToggle)

		// CSRF-protected writes
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/seo/regenerate", a.handleSEORegenerate)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/seo/indexnow-test", a.handleOSIndexNowTest)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/settings", a.handleOSSettingsAPI)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/notifications/seen", a.handleOSNotificationsSeen)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/quick-create", a.handleOSQuickCreatePost)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/status", a.handleOSPostStatus)
		// One-request bulk apply for the Posts manager (Wave 4): per-slug outcomes,
		// in-place row updates and honest tab counts instead of N parallel fetches
		// and a blind full-page reload.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/bulk", a.handleOSPostsBulk)
		// HTMX in-place publish/unpublish toggle: returns an HTML row fragment
		// (flipped button + out-of-band status pill) instead of JSON, so the
		// Posts manager updates the row without a full-page reload.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/{slug}/status-fragment", a.handleOSPostToggleFragment)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/{slug}/indexnow-fragment", a.handleOSPostIndexNowFragment)
		// Signed draft-share link (Wave 4.4): the editor's Share button mints a
		// 48h preview token through the same signer the API uses, so a draft
		// reaches a reviewer without becoming public.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/{slug}/share", a.handleOSPostShare)
		// HTMX in-place pin/unpin: returns the flipped pin button + an out-of-band
		// "Pinned" badge, so the row updates without a full-page reload.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/posts/{slug}/pin-fragment", a.handleOSPostPinFragment)
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
		pr.Get("/os/api/editor/ai-providers", a.handleOSEditorAIProviders)
		pr.Get("/os/api/editor/ai-models", a.handleOSEditorAIModels)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/generate", a.handleOSEditorGenerate)
		// Generation is a background job; the panel polls this for the result. It is
		// a plain GET like the other editor reads — the job id is unguessable and
		// owner-checked, so there is no state change to protect with a CSRF token.
		pr.Get("/os/api/editor/generate/status", a.handleOSEditorGenerateStatus)
		pr.Get("/os/api/editor/versions/{slug}", a.handleOSEditorVersionList)
		pr.Get("/os/api/editor/versions/{slug}/{id}", a.handleOSEditorVersionGet)
		// Restore rewinds the article to a snapshot — a write, so CSRF-gated like
		// the save it mirrors.
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/editor/versions/{slug}/{id}/restore", a.handleOSEditorVersionRestore)

		// Read-only APIs (no CSRF needed)
		pr.Get("/os/api/activity", a.handleOSActivity)
		pr.Get("/os/api/cmd-index", a.handleOSCmdIndex)

		// Interactive operator consoles — rendered in the VayuOS shell.
		// These were previously in the RequireAPIKey group in routes.go, which
		// caused a 401 JSON error when visited from a browser (no API key header).
		// They belong here under requireSessionOrAPIKey so a browser session works.
		pr.Get("/os/modes", a.handleModesPage)
		pr.Get("/os/faults", a.handleFaultPage)
		pr.Get("/os/topology", a.handleTopologyPage)
		pr.Get("/os/replay", a.handleReplayPage)
		pr.Get("/os/adr", a.handleAdminADR)

		// Operator-initiated actions. API-key callers hold no browser session and
		// bypass CSRF (auth.CSRFTokenMiddleware exempts API-key auth); these two
		// endpoints have no browser-form caller (the panel's regenerate button hits
		// /os/api/seo/regenerate), so wrapping them in CSRF middleware hardens the
		// session lane without SameSite being the sole control (audit L2).
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/search/reindex", a.handleOSSearchReindex)
		pr.With(auth.CSRFTokenMiddleware).Post("/os/api/feed/regenerate", a.handleOSFeedRegenerate)
	})

	// Redirect bare /os/* to dashboard if hitting unknown paths
	r.Get("/os/*", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, osHome, http.StatusSeeOther)
	})
}

func serveAdminOSAsset(rel, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		diskPath := filepath.Join(adminOSStaticDir(), filepath.FromSlash(rel))
		css := strings.HasPrefix(contentType, "text/css")
		if fi, err := os.Stat(diskPath); err == nil {
			if !css {
				http.ServeFile(w, req, diskPath)
				return
			}
			if data, err := os.ReadFile(diskPath); err == nil { //nolint:gosec // rel is a fixed route literal, joined onto the static dir
				http.ServeContent(w, req, filepath.Base(rel), fi.ModTime(), bytes.NewReader(minifiedCSSFor(data)))
				return
			}
		}
		// The on-disk copy is missing — e.g. STATIC_DIR was never provisioned, or
		// is read-only under a hardened service sandbox so syncEmbeddedStatic
		// could not write it. Serve the copy compiled into the binary so the
		// panel always works, even immediately after a one-click self-update.
		if data, err := fs.ReadFile(embeddedStaticFS, rel); err == nil {
			if css {
				data = minifiedCSSFor(data)
			}
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

// spaceSwitch renders the one-click Clearnet⟷Tor world switch pinned to the top
// of the sidebar (ADR-0141). Admin-only — returns "" for every lower role. On a
// clearnet install it is interactive: clicking the inactive segment enables or
// disables the Anonymous Tor Space (the data-space-switch handler primes the CSRF
// cookie, then POSTs /os/spaces/toggle). On a whole-install Tor world it is a
// static indicator (you cannot turn a dedicated Tor install back to clearnet from
// here — that is a separate install). CSP-safe: no inline styles or handlers, all
// behaviour is wired by data attributes in the nonce-gated foot script.
func spaceSwitch(lvl int, _ *osSettings) string {
	if lvl < accessAdmin {
		return ""
	}
	if config.Cfg.OnionMode {
		// This install IS the Tor world. The Clearnet segment is ALWAYS a real link
		// back to /os/world?target=clearnet — when managed from the parent console
		// the parent intercepts it and drops the Tor view; opened directly at the
		// .onion in Tor Browser the child's own handler just redirects to /os (a
		// harmless no-op). Rendering it unconditionally guarantees there is never a
		// dead-end "stuck in Tor" state, regardless of how the child was launched.
		// The live .onion address itself is surfaced on the DASHBOARD now (world
		// card), not crammed under the switch — the toggle stays a clean control.
		return `<div class="space-switch-wrap">
  <div class="space-switch" data-active="tor" role="group" aria-label="Active world">
    <span class="space-switch__thumb" aria-hidden="true"></span>
    <a class="space-switch__seg" data-world="clearnet" href="/os/world?target=clearnet">` + iconWorldClearnet + `<span>Clearnet</span></a>
    <span class="space-switch__seg is-active" data-world="tor" aria-current="true">` + iconWorldTor + `<span>Tor</span></span>
  </div>
  <a class="space-switch__manage" href="/os/spaces">Manage worlds<span aria-hidden="true"> →</span></a>
</div>`
	}
	// This is the CLEARNET console (not OnionMode): rendering it at all means the
	// operator is currently VIEWING clearnet — viewing Tor proxies to the child's
	// own console instead. So Clearnet is ALWAYS the active segment and Tor is
	// ALWAYS the click-to-enter one, regardless of whether the Tor world happens to
	// be running. (Basing "active" on whether the Space was enabled made Tor look
	// selected while you were still on clearnet, so clicking it did nothing.)
	//
	// The anonymous world's live status + .onion address are shown on the DASHBOARD
	// (the world card), not under the switch — the toggle is just the world control.
	// data-space-switch on a segment = "clicking me switches to this world":
	// "on" enters the Tor world (this console proxies into it), "off" stays here.
	return `<div class="space-switch-wrap">
  <div class="space-switch" data-active="clearnet" role="group" aria-label="Switch world">
    <span class="space-switch__thumb" aria-hidden="true"></span>
    <button type="button" class="space-switch__seg is-active" data-space-switch="off" data-world="clearnet" aria-pressed="true">` + iconWorldClearnet + `<span>Clearnet</span></button>
    <button type="button" class="space-switch__seg" data-space-switch="on" data-world="tor" aria-pressed="false">` + iconWorldTor + `<span>Tor</span></button>
  </div>
  <a class="space-switch__manage" href="/os/spaces">Manage worlds<span aria-hidden="true"> →</span></a>
</div>`
}

// osGroupInt formats an integer with thousands separators (234465 → "234,465")
// so large counts read cleanly on the premium dashboard tiles.
func osGroupInt(n int) string {
	s := strconv.Itoa(n)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return neg + string(out)
}

// notifCap clamps a badge count to a compact "99+" so a large backlog never
// blows out the bell badge or a row's count chip.
func notifCap(n int) string {
	if n > 99 {
		return "99+"
	}
	return strconv.Itoa(n)
}

// line is what a notification says under its title. A tally reads "<count>
// <detail>" (e.g. "3 unread in your inbox"); a state's detail stands alone;
// storage is a percentage.
func (n osNotification) line() string {
	switch {
	case n.State:
		return n.Detail
	case n.Kind == "storage":
		return notifCap(n.Count) + "% " + n.Detail
	}
	return notifCap(n.Count) + " " + n.Detail
}

// osNotifItem renders one needs-action item: a condition, so no time, and a
// row that navigates straight to the page that clears it (a plain <a>, so no
// JS is needed for the jump). Its line already says the count; a chip beside
// it repeated the number, and put a "1" beside every condition.
func osNotifItem(n osNotification) string {
	return `<a class="notif-item notif-item--` + n.Severity + `" href="` + n.Href + `">
  <span class="notif-item__icon">` + saIcon(saNotifIcon(n.Kind)) + `</span>
  <span class="notif-item__body">
    <span class="notif-item__title">` + html.EscapeString(n.Title) + `</span>
    <span class="notif-item__detail">` + html.EscapeString(n.line()) + `</span>
  </span>
</a>`
}

// osRecentItem renders one event with its time. The server writes the time in
// UTC; the page rewrites it in the viewer's own clock.
func osRecentItem(e osRecentEvent, unread bool) string {
	cls := "notif-item notif-item--event"
	if unread {
		cls += " is-unread"
	}
	at := e.At.UTC()
	detail := ""
	if e.Detail != "" {
		detail = `<span class="notif-item__detail">` + html.EscapeString(e.Detail) + `</span>`
	}
	return `<a class="` + cls + `" href="` + e.Href + `">
  <span class="notif-item__icon">` + saIcon(saNotifIcon(e.Kind)) + `</span>
  <span class="notif-item__body"><span class="notif-item__title">` + html.EscapeString(e.Title) + `</span>` + detail + `</span>
  <time class="notif-item__time" datetime="` + at.Format(time.RFC3339) + `">` + at.Format("15:04") + ` UTC</time>
</a>`
}

// osNotifBell renders the system bar's notification centre (render 09): what
// needs the viewer, then what happened in the last day. The badge counts the
// first in full and the second until marked read. Each row is a direct link;
// the toggle, the times and Mark all read are wired in admin-os.js.
func osNotifBell(s *osSettings) string {
	var notifs []osNotification
	var recent []osRecentEvent
	var seen time.Time
	if s != nil {
		notifs, recent, seen = s.Notifications, s.Recent, s.RecentSeen
	}
	needs := 0
	hasDanger := false
	for _, n := range notifs {
		// Storage's count is a percentage, not a number of things to clear:
		// adding it made a disk at 80% read as eighty notifications.
		if n.Kind == "storage" {
			needs++
		} else {
			needs += n.Count
		}
		if n.Severity == "danger" {
			hasDanger = true
		}
	}
	unread := 0
	for _, e := range recent {
		if e.At.After(seen) {
			unread++
		}
	}
	total := needs + unread
	badge, activeCls, markAll := "", "", ""
	if total > 0 {
		// The badge wears its worst severity: ten failed jobs must read as an
		// alarm, not as three pending comments.
		badgeCls := ""
		if hasDanger {
			badgeCls = " topbar-notif__badge--danger"
		}
		badge = `<span class="topbar-notif__badge` + badgeCls + `" data-notif-badge>` + notifCap(total) + `</span>`
		activeCls = " topbar-notif__btn--active"
	}
	if unread > 0 {
		markAll = `<button type="button" class="btn btn--ghost btn--xs" data-notif-seen>Mark all read</button>`
	}
	var list strings.Builder
	if len(notifs) > 0 {
		list.WriteString(`<div class="notif-group" role="group" aria-labelledby="notif-needs"><div class="notif-group__label" id="notif-needs">Needs action</div>`)
		for _, n := range notifs {
			list.WriteString(osNotifItem(n))
		}
		list.WriteString(`</div>`)
	}
	if len(recent) > 0 {
		list.WriteString(`<div class="notif-group" role="group" aria-labelledby="notif-recent" data-notif-recent><div class="notif-group__label" id="notif-recent">Last 24 hours</div>`)
		for _, e := range recent {
			list.WriteString(osRecentItem(e, e.At.After(seen)))
		}
		list.WriteString(`</div>`)
	}
	if list.Len() == 0 {
		list.WriteString(`<div class="topbar-notif__empty">Nothing needs you, and nothing has happened in the last day.</div>`)
	}
	return `<div class="topbar-notif" data-notif data-notif-needs="` + strconv.Itoa(needs) + `">
  <button type="button" class="btn--icon topbar-notif__btn` + activeCls + `" data-notif-toggle aria-haspopup="true" aria-expanded="false" aria-label="Notifications">
    ` + iconBell + badge + `
  </button>
  <div class="topbar-notif__panel" data-notif-panel hidden>
    <div class="topbar-notif__head"><span>Notifications</span>` + markAll + `</div>
    <div class="topbar-notif__list">` + list.String() + `</div>
  </div>
</div>`
}

// svgIcon returns a minimal inline SVG for the sidebar.
// Using path data keeps us CDN-free and avoids an extra HTTP round-trip.
func svgIcon(path string) string {
	return `<svg viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true"><path d="` + path + `" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`
}

// World-switch glyphs shown inside the sidebar Clearnet⟷Tor switch: a globe for
// the public Clearnet world and a layered onion (the Tor mark) for the anonymous
// world, so each segment is recognisable at a glance. currentColor lets the
// stylesheet tint each in its own world accent.
const iconWorldClearnet = `<svg class="space-switch__ico" viewBox="0 0 16 16" width="14" height="14" fill="none" aria-hidden="true"><circle cx="8" cy="8" r="6.1" stroke="currentColor" stroke-width="1.2"/><path d="M1.9 8h12.2M8 1.9v12.2M8 1.9c-2.3 1.7-2.3 10.5 0 12.2M8 1.9c2.3 1.7 2.3 10.5 0 12.2" stroke="currentColor" stroke-width="1.1"/></svg>`
const iconWorldTor = `<svg class="space-switch__ico" viewBox="0 0 16 16" width="14" height="14" fill="none" aria-hidden="true"><path d="M8 1.7c-.8.9-.8 1.8 0 2.7" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/><path d="M8 4c-2.9 0-4.9 2.5-4.9 5.3 0 2.6 2.2 4.9 4.9 4.9s4.9-2.3 4.9-4.9C12.9 6.5 10.9 4 8 4z" stroke="currentColor" stroke-width="1.2"/><path d="M8 4.3c-1.3 1.4-2 3.2-2 5s.7 3.4 2 4.8M8 4.3c1.3 1.4 2 3.2 2 5s-.7 3.4-2 4.8" stroke="currentColor" stroke-width="1"/></svg>`

var (
	iconDomains = svgIcon("M10 2a8 8 0 100 16 8 8 0 000-16zM2 10h16M10 2c2.2 2 3.3 4.9 3.3 8s-1.1 6-3.3 8c-2.2-2-3.3-4.9-3.3-8s1.1-6 3.3-8z")
	iconBell    = svgIcon("M10 3a4 4 0 00-4 4c0 4-2 5-2 5h12s-2-1-2-5a4 4 0 00-4-4zm-1.5 13a1.5 1.5 0 003 0")
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
		adminOSShellFoot(nonce, "", pageUsesAlpine(string(bodyHTML)), pageUsesPurify(string(bodyHTML)))
}

// pageUsesAlpine reports whether a rendered admin page body hosts an Alpine
// island (an x-data component), so adminOSShellFoot loads the Alpine runtime
// only where it is actually used (ADR-0136). Any page that gains an island is
// covered automatically — no per-page bookkeeping to drift out of sync.
func pageUsesAlpine(body string) bool { return strings.Contains(body, "x-data") }

// pageUsesPurify reports whether a rendered admin page body is a block-editor
// page (the canvas marker only the editor renders), so adminOSShellFoot loads
// DOMPurify — which only the editor's client code uses — on exactly those pages.
func pageUsesPurify(body string) bool { return strings.Contains(body, "data-editor-canvas") }

// adminOSShellHead emits the VayuOS document head, sidebar, topbar and the
// opening <main class="content"> tag. The caller appends body content and then
// adminOSShellFoot.
// vpConfirm is the shared CSP-safe confirmation dialog (Wave 3.11): a real
// focus-aware modal built with createElement and textContent only — the
// browser-native confirm() is an unstyled chrome blob that cannot say what will
// happen and blocks the whole tab. Defined in the foot's bootstrap script so
// the per-page operator scripts (which run before admin-os.js) can use it too.
// message/labels must be plain text; they are set via textContent.
const vpConfirmScript = `window.vpConfirm=function(opts,onYes){
  var lastFocus=document.activeElement;
  var backdrop=document.createElement('div');backdrop.className='vp-confirm-backdrop';
  var box=document.createElement('div');box.className='vp-confirm';box.setAttribute('role','alertdialog');box.setAttribute('aria-modal','true');
  var t=document.createElement('div');t.className='vp-confirm__title';t.textContent=opts.title||'Are you sure?';
  var m=document.createElement('div');m.className='vp-confirm__msg';if(opts.message)m.textContent=opts.message;
  var row=document.createElement('div');row.className='vp-confirm__row';
  var cancel=document.createElement('button');cancel.type='button';cancel.className='btn btn--ghost btn--sm';cancel.textContent=opts.cancel||'Cancel';
  var ok=document.createElement('button');ok.type='button';ok.className='btn btn--danger btn--sm';ok.textContent=opts.confirm||'Confirm';
  row.appendChild(cancel);row.appendChild(ok);
  box.appendChild(t);if(opts.message)box.appendChild(m);box.appendChild(row);backdrop.appendChild(box);
  function close(){if(backdrop.parentNode)backdrop.parentNode.removeChild(backdrop);document.removeEventListener('keydown',onKey);if(lastFocus&&lastFocus.focus)lastFocus.focus();}
  function onKey(e){if(e.key==='Escape'){e.preventDefault();close();}else if(e.key==='Tab'){var f=[ok,cancel];var i=f.indexOf(document.activeElement);e.preventDefault();f[(i+1)%2].focus();}}
  cancel.addEventListener('click',close);
  backdrop.addEventListener('click',function(e){if(e.target===backdrop)close();});
  ok.addEventListener('click',function(){close();if(onYes)onYes();});
  document.addEventListener('keydown',onKey);
  document.body.appendChild(backdrop);
  ok.focus();
};`

// vpIconScript gives scripts the server's icon set: an <svg> that uses the
// page's sprite (saSprite), so a row or chip built in the browser carries the
// same icon as one rendered in Go. Both shells define it beside the sprite, at
// the top of the body, because some page scripts (the editor's) build their
// toolbars before the foot's bootstrap has run.
const vpIconScript = `window.vpIcon=function(name){
  var ns='http://www.w3.org/2000/svg';
  var svg=document.createElementNS(ns,'svg');svg.setAttribute('class','sa-ico');svg.setAttribute('viewBox','0 0 20 20');svg.setAttribute('aria-hidden','true');
  var use=document.createElementNS(ns,'use');use.setAttribute('href','#sa-i-'+name);svg.appendChild(use);
  return svg;
};`

// vpPromptScript is the shared single-field dialog: window.prompt's job, in the
// console's own clothes.
//
// A native prompt is unstyled chrome that blocks the tab, cannot be themed, and
// is forbidden by the same house rule that banned confirm() — but replacing it
// needs a real dialog, so this exists rather than each app rolling its own.
// Labels and values are plain text (set via textContent); the callback receives
// the trimmed value, or null when the user cancels.
const vpPromptScript = `window.vpPrompt=function(opts,onDone){
  var lastFocus=document.activeElement;
  var backdrop=document.createElement('div');backdrop.className='vp-confirm-backdrop';
  var box=document.createElement('div');box.className='vp-confirm';box.setAttribute('role','dialog');box.setAttribute('aria-modal','true');
  var t=document.createElement('div');t.className='vp-confirm__title';t.textContent=opts.title||'Enter a value';
  box.appendChild(t);
  if(opts.message){var m=document.createElement('div');m.className='vp-confirm__msg';m.textContent=opts.message;box.appendChild(m);}
  var lab=document.createElement('label');lab.className='vp-confirm__label';lab.textContent=opts.label||'Value';
  var inp=document.createElement('input');inp.className='input';inp.type=opts.type||'text';
  if(opts.autocomplete)inp.setAttribute('autocomplete',opts.autocomplete);
  if(opts.placeholder)inp.placeholder=opts.placeholder;
  inp.value=opts.value||'';
  lab.setAttribute('for','vp-prompt-input');inp.id='vp-prompt-input';
  lab.appendChild(inp);box.appendChild(lab);
  var row=document.createElement('div');row.className='vp-confirm__row';
  var cancel=document.createElement('button');cancel.type='button';cancel.className='btn btn--ghost btn--sm';cancel.textContent=opts.cancel||'Cancel';
  var ok=document.createElement('button');ok.type='button';ok.className='btn btn--primary btn--sm';ok.textContent=opts.confirm||'Save';
  row.appendChild(cancel);row.appendChild(ok);box.appendChild(row);backdrop.appendChild(box);
  function finish(v){if(backdrop.parentNode)backdrop.parentNode.removeChild(backdrop);document.removeEventListener('keydown',onKey);if(lastFocus&&lastFocus.focus)lastFocus.focus();if(onDone)onDone(v);}
  function close(){finish(null);}
  function submit(){finish((inp.value||'').trim());}
  function onKey(e){if(e.key==='Escape'){e.preventDefault();close();}else if(e.key==='Tab'){var f=[inp,ok,cancel];var i=f.indexOf(document.activeElement);e.preventDefault();f[(i+1)%3].focus();}}
  cancel.addEventListener('click',close);
  backdrop.addEventListener('click',function(e){if(e.target===backdrop)close();});
  ok.addEventListener('click',submit);
  inp.addEventListener('keydown',function(e){if(e.key==='Enter'){e.preventDefault();submit();}});
  document.addEventListener('keydown',onKey);
  document.body.appendChild(backdrop);
  inp.focus();inp.select();
};`

// osThemeColorMetas renders the browser-chrome theme-colour meta for the
// console's resolved theme (Wave 2.8 login polish). A fixed dark value lied to
// light-theme and auto operators: the mobile browser chrome stayed near-black
// over a near-white console. A "light"/"dark" theme renders its one honest
// value; "auto" (the default) genuinely follows the OS, so it renders BOTH
// media-scoped values and lets the browser pick per system preference.
func osThemeColorMetas(theme string) string {
	dark, light := "#080e1a", "#f8fafc"
	switch theme {
	case "light":
		return `<meta name="theme-color" content="` + light + `">`
	case "dark":
		return `<meta name="theme-color" content="` + dark + `">`
	default: // "auto" — the OS decides, so both are declared
		return `<meta name="theme-color" media="(prefers-color-scheme: dark)" content="` + dark + `">` +
			`<meta name="theme-color" media="(prefers-color-scheme: light)" content="` + light + `">`
	}
}

func adminOSShellHead(nonce, title, active string, settings *osSettings) string {
	return stillAirShellHead(nonce, title, active, settings)
}

// adminOSShellFoot closes the content/main/shell, renders the mobile bottom nav,
// command palette and toast container, then the nonce-gated bootstrap script.
// When pageScript is non-empty it is emitted as an additional nonce-gated inline
// script alongside the shared operator-control helpers (csrf/vpPost/show) and a
// live status region, so streaming operator pages keep their POST controls.
//
// needsPurify (variadic, adminOSLayout only) scopes DOMPurify (Wave 3.3): the
// 21 KB library is used exclusively by the block editor (admin-os-editor.js),
// so every non-editor console page was shipping it for nothing. Editor pages
// are detected from the body markup via pageUsesPurify.
func adminOSShellFoot(nonce, pageScript string, needsAlpine bool, needsPurify ...bool) string {
	// Alpine (ADR-0136) is loaded ONLY on pages that actually host an island
	// (an x-data component). Emitting the 61 KB CSP build + its document-wide
	// MutationObserver on every admin page would tax parse time and every HTMX
	// swap for zero benefit — Alpine is the exception, not the rule. Island-free
	// pages (the overwhelming majority) stay fully Alpine-free.
	alpine := ""
	if needsAlpine {
		alpine = `<!-- Alpine.js islands (ADR-0136): the eval-free CSP build + the VayuOS island
     registry. Self-hosted, deferred, same-origin so they satisfy script-src
     'self' with no dynamic code evaluation. The registry loads FIRST so its
     alpine:init listener is armed before Alpine starts; components are
     referenced by name in x-data, never as inline expressions. HTMX stays the
     backbone — Alpine only powers isolated client-reactive islands, and every
     island degrades to plain HTML. Loaded only when this page hosts one. -->
<script src="/os/static/js/vayu-islands.js?v=` + assetVer("js/vayu-islands.js") + `" defer></script>
<script src="/os/static/js/alpine-csp.min.js?v=` + assetVer("js/alpine-csp.min.js") + `" defer></script>
`
	}
	// DOMPurify (Wave 3.3): only the block editor sanitises client-side, so only
	// the editor ships the library. Non-editor pages stop paying the download
	// and parse cost entirely.
	purifyTag := ""
	if len(needsPurify) > 0 && needsPurify[0] {
		purifyTag = `<script src="/os/static/js/purify.min.js"></script>`
	}
	ops := ""
	if pageScript != "" {
		// A page script uses admin-os.js's vpPost(url, body, onok, onerr). The shell
		// used to define a second vpPost(url, onok) here; admin-os.js loads later
		// and replaced it, so every page written against this one got no
		// confirmation and no refresh after its action succeeded.
		ops = `<script nonce="` + nonce + `">
(function(){'use strict';
` + pageScript + `
})();
</script>`
	}
	return `  </main>
</div><!-- .main -->
</div><!-- .shell -->
` + ops + `
<!-- Command palette -->
<div id="cmd-backdrop" class="cmd-backdrop" hidden role="dialog" aria-modal="true" aria-label="Command palette">
  <div class="cmd-panel">
    <div class="cmd-input-wrap">
      <svg class="cmd-search-icon" viewBox="0 0 20 20" fill="none" width="18" height="18" aria-hidden="true">
        <path d="M8 15A7 7 0 108 1a7 7 0 000 14zm5-1l4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
      </svg>
      <input id="cmd-input" class="cmd-input" type="text" placeholder="Search or run a command" autocomplete="off" aria-label="Search or run a command" aria-controls="cmd-results">
      <button type="button" class="cmd-kind" data-cmd-kind aria-live="polite" aria-label="Filter by kind">All</button>
    </div>
    <div class="cmd-body">
      <div id="cmd-results" class="cmd-results" role="listbox" aria-label="Results"></div>
      <aside class="cmd-preview" data-cmd-preview aria-label="Preview"></aside>
    </div>
    <div class="cmd-footer">
      <span class="cmd-footer-hint"><kbd>↑</kbd><kbd>↓</kbd> move</span>
      <span class="cmd-footer-hint"><kbd>↵</kbd> open or run</span>
      <span class="cmd-footer-hint"><kbd>tab</kbd> filter by kind</span>
      <span class="cmd-footer-hint cmd-footer-hint--end"><kbd>esc</kbd> close</span>
    </div>
  </div>
</div>

<!-- Toast container -->
<div class="toast-container" aria-live="polite" aria-atomic="true"></div>

<!-- Screen-reader announce region for HTMX outcomes (WCAG 2.2 AA). Visually
     hidden; the nonce-gated glue script updates it after each hx-* request. -->
<div id="vp-live" class="vp-sr-only" role="status" aria-live="polite" aria-atomic="true"></div>

<!-- Self-hosted HTMX (static/js/htmx.min.js) — embedded in the binary, served
     same-origin so it satisfies script-src 'self' with no nonce and no external
     host. Deferred so hx-* attributes are wired after the document parses. -->
<script src="/static/js/htmx.min.js?v=` + assetVer("js/htmx.min.js") + `" defer></script>
<!-- HTMX glue (nonce-gated → CSP-safe):
     1. CSRF — mirror the double-submit vp_csrf cookie into the X-CSRF-Token
        header on every hx-* mutating request, so admin HTMX POST/DELETE pass the
        same CSRFTokenMiddleware the fetch() controls already use.
     2. Failure feedback — surface any HTTP or network error from an hx-* request
        as a toast, so a failed publish/pin/moderate never fails silently. -->
<script nonce="` + nonce + `">
(function(){var b=document.body;if(!b)return;
b.addEventListener('htmx:configRequest',function(e){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);if(m)e.detail.headers['X-CSRF-Token']=m[1];});
function vpHtmxFail(){if(window.vpToast)window.vpToast('Action failed — please try again.','error');}
b.addEventListener('htmx:responseError',vpHtmxFail);
b.addEventListener('htmx:sendError',vpHtmxFail);
b.addEventListener('htmx:afterRequest',function(e){var d=e.detail;if(!d||!d.successful)return;var l=document.getElementById('vp-live');if(!l)return;var v=(d.requestConfig&&(d.requestConfig.verb||'')).toLowerCase();l.textContent='';l.textContent=(v==='get'?'Content refreshed.':'Change saved.');});
// One-click world switch (ADR-0141): the switch lives on every admin page but the
// vp_csrf cookie is only issued by CSRF-wrapped GETs, so prime it with a GET of
// the (admin-only, side-effect-free) Spaces page, then POST the toggle with the
// fresh token. Reloads on success to pick up the new world + its colour.
function vpSpaceStatus(txt){var st=document.querySelector('[data-space-status]');if(st){st.hidden=false;st.textContent=txt;}}
Array.prototype.forEach.call(document.querySelectorAll('[data-space-switch]'),function(seg){
  seg.addEventListener('click',function(){
    if(seg.classList.contains('is-active')||seg.disabled)return;
    var on=seg.getAttribute('data-space-switch')==='on';
    var sw=seg.parentNode;if(sw)sw.classList.add('is-busy');
    vpSpaceStatus(on?'Starting your anonymous world…':'Switching to Clearnet…'); // instant feedback
    if(!on){location.href='/os/world?target=clearnet';return;} // leave: just drop the view
    // Enter Tor: make sure the anonymous world is on, then step INTO it.
    fetch('/os/spaces',{credentials:'same-origin'}).then(function(){
      var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);
      return fetch('/os/spaces/toggle?enable=1',{method:'POST',credentials:'same-origin',headers:{'X-CSRF-Token':m?m[1]:''}});
    }).then(function(r){
      if(r.ok){location.href='/os/world?target=tor';return;} // enter the Tor world console
      if(sw)sw.classList.remove('is-busy');
      // Surface the SERVER's actual reason (admin required, settings unavailable,
      // write failed…) instead of always blaming permissions — so a real failure
      // is diagnosable on mobile where there is no console to inspect.
      r.json().then(function(d){
        var msg=(d&&(d.detail||d.title))||'Could not start the anonymous world.';
        vpSpaceStatus(msg);if(window.vpToast)window.vpToast(msg,'error');
      }).catch(function(){
        var m='Could not start the anonymous world (HTTP '+r.status+'). Please try again.';
        vpSpaceStatus(m);if(window.vpToast)window.vpToast(m,'error');
      });
    }).catch(function(){if(sw)sw.classList.remove('is-busy');vpSpaceStatus('Could not switch — please try again.');if(window.vpToast)window.vpToast('Could not switch world — please try again.','error');});
  });
});
// Copy buttons for the Tor .onion address — in the sidebar and on the dashboard
// world card. Scoped so it never double-binds with the VayuTor page's own island.
Array.prototype.forEach.call(document.querySelectorAll('.sidebar [data-copy], .world-card [data-copy]'),function(btn){
  btn.addEventListener('click',function(){
    var v=btn.getAttribute('data-copy')||'',p=btn.textContent;
    var done=function(){btn.textContent='copied';setTimeout(function(){btn.textContent=p;},1400);};
    // A rejected write — or no Clipboard API at all, which is the normal case on a
    // plain-http .onion console — must not report success. Claiming "copied" when
    // nothing reached the clipboard is how someone pastes an empty line later.
    var fail=function(){btn.textContent='copy failed — select it';setTimeout(function(){btn.textContent=p;},2600);};
    if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(v).then(done,function(){fail();});}else{fail();}
  });
});
})();
` + vpConfirmScript + vpPromptScript + `
</script>
` + alpine + `<!-- Bootstrap (nonce-gated, reads data-admin-theme from body) -->
` + purifyTag + `
<script nonce="` + nonce + `" src="/os/static/js/admin-os.js?v=` + assetVer("js/admin-os.js") + `"></script>
</body></html>`
}

// osSettings holds the subset of site settings needed to render every page.
type osSettings struct {
	SiteName   string
	AdminTheme string
	// Route is the matched route pattern (chi), which the Still Air shell uses to
	// find the current app section without every page naming it.
	Route string
	// Mode and ModeSince drive the Still Air status area and state strip.
	Mode      mode.Mode
	ModeSince time.Time
	// UnreadMail is the mail count already gathered for the bell; the Still Air
	// rail shows it on Mail rather than counting again.
	UnreadMail int
	// MailDNSAttention marks the Mail app's DNS section (the stored verdict,
	// never a live lookup).
	MailDNSAttention bool
	// Signed-in user, surfaced in the sidebar footer card.
	UserID     string
	UserName   string
	UserRole   string
	UserAvatar string
	// MailOnly / AccessLevel drive role-scoped sidebar visibility and match the
	// route guard in requireSessionOrAPIKey (hidden == unreachable).
	MailOnly    bool
	AccessLevel int
	// MailReadOnly: a mail-only session whose mailbox holds the read-only role;
	// the rail leaves out Compose, which the send path would refuse.
	MailReadOnly bool
	// UnreadMessages drives the sidebar badge on the Messages item.
	UnreadMessages int
	// TorSpaceOn: the Anonymous Tor Space is enabled — the shell wears the Tor
	// (purple) palette so the operator always knows the anonymous world is live.
	TorSpaceOn bool
	// TorSpaceRunning / TorSpaceOnion back the inline status shown under the sidebar
	// world switch, so the operator sees whether the anonymous world is live and its
	// .onion address without opening a separate page.
	TorSpaceRunning bool
	TorSpaceOnion   string
	// Notifications backs the topbar notification centre (the bell that replaced the
	// New Post shortcut): every actionable signal in the system — new contact mail,
	// comments to moderate, mail devices awaiting approval, domains waiting to sync —
	// each linking straight to the page that clears it. Gated per-item to the
	// viewer's access level, so an item never points at a page they cannot open.
	Notifications []osNotification
	// Recent is the bell's second half: what happened in the last day, with
	// when (render 09). RecentSeen is when this viewer last marked them read.
	Recent     []osRecentEvent
	RecentSeen time.Time
	// Sites are the hosted sites whose consoles this session may open, for
	// the system bar's switcher; Scope is the one this page belongs to, nil
	// on the install's own console.
	Sites []osSite
	Scope *osSite
}

// osSite is one hosted site in the switcher.
type osSite struct {
	ID, Host string
	Active   bool
}

// osNotification is one actionable item in the topbar notification centre: a
// short title, a human detail, the count driving its badge, a target page, and a
// kind slug that selects both its icon and its accent colour (a fixed literal, so
// it is safe to interpolate straight into the class name).
type osNotification struct {
	Title  string
	Detail string
	Href   string
	Count  int
	Kind   string // "mail" | "comment" | "message" | "domain" | "update" | "backup" | "jobs" | "storage" | "mode"
	// Severity (Wave 2.2): "" / "info" is a todo, "warn" needs attention soon,
	// "danger" is something failing NOW. Drives the badge colour on the bell and
	// the dashboard attention strip, so "3 pending comments" never screams the
	// way "10 failed jobs" must.
	Severity string
	// State marks a notice that is a condition rather than a tally (an update
	// ready, a mail domain half set up, backups unproven): its detail is a whole
	// statement and reads without a count in front of it.
	State bool
}

// getOSSettings loads settings needed for layout rendering.
func (a *App) getOSSettings(ctx context.Context) *osSettings {
	s := &osSettings{}
	if a.siteSettings != nil {
		s.SiteName = a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeySiteName)
		s.AdminTheme = a.siteSettings.Get(ctx, settings.ForPrimary(), "admin.theme")
		// Anonymous Tor Space on ⇒ shift the whole VayuOS chrome to the Tor palette.
		s.TorSpaceOn = a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyTorSpaceEnabled) == "on"
	}
	// Live Tor-Space status for the inline panel under the sidebar switch (nil-safe;
	// zero values in a Tor-Space child, which has no supervisor).
	if s.TorSpaceOn {
		st := a.torSpaceStatusNow()
		s.TorSpaceRunning = st.Running
		s.TorSpaceOnion = st.Onion
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
				s.MailReadOnly = a.vayuMail != nil && u.MailAddress != "" && a.vayuMail.MailboxReadOnly(u.MailAddress)
			}
			s.AccessLevel = accessLevelFor(u.Role, s.MailOnly)
		}
	}
	// Notification centre (topbar bell): computed last, once the access level is
	// known, so each item can be gated to what the viewer can actually open.
	s.Notifications = a.osNotifications(ctx, s)
	s.Recent = a.osRecentEvents(ctx, s, time.Now().UTC())
	s.RecentSeen = notifSeenAt(ctx, s.UserID)
	for _, n := range s.Notifications {
		if n.Kind == "mail" {
			s.UnreadMail += n.Count
		}
	}
	if rc := chi.RouteContext(ctx); rc != nil {
		s.Route = resolvedRoute(rc)
	}
	a.osSitesFor(ctx, s)
	s.Mode = mode.Global.Current()
	if h := mode.Global.History(); len(h) > 0 {
		s.ModeSince = h[len(h)-1].OccurredAt
	}
	_, s.MailDNSAttention = a.mailDNSNeedsAttention()
	return s
}

// osNotifications aggregates the actionable signals shown in the topbar bell.
// Every source is a cheap, best-effort read (a nil DB, a missing table on an
// older schema, or a query error simply drops that item — the console never
// fails to render because a count could not be taken). Each item is gated to the
// viewer's access level via osPathMinLevel(href), so it never advertises a page
// the viewer cannot open.
func (a *App) osNotifications(ctx context.Context, s *osSettings) []osNotification {
	var out []osNotification
	add := func(href, title, detail, kind string, count int, severity ...string) {
		if count <= 0 || s.AccessLevel < osPathMinLevel(href) {
			return
		}
		sev := ""
		if len(severity) > 0 {
			sev = severity[0]
		}
		out = append(out, osNotification{Title: title, Detail: detail, Href: href, Count: count, Kind: kind, Severity: sev})
	}
	state := func(href, title, detail, kind, severity string) {
		n := len(out)
		add(href, title, detail, kind, 1, severity)
		if len(out) > n {
			out[n].State = true
		}
	}
	// A mail domain whose DNS is not finished, from the last stored check —
	// never a lookup here (vayuos_mail_dns_watch.go). The DNS tab is
	// administrator-only while /os/vayumail is open to authors, so the path gate
	// in add() is not enough on its own.
	if next, bad := a.mailDNSNeedsAttention(); bad && s.AccessLevel >= accessAdmin {
		state("/os/vayumail/dns", "Mail domain needs attention", next, "mail", "warn")
	}
	// Backups that are not proven. This verdict used to sit on the Operations
	// hub; Home and the bell are where an operator looks without being sent, and
	// a broken recovery path is otherwise silent until the day it is needed.
	if n, ok := backupNotification(a.vayuKeepStatus(), a.vayuKeepErr, time.Now().UTC()); ok {
		state(n.Href, n.Title, n.Detail, n.Kind, n.Severity)
	}
	if dbpkg.DB == nil {
		return out
	}
	rdb := dbpkg.Reader()
	// Unread contact-form mail (already counted for the sidebar badge).
	add("/os/messages", "New messages", "unread in your inbox", "message", s.UnreadMessages)
	// Comments awaiting moderation.
	pendingComments := 0
	_ = rdb.QueryRowContext(ctx, `SELECT COUNT(1) FROM comments WHERE status='pending'`).Scan(&pendingComments)
	add("/os/comments", "Comments to review", "awaiting moderation", "comment", pendingComments)
	// New mail waiting in the viewer's mailboxes — the count that also raises the
	// live desktop notification (admin-os.js). Cheap readdir-only counts.
	if unseen, href := a.mailUnseenForViewer(ctx, s); unseen > 0 {
		noun := "unread in your mailbox"
		if href == "/os/vayumail/inbox" {
			noun = "unread across your mailboxes"
		}
		add(href, "New mail", noun, "mail", unseen)
	}
	// Mail devices waiting for approval to sync a mailbox (VayuMail direct-connect).
	if a.vayuMail != nil {
		pendingDevices := 0
		_ = rdb.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_app_passwords WHERE device_id IS NOT NULL AND device_id <> '' AND status='pending'`).Scan(&pendingDevices)
		add("/os/vayumail", "Mail devices", "waiting for approval", "mail", pendingDevices)
	}
	// Secondary domains registered but still on manual hold (not yet approved to
	// provision). Pending Tor sites (placeholder host, minting their .onion) are
	// excluded — there is nothing to approve until their address lands.
	if a.domains != nil {
		if list, err := a.domains.List(ctx); err == nil {
			held := 0
			for _, d := range list {
				if !d.IsPrimary && !d.IsSyncApproved() && !isPendingTorSite(d.Host) {
					held++
				}
			}
			add("/os/domains", "Domains to sync", "waiting for approval", "domain", held)
			// Sites publishing a template's sample content as if it were the
			// real business (vayupress.johal.in served Bistro's "Maison Olive").
			// A site marked as a demo on its Website page is not counted.
			sample, href, detail := 0, "/os/domains", ""
			for _, d := range list {
				if d.IsPrimary || d.Status != domain.StatusActive {
					continue
				}
				if _, warn := a.sampleWarned(ctx, d); warn {
					sample++
					href, detail = "/os/d/"+d.ID+"/website", d.Host+" shows a design's sample business — replace it, or mark it a demo"
				}
			}
			if sample > 1 {
				href, detail = "/os/domains", "sites show a design's sample business as if it were real"
			}
			add(href, "Sample content is live", detail, "domain", sample, "warn")
			// Sites whose live pages now have a dead end in them: a picture
			// deleted from Media, a post a button linked to taken down.
			broken, bhref := 0, "/os/domains"
			for _, d := range list {
				if d.Status == domain.StatusActive && errorCount(a.hostedSiteChecks(ctx, d)) > 0 {
					broken++
					bhref = "/os/d/" + d.ID + "/website/editor"
				}
			}
			if doc, ok := a.livePrimaryDocument(ctx); ok && errorCount(a.checkSite(ctx, "", doc)) > 0 {
				broken++
				bhref = "/os/website/editor"
			}
			if broken > 1 {
				bhref = "/os/domains"
			}
			add(bhref, "Website problems", "a live site has a dead link or a missing picture", "domain", broken, "danger")
		}
	}
	// A newer signed VayuPress release is ready to install (read from the cached
	// update-check history, refreshed by the background watcher). Admin-only via
	// the /os/update gate in add(); silent in a Tor Space.
	if v, ok := a.latestUpdateNotice(ctx); ok {
		state("/os/update", "VayuOS update ready", "Install "+v+" in one click", "update", "")
	}
	// Wave 2.2 — the operational signals the bell used to ignore. All three come
	// from the metrics snapshot (an atomic load; collectAdminMetrics is already
	// running on a 30s ticker), so the bell costs nothing extra.
	if snap := a.getAdminSnapshot(); snap != nil {
		// Failed render/write jobs: the public site is silently going stale.
		// One failure is "look soon"; a pile is "failing now".
		if snap.FailedJobs > 0 {
			sev := "warn"
			if snap.FailedJobs >= 10 {
				sev = "danger"
			}
			add("/os/monitoring", "Failed jobs", "render/write jobs failed — the site may be serving stale content", "jobs", snap.FailedJobs, sev)
		}
		// Disk pressure: quota consumption from the same snapshot. At 90% the
		// next upload or backup can start failing — that is danger, not todo.
		if snap.StoragePct >= 75 {
			sev := "warn"
			if snap.StoragePct >= 90 {
				sev = "danger"
			}
			add("/os/storage", "Storage filling up", "of your storage quota is in use", "storage", int(snap.StoragePct), sev)
		}
		// Maintenance mode on: deliberate, but visible — an install parked in
		// maintenance "for a moment" three days ago deserves a bell entry.
		if config.Cfg.MaintenanceMode {
			state("/os/modes", "Maintenance mode on", "The public site is offline behind a maintenance page", "mode", "info")
		}
	}
	return out
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

// writeOSHTML writes HTML with the standard os response headers and CSRF cookie.
func writeOSHTML(w http.ResponseWriter, r *http.Request, body string) {
	// Reuse the browser's existing token rather than rotating on every render —
	// rotating is what made a second console tab's form 403 on submit.
	csrfTokenFor(w, r)
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

// setAuthPageNoCache marks an auth page (login, change-password) uncacheable by
// the browser AND every proxy/CDN.
//
// This is load-bearing, not defensive boilerplate: the login page is written
// directly (it bypasses writeOSHTML, which is where the dashboard gets its
// no-store). Without an explicit Cache-Control the browser is free to
// heuristically cache the rendered 200 form and serve it on a LATER visit
// WITHOUT hitting the server — so the "already signed in → redirect to /os"
// check below never runs, and a logged-in operator keeps seeing the login page
// while /os (always no-store) correctly shows the dashboard. That exact
// asymmetry is the reported bug. no-store forbids all caching/reuse, so the
// session-aware redirect runs on every /os/login navigation.
func setAuthPageNoCache(w http.ResponseWriter) {
	// "private" and Vary: Cookie are what stop a SHARED cache (a CDN or reverse
	// proxy in front of the origin) from storing this response and later handing it
	// to somebody else. Without them a proxy can serve a stored sign-in form to a
	// visitor who already holds a valid session — which looks exactly like being
	// logged out, right up until you edit the URL and land inside, still signed in.
	// no-store alone is not reliably honoured by every edge, so state it three ways:
	// the standard directives, the explicit private/Vary contract, and the
	// CDN-specific override that takes precedence at the edge.
	w.Header().Set("Cache-Control", "private, no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Vary", "Cookie")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func (a *App) handleOSLogin(w http.ResponseWriter, r *http.Request) {
	// no-store BEFORE the branch so both the redirect and the form response are
	// uncacheable — otherwise a cached form silently defeats the redirect.
	setAuthPageNoCache(w)
	// Already signed in? Opening /os/login with a live session must land on the
	// dashboard, not re-prompt for credentials — the seamless posture the operator
	// expects whether they typed /os or /os/login.
	next := r.URL.Query().Get("next")
	if !isLocalURL(next) {
		next = ""
	}
	if a.hasValidConsoleSession(r) {
		// Redirect the guarded value directly inside `if isLocalURL(next)` so the
		// static analyser sees `next` neutralised on this branch (barrier guard).
		if isLocalURL(next) {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, osHome, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(osLoginPage("", "", next)))
}

// isLocalURL reports whether s is a safe SAME-ORIGIN path. It must parse as a
// purely relative reference — no scheme, no host/authority, no userinfo, no
// opaque part — that is site-rooted ("/…"), and must not be protocol-relative
// ("//host"), a backslash trick ("/\host"), or carry control characters or an
// embedded scheme. Anything else is rejected, so a post-login "next" bounce can
// never leave this site.
//
// The name deliberately matches CodeQL's redirect-check barrier-guard heuristic
// (isLocalUrl / isValidRedirect / …): used as `if isLocalURL(v) { redirect(v) }`
// the analyser treats v as a neutralised, safe-to-redirect value on the true
// branch. A prior version returned a normalised string, which the analyser never
// recognised as a sanitizer — a boolean guard is what the query actually models.
func isLocalURL(s string) bool {
	if s == "" || len(s) > 512 || strings.Contains(s, "://") {
		return false
	}
	// Some browsers treat "\" as "/", so a "/\evil.com" could become
	// protocol-relative; reject backslashes and control characters outright.
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.IsAbs() || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return false
	}
	return strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(u.Path, "//")
}

func (a *App) handleOSLoginSubmit(w http.ResponseWriter, r *http.Request) {
	setAuthPageNoCache(w)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := r.FormValue("next")
	if !isLocalURL(next) {
		next = ""
	}
	loginDest := osHome
	if isLocalURL(next) {
		loginDest = next
	}
	email := strings.TrimSpace(r.FormValue("email"))
	pass := r.FormValue("password")
	if email == "" || pass == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osLoginPage(email, "Email and password are required.", next)))
		return
	}
	if a.userStore == nil || a.sessions == nil {
		http.Error(w, "accounts not initialised", http.StatusServiceUnavailable)
		return
	}
	// "Remember me": checked (default) keeps a persistent cookie across browser
	// restarts; unchecked issues a browser-session cookie that is dropped on close,
	// so the operator is signed out and must log in again on the next visit.
	remember := r.FormValue("remember") != ""
	// Brute-force guard — shared lockout state with the v2 surface and the
	// API-key path so attempts cannot be split across surfaces.
	ip := loginClientIP(r)
	if locked, until := auth.CheckAuthLockout(ip); locked {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osLoginPage(email, loginLockoutMessage(until), next)))
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
			auth.SetSessionCookieRemember(w, token, remember)
			http.Redirect(w, r, loginDest, http.StatusSeeOther)
			return
		} else if totpMissing {
			auth.RecordAuthFailure(ip)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(osLoginPage(email, "Enter the 6-digit code from your authenticator app, then re-enter your password.", next)))
			return
		}
		auth.RecordAuthFailure(ip)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osLoginPage(email, "Invalid email or password.", next)))
		return
	}
	// Second factor: if the account has 2FA enabled, a valid TOTP code is required.
	// On failure the password must be re-entered (it is never echoed back).
	if ok, required := a.verifyTOTPForLogin(r.Context(), email, r.FormValue("totp")); required && !ok {
		auth.RecordAuthFailure(ip)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osLoginPage(email, "Enter the 6-digit code from your authenticator app, then re-enter your password.", next)))
		return
	}
	token, err := a.sessions.Create(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "could not start session", http.StatusInternalServerError)
		return
	}
	auth.RecordAuthSuccess(ip)
	a.userStore.TouchLastLogin(r.Context(), u.ID)
	auth.SetSessionCookieRemember(w, token, remember)
	http.Redirect(w, r, loginDest, http.StatusSeeOther)
}

// handleOSChangePassword renders the forced first-login password-change page for
// a bootstrapped default admin. Reached via the serveWithAccess gate.
func (a *App) handleOSChangePassword(w http.ResponseWriter, r *http.Request) {
	setAuthPageNoCache(w)
	u := currentUser(r)
	em := ""
	if u != nil {
		em = u.Email
	}
	// Reuse a valid token when the browser already holds one (see csrfTokenFor):
	// minting on every render invalidated a form left open in another tab. The
	// cookie is host-only (no Domain), so a same-site subdomain foothold cannot
	// read it to forge the token.
	token := csrfTokenFor(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(osChangePasswordPage(em, "", token)))
}

// handleOSChangePasswordSubmit sets a new password for the signed-in user and
// clears the must-change flag, then sends them to the console. New password must
// be ≥8 chars and match the confirmation.
func (a *App) handleOSChangePasswordSubmit(w http.ResponseWriter, r *http.Request) {
	setAuthPageNoCache(w)
	u := currentUser(r)
	if u == nil || a.userStore == nil {
		http.Redirect(w, r, "/os/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	pass := r.FormValue("password")
	confirm := r.FormValue("confirm")
	// Re-embed the still-valid CSRF token from the cookie so a validation-error
	// re-render can be resubmitted without a fresh page load.
	csrf := ""
	if c, cerr := r.Cookie("vp_csrf"); cerr == nil {
		csrf = c.Value
	}
	render := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(osChangePasswordPage(u.Email, msg, csrf)))
	}
	if len(pass) < 8 {
		render("Password must be at least 8 characters.")
		return
	}
	if pass != confirm {
		render("The two passwords do not match.")
		return
	}
	if err := a.userStore.SetPassword(r.Context(), u.Email, pass); err != nil {
		render("Could not update the password: " + err.Error())
		return
	}
	dbpkg.AuditLog("user.password_change", u.Email, "", "forced first-login change")
	http.Redirect(w, r, osHome, http.StatusSeeOther)
}

// osChangePasswordPage renders the forced password-change form. Self-contained
// (reuses the login page chrome); the only inline script is the nonce'd one in
// the shared shell, so no CSP exception is needed.
func osChangePasswordPage(email, msg, csrf string) string {
	banner := ""
	if msg != "" {
		banner = `<div class="login-error" role="alert">` + html.EscapeString(msg) + `</div>`
	}
	return authPageShell("Set a new password — VayuOS", `
  <div class="login-brandline">`+saMark()+`<span>VayuPress</span></div>
  <div class="login-card">
    <h1 class="login-title">Set a new password</h1>
    <p class="login-sub">You're signing in with the default administrator password. Choose a new one to continue.</p>
    `+banner+`
    <form class="login-form" method="POST" action="/os/change-password" novalidate>
      <input type="hidden" name="csrf_token" value="`+html.EscapeString(csrf)+`">
      <div class="field">
        <label class="field-label" for="cp-email">Account</label>
        <input id="cp-email" class="input" type="email" value="`+html.EscapeString(email)+`" readonly>
      </div>
      <div class="field">
        <label class="field-label" for="cp-pass">New password</label>
        <input id="cp-pass" class="input" type="password" name="password" required minlength="8" autocomplete="new-password" placeholder="At least 8 characters">
      </div>
      <div class="field">
        <label class="field-label" for="cp-confirm">Confirm new password</label>
        <input id="cp-confirm" class="input" type="password" name="confirm" required minlength="8" autocomplete="new-password" placeholder="Re-enter the password">
      </div>
      <button class="btn btn--primary login-submit" type="submit">Save &amp; continue</button>
    </form>
  </div>`)
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
	writeSecureCookie(w, &http.Cookie{
		Name: memberCookie, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	http.Redirect(w, r, "/os/login", http.StatusSeeOther)
}

// osLoginPage builds the sign-in page: a single, calm, centered card on a clean
// background (light by default, following the OS) with an unobtrusive
// light/dark/auto theme switch. No gradients, no animation — minimalist premium.
func osLoginPage(prefillEmail, errMsg, next string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div class="login-error" role="alert">` + html.EscapeString(errMsg) + `</div>`
	}
	// next carries the post-login destination (e.g. an /oauth/authorize URL the
	// operator was bounced from). It is a safe same-origin path (isLocalURL),
	// so it can never redirect off-site; it is round-tripped as a hidden field.
	nextHTML := ""
	if next != "" {
		nextHTML = `<input type="hidden" name="next" value="` + html.EscapeString(next) + `">`
	}
	return authPageShell("Sign in — VayuPress", `
  <div class="login-brandline">`+saMark()+`<span>VayuPress</span></div>
  <div class="login-card">
    <h1 class="login-title">Welcome back</h1>
    <p class="login-sub">Sign in to your dashboard</p>
    `+errHTML+`
    <form class="login-form" method="POST" action="/os/login" novalidate>`+nextHTML+`
      <div class="field">
        <label class="field-label" for="login-email">Email</label>
        <input id="login-email" class="input" type="email" name="email"
          value="`+html.EscapeString(prefillEmail)+`"
          placeholder="you@example.com" autocomplete="username" required autofocus>
      </div>
      <div class="field">
        <label class="field-label" for="login-password">Password</label>
        <input id="login-password" class="input" type="password" name="password"
          placeholder="Your password" autocomplete="current-password" required>
      </div>
      <details class="login-totp"`+loginTOTPOpen(errHTML)+`>
        <summary class="login-totp__summary">Sign in with a 2FA code</summary>
        <div class="field">
          <label class="field-label" for="login-totp">Two-factor code</label>
          <input id="login-totp" class="input" type="text" name="totp"
            inputmode="numeric" autocomplete="one-time-code" maxlength="6" placeholder="000000">
        </div>
      </details>
      <label class="login-remember" for="login-remember">
        <input id="login-remember" type="checkbox" name="remember" value="1" checked>
        <span>Remember me on this device</span>
      </label>
      <button type="submit" class="btn btn--primary login-submit">Sign in</button>
    </form>
    <p class="login-recover"><a href="/mail/recover">Forgot your mailbox password?</a></p>
  </div>
  <div class="login-footer">Sovereign · zero-telemetry · yours completely</div>`)
}

// authPageShell wraps the calm auth-page layout (theme toggle + centered column)
// shared by the sign-in and change-password pages. The .vp-os class and the
// data-theme attribute both sit on <html> so the token overrides apply; the
// theme defaults to "auto" (follows the OS) and is switchable via os-theme.js.
// loginTOTPOpen reports whether the collapsed 2FA disclosure must start open:
// only when the sign-in error was about the code itself, so a failed attempt
// reopens the field instead of hiding the reason it failed.
func loginTOTPOpen(errHTML string) string {
	if strings.Contains(errHTML, "two-factor") || strings.Contains(errHTML, "2FA") ||
		strings.Contains(errHTML, "code") || strings.Contains(errHTML, "TOTP") {
		return " open"
	}
	return ""
}

func authPageShell(title, inner string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + html.EscapeString(title) + `</title>
<meta name="robots" content="noindex, nofollow">
<link rel="stylesheet" href="/os/static/css/vayuos.css?v=` + assetVer("css/vayuos.css") + `">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
<!-- Installable app (PWA): manifest + icons so the browser offers "Install VayuOS"
     on desktop and mobile, and the installed app opens straight into the console. -->
<link rel="manifest" href="/os/manifest.webmanifest">
` + osThemeColorMetas("auto") + `
<meta name="mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<meta name="apple-mobile-web-app-title" content="VayuOS">
<link rel="apple-touch-icon" href="/os/static/icons/vayuos-apple-180.png">
</head>
<body class="vp-os auth-page" data-ui="still-air" data-theme="auto">
  <div class="theme-switch" role="group" aria-label="Colour theme">
    <button type="button" class="theme-opt" data-set-theme="light" aria-label="Light" title="Light">` + saIcon("sun") + `</button>
    <button type="button" class="theme-opt" data-set-theme="dark" aria-label="Dark" title="Dark">` + saIcon("moon") + `</button>
    <button type="button" class="theme-opt" data-set-theme="auto" aria-label="Auto (match system)" title="Auto">◐</button>
  </div>
  <main class="auth-col">` + inner + `
  </main>
<script src="/os/static/js/os-theme.js?v=` + assetVer("js/os-theme.js") + `"></script>
</body></html>`
}

// ── Dashboard ────────────────────────────────────────────────────────────────

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

// ── Dashboard: first-run checklist (Wave 2.5) ────────────────────────────────

// osChecklistItem is one row of the dashboard's first-run checklist. Review
// items are actions the operator must eyeball elsewhere (DNS/HTTPS state lives
// on its own page) — they render as a neutral "Review" step, never as a ✓/✗ the
// server cannot honestly know.
type osChecklistItem struct {
	Label  string
	Detail string
	Href   string
	Done   bool
	Review bool
}

// osFirstRunChecklist assembles the dashboard's dismissable first-run card from
// cheap, server-known facts only — DB counts, settings presence, the configured
// domain. It deliberately performs NO network probes (DNS lookups, certificate
// fetches): the dashboard render path must stay fast, and a probe that times
// out would hang sign-in-to-dashboard. Nil when the viewer cannot reach the
// pages the items link to, or when everything is already done.
func (a *App) osFirstRunChecklist(ctx context.Context, accessLevel int) []osChecklistItem {
	// Every item links into an admin-gated page; an author who cannot open
	// /os/settings must not be handed a card of dead links.
	if accessLevel < osPathMinLevel("/os/settings") {
		return nil
	}
	var items []osChecklistItem
	add := func(it osChecklistItem) { items = append(items, it) }

	published := 0
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE status='published'`).Scan(&published)
	}
	add(osChecklistItem{
		Label:  "Publish your first post",
		Detail: "Drafts stay private until you flip them live from the editor",
		Href:   "/os/posts",
		Done:   published > 0,
	})

	// Site name + theme: value-diff against the compiled-in defaults. GetAll
	// merges the defaults into its result, so presence proves nothing — an
	// unset key resolves to "VayuPress" — but a value that DIFFERS from the
	// default can only come from the operator.
	if a.siteSettings != nil {
		if kv, err := a.siteSettings.GetAll(ctx, settings.ForPrimary()); err == nil {
			differsFromDefault := func(key string) bool {
				v, ok := kv[key]
				def, hasDefault := settings.Defaults[key]
				if !hasDefault {
					return ok && v != "" // no default ⇒ any stored value is a choice
				}
				return ok && v != def
			}
			add(osChecklistItem{
				Label:  "Name your site",
				Detail: "The name your readers see in the header and their inbox",
				Href:   "/os/settings",
				Done:   differsFromDefault(settings.KeySiteName),
			})
			themed := false
			for k := range kv {
				if strings.HasPrefix(k, "theme.") && differsFromDefault(k) {
					themed = true
					break
				}
			}
			add(osChecklistItem{
				Label:  "Make it yours",
				Detail: "Pick colours and typography in Theme",
				Href:   "/os/theme",
				Done:   themed,
			})
		}
	}

	// A bare single-binary install serves "localhost" — the machine name no
	// reader can type. Anything else counts as pointed.
	domain := strings.TrimSpace(config.Cfg.Domain)
	add(osChecklistItem{
		Label:  "Point a real domain",
		Detail: "A bare install only answers on localhost",
		Href:   "/os/domains",
		Done:   domain != "" && domain != "localhost",
	})
	add(osChecklistItem{
		Label:  "Review DNS & HTTPS",
		Detail: "Records resolve and the certificate is live — once a domain points here",
		Href:   "/os/dns",
		Review: true,
	})

	// All done ⇒ no card. A checklist that nags completed work is the same
	// dishonesty the plan set out to remove.
	for _, it := range items {
		if !it.Done {
			return items
		}
	}
	return nil
}

// osHome is the console's address. It is the installed app's start_url and
// sits inside its scope, "/os/" (handleOSManifest). Scope matching is a plain
// prefix test, so "/os" is OUTSIDE it: every link to "/os" took an installed
// app out of app mode and showed the address bar, which is how the Dashboard
// link behaved. Every link and redirect to the console home uses this.
const osHome = "/os/"

func (a *App) handleOSDashboard(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	writeOSHTML(w, r, adminOSLayout(nonce, "Home", "dashboard", cfg, htmpl.HTML(a.stillAirHomeBody(r.Context(), cfg, a.getAdminSnapshot()))))

}

// ── Posts ────────────────────────────────────────────────────────────────────

// osPostsPageSize is how many posts the manager shows per page. The newest
// page (page 1) lists the latest osPostsPageSize posts; older posts live on
// subsequent pages reachable via the pager. This replaces the old hard
// `LIMIT 500` cap so every post is reachable regardless of archive size.
const osPostsPageSize = 100

// osPostStatusPill renders the status badge shown in the Posts manager for a
// post's current status ("draft" → Draft, anything else → Published).
func osPostStatusPill(status string) string {
	if status == "draft" {
		return `<span class="status-pill status-pill--draft">● Draft</span>`
	}
	return `<span class="status-pill status-pill--live">● Published</span>`
}

// osPostStatusButton renders the HTMX publish/unpublish toggle for a post in its
// CURRENT status. Clicking it POSTs the opposite status to the fragment endpoint
// (handleOSPostToggleFragment), which returns the flipped button plus an
// out-of-band swap of the status pill — so the row updates in place with no
// full-page reload. slugEsc must already be HTML-escaped by the caller.
// The button carries a stable per-slug id and data-src="body" so the fragment
// endpoint and the bulk updater can always tell the row-face copy from this
// accordion copy (Wave 3.5: the publish action lives on the row face too).
func osPostStatusButton(slugEsc, status string) string {
	label, to := "Unpublish", "draft"
	if status == "draft" {
		label, to = "Publish", "published"
	}
	return `<button type="button" id="post-pub-` + slugEsc + `" data-src="body" class="btn btn--ghost btn--sm"` +
		` hx-post="/os/api/posts/` + slugEsc + `/status-fragment"` +
		` hx-vals='{"status":"` + to + `"}'` +
		` hx-target="this" hx-swap="outerHTML" hx-disabled-elt="this">` + label + `</button>`
}

// osPostStatusFaceButton is the Wave 3.5 row-face copy of the publish toggle: a
// compact icon button rendered in the summary row itself, so the most common
// action (publish a draft) never requires opening the card. Same endpoint and
// id scheme as the accordion copy — data-src="face" tells the fragment endpoint
// which copy to return in place so BOTH copies flip on a single click.
func osPostStatusFaceButton(slugEsc, status string) string {
	icon, label, to := "send", "Publish", "published"
	if status != "draft" {
		icon, label, to = "draft", "Unpublish", "draft"
	}
	return `<button type="button" id="post-pubface-` + slugEsc + `" data-src="face" class="btn btn--ghost btn--sm post-acc__face-btn"` +
		` title="` + label + ` (opens nothing — toggles right here)" aria-label="` + label + ` post"` +
		` hx-post="/os/api/posts/` + slugEsc + `/status-fragment"` +
		` hx-vals='{"status":"` + to + `","src":"face"}'` +
		` hx-target="this" hx-swap="outerHTML" hx-disabled-elt="this">` + saIcon(icon) + `</button>`
}

// osPostStatusButtonOOB renders the accordion publish toggle as an out-of-band
// swap target, so a click on the row-face copy also flips the hidden copy.
func osPostStatusButtonOOB(slugEsc, status string) string {
	return strings.Replace(osPostStatusButton(slugEsc, status),
		`data-src="body"`, `data-src="body" hx-swap-oob="true"`, 1)
}

// osPostStatusFaceButtonOOB renders the row-face publish toggle as an
// out-of-band swap target, so a click on the accordion copy also flips the face.
func osPostStatusFaceButtonOOB(slugEsc, status string) string {
	return strings.Replace(osPostStatusFaceButton(slugEsc, status),
		`data-src="face"`, `data-src="face" hx-swap-oob="true"`, 1)
}

// osPostStatusOOB renders the out-of-band status-pill update the fragment
// endpoint returns alongside the flipped button, keyed by the row's stable
// per-slug id so HTMX swaps only that cell.
func osPostStatusOOB(slugEsc, status string) string {
	return `<span id="post-status-` + slugEsc + `" hx-swap-oob="true">` + osPostStatusPill(status) + `</span>`
}

// osPostPinButton renders the HTMX pin/unpin toggle for a post in its CURRENT
// featured state. Clicking it POSTs the opposite state to the pin-fragment
// endpoint, which returns the flipped button plus an out-of-band swap of the
// row's "📌 Pinned" badge. slugEsc must already be HTML-escaped.
func osPostPinButton(slugEsc string, featured bool) string {
	label, to := "Pin", "1"
	if featured {
		label, to = "Unpin", "0"
	}
	return `<button type="button" id="post-pin-` + slugEsc + `" data-src="body" class="btn btn--ghost btn--sm"` +
		` hx-post="/os/api/posts/` + slugEsc + `/pin-fragment"` +
		` hx-vals='{"pinned":"` + to + `"}'` +
		` hx-target="this" hx-swap="outerHTML" hx-disabled-elt="this">` + label + `</button>`
}

// osPostPinFaceButton is the Wave 3.5 row-face copy of the pin toggle: one click
// pins or unpins from the summary row, without opening the card.
func osPostPinFaceButton(slugEsc string, featured bool) string {
	label, pressed, to := "Pin", "false", "1"
	if featured {
		label, pressed, to = "Unpin", "true", "0"
	}
	return `<button type="button" id="post-pinface-` + slugEsc + `" data-src="face" class="btn btn--ghost btn--sm post-acc__face-btn"` +
		` title="` + label + ` (toggles right here)" aria-label="` + label + ` post" aria-pressed="` + pressed + `"` +
		` hx-post="/os/api/posts/` + slugEsc + `/pin-fragment"` +
		` hx-vals='{"pinned":"` + to + `","src":"face"}'` +
		` hx-target="this" hx-swap="outerHTML" hx-disabled-elt="this">` + saIcon("pin") + `</button>`
}

// osPostPinButtonOOB renders the accordion pin toggle as an out-of-band swap
// target, so a click on the row-face copy also flips the hidden copy.
func osPostPinButtonOOB(slugEsc string, featured bool) string {
	return strings.Replace(osPostPinButton(slugEsc, featured),
		`data-src="body"`, `data-src="body" hx-swap-oob="true"`, 1)
}

// osPostPinFaceButtonOOB renders the row-face pin toggle as an out-of-band swap
// target, so a click on the accordion copy also flips the face.
func osPostPinFaceButtonOOB(slugEsc string, featured bool) string {
	return strings.Replace(osPostPinFaceButton(slugEsc, featured),
		`data-src="face"`, `data-src="face" hx-swap-oob="true"`, 1)
}

// osPostPinBadge renders the pinned indicator next to a post's title, keyed by a
// stable per-slug id so the pin toggle can update it out-of-band. It is always
// emitted (empty when unpinned) so the OOB target exists for a later pin.
func osPostPinBadge(slugEsc string, featured, oob bool) string {
	inner := ""
	if featured {
		inner = ` <span class="chip" title="Pinned to the homepage and trending widget">` + saIcon("pin") + ` Pinned</span>`
	}
	oobAttr := ""
	if oob {
		oobAttr = ` hx-swap-oob="true"`
	}
	return `<span id="ppin-` + slugEsc + `"` + oobAttr + `>` + inner + `</span>`
}

// osIndexNowBadge renders a post's IndexNow (search-engine instant-index)
// submission state as a small chip, keyed by a stable per-slug id so the manual
// re-ping can swap it out-of-band. slugEsc must already be HTML-escaped. Drafts
// are not public, so they show a neutral "—" instead of a submission state.
func osIndexNowBadge(slugEsc string, st dbpkg.IndexNowStatus, ok, isDraft bool) string {
	var inner string
	switch {
	case isDraft:
		inner = `<span class="chip" title="Drafts are not public, so nothing is submitted to IndexNow until you publish.">IndexNow: —</span>`
	case ok && st.State == dbpkg.IndexNowSubmitted:
		when := st.SubmittedAt.Format("2 Jan 2006 15:04 UTC")
		inner = `<span class="chip chip--brand" title="Submitted to IndexNow on ` + html.EscapeString(when) + `">` + saIcon("check") + ` IndexNow</span>`
	case ok && st.State == dbpkg.IndexNowPending:
		// HTTP 202 — received, but the engine has not yet validated the key file.
		// Shown distinctly on purpose: if that validation fails the URL is dropped
		// silently, so a tick here would be a claim the install cannot support.
		when := st.SubmittedAt.Format("2 Jan 2006 15:04 UTC")
		title := st.Detail
		if title == "" {
			title = "The engine received this URL but has not finished validating your key file."
		}
		inner = `<span class="chip chip--warn" title="` + html.EscapeString(title+" Sent "+when) + `">` + saIcon("hourglass") + ` IndexNow pending</span>`
	case ok && st.State == dbpkg.IndexNowFailed:
		inner = `<span class="chip chip--warn" title="` + html.EscapeString(st.Detail) + `">` + saIcon("warn") + ` IndexNow failed</span>`
	default:
		inner = `<span class="chip" title="Not yet submitted to IndexNow. Use “Ping IndexNow” to submit it now.">IndexNow: not sent</span>`
	}
	return `<span id="post-indexnow-` + slugEsc + `">` + inner + `</span>`
}

// osIndexNowBadgeOOB is the out-of-band variant returned by the manual re-ping
// endpoint so HTMX updates just that post's badge in place.
func osIndexNowBadgeOOB(slugEsc string, st dbpkg.IndexNowStatus, ok, isDraft bool) string {
	base := osIndexNowBadge(slugEsc, st, ok, isDraft)
	return strings.Replace(base, `<span id="post-indexnow-`+slugEsc+`">`, `<span id="post-indexnow-`+slugEsc+`" hx-swap-oob="true">`, 1)
}

// osIndexNowButton renders the manual "Ping IndexNow" control. It is only
// meaningful for a published post (a draft has no public URL to announce), so it
// returns empty for drafts. The label reads "Re-ping" once a post was already
// submitted. Clicking POSTs to the fragment endpoint, which returns the flipped
// button plus an out-of-band badge update.
func osIndexNowButton(slugEsc string, st dbpkg.IndexNowStatus, ok, isDraft bool) string {
	if isDraft {
		return ""
	}
	label := "Ping IndexNow"
	if ok && (st.State == dbpkg.IndexNowSubmitted || st.State == dbpkg.IndexNowPending) {
		label = "Re-ping"
	}
	return `<button type="button" class="btn btn--ghost btn--sm"` +
		` hx-post="/os/api/posts/` + slugEsc + `/indexnow-fragment"` +
		` hx-target="this" hx-swap="outerHTML" hx-disabled-elt="this">` + label + `</button>`
}

func (a *App) handleOSPosts(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// A CSRF token cookie so the inline publish/unpublish control can POST.
	csrfTokenFor(w, r)

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
	// A bounded context so a slow or contended query can never hang the request
	// until the upstream proxy gives up — the cause of the intermittent 502 on
	// large catalogs. On deadline the queries return an error and the handler
	// degrades to a friendly, retryable page (HTTP 200) instead of a gateway
	// error.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	loadErr := false

	allCount, published, drafts := 0, 0, 0
	if dbpkg.DB != nil {
		// status is NOT NULL DEFAULT 'published' (migration 030), so we group by
		// the bare column — `COALESCE(status,'published')` would defeat
		// idx_articles_status and force a full-table scan (a 502-class stall on a
		// large catalog). With no search/date filter this is an index-only count.
		if rows, err := dbpkg.Reader().QueryContext(ctx,
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
		} else {
			loadErr = true
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
		Featured            bool
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
		if rows, err := dbpkg.Reader().QueryContext(ctx,
			`SELECT title,slug,COALESCE(tags,''),updated_at,COALESCE(status,'published'),COALESCE(featured,0) FROM articles`+listClause+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, listArgs...); err == nil {
			defer rows.Close()
			for rows.Next() {
				var p postRow
				var tagsCSV string
				var featured int
				if rows.Scan(&p.Title, &p.Slug, &tagsCSV, &p.Updated, &p.Status, &featured) == nil {
					p.Tags = splitCSVTags(tagsCSV)
					p.Featured = featured != 0
					posts = append(posts, p)
				}
			}
			_ = rows.Err() // best-effort admin post list
		} else {
			loadErr = true
		}
	}

	filtersActive := q != "" || from != "" || to != "" || status != "all"

	// When a query times out or errors we never want to mislead the operator
	// with the "No posts yet" empty state; instead we render the manager shell
	// with a non-blocking retry notice so the page always loads.
	notice := ""
	if loadErr {
		notice = `<div class="card load-notice"><strong>Couldn't load everything just now</strong> — the database was busy. <a href="/os/posts">Retry</a>.</div>`
	}

	var body string
	if allCount == 0 && !filtersActive && !loadErr {
		body = `<div class="page-header"><h1>Posts</h1></div>
<div class="card empty-state">
  <div class="empty-icon">` + saIcon("pencil") + `</div>
  <div class="empty-title">No posts yet</div>
  <div class="empty-sub">Your articles will appear here. Write your first one — it only takes a minute.</div>
  <a class="btn btn--primary mt-4" href="/os/editor">Write your first post</a>
</div>`
	} else {
		// Batch-load each shown post's IndexNow submission status in one query
		// (avoids an N+1) so every row can show whether it was announced to
		// search engines and offer a manual re-ping.
		slugList := make([]string, 0, len(posts))
		for _, p := range posts {
			slugList = append(slugList, p.Slug)
		}
		inStatus := dbpkg.IndexNowStatuses(slugList)
		cards := ""
		for _, p := range posts {
			tags := ""
			for _, t := range p.Tags {
				tags += `<span class="chip chip--brand">#` + html.EscapeString(t) + `</span> `
			}
			esc := html.EscapeString(p.Slug)
			isDraft := p.Status == "draft"
			inSt, inOK := inStatus[p.Slug]
			viewBtn := `<a class="btn btn--ghost btn--sm" href="/` + esc + `" target="_blank" rel="noopener">View ↗</a>`
			if isDraft {
				// A draft is hidden from the public site (previewed in the editor).
				viewBtn = ""
			}
			tagsBlock := ""
			if tags != "" {
				tagsBlock = `
      <div class="post-acc__tags">` + tags + `</div>`
			}
			// Each post is a premium collapsible card (Monetization-console style).
			// The bulk-select checkbox sits OUTSIDE the <summary> (column 1 of the
			// row grid) so ticking it never toggles the card; tapping the card body
			// reveals ONLY that post's actions. Pin/status/IndexNow keep their stable
			// per-slug ids so the HTMX out-of-band swaps still land in place.
			cards += `<div class="post-row" data-post-row>
  <input type="checkbox" class="post-acc__check" data-post-select value="` + esc + `" aria-label="Select ` + html.EscapeString(p.Title) + `">
  <details class="mon-acc post-acc">
    <summary class="mon-acc__sum">
      <span class="mon-acc__head">
        <span class="mon-acc__title">` + html.EscapeString(p.Title) + osPostPinBadge(esc, p.Featured, false) + `</span>
        <span class="mon-acc__sub">/` + esc + ` · Updated ` + config.FormatSite(p.Updated, "2 Jan 2006") + `</span>
      </span>
      <span id="post-status-` + esc + `" class="post-acc__status">` + osPostStatusPill(p.Status) + `</span>
      <span class="post-acc__face">` + osPostStatusFaceButton(esc, p.Status) + osPostPinFaceButton(esc, p.Featured) + `</span>
      <svg class="mon-acc__chev" viewBox="0 0 20 20" width="16" height="16" fill="none" aria-hidden="true"><path d="M6 8l4 4 4-4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>
    </summary>
    <div class="mon-acc__body post-acc__body">` + tagsBlock + `
      <div class="post-acc__meta-row">` + osIndexNowBadge(esc, inSt, inOK, isDraft) + `</div>
      <div class="post-acc__actions">
        <a class="btn btn--primary btn--sm" href="/os/editor/` + esc + `">Edit</a>
        ` + viewBtn + `
        ` + osPostPinButton(esc, p.Featured) + `
        ` + osPostStatusButton(esc, p.Status) + `
        ` + osIndexNowButton(esc, inSt, inOK, isDraft) + `
        <button type="button" class="btn btn--ghost btn--sm" data-post-delete data-slug="` + esc + `" data-title="` + html.EscapeString(p.Title) + `">Delete</button>
      </div>
    </div>
  </details>
</div>`
		}

		listBlock := `<div class="post-list-head">
    <label class="post-selectall"><input type="checkbox" data-post-select-all aria-label="Select all posts on this page"> Select all on this page</label>
    <span class="muted text-xs">Tap a post to see its actions</span>
  </div>
  <div class="mon-stack post-stack">` + cards + `</div>`
		if len(posts) == 0 {
			listBlock = `<div class="table-empty">No posts match your filter. <a href="/os/posts">Clear filters</a>.</div>`
		}

		shownFrom, shownTo := 0, 0
		if len(posts) > 0 {
			shownFrom = offset + 1
			shownTo = offset + len(posts)
		}

		body = notice + `<div class="page-header">
  <h1>Posts <span class="count-pill">` + strconv.Itoa(allCount) + `</span></h1>
  <div class="page-actions">
    <a class="btn btn--primary" href="/os/editor">New post</a>
  </div>
</div>
<p class="page-sub">Every article in one place — search, filter by status or date, publish or unpublish inline, and see at a glance what's announced to search engines.</p>
<div class="stat-grid mb-6">
  <div class="stat-card"><div class="stat-card__label">Total posts</div><div class="stat-card__value">` + strconv.Itoa(allCount) + `</div><div class="stat-card__bottom"><span class="muted text-xs">across your whole catalogue</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Published</div><div class="stat-card__value">` + strconv.Itoa(published) + `</div><div class="stat-card__bottom"><span class="muted text-xs">live on your site</span></div></div>
  <div class="stat-card"><div class="stat-card__label">Drafts</div><div class="stat-card__value">` + strconv.Itoa(drafts) + `</div><div class="stat-card__bottom"><span class="muted text-xs">not yet published</span></div></div>
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
  ` + listBlock + `
  ` + osPostsPager(status, q, from, to, period, page, totalPages, total, shownFrom, shownTo) + `
</div>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?m[1]:'';}
var msg=document.getElementById('action-msg');
function show(t,e){if(!msg)return;msg.textContent=t;msg.classList.toggle('is-error',!!e);msg.classList.add('visible');}
// Pin/unpin is HTMX-driven (hx-post → out-of-band button + badge update); see
// osPostPinButton and handleOSPostPinFragment. No JS handler needed here.
// Publish/unpublish is HTMX-driven (hx-post → out-of-band row update); see
// osPostStatusButton and handleOSPostToggleFragment. No JS handler needed here.
document.querySelectorAll('[data-post-delete]').forEach(function(b){
  b.addEventListener('click',function(){
    var t=b.getAttribute('data-title')||'this post';
    vpConfirm({title:'Delete post',message:'Delete "'+t+'"? This permanently removes the post and its comments and cannot be undone.',confirm:'Delete'},function(){
      b.disabled=true;
      fetch('/os/api/posts/'+encodeURIComponent(b.getAttribute('data-slug')),{method:'DELETE',headers:{'X-CSRF-Token':csrf()}})
        .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
        .then(function(res){if(res.ok){show('Deleted',false);var row=b.closest('[data-post-row]');if(row)row.remove();}else{b.disabled=false;show(res.d.detail||res.d.title||'Error',true);}})
        .catch(function(e){b.disabled=false;show('Error: '+e,true);});
    });
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
    if(act==='delete'){vpConfirm({title:'Delete posts',message:'Delete '+slugs.length+' post'+(slugs.length>1?'s':'')+'? This cannot be undone.',confirm:'Delete'},function(){runBulk(b,act,slugs);});return;}
    runBulk(b,act,slugs);
  });
});
function runBulk(b,act,slugs){
    b.disabled=true;show(act==='delete'?'Deleting…':'Updating…',false);
    fetch('/os/api/posts/bulk',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()},body:JSON.stringify({action:act,slugs:slugs})})
      .then(function(r){return r.json().then(function(d){return{ok:r.ok,d:d};});})
      .then(function(res){
        if(!res.ok){b.disabled=false;show((res.d&&res.d.detail)||'Bulk request failed',true);return;}
        var d=res.d,fail=[];
        (d.results||[]).forEach(function(r0){
          var row=null;
          document.querySelectorAll('[data-post-select]').forEach(function(c){if(c.value===r0.slug)row=c.closest('[data-post-row]');});
          if(r0.ok){
            if(act==='delete'){if(row)row.remove();}
            else{
              var pill=document.getElementById('post-status-'+r0.slug);if(pill&&r0.pill)pill.innerHTML=r0.pill;
              if(row&&r0.button){var btn=row.querySelector('button[hx-post$="/status-fragment"][data-src="body"]');if(btn)btn.outerHTML=r0.button;}
              if(row){var chk=row.querySelector('[data-post-select]');if(chk)chk.checked=false;}
            }
          }else{fail.push(r0.slug+(r0.error?': '+r0.error:''));}
        });
        if(d.counts){var seg=document.querySelectorAll('.seg-btn .muted');if(seg.length>=3){seg[0].textContent=String(d.counts.all||0);seg[1].textContent=String(d.counts.published||0);seg[2].textContent=String(d.counts.draft||0);}}
        refreshBulk();
        if(fail.length){b.disabled=false;show('Failed: '+fail.join('; '),true);}
        else{show('Done — '+d.ok+(act==='delete'?' deleted':' updated'),false);setTimeout(function(){b.disabled=false;},800);}
      })
      .catch(function(e){b.disabled=false;show('Error: '+e,true);});
}
})();
</script>`
	}
	writeOSHTML(w, r, adminOSLayout(nonce, "Posts", "posts", cfg, htmpl.HTML(body)))
}

// ── Comments moderation ──────────────────────────────────────────────────────

// commentIDRe is the strict allowlist for a comment id. Comment ids are
// generated as 24 lowercase hex chars (comments.newID), so a value outside this
// set is invalid input — validating against it before the id is ever reflected
// into the moderation fragment removes any HTML/URL/CSS-injection vector
// (reflected XSS, CodeQL go/reflected-xss).
var commentIDRe = regexp.MustCompile(`^[0-9a-f]{1,64}$`)

// canonicalCommentStatus maps a requested moderation status to a fixed,
// compile-time constant (or "" if unrecognised). Returning a literal — rather
// than the request string — means the value later reflected into the moderation
// fragment is provably not request-tainted, which removes the reflected-XSS
// flow at the source (defence in depth alongside output escaping).
func canonicalCommentStatus(s string) string {
	switch strings.TrimSpace(s) {
	case "approved":
		return "approved"
	case "rejected":
		return "rejected"
	case "spam":
		return "spam"
	default:
		return ""
	}
}

// osCommentPill renders a comment's status badge. It carries a stable per-id id
// and a data-status attribute so the client-side status filter can read the live
// status after an HTMX moderation swap. When oob is true it is emitted as an
// out-of-band swap so the fragment endpoint can update the pill in place. idEsc
// and status must already be safe (escaped id; status from the validated enum).
func osCommentPill(idEsc, status string, oob bool) string {
	cls := "status-pill"
	switch status {
	case "approved":
		cls = "status-pill status-pill--live"
	case "pending":
		cls = "status-pill status-pill--draft"
	}
	oobAttr := ""
	if oob {
		oobAttr = ` hx-swap-oob="true"`
	}
	// status is escaped in BOTH the attribute and the text: it reaches here from
	// request input on the moderation-fragment path, and an unescaped reflection
	// (even of a validated value) is a reflected-XSS sink (CodeQL go/reflected-xss).
	esc := html.EscapeString(status)
	return `<span class="` + cls + `" id="cpill-` + idEsc + `" data-status="` + esc + `"` + oobAttr + `>● ` + esc + `</span>`
}

// osCommentActions renders the moderation buttons for a comment in its CURRENT
// status (the action matching the current status is omitted). Each button is
// HTMX-driven: it POSTs the new status to the fragment endpoint and swaps the
// row's action cell in place, so moderation needs no full-page reload. The
// buttons depend only on (id, status), so the fragment endpoint can re-render
// them without re-fetching the comment. idEsc must already be HTML-escaped.
func osCommentActions(idEsc, status string) string {
	btn := func(to, label, cls string) string {
		if status == to {
			return ""
		}
		return `<button type="button" class="btn ` + cls + ` btn--sm"` +
			` hx-post="/os/api/comments/` + idEsc + `/status-fragment"` +
			` hx-vals='{"status":"` + to + `"}'` +
			` hx-target="#cact-` + idEsc + `" hx-swap="innerHTML" hx-disabled-elt="this">` + label + `</button> `
	}
	return btn("approved", "Approve", "btn--primary") + btn("rejected", "Reject", "btn--ghost") + btn("spam", "Spam", "btn--ghost")
}

func (a *App) handleOSComments(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	// CSRF token cookie so the inline approve/reject controls can POST.
	csrfTokenFor(w, r)

	var body string
	if a.commentStore == nil {
		body = `<div class="page-header"><h1>Comments</h1></div>
<div class="card empty-state"><div class="empty-icon">` + saIcon("talk") + `</div>
<div class="empty-title">Comments unavailable</div>
<div class="empty-sub">The comment store is not initialised.</div></div>`
		writeOSHTML(w, r, adminOSLayout(nonce, "Comments", "comments", cfg, htmpl.HTML(body)))
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
		idEsc := html.EscapeString(c.ID)
		slug := slugByID[c.ArticleID]
		postCell := html.EscapeString(slug)
		if slug != "" {
			postCell = `<a href="/` + html.EscapeString(slug) + `" target="_blank" rel="noopener">/` + html.EscapeString(slug) + `</a>`
		}
		// The status filter reads data-status off the pill (updated in place by the
		// HTMX moderation swap), not off the <tr>, so a moderated row re-filters
		// correctly without a full-page reload.
		rowsHTML += `<tr data-comment-row>
  <td class="row-title"><strong>` + html.EscapeString(c.Author) + `</strong>
    <div class="row-meta">` + html.EscapeString(c.Email) + `</div></td>
  <td>` + html.EscapeString(c.Body) + `</td>
  <td>` + postCell + `</td>
  <td class="text-sm">` + geoDisplayHTML(c.Country, c.City) + `</td>
  <td>` + osCommentPill(idEsc, c.Status, false) + `</td>
  <td class="muted text-sm">` + config.FormatSite(c.CreatedAt, "2 Jan 2006 15:04") + `</td>
  <td class="row-actions" id="cact-` + idEsc + `">` + osCommentActions(idEsc, c.Status) + `</td>
</tr>`
	}

	if len(all) == 0 {
		body = `<div class="page-header"><h1>Comments</h1></div>
<div class="card empty-state"><div class="empty-icon">` + saIcon("talk") + `</div>
<div class="empty-title">No comments yet</div>
<div class="empty-sub">When readers comment on your articles, they appear here for moderation before going public.</div></div>`
	} else {
		body = `<div class="page-header">
  <h1>Comments <span class="count-pill">` + strconv.Itoa(len(all)) + `</span></h1>
  <div class="page-actions"><span class="text-sm muted"><span id="cc-sum-pending">` + strconv.Itoa(pending) + `</span> pending · <span id="cc-sum-approved">` + strconv.Itoa(approved) + `</span> approved</span></div>
</div>
<p class="page-sub">Moderate the conversation on your posts — approve, reply to or remove comments before they go public.</p>
<div class="card">
  <div class="toolbar-row">
    <div class="seg-filter" role="tablist" aria-label="Filter by status">
      <button type="button" class="seg-btn is-active" data-comment-filter="all">All <span class="muted">` + strconv.Itoa(len(all)) + `</span></button>
      <button type="button" class="seg-btn" data-comment-filter="pending">Pending <span class="muted" id="cc-pending">` + strconv.Itoa(pending) + `</span></button>
      <button type="button" class="seg-btn" data-comment-filter="approved">Approved <span class="muted" id="cc-approved">` + strconv.Itoa(approved) + `</span></button>
    </div>
  </div>
  <div class="table-wrap">
    <table class="table">
      <thead><tr><th>Author</th><th>Comment</th><th>Post</th><th>Location</th><th>Status</th><th>When</th><th></th></tr></thead>
      <tbody>` + rowsHTML + `</tbody>
    </table>
  </div>
  <div class="table-empty" data-filter-empty hidden>No comments match this filter.</div>
</div>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
// Moderation is HTMX-driven (hx-post → swap the row's action cell + out-of-band
// pill and counts); see osCommentActions and handleOSCommentModerateFragment.
// The filter reads each row's live status from its pill's data-status (updated
// by the swap) and re-applies after every HTMX swap so a moderated row moves to
// the right tab without a page reload.
var activeFilter='all';
function applyFilter(){
  document.querySelectorAll('[data-comment-row]').forEach(function(row){
    var pill=row.querySelector('[data-status]');
    var st=pill?pill.getAttribute('data-status'):'';
    row.hidden=(activeFilter!=='all'&&st!==activeFilter);
  });
}
document.querySelectorAll('[data-comment-filter]').forEach(function(s){
  s.addEventListener('click',function(){
    document.querySelectorAll('[data-comment-filter]').forEach(function(x){x.classList.remove('is-active');});
    s.classList.add('is-active');
    activeFilter=s.getAttribute('data-comment-filter');
    applyFilter();
  });
});
document.body.addEventListener('htmx:afterSwap',applyFilter);
})();
</script>`
	}
	writeOSHTML(w, r, adminOSLayout(nonce, "Comments", "comments", cfg, htmpl.HTML(body)))
}

// handleOSCommentModerateFragment is the HTMX counterpart to handleCommentModerate:
// it moderates a comment and returns an HTML fragment — the row's new action
// buttons (main swap) plus out-of-band updates of its status pill and the
// pending/approved counts — so the Comments manager updates in place with no
// full-page reload. CSRF is enforced by the route's CSRFTokenMiddleware (the
// admin layout mirrors the vp_csrf cookie into the X-CSRF-Token header for every
// hx-* request). The JSON PUT endpoint remains for API clients.
func (a *App) handleOSCommentModerateFragment(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		http.Error(w, "comments not initialised", http.StatusServiceUnavailable)
		return
	}
	id := chi.URLParam(r, "id")
	// Map the requested status to a compile-time constant rather than passing the
	// request string through: the value later reflected into the response fragment
	// is then provably untainted (not request-derived), closing the reflected-XSS
	// vector at the source. Likewise the id is constrained to a well-formed comment
	// id (hex) before it is used or echoed.
	status := canonicalCommentStatus(r.FormValue("status"))
	if !commentIDRe.MatchString(id) || status == "" {
		http.Error(w, "a valid comment id and status (approved|rejected|spam) are required", http.StatusBadRequest)
		return
	}
	if err := a.commentStore.Moderate(r.Context(), id, status); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Mirror the JSON path's side effects: notify on approval.
	if status == "approved" {
		go a.notifyCommentApproved(context.WithoutCancel(r.Context()), id)
		go a.notifyCommentReply(context.WithoutCancel(r.Context()), id)
	}
	// Recompute pending/approved for the out-of-band count badges via the store's
	// GROUP BY count (accurate on any size catalog and cheaper than re-listing).
	// Only these two change on a status move; the All total is unaffected.
	pending, approved := 0, 0
	if counts, err := a.commentStore.Count(r.Context()); err == nil {
		pending = int(counts["pending"])
		approved = int(counts["approved"])
	}
	idEsc := html.EscapeString(id)
	p, ap := strconv.Itoa(pending), strconv.Itoa(approved)
	frag := osCommentActions(idEsc, status) +
		osCommentPill(idEsc, status, true) +
		`<span id="cc-pending" class="muted" hx-swap-oob="true">` + p + `</span>` +
		`<span id="cc-approved" class="muted" hx-swap-oob="true">` + ap + `</span>` +
		`<span id="cc-sum-pending" hx-swap-oob="true">` + p + `</span>` +
		`<span id="cc-sum-approved" hx-swap-oob="true">` + ap + `</span>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(frag))
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
		`</strong> of <strong>` + strconv.Itoa(total) + `</strong> post` + plural(total) + `</div>`
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
			authorSel := meta.AuthorID
			if authorSel == "" {
				authorSel = currentUserIDOf(r)
			}
			authorOpts := a.authorSelectOptions(r.Context(), authorSel)
			if hasBlocks || emptyDraft {
				body := osEditorBody(slug, art.Title, blocksJSON, authorOpts) + metaScript
				body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-editor.js?v=` + assetVer("js/admin-os-editor.js") + `"></script>`
				writeOSHTML(w, r, adminOSLayout(nonce, "Edit Post", "editor", cfg, htmpl.HTML(body)))
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
			body := osEditorBody(slug, art.Title, string(raw), authorOpts) + metaScript
			body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-editor.js?v=` + assetVer("js/admin-os-editor.js") + `"></script>`
			writeOSHTML(w, r, adminOSLayout(nonce, "Edit Post", "editor", cfg, htmpl.HTML(body)))
			return
		}
	}

	// Brand-new post: the native block editor owns the create path (v1.6.0).
	// It hydrates with an empty document and an empty slug; the first Save POSTs
	// to /os/api/editor/save, which creates the article and returns its slug.
	body := osEditorBody("", "", "[]", a.authorSelectOptions(r.Context(), currentUserIDOf(r))) + osEditorMetaScript("", "", time.Time{}, nil, PostMeta{})
	body += `
<script nonce="` + nonce + `" src="/os/static/js/admin-os-editor.js?v=` + assetVer("js/admin-os-editor.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "New post", "editor", cfg, htmpl.HTML(body)))
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
	if group == "security" {
		// Sign-in security is Shield's; the old tab here was only a link to it.
		http.Redirect(w, r, "/os/security", http.StatusMovedPermanently)
		return
	}
	cat, ok := settingsCategoryFor(group)
	if !ok {
		http.Redirect(w, r, "/os/settings", http.StatusFound)
		return
	}
	body := string(settingsPageBody(r.Context(), a, cat))

	saveScript := settingsPageScript
	fullHTML := adminOSShellHead(nonce, cat.Label, "settings", cfg) +
		renderTrustedHTML(htmpl.HTML(body)) +
		adminOSShellFoot(nonce, saveScript, pageUsesAlpine(body))
	writeOSHTML(w, r, fullHTML)
}

// resolvedRoute is the matched pattern with its own parameters filled in. A
// Settings category is served by /os/settings/{group}, and the chrome can only
// mark Appearance current if it sees /os/settings/appearance.
func resolvedRoute(rc *chi.Context) string {
	route := rc.RoutePattern()
	for i, k := range rc.URLParams.Keys {
		if k != "*" && i < len(rc.URLParams.Values) {
			route = strings.Replace(route, "{"+k+"}", rc.URLParams.Values[i], 1)
		}
	}
	return route
}

// ── JSON APIs ─────────────────────────────────────────────────────────────────

// handleOSActivity returns recent admin activity as JSON for the dashboard feed.
func (a *App) handleOSActivity(w http.ResponseWriter, r *http.Request) {
	type activityItem struct {
		Kind string `json:"kind"`
		Icon string `json:"icon"`
		Text string `json:"text"`
		Time string `json:"time"`
		Href string `json:"href,omitempty"` // Wave 2.3: every row links to where the work happens
	}

	// Wave 2.3 member gating: the feed used to show member emails to every
	// signed-in role. Members are an admin surface (/os/members), so member
	// rows are included only for viewers who could actually open that page —
	// shown == reachable, the same RBAC parity the sidebar and palette hold.
	lvl := accessAdmin
	if u := currentUser(r); u != nil {
		// Mail-only is a members-store property, not a console-user one; the
		// client-role floor inside accessLevelFor covers confinement here.
		lvl = accessLevelFor(u.Role, false)
	}

	items := []activityItem{}

	// Recent articles — with an honest verb. The ArticleService list projection
	// carries no status (and hardcoding "published" for every row was the lie
	// this feed told), so the feed reads the five newest rows directly, status
	// included: a draft row says "drafted" and links into the editor either way.
	if dbpkg.DB != nil {
		if rows, err := dbpkg.Reader().QueryContext(r.Context(),
			`SELECT slug,title,COALESCE(status,'published'),created_at FROM articles ORDER BY created_at DESC LIMIT 5`); err == nil {
			for rows.Next() {
				var slug, title, status string
				var created time.Time
				if rows.Scan(&slug, &title, &status, &created) == nil {
					verb := "Article published"
					if status == "draft" {
						verb = "Article drafted"
					}
					items = append(items, activityItem{
						Kind: "post",
						Icon: "pencil",
						Text: verb + ": " + title,
						Time: created.UTC().Format(time.RFC3339),
						Href: "/os/editor/" + slug,
					})
				}
			}
			_ = rows.Err() // best-effort feed; a read failure just yields fewer rows
			rows.Close()
		}
	}

	// Recent members (gated to admins — see above).
	if lvl >= osPathMinLevel("/os/members") && a.members != nil {
		list, err := a.members.List(r.Context(), 3)
		if err == nil {
			for _, m := range list {
				items = append(items, activityItem{
					Kind: "member",
					Icon: "user",
					Text: "Member joined: " + m.Email,
					Time: m.CreatedAt.UTC().Format(time.RFC3339),
					Href: "/os/members",
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

// handleOSCmdIndex returns what the command bar searches beyond the rail's own
// index of pages (render 03): posts to open, actions to run, and settings rows
// to jump to. Every entry is gated by the page it belongs to, the predicate the
// rail uses, so the bar never offers what this session could not open. The
// pages themselves are not listed here: the rail's index already names each
// one, and a second hand-kept list of them had drifted to hubs that no longer
// exist.
func (a *App) handleOSCmdIndex(w http.ResponseWriter, r *http.Request) {
	type cmdPost struct {
		Label   string `json:"label"`
		Slug    string `json:"slug"`
		Status  string `json:"status"`
		Updated string `json:"updated"`
		Excerpt string `json:"excerpt,omitempty"`
	}
	// An action either opens a page (Href) or runs one request (Post) and
	// reports what the server answered.
	type cmdAction struct {
		Label string `json:"label"`
		Icon  string `json:"icon"`
		Hint  string `json:"hint"`
		Href  string `json:"href,omitempty"`
		Post  string `json:"post,omitempty"`
		Done  string `json:"done,omitempty"`
		gate  string
	}
	type cmdSetting struct {
		Label string `json:"label"`
		Where string `json:"where"`
		Hint  string `json:"hint,omitempty"`
		Href  string `json:"href"`
	}

	lvl := accessAdmin
	if cfg := a.getOSSettings(r.Context()); cfg != nil {
		lvl = cfg.AccessLevel
	}
	can := func(href string) bool {
		path, _, _ := strings.Cut(href, "#")
		return lvl >= osPathMinLevel(path)
	}

	posts := []cmdPost{}
	// Read directly: the article list projection carries neither status nor
	// content. The first 2,000 characters are plenty for an excerpt.
	if can("/os/editor") && dbpkg.DB != nil {
		if rows, err := dbpkg.Reader().QueryContext(r.Context(),
			`SELECT slug, title, COALESCE(NULLIF(status,''),'published'), updated_at, substr(COALESCE(content,''),1,2000) FROM articles ORDER BY updated_at DESC LIMIT 50`); err == nil {
			for rows.Next() {
				var p cmdPost
				var updated time.Time
				var content string
				if rows.Scan(&p.Slug, &p.Label, &p.Status, &updated, &content) == nil {
					p.Updated, p.Excerpt = updated.UTC().Format(time.RFC3339), cmdExcerpt(content)
					posts = append(posts, p)
				}
			}
			_ = rows.Err() // a read cut short just offers fewer posts
			rows.Close()
		}
	}

	actions := []cmdAction{}
	for _, act := range []cmdAction{
		{Label: "New post", Icon: "pencil", Hint: "Open the block editor on a blank post", Href: "/os/editor", gate: "/os/editor"},
		{Label: "Take a backup now", Icon: "archive", Hint: "Saves a restore point and test-restores it before saying it worked", Post: "/os/api/vayukeep/backup", Done: "Backup taken and tested.", gate: "/os/vayukeep"},
		{Label: "Clear the page cache", Icon: "refresh", Hint: "Pages are rebuilt on their next visit; the sitemap, feed and robots.txt now", Post: "/os/api/storage/clear-cache", Done: "Page cache cleared.", gate: "/os/storage"},
		{Label: "Regenerate sitemap, RSS and robots.txt", Icon: "refresh", Hint: "Rebuilds the three files search engines read", Post: "/os/api/seo/regenerate", Done: "Sitemap, RSS and robots.txt rebuilt.", gate: "/os/seo"},
		{Label: "Check for security updates", Icon: "shield", Hint: "Fetches public release metadata only; nothing about your site is sent", Post: "/os/api/vayuos/security/check", Done: "Security updates checked.", gate: "/os/security"},
	} {
		if can(act.gate) {
			actions = append(actions, act)
		}
	}

	settingsList := []cmdSetting{}
	for _, e := range settingsEntries() {
		if can(e.Href) {
			settingsList = append(settingsList, cmdSetting{Label: e.Label, Where: e.Where, Hint: e.Hint, Href: e.Href})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"posts":    posts,
		"actions":  actions,
		"settings": settingsList,
	})
}

// cmdExcerpt is a post's opening words as plain text, for the command bar's
// preview: tags dropped, entities read, whitespace folded, cut on a word.
func cmdExcerpt(content string) string {
	text := strings.Join(strings.Fields(html.UnescapeString(cmdTagRe.ReplaceAllString(content, " "))), " ")
	if utf8.RuneCountInString(text) <= 160 {
		return text
	}
	cut := string([]rune(text)[:160])
	if at := strings.LastIndex(cut, " "); at > 100 {
		cut = cut[:at]
	}
	return cut + "…"
}

var cmdTagRe = regexp.MustCompile(`<[^>]*>`)

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
	// SetMany skips a key it does not know and reports success, so a page
	// offering a control for one would say "Saved" and store nothing — which is
	// how three controls on the old Members page shipped doing nothing.
	if !settings.AllKeys[body.Key] {
		writeAPIError(w, r, http.StatusBadRequest, "unknown-setting", "There is no setting called "+strconv.Quote(body.Key), "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{body.Key: body.Value}); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "settings-error", err.Error(), "")
		return
	}
	a.reloadRenderSettings(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// reloadRenderSettings re-reads the site settings and pushes them into the live
// render pipeline, then drops cached pages so a settings change takes effect
// immediately rather than on the next restart. It is the single source of truth
// for the settings→render mapping, shared by the VayuOS settings API and the
// update_site_settings MCP tool. A no-op if the settings store is unavailable.
func (a *App) reloadRenderSettings(ctx context.Context) {
	if a.siteSettings == nil {
		return
	}
	sv, err := a.siteSettings.GetAll(ctx, settings.ForPrimary())
	if err != nil {
		return
	}
	// Display timezone. Stored timestamps stay UTC; this only decides what is
	// rendered, so applying it here means a change takes effect on the next page
	// without a restart. An invalid name is ignored (the previous zone is kept)
	// rather than leaving every timestamp unrenderable.
	// An invalid name is ignored (the previous zone is kept) rather than leaving
	// every timestamp unrenderable; the settings form validates before saving.
	_ = config.SetSiteTimeZone(sv[settings.KeySiteTimezone])
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
	render.CachePurgeAll()
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
	// replaced by the rendered blocks on the first save. CreateDraft (Wave 1)
	// makes the status travel inside the queued insert itself, so the post is
	// never briefly live between enqueue and a follow-up UPDATE.
	if _, err := a.articles.CreateDraft(r.Context(), title, slug, " ", nil); err != nil {
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
