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

	"github.com/johalputt/vayupress/internal/ads"
	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/collections"
	"github.com/johalputt/vayupress/internal/comments"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/emailtmpl"
	"github.com/johalputt/vayupress/internal/events"
	"github.com/johalputt/vayupress/internal/graphqlapi"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/newsletter"
	"github.com/johalputt/vayupress/internal/payments"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/preview"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/redirects"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/scheduler"
	"github.com/johalputt/vayupress/internal/search"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/social"
	"github.com/johalputt/vayupress/internal/update"
	"github.com/johalputt/vayupress/internal/users"
	vkernel "github.com/johalputt/vayupress/internal/vayuos/kernel"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
	"github.com/johalputt/vayupress/internal/vayuos/secwatch"
	"github.com/johalputt/vayupress/internal/versions"
	"github.com/johalputt/vayupress/internal/webhooks"
	"github.com/johalputt/vayupress/internal/webmention"
	"github.com/johalputt/vayupress/internal/ws"
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

	// Search reindex state (Ω-search reconciler)
	reindexRunning int32
	lastReindex    *reindexResult
	lastReindexMu  sync.Mutex

	// Site/theme settings store (migration 006)
	siteSettings *settings.Store

	// API key management (migration 041): VayuPress's own rotatable bearer
	// tokens, plus encrypted-at-rest third-party service credentials.
	apiKeys *apikeys.Store
	secrets *secrets.Store

	// Plugin stores (wired at startup when DB is ready)
	commentStore    *comments.Store
	versionStore    *versions.Store
	collectionStore *collections.Store
	newsletterStore *newsletter.Store
	webmentionStore *webmention.Store
	redirectMgr     *redirects.Manager
	previewSigner   *preview.Signer
	updateStore     *update.Store

	// Email delivery (Tier 1) — no-op when SMTP is unconfigured.
	mailer *email.Sender

	// Scheduled publishing (Tier 1).
	scheduler *scheduler.Store

	// Multi-author accounts + login sessions (Tier 1).
	userStore *users.Store
	sessions  *auth.SessionStore

	// Privacy-first analytics (Tier 2).
	analytics *analytics.Store

	// Outbound webhooks (Tier 2).
	webhooks *webhooks.Store

	// Social auto-posting (Tier 2).
	social *social.Poster

	// AI writing assistant — local Ollama, opt-in (Tier 2).
	aiAssist *aiassist.Client

	// Reader memberships & paywalls (Tier 2).
	members *members.Store

	// Monetization (Tier 5): sovereign payment order ledger + gateway registry,
	// and the activation-gated advertising surface. Both are off by default and
	// only act once the operator enables the corresponding module.
	payments *payments.Store
	ads      *ads.Store

	// Read-only public GraphQL API (Tier 4).
	graphql *graphqlapi.Service
	// Real-time collaboration / live admin event stream (Tier 4).
	collab *ws.Hub
	// Operator-customisable email templates (Tier 4).
	emailTmpl *emailtmpl.Store
	// UI/content internationalisation (Tier 4).
	i18n *i18n.Catalog

	// VayuOS — native control layer (Phase 2): mail sovereignty + PGP privacy.
	vayuKernel *vkernel.Bus
	vayuHealth *vkernel.HealthMonitor
	vayuPGP    *vpgp.Engine
	vayuMail   *vmail.Engine
	vayuSec    *secwatch.Watcher
}

// startScheduler runs the background ticker that promotes due scheduled posts to
// live articles via the normal create pipeline. Disabled when SchedulerTickSec<=0.
func (a *App) startScheduler(done <-chan struct{}) {
	tick := config.Cfg.SchedulerTickSec
	if tick <= 0 || a.scheduler == nil {
		logging.LogInfo("scheduler", "scheduled publishing disabled (SCHEDULER_TICK_SEC<=0)")
		return
	}
	logging.LogInfo("scheduler", fmt.Sprintf("scheduled publishing active — tick=%ds", tick))
	go func() {
		ticker := time.NewTicker(time.Duration(tick) * time.Second)
		defer ticker.Stop()
		a.publishDuePosts() // run once at startup to catch anything missed while down
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				a.publishDuePosts()
			}
		}
	}()
}

