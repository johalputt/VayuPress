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
		chimw.RealIP,
		structuredLoggerMiddleware,
		chimw.Recoverer,
		chimw.Timeout(30*time.Second),
		securityHeadersMiddleware,
	)
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
	r.Get("/static/favicon-dark.png", servePNG(faviconDarkPNG))
	r.Get("/static/favicon-light.png", servePNG(faviconLightPNG))
	r.Get("/favicon.ico", servePNG(faviconDarkPNG))
	r.Get("/static/css/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := chi.URLParam(r, "file")
		if !map[string]bool{"article.css": true, "admin.css": true, "high-contrast.css": true, "pico.min.css": true, "custom.css": true}[file] {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(staticDir, "css", file))
	})

	// Public API
	r.Get("/api/v1/articles", a.handleListArticles)
	r.Get("/api/v1/articles/{slug}", a.handleGetArticle)
	r.Get("/api/v1/search", a.handleSearch)
	r.Get("/api/v1/tags", a.handleListTags)
	r.Get("/api/v1/stats", a.handleStats)
	r.Get("/metrics", a.handleMetrics)
	r.Get("/smoke-test", a.handleSmokeTest)
	r.Get("/api/v1/openapi.json", a.handleOpenAPISpec)

	// Protected admin + write API
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAPIKey, auth.RateLimitMiddleware)

		r.Post("/api/v1/articles", a.handleCreateArticle)
		r.Post("/api/v1/articles/bulk", a.handleBulkCreateArticles)
		r.Put("/api/v1/articles/{slug}", a.handleUpdateArticle)
		r.Delete("/api/v1/articles/{slug}", a.handleDeleteArticle)
		r.Get("/api/v1/queue", a.handleQueueStatus)
		r.Post("/api/v1/queue/replay", a.handleQueueReplay)

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
		r.With(auth.CSRFTokenMiddleware).Post("/admin/theme", a.handleThemeSave)

		r.HandleFunc("/debug/pprof/", a.pprofHandler)
		r.HandleFunc("/debug/pprof/cmdline", a.pprofHandler)
		r.HandleFunc("/debug/pprof/profile", a.pprofHandler)
		r.HandleFunc("/debug/pprof/symbol", a.pprofHandler)
		r.HandleFunc("/debug/pprof/trace", a.pprofHandler)
		r.HandleFunc("/debug/pprof/*", a.pprofHandler)
	})

	r.Get("/", a.handleHome)
	r.NotFound(a.handleNotFound)
	r.Get("/{slug}", a.handleArticlePage)
}
