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

	// Reader memberships (Tier 2) — public passwordless login + paywall.
	r.Post("/api/v1/members/login", a.handleMemberLogin)
	r.Post("/members/login", a.handleMemberLogin) // HTML form variant from the paywall
	r.Get("/members/verify", a.handleMemberVerify)
	r.Post("/members/logout", a.handleMemberLogout)
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
		r.With(auth.CSRFTokenMiddleware).Delete("/api/v1/admin/users/{email}", a.handleUserDelete)

		// Privacy-first analytics (Tier 2).
		r.Get("/api/v1/admin/analytics", a.handleAnalytics)

		// AI writing assistant — local Ollama, opt-in (Tier 2).
		r.Get("/api/v1/admin/ai/status", a.handleAIStatus)
		r.With(auth.CSRFTokenMiddleware).Post("/api/v1/admin/ai/assist", a.handleAIAssist)

		// Reader memberships & paywalls (Tier 2) — admin management.
		r.Get("/api/v1/admin/members", a.handleMemberListAdmin)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/members/{email}/tier", a.handleMemberSetTier)
		r.Get("/api/v1/admin/articles/{slug}/access", a.handleArticleAccessGet)
		r.With(auth.CSRFTokenMiddleware).Put("/api/v1/admin/articles/{slug}/access", a.handleArticleAccessSet)

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

		r.Get("/admin", a.handleAdminDashboard)
		r.Get("/admin/adr", a.handleAdminADR)
		r.Get("/admin/backup/validate", a.handleAdminBackupValidate)

		// Interactive operator console pages (Ω9).
		r.Get("/admin/modes", a.handleModesPage)
		r.Get("/admin/faults", a.handleFaultPage)
		r.Get("/admin/topology", a.handleTopologyPage)
		r.Get("/admin/replay", a.handleReplayPage)
		r.Get("/admin/policy", a.handlePolicyPage)

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

		r.HandleFunc("/debug/pprof/", a.pprofHandler)
		r.HandleFunc("/debug/pprof/cmdline", a.pprofHandler)
		r.HandleFunc("/debug/pprof/profile", a.pprofHandler)
		r.HandleFunc("/debug/pprof/symbol", a.pprofHandler)
		r.HandleFunc("/debug/pprof/trace", a.pprofHandler)
		r.HandleFunc("/debug/pprof/*", a.pprofHandler)
	})

	// Modern admin UI (/admin/v2) — vendored, CSP-compliant, non-breaking (ADR-0065).
	// It manages its own public/auth split internally.
	a.registerAdminUIRoutes(r)

	// Admin v3 — next-generation UI surpassing Ghost/WordPress/Substack (ADR-0068).
	a.registerAdminV3UIRoutes(r)

	r.Get("/", a.handleHome)
	r.NotFound(a.handleNotFound)
	r.Get("/{slug}", a.handleArticlePage)
}
