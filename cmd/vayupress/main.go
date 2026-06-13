// VayuPress — main.go  v1.0.0-p19
// Bootstrap, route wiring, and graceful shutdown only.
// Domain logic lives in internal/* packages (ADR-0045 – ADR-0050).
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/events"
	"github.com/johalputt/vayupress/internal/health"
	"github.com/johalputt/vayupress/internal/httputil"
	"github.com/johalputt/vayupress/internal/lifecycle"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/outbox"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/resource"
	"github.com/johalputt/vayupress/internal/search"
	"github.com/johalputt/vayupress/internal/trace"
)

var Version = "1.0.0-p26"
var bootTime = time.Now()

// Immutable package-level values (compiled once, never mutated).
var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// =============================================================================
// Magic-number file-type verification
// =============================================================================

var allowedMagicNumbers = map[string][]byte{
	"image/jpeg":      {0xFF, 0xD8, 0xFF},
	"image/png":       {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
	"image/gif":       {0x47, 0x49, 0x46, 0x38},
	"image/webp":      {0x52, 0x49, 0x46, 0x46},
	"application/pdf": {0x25, 0x50, 0x44, 0x46},
}

func verifyMagicNumber(data []byte) (string, error) {
	for mime, sig := range allowedMagicNumbers {
		if len(data) >= len(sig) && bytes.Equal(data[:len(sig)], sig) {
			return mime, nil
		}
	}
	return "", fmt.Errorf("file type not allowed: magic number does not match any permitted media type")
}

// =============================================================================
// Response helpers (thin wrappers over internal/httputil)
// =============================================================================

func writeJSON(w http.ResponseWriter, r *http.Request, code int, v interface{}) {
	httputil.WriteJSON(w, code, v)
}

func writeAPIError(w http.ResponseWriter, r *http.Request, code int, errCode, msg, docsURL string) {
	reqID := ""
	if r != nil {
		reqID = getRequestID(r)
	}
	httputil.WriteError(w, code, errCode, msg, reqID, docsURL)
}

func readJSONDirect(r *http.Request, v interface{}) error {
	return httputil.DecodeJSON(r, v)
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// =============================================================================
// Sitemap / RSS / robots
// =============================================================================

func generateSitemap() {
	rows, err := dbpkg.DB.Query(`SELECT slug,updated_at FROM articles ORDER BY updated_at DESC LIMIT 50000`)
	if err != nil {
		return
	}
	defer rows.Close()
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for rows.Next() {
		var slug string
		var updated time.Time
		rows.Scan(&slug, &updated)
		fmt.Fprintf(&sb, "<url><loc>https://%s/%s</loc><lastmod>%s</lastmod></url>", config.Cfg.Domain, slug, updated.Format("2006-01-02"))
	}
	sb.WriteString("</urlset>")
	render.CacheWrite("sitemap.xml", sb.String()) //nolint:errcheck
}

func generateRSS() {
	rows, err := dbpkg.DB.Query(`SELECT title,slug,content,created_at FROM articles ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return
	}
	defer rows.Close()
	var items strings.Builder
	for rows.Next() {
		var title, slug, content string
		var created time.Time
		rows.Scan(&title, &slug, &content, &created)
		plain := htmlTagRe.ReplaceAllString(bluemonday.StrictPolicy().Sanitize(content), "")
		if len(plain) > 500 {
			plain = plain[:500] + "..."
		}
		fmt.Fprintf(&items, "<item><title><![CDATA[%s]]></title><link>https://%s/%s</link><guid isPermaLink=\"true\">https://%s/%s</guid><pubDate>%s</pubDate><description><![CDATA[%s]]></description></item>",
			title, config.Cfg.Domain, slug, config.Cfg.Domain, slug, created.Format(time.RFC1123Z), plain)
	}
	rss := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>%s</title><link>https://%s</link><description>%s</description>%s</channel></rss>`,
		config.Cfg.Domain, config.Cfg.Domain, config.Cfg.Domain, items.String())
	render.CacheWrite("feed.xml", rss) //nolint:errcheck
}

func generateRobots() {
	render.CacheWrite("robots.txt", fmt.Sprintf("User-agent: *\nAllow: /\nDisallow: /api/\nDisallow: /admin\n\nSitemap: https://%s/sitemap.xml\n", config.Cfg.Domain)) //nolint:errcheck
}

// =============================================================================
// main
// =============================================================================

func main() {
	log.SetFlags(0)
	logging.LogInfo("main", fmt.Sprintf("VayuPress v%s starting — P1–P26 active", Version))
	config.Load()
	logging.LogInfo("main", fmt.Sprintf("domain=%s port=%s workers=%d config_version=%s maintenance=%v",
		config.Cfg.Domain, config.Cfg.Port, config.Cfg.WorkerCount, config.ConfigVersion, config.Cfg.MaintenanceMode))

	// Initialise App — the single owner of all mutable runtime state (ADR-0046).
	a := &App{
		policy:         bluemonday.UGCPolicy(),
		outboundClient: &http.Client{Timeout: 5 * time.Second, Transport: ssrfSafeTransport()},
		pluginRegistry: plugins.NewRegistry(),
		eventBus:       events.NewBus(),
	}
	a.pluginManager = plugins.New(a.pluginRegistry)

	auth.InitCSRFSecret()
	initPprofMux()
	auth.StartBucketSweeper(context.Background())

	staticDir := config.EnvOr("STATIC_DIR", "/var/www/vayupress/static")
	render.WriteCSSAssets(staticDir)

	docsDir := config.EnvOr("VAYU_DOCS_DIR", "/var/www/vayupress/docs")
	os.MkdirAll(docsDir, 0755)
	writeADRs(docsDir)

	if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
		a.pluginManager.Start(plugins.DefaultPoolSize, plugins.DefaultQueueDepth)
	}

	if err := dbpkg.Init(); err != nil {
		logging.LogError("main", "DB init failed", err.Error())
		os.Exit(1)
	}
	logging.LogInfo("main", "database ready — WAL adaptive + migrations + checksum drift verified (ADR-0033/0034)")

	// Resource governance — limiters and watchdog (ADR-0055).
	resource.Register("articles.write", config.Cfg.WorkerCount*4)
	resource.Register("plugin.exec", config.Cfg.PluginMaxConcurrent)
	resource.Global = resource.NewWatchdog(250 * time.Millisecond)

	// Wire article service with repository pattern (ADR-0050).
	a.articles = &api.ArticleService{
		Repo:  dbpkg.NewArticleRepo(dbpkg.DB),
		Queue: queue.NewSQLiteWriter(dbpkg.DB, config.Cfg.QueueHardLimit),
		StorageCheckFn: func() (int64, int64) {
			return dbpkg.StorageUsedBytes(), dbpkg.StorageQuotaBytes()
		},
	}

	// Wire search service (ADR-0050).
	a.search = search.NewMeiliService(a.outboundClient, dbpkg.DB)

	if n, err := dbpkg.DB.Exec(`UPDATE write_jobs SET status='pending' WHERE status='processing'`); err == nil {
		if rows, _ := n.RowsAffected(); rows > 0 {
			logging.LogInfo("main", fmt.Sprintf("recovered %d stale processing jobs", rows))
		}
	}

	dbpkg.InitStorageCachedBytes()
	dbpkg.StartWALCheckpointGoroutine(queue.DoneCh)
	dbpkg.StartStuckJobReaper(queue.DoneCh)
	a.startMetricsSnapshotCollector()

	// Wire queue injections.
	queue.RenderFn = render.RenderArticle
	queue.SetCacheWriteFn(func(relPath, content string) {
		render.CacheWrite(relPath, content) //nolint:errcheck
	})
	queue.EventBus = a.eventBus

	// Register domain event handlers after all services are wired (ADR-0050).
	a.registerEventHandlers()

	// Wire health package injections.
	health.Version = Version
	health.ConfigVersion = config.ConfigVersion
	health.BootTime = bootTime
	health.MeiliDoFn = func(_, _ string, _ interface{}) error {
		return a.search.Ping(context.Background())
	}
	health.WriteJSON = writeJSON
	health.WriteAPIError = writeAPIError

	// Wire render package version.
	render.Version = Version

	// Meilisearch startup — search service handles the circuit breaker internally.
	if search.WaitReady(context.Background(), a.search, 12) {
		logging.LogInfo("main", "Meilisearch ready")
		search.ConfigureIndex(context.Background(), a.search)
	} else {
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "main", Msg: "Meilisearch unavailable — SQLite search fallback active"})
	}

	go func() {
		logging.LogInfo("cache-warm", "starting...")
		render.WarmCache(api.SplitTags)
		generateSitemap()
		generateRSS()
		generateRobots()
		logging.LogInfo("cache-warm", "complete")
	}()

	// Lifecycle manager — ordered startup and shutdown (ADR-0051).
	lc := lifecycle.New()
	lc.Register("queue-workers", func(_ context.Context) error {
		queue.StartWorkerPool(&metrics.WorkerWg)
		logging.LogInfo("main", fmt.Sprintf("started %d write workers (maintenance_mode=%v)", config.Cfg.WorkerCount, config.Cfg.MaintenanceMode))
		return nil
	}, nil)

	// Outbox relay — dispatches events written atomically with article mutations (ADR-0051/0052/0053).
	outboxRelay := outbox.NewRelay(dbpkg.DB, func(ctx context.Context, _ string, payload []byte) error {
		var env events.Envelope
		if err := json.Unmarshal(payload, &env); err != nil {
			return err
		}
		// Thread correlation through dispatch context for downstream log correlation.
		ctx = trace.WithCorrelationID(ctx, env.CorrelationID)
		ctx = trace.WithCausationID(ctx, env.CausationID)
		ctx, dispatchSpan := trace.Start(ctx, "outbox.dispatch."+env.EventType)
		dispatchSpan.SetAttribute("event_id", env.EventID)
		dispatchSpan.SetAttribute("event_type", env.EventType)
		dispatchSpan.SetAttribute("causation_id", env.CausationID)
		logging.LogJSON(logging.LogFields{
			Level: "info", Component: "outbox",
			CorrelationID: env.CorrelationID,
			CausationID:   env.CausationID,
			Msg:           "dispatching " + env.EventType + " event_id=" + env.EventID,
		})
		switch env.EventType {
		case "article.created.v1":
			var ev events.ArticleCreated
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			a.eventBus.Publish(ctx, ev)
		case "article.updated.v1":
			var ev events.ArticleUpdated
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			a.eventBus.Publish(ctx, ev)
		case "article.deleted.v1":
			var ev events.ArticleDeleted
			if err := json.Unmarshal(env.Payload, &ev); err != nil {
				return err
			}
			a.eventBus.Publish(ctx, ev)
		default:
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "outbox", CorrelationID: env.CorrelationID, Msg: "unknown event type: " + env.EventType})
		}
		dispatchSpan.End()
		return nil
	}, queue.DoneCh)
	lc.Register("outbox-relay", func(_ context.Context) error {
		outboxRelay.Start()
		logging.LogInfo("main", "outbox relay started")
		return nil
	}, nil)

	if err := lc.Start(context.Background()); err != nil {
		logging.LogError("main", "lifecycle start failed", err.Error())
		os.Exit(1)
	}

	logging.LogInfo("main", fmt.Sprintf("startup complete in %dms", time.Since(bootTime).Milliseconds()))

	r := chi.NewRouter()
	a.registerRoutes(r, staticDir)

	srv := &http.Server{
		Addr:         ":" + config.Cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logging.LogInfo("main", fmt.Sprintf("received %v — graceful shutdown", sig))

		// Phase 1: stop ingress
		httpCtx, httpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer httpCancel()
		if err := srv.Shutdown(httpCtx); err != nil {
			logging.LogError("main", "HTTP shutdown", err.Error())
		}
		logging.LogInfo("main", "phase 1 complete — ingress stopped")

		// Phase 2: drain write queue (45s)
		close(queue.DoneCh)
		drainDone := make(chan struct{})
		go func() { metrics.WorkerWg.Wait(); close(drainDone) }()
		select {
		case <-drainDone:
			logging.LogInfo("main", "phase 2 complete — write queue drained")
		case <-time.After(45 * time.Second):
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "main", Msg: "phase 2 timeout (45s) — in-flight jobs retried on next startup"})
		}

		// Phase 3: stop plugin pool + resource watchdog
		if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
			a.pluginManager.Shutdown()
		}
		if resource.Global != nil {
			resource.Global.Stop()
		}
		logging.LogInfo("main", "phase 3 complete — plugin pool + watchdog stopped")

		// Phase 4: WAL checkpoint before close
		if dbpkg.DB != nil {
			if _, err := dbpkg.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				logging.LogError("main", "WAL checkpoint on shutdown", err.Error())
			} else {
				logging.LogInfo("main", "phase 4 complete — WAL checkpointed")
			}
		}

		// Phase 5: flush final metrics snapshot
		a.collectAdminMetrics()
		logging.LogInfo("main", "phase 5 complete — metrics flushed")

		// Phase 6: close database
		if dbpkg.DB != nil {
			if err := dbpkg.DB.Close(); err != nil {
				logging.LogError("main", "DB close", err.Error())
			} else {
				logging.LogInfo("main", "phase 6 complete — database closed")
			}
		}

		logging.LogInfo("main", "shutdown complete — goodbye")
		os.Exit(0)
	}()

	logging.LogInfo("main", fmt.Sprintf("listening on :%s (v%s)", config.Cfg.Port, Version))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logging.LogError("main", "ListenAndServe error", err.Error())
		os.Exit(1)
	}
}

// suppress unused import for verifyMagicNumber (kept for media upload endpoints)
var _ = verifyMagicNumber
