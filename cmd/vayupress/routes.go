package main

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/cors"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/health"
)

// registerRoutes wires all HTTP routes onto r. Route registration is kept in
// one place so main() stays focused on lifecycle orchestration (ADR-0048).
func (a *App) registerRoutes(r chi.Router, staticDir string) {
	r.Use(
		requestIDMiddleware,
		//lint:ignore SA1019 RealIP is used intentionally; VayuPress runs behind a
		// trusted reverse proxy (nginx/Caddy) that sets X-Forwarded-For, so the
		// spoofing concern in the deprecation note does not apply to this topology.
		chimw.RealIP,
		structuredLoggerMiddleware,
		chimw.Recoverer,
		chimw.Timeout(30*time.Second),
		securityHeadersMiddleware,
	)
	// Redirect middleware — runs after core middleware, serves 301/302 before routing.
	if a.redirectMgr != nil {
		r.Use(a.redirectMgr.Middleware)
	}
	r.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"https://" + config.Cfg.Domain},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "X-API-Key", "Authorization", "X-Request-ID", "X-CSRF-Token"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
	}).Handler)

	// Public health endpoints
	r.Get("/health", health.HandleHealthLiveness)
	r.Get("/health/live", health.HandleHealthLiveness)
	r.Get("/health/ready", health.HandleHealthReady)
	r.Get("/health/db", health.HandleHealthDB)
	r.Get("/health/meilisearch", health.HandleHealthMeilisearch)
	r.Get("/health/workers", health.HandleHealthWorkers)
	r.Get("/health/storage", health.HandleHealthStorage)
	r.Get("/health/benchmarks", a.handleHealthBenchmarks)
	r.Get("/health/migrations", health.HandleHealthMigrations)
	r.Get("/health/ethics", health.HandleHealthEthics)
	r.Get("/health/dependencies", health.HandleHealthDependencies)
	r.Get("/health/search", health.HandleHealthSearch)
	r.Get("/health/queue", health.HandleHealthQueue)

	// Static files + feeds
	r.Get("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, "sitemap.xml"))
	})
	r.Get("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, "feed.xml"))
	})
	r.Get("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, "robots.txt"))
	})
	// Dynamic per-site theme stylesheet (operator palette + custom CSS).
	// Served same-origin so it satisfies the strict style-src 'self' CSP.
	r.Get("/theme.css", a.handleThemeCSS)
	// Public theme toggle script (same-origin → script-src 'self', no nonce).
	r.Get("/static/js/theme-toggle.js", a.handleThemeToggleJS)
	r.Get("/static/js/video-facade.js", a.handleVideoFacadeJS)
	r.Get("/static/js/comments.js", a.handleCommentsJS)
	r.Get("/static/js/post-card-media.js", a.handlePostCardMediaJS)
	// Favicon routes serve the operator's uploaded brand mark when one is stored
	// (see /admin/theme branding), falling back to the embedded default per scheme.
	r.Get("/static/favicon-dark.png", a.serveFavicon(faviconDarkPNG))
	r.Get("/static/favicon-light.png", a.serveFavicon(faviconLightPNG))
	r.Get("/favicon.ico", a.serveFavicon(faviconDarkPNG))
	// cssAllowlist maps the URL parameter to its canonical on-disk name.
	// The path passed to http.ServeFile comes from the *value* (a string literal),
	// not from the user-supplied key, so there is no path-traversal vector.
	cssAllowlist := map[string]string{
		"article.css":       "article.css",
		"admin.css":         "admin.css",
		"high-contrast.css": "high-contrast.css",
		"pico.min.css":      "pico.min.css",
		"custom.css":        "custom.css",
		"signup.css":        "signup.css",
	}
	r.Get("/static/css/{file}", func(w http.ResponseWriter, r *http.Request) {
		canon, ok := cssAllowlist[chi.URLParam(r, "file")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(staticDir, "css", canon))
	})

	// Chroma syntax-highlighting stylesheet (generated from github-dark theme).
	r.Get("/static/chroma.css", a.handleChromaCSS)
	// PWA manifest and service worker.
	r.Get("/manifest.json", a.handlePWAManifest)
	r.Get("/sw.js", a.handleServiceWorker)

	// Public: same-origin media (editor image uploads). Name is strictly
	// validated in the handler — no path-traversal surface.
	r.Get("/media/{file}", a.serveMedia)

	// Public API
	r.Get("/api/v1/graphql", a.handleGraphQL)          // read-only GraphQL (GET)
	r.Post("/api/v1/graphql", a.handleGraphQL)         // read-only GraphQL (POST)
	r.Get("/api/v1/i18n/{lang}", a.handleI18nMessages) // i18n message bundle
	r.Get("/api/v1/articles", a.handleListArticles)
	r.Get("/api/v1/articles/{slug}", a.handleGetArticle)
	r.Get("/api/v1/articles/{slug}/comments", a.handleCommentList)
	r.Get("/api/v1/articles/{slug}/toc", a.handleArticleTOC)
	r.Post("/api/v1/articles/{slug}/comments", a.handleCommentSubmit)
	r.Get("/api/v1/search", a.handleSearch)
	r.Get("/api/v1/tags", a.handleListTags)
	r.Get("/api/v1/stats", a.handleStats)
	r.Get("/api/v1/collections", a.handleCollectionList)
	r.Get("/api/v1/collections/{id}", a.handleCollectionGet)
	r.Get("/api/v1/preview/verify", a.handlePreviewVerify)
	r.Get("/metrics", a.handleMetrics)
	r.Post("/csp-report", a.handleCSPReport)
	r.Get("/smoke-test", a.handleSmokeTest)
	// Webmention receiver (W3C standard, public endpoint)
	r.Post("/webmention", a.handleWebmentionReceive)
	// Newsletter (public subscribe/confirm/unsubscribe flows)
	r.Post("/api/v1/newsletter/subscribe", a.handleNewsletterSubscribe)
	r.Get("/api/v1/newsletter/confirm", a.handleNewsletterConfirm)
	r.Get("/api/v1/newsletter/unsubscribe", a.handleNewsletterUnsubscribe)
	r.Get("/api/v1/openapi.json", a.handleOpenAPISpec)

	// VayuAnalytics — public ingest + tracking script (no auth; body-capped and per-IP rate-limited).
	r.Post("/api/v1/analytics/collect", a.handleAnalyticsCollect)
	r.Get("/static/vp-analytics.js", a.handleAnalyticsScript)

	// VayuPGP Web Key Directory — public key discovery (RFC WKD advanced method).
	r.Get("/.well-known/openpgpkey/*", a.handleWKD)

	// Reader memberships (Tier 2) — public passwordless login + paywall.
	r.Get("/signup", a.handleMemberSignup)
	r.Post("/api/v1/members/login", a.handleMemberLogin)
	r.Post("/members/login", a.handleMemberLogin) // HTML form variant from the paywall
	r.Get("/members/verify", a.handleMemberVerify)
	r.Post("/members/logout", a.handleMemberLogout)
	// Premium membership: sign-in page, member portal/account, and the public
	// pricing page + tier catalogue.
	r.Get("/members", a.handleMemberSigninPage)
	r.Get("/members/account", a.handleMemberAccount)
	r.Post("/members/account", a.handleMemberAccountUpdate)
	r.Get("/pricing", a.handlePricingPage)
	r.Get("/api/v1/tiers", a.handleTiersPublic)
	// Public author profile pages.
	r.Get("/author/{id}", a.handlePublicAuthor)
	// Stripe webhook for paid upgrades — verified by signature, not API key.
	r.Post("/api/v1/stripe/webhook", a.handleStripeWebhook)

	// Protected admin + write API
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAPIKey, auth.RateLimitMiddleware)

		r.Post("/api/v1/articles", a.handleCreateArticle)
		r.Post("/api/v1/articles/bulk", a.handleBulkCreateArticles)
		r.Put("/api/v1/articles/{slug}", a.handleUpdateArticle)
		r.Delete("/api/v1/articles/{slug}", a.handleDeleteArticle)
		r.Get("/api/v1/queue", a.handleQueueStatus)
		r.Post("/api/v1/queue/replay", a.handleQueueReplay)

		// Plugin features — comments, versions, collections, newsletter, webmentions, redirects, preview.
		r.Get("/api/v1/admin/comments", a.handleCommentListAdmin)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/comments/{id}/status", a.handleCommentModerate)
		r.Get("/api/v1/admin/articles/{slug}/versions", a.handleVersionList)
		r.Get("/api/v1/admin/articles/{slug}/versions/{id}", a.handleVersionGet)
		// Editable source side-car (Admin v2 multi-format authoring).
		r.Get("/api/v1/admin/articles/{slug}/source", a.handleArticleSourceGet)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/articles/{slug}/source", a.handleArticleSourcePut)
		r.Post("/api/v1/collections", a.handleCollectionCreate)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/collections/{id}/articles", a.handleCollectionAddArticle)
		r.Get("/api/v1/admin/newsletter/subscribers", a.handleNewsletterList)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/newsletter/broadcast", a.handleNewsletterBroadcast)

		// Scheduled publishing (Tier 1).
		r.Get("/api/v1/admin/schedule", a.handleScheduleList)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/schedule", a.handleScheduleCreate)
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/admin/schedule/{id}", a.handleScheduleCancel)

		// Multi-author accounts (Tier 1) — admin-role guarded.
		r.Get("/api/v1/admin/users", a.handleUserList)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/users", a.handleUserCreate)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/users/{email}/role", a.handleUserSetRole)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/users/{email}/mailbox", a.handleAssignMailbox)
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/admin/users/{email}", a.handleUserDelete)

		// Privacy-first analytics (Tier 2).
		r.Get("/api/v1/admin/analytics", a.handleAnalytics)

		// VayuAnalytics — extended endpoints (Tier 2, protected).
		r.Get("/api/v1/analytics/overview", a.handleAnalyticsOverview)
		r.Get("/api/v1/analytics/pageviews", a.handleAnalyticsPageviews)
		r.Get("/api/v1/analytics/pages", a.handleAnalyticsPages)
		r.Get("/api/v1/analytics/referrers", a.handleAnalyticsReferrers)
		r.Get("/api/v1/analytics/browsers", a.handleAnalyticsBrowsers)
		r.Get("/api/v1/analytics/devices", a.handleAnalyticsDevices)
		r.Get("/api/v1/analytics/os", a.handleAnalyticsOS)
		r.Get("/api/v1/analytics/utm", a.handleAnalyticsUTM)
		r.Get("/api/v1/analytics/events", a.handleAnalyticsEvents)
		r.Get("/api/v1/analytics/realtime", a.handleAnalyticsRealtime)
		r.Get("/api/v1/analytics/sessions", a.handleAnalyticsSessions)
		r.Get("/api/v1/analytics/funnels", a.handleAnalyticsFunnels)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/analytics/funnels", a.handleAnalyticsCreateFunnel)
		r.Get("/api/v1/analytics/funnels/{id}", a.handleAnalyticsGetFunnel)
		r.Get("/api/v1/analytics/retention", a.handleAnalyticsRetention)
		r.Get("/api/v1/analytics/revenue", a.handleAnalyticsRevenue)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/analytics/revenue", a.handleAnalyticsRecordRevenue)

		// Goals (conversion targets) — list/create/delete + computed results.
		r.Get("/api/v1/analytics/goals", a.handleAnalyticsGoals)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/analytics/goals", a.handleAnalyticsCreateGoal)
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/analytics/goals/{id}", a.handleAnalyticsDeleteGoal)

		// Visitor journey / path-flow analysis.
		r.Get("/api/v1/analytics/journey", a.handleAnalyticsJourney)

		// Report export (CSV/JSON download) for every report.
		r.Get("/api/v1/analytics/export", a.handleAnalyticsExport)

		// AI writing assistant — local Ollama, opt-in (Tier 2).
		r.Get("/api/v1/admin/ai/status", a.handleAIStatus)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/ai/assist", a.handleAIAssist)

		// Reader memberships & paywalls (Tier 2) — admin management.
		r.Get("/api/v1/admin/members", a.handleMemberListAdmin)
		r.Get("/api/v1/admin/members/stats", a.handleMemberStats)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/members/{email}/tier", a.handleMemberSetTier)
		r.Get("/api/v1/admin/members/{email}", a.handleMemberDetail)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/members/{email}/labels", a.handleMemberLabelAdd)
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/admin/members/{email}/labels/{label}", a.handleMemberLabelRemove)
		r.Get("/api/v1/admin/articles/{slug}/access", a.handleArticleAccessGet)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/articles/{slug}/access", a.handleArticleAccessSet)

		// Membership tiers (premium plans) — list / create / update / archive.
		r.Get("/api/v1/admin/tiers", a.handleTierListAdmin)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/tiers", a.handleTierCreate)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/tiers/{id}", a.handleTierUpdate)
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/admin/tiers/{id}", a.handleTierDelete)

		// Outbound webhooks (Tier 2).
		r.Get("/api/v1/admin/webhooks", a.handleWebhookList)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/webhooks", a.handleWebhookCreate)
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/admin/webhooks/{id}", a.handleWebhookDelete)
		r.Get("/api/v1/admin/webhooks/{id}/deliveries", a.handleWebhookDeliveries)
		r.Get("/api/v1/admin/webmentions", a.handleWebmentionList)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/webmentions/{id}/status", a.handleWebmentionModerate)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/preview", a.handlePreviewIssue)
		// Editor image upload — sovereign, same-origin, magic-number validated.
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/media", a.handleMediaUpload)
		// Remote-image import — SSRF-safe fetch + re-host (ADR-0070).
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/media/import", a.handleMediaImport)
		// Embed unfurl — server-side OG metadata fetch + thumbnail import (ADR-0070).
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/embed/unfurl", a.handleEmbedUnfurl)
		// Diagram live preview — pure-Go Mermaid→SVG, content-addressed (ADR-0070).
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/diagram/preview", a.handleDiagramPreview)
		r.Get("/api/v1/admin/redirects", a.handleRedirectList)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/redirects", a.handleRedirectCreate)
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/admin/redirects/{id}", a.handleRedirectDelete)

		// Self-update — READ-ONLY check + history (ADR-0064). No web apply path.
		r.Get("/admin/api/updates/check", a.handleUpdateCheck)
		r.Get("/admin/api/updates/history", a.handleUpdateHistory)

		// Observability & correlation trace API (ADR-0053).
		r.Get("/api/v1/admin/outbox/stats", a.handleOutboxStats)
		r.Get("/api/v1/admin/outbox/events", a.handleOutboxEvents)
		r.Get("/api/v1/admin/outbox/events/{id}", a.handleOutboxEvent)
		r.Get("/api/v1/admin/trace/{correlation_id}", a.handleCorrelationTrace)

		// Structured span tracing API (ADR-0054).
		r.Get("/api/v1/admin/traces", a.handleTraceSpans)
		r.Get("/api/v1/admin/traces/{trace_id}", a.handleTraceByID)

		// Resource governance stats (ADR-0055).
		r.Get("/api/v1/admin/resource/stats", a.handleResourceStats)

		// Sandbox subprocess plugin stats (ADR-0056).
		r.Get("/api/v1/admin/sandbox/stats", a.handleSandboxStats)

		// Search reconciler: drift report (read) + rebuild (CSRF-protected write).
		r.Get("/api/v1/admin/search/drift", a.handleSearchDrift)

		// System mode state machine (Ω5/Ω6).
		r.Get("/api/v1/admin/mode", a.handleModeStatus)
		r.Get("/api/v1/admin/fault/status", a.handleFaultStatus)
		r.Get("/api/v1/admin/timeline", a.handleTimelineJSON)
		r.Get("/api/v1/admin/severity", a.handleSeverityTaxonomy)
		r.Get("/api/v1/admin/budgets", a.handleGovernanceBudgets)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/budgets/ack", a.handleGovernanceBudgetAck)

		// The classic console root permanently redirects to VayuOS (ADR-0069
		// Stage 3). The operator consoles now live inside the single VayuOS shell
		// under /os/*; the legacy /admin/* page URLs 301-redirect to them.
		r.Get("/admin", legacyRedirect())
		r.Get("/admin/backup/validate", a.handleAdminBackupValidate)

		// Legacy /admin/* operator page URLs 301-redirect into VayuOS (/os/*).
		opsRedirect := operatorLegacyRedirect()
		r.Get("/admin/modes", opsRedirect)
		r.Get("/admin/faults", opsRedirect)
		r.Get("/admin/topology", opsRedirect)
		r.Get("/admin/replay", opsRedirect)
		r.Get("/admin/policy", opsRedirect)
		r.Get("/admin/adr", opsRedirect)

		r.With(auth.CSRFTokenMiddleware).Post("/admin/benchmark", a.handleRunBenchmark)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/cache-purge", a.handleAdminCachePurge)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/vacuum", a.handleAdminVacuum)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/mode/transition", a.handleModeTransition)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/fault/simulate", a.handleFaultSimulate)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/replay/job", a.handleReplayJob)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/search/reindex", a.handleSearchReindex)

		// Theme & site settings editor.
		r.Get("/admin/theme", a.handleThemeGet)
		r.Get("/admin/theme/export", a.handleThemeExport)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/theme", a.handleThemeSave)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/theme/reset", a.handleThemeReset)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/theme/favicon", a.handleFaviconUpload)

		// Tier 4: real-time SSE event stream.
		r.Get("/api/v1/stream", a.handleEventStream)
		// Tier 4: email template management.
		r.Get("/api/v1/admin/email-templates", a.handleEmailTemplateList)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/email-templates/{kind}", a.handleEmailTemplateSet)
		// Tier 4: i18n catalog management.
		r.Get("/api/v1/admin/i18n", a.handleI18nLanguageList)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/i18n/{lang}", a.handleI18nLanguageSet)

		// Theme Studio — design-token system (Phase 5).
		r.Get("/api/v1/admin/theme/presets", a.handleThemePresets)
		r.Get("/api/v1/admin/theme/tokens", a.handleThemeTokens)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/theme/preview", a.handleThemePreview)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/theme/apply", a.handleThemeApply)

		r.HandleFunc("/debug/pprof/", a.pprofHandler)
		r.HandleFunc("/debug/pprof/cmdline", a.pprofHandler)
		r.HandleFunc("/debug/pprof/profile", a.pprofHandler)
		r.HandleFunc("/debug/pprof/symbol", a.pprofHandler)
		r.HandleFunc("/debug/pprof/trace", a.pprofHandler)
		r.HandleFunc("/debug/pprof/*", a.pprofHandler)
	})

	// Admin v2 was removed in v1.6.0 (ADR-0069 Stage 3). Its old URLs permanently
	// redirect into VayuOS; the redirect handler maps /admin/v2[/...] -> /os[/...].
	v2Redirect := legacyRedirect()
	r.Get("/admin/v2", v2Redirect)
	r.Handle("/admin/v2/*", v2Redirect)

	// VayuOS — the single admin, mounted at /os (ADR-0068, ADR-0069).
	a.registerAdminOSUIRoutes(r)

	r.Get("/", a.handleHome)
	// Public taxonomy pages — the topic index and per-tag listings. Registered
	// before the single-segment "/{slug}" catch-all so "/tags" and "/tags/{tag}"
	// resolve here instead of falling through to a 404 (the two-segment form
	// previously matched no route).
	r.Get("/tags", a.handleTagIndex)
	r.Get("/tags/{tag}", a.handleTagPage)
	r.NotFound(a.handleNotFound)
	r.Get("/{slug}", a.handleArticlePage)
}
