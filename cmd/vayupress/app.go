package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/sony/gobreaker"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/queue"
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

	// Meilisearch
	meiliCB *gobreaker.CircuitBreaker

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
// Meilisearch (circuit-breaker guarded)
// =============================================================================

func (a *App) initMeilisearchCB() {
	a.meiliCB = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: "meilisearch", MaxRequests: 3, Interval: 10 * time.Second, Timeout: 30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= 3 && float64(counts.TotalFailures)/float64(counts.Requests) >= 0.60
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "meili-cb", Msg: fmt.Sprintf("%s → %s", from, to)})
		},
	})
}

func (a *App) meiliDo(method, path string, body interface{}) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, config.Cfg.MeiliHost+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if config.Cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.Cfg.MeiliMasterKey)
	}
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("meili %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (a *App) configureMeilisearch() {
	_ = a.meiliDo("PATCH", "/indexes/articles/settings", map[string]interface{}{
		"rankingRules":         []string{"words", "proximity", "attribute", "sort", "exactness"},
		"searchableAttributes": []string{"title", "tags", "content"},
		"filterableAttributes": []string{"tags", "created_at"},
		"sortableAttributes":   []string{"created_at", "updated_at"},
	})
}

func (a *App) indexArticle(art dbpkg.Article) {
	if a.meiliCB == nil {
		return
	}
	doc := map[string]interface{}{
		"id": art.ID, "title": art.Title, "slug": art.Slug,
		"content":    htmlTagRe.ReplaceAllString(a.policy.Sanitize(art.Content), ""),
		"tags":       art.Tags,
		"created_at": art.CreatedAt.Unix(),
	}
	_, err := a.meiliCB.Execute(func() (interface{}, error) {
		return nil, a.meiliDo("POST", "/indexes/articles/documents", []map[string]interface{}{doc})
	})
	if err != nil {
		atomic.AddInt64(&metrics.MetricMeiliErrors, 1)
	}
}

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
	body, _ := json.Marshal(map[string]interface{}{
		"host": config.Cfg.Domain, "key": config.Cfg.IndexNowKey,
		"keyLocation": "https://" + config.Cfg.Domain + "/.well-known/" + config.Cfg.IndexNowKey + ".txt",
		"urlList":     []string{"https://" + config.Cfg.Domain + "/" + slug},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.indexnow.org/indexnow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
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
