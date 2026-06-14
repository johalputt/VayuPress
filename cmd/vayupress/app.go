package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/events"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/search"
)

// App holds all mutable runtime state. Handlers are methods on *App so that
// they depend on explicit fields rather than package-level globals (ADR-0046).
type App struct {
	// HTTP
	outboundClient *http.Client

	// Sanitization
	policy *bluemonday.Policy

	// Article business logic
	articles *api.ArticleService

	// Search service (Meilisearch + SQLite fallback)
	search search.Service

	// Domain event bus
	eventBus *events.Bus

	// Plugin subsystem
	pluginRegistry *plugins.Registry
	pluginManager  *plugins.Manager

	// Vacuum lifecycle
	vacuumMu      sync.Mutex
	vacuumLastRun time.Time

	// Smoke test
	smokeTestMutex sync.Mutex

	// Admin metrics snapshot cache
	metricsSnapshot atomic.Value

	// Benchmark state
	lastBenchmark    *benchmarkResult
	lastBenchmarkMu  sync.Mutex
	benchmarkRunning int32
}

// RegisterHook registers a plugin hook with the App's plugin registry.
func (a *App) RegisterHook(event string, fn plugins.HookFunc) {
	a.pluginRegistry.Register(event, fn)
}

// FireHook dispatches an event to the App's plugin manager (noop if VAYU_PLUGINS_ENABLED != true).
func (a *App) FireHook(event string, payload map[string]interface{}) {
	if os.Getenv("VAYU_PLUGINS_ENABLED") != "true" {
		return
	}
	a.pluginManager.Fire(event, payload)
}

// =============================================================================
// CDN / search-engine side effects
// =============================================================================

func (a *App) purgeCloudflare(slug string) {
	if config.Cfg.CFZoneID == "" || config.Cfg.CFAPIToken == "" {
		return
	}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/purge_cache", config.Cfg.CFZoneID)
	body, _ := json.Marshal(map[string][]string{"files": {"https://" + config.Cfg.Domain + "/" + slug}})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.Cfg.CFAPIToken)
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

func (a *App) pingIndexNow(slug string) {
	if config.Cfg.IndexNowKey == "" {
		return
	}
	// Governance: IndexNow is an outbound mutation announcement. Suppress it in
	// any mode where the system has withdrawn from normal write/federation
	// activity, and journal the suppression so the timeline stays truthful.
	if m := mode.Global.Current(); m == mode.ModeReadOnly || m == mode.ModeQuarantined || m == mode.ModeMaintenance {
		logging.LogJSON(logging.LogFields{
			Level: "info", Component: "indexnow", Severity: "info",
			Msg: "submission suppressed by system mode", Path: slug, Error: string(m),
		})
		return
	}
	body, _ := json.Marshal(map[string]interface{}{
		"host": config.Cfg.Domain, "key": config.Cfg.IndexNowKey,
		"keyLocation": "https://" + config.Cfg.Domain + "/.well-known/" + config.Cfg.IndexNowKey + ".txt",
		"urlList":     []string{"https://" + config.Cfg.Domain + "/" + slug},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.indexnow.org/indexnow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		logging.LogError("indexnow", "submission failed: "+slug, err.Error())
		return
	}
	defer resp.Body.Close()
	// IndexNow returns 200/202 on accept; surface anything else for operators.
	if resp.StatusCode >= 300 {
		logging.LogError("indexnow", "submission rejected: "+slug, fmt.Sprintf("status %d", resp.StatusCode))
		return
	}
	logging.LogInfo("indexnow", "submitted "+slug)
}

// =============================================================================
// Domain event subscriptions
// =============================================================================

// registerEventHandlers wires the article mutation event subscribers. Called
// after all services are initialised in main().
func (a *App) registerEventHandlers() {
	bus := a.eventBus

	// Search index + CDN purge + IndexNow on create / update.
	bus.Subscribe(events.ArticleCreated{}, func(ctx context.Context, ev interface{}) {
		e := ev.(events.ArticleCreated)
		go func() {
			var art dbpkg.Article
			var tagsStr string
			if dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, e.Slug).
				Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt) == nil {
				art.Tags = api.SplitTags(tagsStr)
				a.search.Index(ctx, art.ID, art.Title, art.Slug,
					htmlTagRe.ReplaceAllString(a.policy.Sanitize(art.Content), ""),
					art.Tags, art.CreatedAt.Unix())
			}
			render.CachePurge(e.Slug, nil, generateSitemap, generateRSS, generateRobots)
			a.purgeCloudflare(e.Slug)
			a.pingIndexNow(e.Slug)
		}()
		a.FireHook("article.create", map[string]interface{}{"slug": e.Slug, "id": e.ID})
	})

	bus.Subscribe(events.ArticleUpdated{}, func(ctx context.Context, ev interface{}) {
		e := ev.(events.ArticleUpdated)
		go func() {
			var art dbpkg.Article
			var tagsStr string
			if dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, e.Slug).
				Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt) == nil {
				art.Tags = api.SplitTags(tagsStr)
				a.search.Index(ctx, art.ID, art.Title, art.Slug,
					htmlTagRe.ReplaceAllString(a.policy.Sanitize(art.Content), ""),
					art.Tags, art.CreatedAt.Unix())
			}
			render.CachePurge(e.Slug, nil, generateSitemap, generateRSS, generateRobots)
			a.purgeCloudflare(e.Slug)
			a.pingIndexNow(e.Slug)
		}()
		a.FireHook("article.update", map[string]interface{}{"slug": e.Slug})
	})

	bus.Subscribe(events.ArticleDeleted{}, func(ctx context.Context, ev interface{}) {
		e := ev.(events.ArticleDeleted)
		go func() {
			a.search.Delete(ctx, e.ID)
			a.purgeCloudflare(e.Slug)
		}()
		a.FireHook("article.delete", map[string]interface{}{"slug": e.Slug, "id": e.ID})
	})
}