// publishDuePosts promotes every post whose publish_at has arrived.
func (a *App) publishDuePosts() {
	ctx := context.Background()
	due, err := a.scheduler.Due(ctx, time.Now(), 50)
	if err != nil {
		logging.LogError("scheduler", "due query failed", err.Error())
		return
	}
	for _, p := range due {
		if _, err := a.articles.Create(ctx, p.Title, p.Slug, p.Content, p.Tags); err != nil {
			logging.LogError("scheduler", "publish failed: "+p.Slug, err.Error())
			if mErr := a.scheduler.MarkFailed(ctx, p.ID, err.Error()); mErr != nil {
				logging.LogError("scheduler", "mark-failed failed", mErr.Error())
			}
			continue
		}
		if err := a.scheduler.MarkPublished(ctx, p.ID); err != nil {
			logging.LogError("scheduler", "mark-published failed", err.Error())
		}
		logging.LogInfo("scheduler", "published scheduled post: "+p.Slug)
	}
}

// dispatchWebhook fans an event out to registered outbound webhooks (Tier 2).
// No-op when no webhook store is wired. Runs asynchronously and best-effort.
func (a *App) dispatchWebhook(event string, payload interface{}) {
	if a.webhooks == nil {
		return
	}
	go a.webhooks.Dispatch(context.Background(), event, payload)
}

