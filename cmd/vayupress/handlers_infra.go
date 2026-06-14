package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/queue"
)

// cspReportDedup suppresses logging of identical (directive|blocked) violations
// within a short window so a looping page can't flood the log. It is bounded:
// when it grows past cspDedupMax entries it is reset wholesale (cheap, and the
// rate limiter already caps ingestion).
var (
	cspDedupMu     sync.Mutex
	cspDedupSeen   = map[string]time.Time{}
	cspDedupWindow = 60 * time.Second
	cspDedupMax    = 512
)

// handleCSPReport ingests browser Content-Security-Policy violation reports
// (report-uri target). It is public and unauthenticated — browsers post these
// without credentials — so it is hardened against abuse:
//   - per-IP rate limit (over-limit reports are dropped before counting/logging,
//     so neither the metric nor the log can be inflated by a single source);
//   - 16 KB body cap;
//   - strict structured parsing (unparseable/empty reports are ignored);
//   - short-window duplicate suppression on (directive|blocked-uri).
//
// Accepted, de-duplicated violations increment vayupress_csp_violations_total
// and emit one structured log line, turning runtime CSP drift into a measurable
// signal rather than a silent assumption.
func (a *App) handleCSPReport(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	// Always answer 204 — browsers ignore the body. Bail out cheaply when the
	// source is over its rate limit so abuse cannot inflate metrics or logs.
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		ip = strings.Split(r.RemoteAddr, ":")[0]
	}
	if !auth.AllowCSPReport(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	raw, _ := io.ReadAll(io.LimitReader(r.Body, 16*1024))
	var env struct {
		Report struct {
			DocumentURI        string `json:"document-uri"`
			ViolatedDirective  string `json:"violated-directive"`
			EffectiveDirective string `json:"effective-directive"`
			BlockedURI         string `json:"blocked-uri"`
		} `json:"csp-report"`
	}
	if json.Unmarshal(raw, &env) != nil || (env.Report.ViolatedDirective == "" && env.Report.BlockedURI == "") {
		w.WriteHeader(http.StatusNoContent) // unparseable / empty → ignore quietly
		return
	}
	directive := env.Report.ViolatedDirective
	if directive == "" {
		directive = env.Report.EffectiveDirective
	}

	// Duplicate suppression: skip counting/logging an identical violation seen
	// within the dedup window.
	key := directive + "|" + env.Report.BlockedURI
	now := time.Now()
	cspDedupMu.Lock()
	if len(cspDedupSeen) > cspDedupMax {
		cspDedupSeen = map[string]time.Time{}
	}
	last, seen := cspDedupSeen[key]
	dup := seen && now.Sub(last) < cspDedupWindow
	cspDedupSeen[key] = now
	cspDedupMu.Unlock()
	if dup {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	atomic.AddInt64(&metrics.MetricCSPViolations, 1)
	recordCSPViolation(directive, env.Report.BlockedURI)
	// Tag with the deployment build version for release attribution. Browser CSP
	// reports carry no session/correlation context (unauthenticated POSTs), so the
	// receiving build version is the meaningful attribution for debugging a
	// frontend change.
	logging.LogJSON(logging.LogFields{
		Level: "warn", Component: "csp", Severity: "warning",
		Msg:  "CSP violation: directive=" + directive + " blocked=" + env.Report.BlockedURI + " build=" + Version,
		Path: env.Report.DocumentURI,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	var totalArticles, pendingJobs, failedJobs int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pendingJobs)
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='failed'`).Scan(&failedJobs)
	used := dbpkg.StorageUsedBytes()
	quota := dbpkg.StorageQuotaBytes()
	writeJSON(w, r, 200, map[string]interface{}{
		"version": Version, "uptime_seconds": time.Since(bootTime).Seconds(),
		"config_version": config.ConfigVersion,
		"articles_total": totalArticles, "queue_pending": pendingJobs, "queue_failed": failedJobs,
		"storage_used_bytes": used, "storage_quota_bytes": quota,
		"workers_alive":    atomic.LoadInt64(&metrics.WorkerLiveness),
		"maintenance_mode": config.Cfg.MaintenanceMode,
		"csp_mode":         cspEnforcementMode(),
		"metrics": map[string]int64{
			"articles_created": atomic.LoadInt64(&metrics.MetricArticlesCreated), "articles_updated": atomic.LoadInt64(&metrics.MetricArticlesUpdated),
			"articles_deleted": atomic.LoadInt64(&metrics.MetricArticlesDeleted), "queue_processed": atomic.LoadInt64(&metrics.MetricQueueProcessed),
			"wal_adaptive_checkpoints": atomic.LoadInt64(&metrics.MetricWALAdaptiveCheckpoints),
			"migration_drift_detected": atomic.LoadInt64(&metrics.MetricMigrationDriftDetected),
			"poison_jobs_quarantined":  atomic.LoadInt64(&metrics.MetricPoisonJobsQuarantined),
			"pprof_accesses":           atomic.LoadInt64(&metrics.MetricPprofAccesses),
			"vacuum_rejected":          atomic.LoadInt64(&metrics.MetricVacuumRejected),
		},
		"latency_ms": map[string]interface{}{
			"http_p95": metrics.HTTPLatency.Percentile(95), "http_p99": metrics.HTTPLatency.Percentile(99),
			"render_p99": metrics.RenderLatency.Percentile(99), "queue_job_p99": metrics.QueueJobLatency.Percentile(99),
			"sqlite_write_p99": metrics.SQLiteWriteLatency.Percentile(99),
		},
	})
}

func (a *App) handleQueueStatus(w http.ResponseWriter, r *http.Request) {
	queue.HandleQueueStatus(w, r, writeJSON)
}

func (a *App) handleQueueReplay(w http.ResponseWriter, r *http.Request) {
	queue.HandleQueueReplay(w, r, writeJSON, writeAPIError)
}

func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var totalArticles int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w,
		"vayupress_uptime_seconds %.0f\nvayupress_articles_total %d\n"+
			"vayupress_articles_created_total %d\nvayupress_articles_updated_total %d\nvayupress_articles_deleted_total %d\n"+
			"vayupress_queue_processed_total %d\nvayupress_queue_failed_total %d\nvayupress_queue_stuck_resets_total %d\n"+
			"vayupress_meili_errors_total %d\nvayupress_cache_hits_total %d\nvayupress_cache_misses_total %d\n"+
			"vayupress_cache_hit_ratio %.4f\nvayupress_memory_alloc_bytes %d\nvayupress_workers_alive %d\n"+
			"vayupress_storage_used_bytes %d\nvayupress_plugin_panics_total %d\nvayupress_auth_lockouts_total %d\n"+
			"vayupress_wal_checkpoints_total %d\nvayupress_slow_queries_total %d\nvayupress_dead_letter_total %d\n"+
			"vayupress_wal_checkpoint_duration_ms_total %d\nvayupress_wal_adaptive_checkpoints_total %d\n"+
			"vayupress_migration_drift_detected_total %d\nvayupress_poison_jobs_quarantined_total %d\n"+
			"vayupress_pprof_accesses_total %d\nvayupress_vacuum_rejected_total %d\n"+
			"vayupress_health_degraded_events_total %d\nvayupress_csp_violations_total %d\n",
		time.Since(bootTime).Seconds(), totalArticles,
		atomic.LoadInt64(&metrics.MetricArticlesCreated), atomic.LoadInt64(&metrics.MetricArticlesUpdated), atomic.LoadInt64(&metrics.MetricArticlesDeleted),
		atomic.LoadInt64(&metrics.MetricQueueProcessed), atomic.LoadInt64(&metrics.MetricQueueFailed), atomic.LoadInt64(&metrics.MetricQueueStuckResets),
		atomic.LoadInt64(&metrics.MetricMeiliErrors), atomic.LoadInt64(&metrics.MetricCacheHits), atomic.LoadInt64(&metrics.MetricCacheMisses),
		metrics.CacheHitRatio(), ms.Alloc, atomic.LoadInt64(&metrics.WorkerLiveness),
		atomic.LoadInt64(&metrics.CachedStorageBytes), atomic.LoadInt64(&metrics.MetricPluginPanics), atomic.LoadInt64(&metrics.MetricAuthLockouts),
		atomic.LoadInt64(&metrics.MetricWALCheckpoints), atomic.LoadInt64(&metrics.MetricSlowQueries), atomic.LoadInt64(&metrics.MetricDeadLetterJobs),
		atomic.LoadInt64(&metrics.MetricWALCheckpointDurationMS), atomic.LoadInt64(&metrics.MetricWALAdaptiveCheckpoints),
		atomic.LoadInt64(&metrics.MetricMigrationDriftDetected), atomic.LoadInt64(&metrics.MetricPoisonJobsQuarantined),
		atomic.LoadInt64(&metrics.MetricPprofAccesses), atomic.LoadInt64(&metrics.MetricVacuumRejected),
		atomic.LoadInt64(&metrics.MetricHealthDegradedEvents), atomic.LoadInt64(&metrics.MetricCSPViolations),
	)
	fmt.Fprint(w, metrics.HTTPLatency.Prometheus("vayupress_http_request_duration_seconds", "HTTP latency"))
	fmt.Fprint(w, metrics.RenderLatency.Prometheus("vayupress_render_duration_seconds", "Render latency"))
	fmt.Fprint(w, metrics.QueueJobLatency.Prometheus("vayupress_queue_job_duration_seconds", "Queue job latency"))
	fmt.Fprint(w, metrics.SQLiteWriteLatency.Prometheus("vayupress_sqlite_write_duration_seconds", "SQLite write latency"))
}