// =============================================================================
// Admin metrics snapshot
// =============================================================================

type adminMetricsSnapshot struct {
	TotalArticles  int
	PendingJobs    int
	FailedJobs     int
	CompletedJobs  int
	StorageBytes   int64
	QuotaBytes     int64
	StoragePct     float64
	WorkersAlive   int64
	CacheHitRatio  float64
	UptimeSeconds  float64
	HTTPP95        int64
	WriteP99       int64
	RenderP99      int64
	RecentArticles []adminRecentArticle
	SnapshotAt     time.Time
}

type adminRecentArticle struct {
	Title     string
	Slug      string
	CreatedAt time.Time
}

func (a *App) collectAdminMetrics() {
	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	row := dbpkg.DB.QueryRow(`SELECT (SELECT COUNT(1) FROM articles),SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) FROM write_jobs`)
	row.Scan(&snap.TotalArticles, &snap.PendingJobs, &snap.FailedJobs, &snap.CompletedJobs)
	snap.StorageBytes = dbpkg.StorageUsedBytes()
	snap.QuotaBytes = dbpkg.StorageQuotaBytes()
	if snap.QuotaBytes > 0 {
		snap.StoragePct = float64(snap.StorageBytes) / float64(snap.QuotaBytes) * 100
	}
	snap.WorkersAlive = atomic.LoadInt64(&metrics.WorkerLiveness)
	snap.CacheHitRatio = metrics.CacheHitRatio()
	snap.UptimeSeconds = time.Since(bootTime).Seconds()
	snap.HTTPP95 = metrics.HTTPLatency.Percentile(95)
	snap.WriteP99 = metrics.QueueJobLatency.Percentile(99)
	snap.RenderP99 = metrics.RenderLatency.Percentile(99)
	rows, err := dbpkg.DB.Query(`SELECT title,slug,created_at FROM articles ORDER BY created_at DESC LIMIT 15`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ra adminRecentArticle
			rows.Scan(&ra.Title, &ra.Slug, &ra.CreatedAt)
			snap.RecentArticles = append(snap.RecentArticles, ra)
		}
	}
	a.metricsSnapshot.Store(snap)
}

func (a *App) startMetricsSnapshotCollector() {
	a.collectAdminMetrics()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-queue.DoneCh:
				return
			case <-ticker.C:
				a.collectAdminMetrics()
			}
		}
	}()
}

func (a *App) getAdminSnapshot() *adminMetricsSnapshot {
	if v := a.metricsSnapshot.Load(); v != nil {
		return v.(*adminMetricsSnapshot)
	}
	a.collectAdminMetrics()
	if v := a.metricsSnapshot.Load(); v != nil {
		return v.(*adminMetricsSnapshot)
	}
	return &adminMetricsSnapshot{SnapshotAt: time.Now()}
}