// shareToSocial auto-posts a newly published article to configured social
// networks (Tier 2). No-op when social posting is unconfigured. The article
// title is looked up from the store; failures are logged, never fatal.
func (a *App) shareToSocial(slug string) {
	if a.social == nil || !a.social.Enabled() {
		return
	}
	go func() {
		var title string
		if err := dbpkg.DB.QueryRow(`SELECT title FROM articles WHERE slug=?`, slug).Scan(&title); err != nil {
			return
		}
		link := "https://" + config.Cfg.Domain + "/" + slug
		if err := a.social.Share(context.Background(), title, link); err != nil {
			logging.LogError("social", "share failed: "+slug, err.Error())
		}
	}()
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
	body, err := json.Marshal(map[string][]string{"files": {"https://" + config.Cfg.Domain + "/" + slug}})
	if err != nil {
		logging.LogError("cloudflare", "marshal failed: "+slug, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		logging.LogError("cloudflare", "build request failed: "+slug, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.Cfg.CFAPIToken)
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		logging.LogError("cloudflare", "purge failed: "+slug, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		logging.LogError("cloudflare", "purge rejected: "+slug, fmt.Sprintf("status %d", resp.StatusCode))
	}
}

// indexNowKey resolves the active IndexNow key, preferring a credential managed
// from the VayuOS API Keys console (encrypted at rest) and falling back to the
// INDEXNOW_KEY environment variable. This lets operators set/rotate the key
// from the admin panel without an env change or restart.
func (a *App) indexNowKey() string {
	if a.secrets != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if k, _ := a.secrets.ProviderSecret(ctx, secrets.ProviderIndexNow); k != "" {
			return k
		}
	}
	return config.Cfg.IndexNowKey
}

func (a *App) pingIndexNow(slug string) {
	indexNowKey := a.indexNowKey()
	if indexNowKey == "" {
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
	body, err := json.Marshal(map[string]interface{}{
		"host": config.Cfg.Domain, "key": indexNowKey,
		"keyLocation": "https://" + config.Cfg.Domain + "/.well-known/" + indexNowKey + ".txt",
		"urlList":     []string{"https://" + config.Cfg.Domain + "/" + slug},
	})
	if err != nil {
		logging.LogError("indexnow", "marshal failed: "+slug, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.indexnow.org/indexnow", bytes.NewReader(body))
	if err != nil {
		logging.LogError("indexnow", "build request failed: "+slug, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err2 := a.outboundClient.Do(req)
	if err2 != nil {
		logging.LogError("indexnow", "submission failed: "+slug, err2.Error())
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
				// Best-effort: search drift is healed by the periodic reconciler
				// (SEARCH_RECONCILE_MIN), so an indexing error here is recoverable.
				_ = a.search.Index(ctx, art.ID, art.Title, art.Slug,
					htmlTagRe.ReplaceAllString(a.policy.Sanitize(art.Content), ""),
					art.Tags, art.CreatedAt.Unix())
			}
			// Local cache invalidation is owned by the cache.invalidated.v1
			// subscriber below (emitted transactionally with this mutation).
			a.purgeCloudflare(e.Slug)
			a.pingIndexNow(e.Slug)
		}()
		a.FireHook("article.create", map[string]interface{}{"slug": e.Slug, "id": e.ID})
		a.dispatchWebhook("article.created.v1", map[string]interface{}{"slug": e.Slug, "id": e.ID})
		a.shareToSocial(e.Slug)
		a.broadcastEvent("article.created", map[string]interface{}{"slug": e.Slug, "id": e.ID})
	})

	bus.Subscribe(events.ArticleUpdated{}, func(ctx context.Context, ev interface{}) {
		e := ev.(events.ArticleUpdated)
		go func() {
			var art dbpkg.Article
			var tagsStr string
			if dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, e.Slug).
				Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt) == nil {
				art.Tags = api.SplitTags(tagsStr)
				// Best-effort: search drift is healed by the periodic reconciler
				// (SEARCH_RECONCILE_MIN), so an indexing error here is recoverable.
				_ = a.search.Index(ctx, art.ID, art.Title, art.Slug,
					htmlTagRe.ReplaceAllString(a.policy.Sanitize(art.Content), ""),
					art.Tags, art.CreatedAt.Unix())
			}
			// Local cache invalidation is owned by the cache.invalidated.v1
			// subscriber below (emitted transactionally with this mutation).
			a.purgeCloudflare(e.Slug)
			a.pingIndexNow(e.Slug)
		}()
		a.FireHook("article.update", map[string]interface{}{"slug": e.Slug})
		a.dispatchWebhook("article.updated.v1", map[string]interface{}{"slug": e.Slug})
		a.broadcastEvent("article.updated", map[string]interface{}{"slug": e.Slug})
	})

	bus.Subscribe(events.ArticleDeleted{}, func(ctx context.Context, ev interface{}) {
		e := ev.(events.ArticleDeleted)
		go func() {
			_ = a.search.Delete(ctx, e.ID)
			a.purgeCloudflare(e.Slug)
		}()
		a.FireHook("article.delete", map[string]interface{}{"slug": e.Slug, "id": e.ID})
		a.dispatchWebhook("article.deleted.v1", map[string]interface{}{"slug": e.Slug, "id": e.ID})
		a.broadcastEvent("article.deleted", map[string]interface{}{"slug": e.Slug, "id": e.ID})
	})

	// Cache invalidation is the single owner of local rendered-cache purging.
	// Emitted transactionally with every article mutation (including deletes,
	// which previously left a stale cached page behind), it purges the article
	// page, homepage, and affected tag pages, then regenerates the global feeds.
	bus.Subscribe(events.CacheInvalidated{}, func(_ context.Context, ev interface{}) {
		e := ev.(events.CacheInvalidated)
		render.CachePurge(e.Slug, e.Tags, generateSitemap, generateRSS, generateRobots)
		logging.LogJSON(logging.LogFields{
			Level: "info", Component: "cache", Severity: "info",
			Msg: "invalidated rendered fragments (" + e.Reason + ")", Path: e.Slug,
		})
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
	// Use the read pool, not the single writer connection, and index-only counts
	// instead of a full-table SUM over write_jobs. Previously this ran every 30s
	// as `SUM(CASE ...) FROM write_jobs` (a full scan reading every article_json
	// blob) on dbpkg.DB — on a large queue table that scan monopolised the lone
	// writer connection for seconds, stalling sessions/writes and causing
	// intermittent 502s. Each COUNT below is served by idx_jobs_status.
	rdb := dbpkg.Reader()
	_ = rdb.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&snap.TotalArticles)
	_ = rdb.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&snap.PendingJobs)
	_ = rdb.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='failed'`).Scan(&snap.FailedJobs)
	_ = rdb.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='completed'`).Scan(&snap.CompletedJobs)
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
	rows, err := rdb.Query(`SELECT title,slug,created_at FROM articles ORDER BY created_at DESC LIMIT 15`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ra adminRecentArticle
			if scanErr := rows.Scan(&ra.Title, &ra.Slug, &ra.CreatedAt); scanErr != nil {
				logging.LogError("metrics", "scan recent article", scanErr.Error())
				continue
			}
			snap.RecentArticles = append(snap.RecentArticles, ra)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			logging.LogError("metrics", "iterate recent articles", rowsErr.Error())
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
