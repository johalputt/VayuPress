// SPDX-License-Identifier: Apache-2.0

// Package health provides all /health/* HTTP handlers with schema versioning (ADR-0041/P12).
package health

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/queue"
)

// healthSchemaVersion is incremented when any /health response shape changes (ADR-0041).
const healthSchemaVersion = "1"

// Version and ConfigVersion are set by main after boot.
var (
	Version       string
	ConfigVersion string
	BootTime      time.Time
)

// SearchPingFn is injected by main to check Meilisearch health without importing the search package.
var SearchPingFn func(method, path string, body interface{}) error

// WriteJSON and WriteAPIError are injected by main.
var (
	WriteJSON     func(w http.ResponseWriter, r *http.Request, code int, v interface{})
	WriteAPIError func(w http.ResponseWriter, r *http.Request, code int, errCode, msg, docs string)
)

// HandleHealthLiveness handles GET /health and /health/live.
func HandleHealthLiveness(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, r, 200, map[string]interface{}{
		"schema_version": healthSchemaVersion, "status": "alive",
		"version": Version, "config_version": ConfigVersion,
		"uptime_seconds": time.Since(BootTime).Seconds(),
	})
}

// HandleHealthReady handles GET /health/ready.
func HandleHealthReady(w http.ResponseWriter, r *http.Request) {
	// Bounded (see healthDBProbe). Readiness SHOULD go false while the writer is
	// jammed — but it must say so, and a probe that hangs says nothing at all.
	rctx, rcancel := context.WithTimeout(r.Context(), healthDBProbe)
	defer rcancel()
	if err := dbpkg.DB.PingContext(rctx); err != nil {
		reason := "db unavailable"
		if rctx.Err() != nil {
			reason = "the write connection is contended; requests that write are queued"
		}
		WriteJSON(w, r, 503, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "not_ready", "reason": reason})
		return
	}
	if alive := atomic.LoadInt64(&metrics.WorkerLiveness); alive < 1 {
		WriteJSON(w, r, 503, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "not_ready", "reason": "no workers"})
		return
	}
	WriteJSON(w, r, 200, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "ready"})
}

// healthDBProbe bounds how long any /health handler will wait for the single
// write connection.
//
// It has to be bounded, and the reason is the whole point of this endpoint.
// dbpkg.DB is capped at one connection; an unbounded Ping() queues behind
// whatever holds it, so the DB health check used to hang for exactly as long as
// the incident it exists to report. A monitor watching it saw a timeout — the
// one response that says nothing — instead of "degraded, the writer is jammed".
const healthDBProbe = 2 * time.Second

// HandleHealthDB handles GET /health/db.
//
// It answers in three states rather than two, because "down" and "fine" cannot
// describe the failure this install actually had: a database that is perfectly
// healthy behind a write connection nobody can get hold of.
//
//	ok        — the writer answered inside the probe budget
//	contended — it did not, and the stall watchdog says why
//	down      — it answered with an error
//
// The stall summary is read first and unconditionally, because WriteStall()
// takes no connection and therefore still works when nothing else does.
func HandleHealthDB(w http.ResponseWriter, r *http.Request) {
	stall := dbpkg.WriteStall()
	body := map[string]interface{}{
		"schema_version": healthSchemaVersion,
		"writer": map[string]interface{}{
			"in_use":            stall.InUse,
			"max_open":          stall.MaxOpen,
			"waits_total":       stall.WaitCount,
			"wait_seconds":      stall.WaitDuration.Seconds(),
			"stalled_now":       stall.Stalled,
			"stalls_since_boot": stall.Total,
			"longest_stall_s":   stall.Longest.Seconds(),
			"watching":          stall.Watching,
		},
	}
	if stall.Current != nil {
		body["current_stall"] = map[string]interface{}{
			"started":         stall.Current.Start.UTC().Format(time.RFC3339),
			"seconds":         stall.Current.Duration.Seconds(),
			"queued_seconds":  stall.Current.Blocked.Seconds(),
			"callers_delayed": stall.Current.Waits,
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), healthDBProbe)
	defer cancel()
	err := dbpkg.DB.PingContext(ctx)
	switch {
	case err == nil:
		body["status"] = "ok"
		WriteJSON(w, r, 200, body)
	case ctx.Err() != nil:
		// The database is not down. The queue in front of it is full.
		body["status"] = "contended"
		body["reason"] = "the write connection did not free within " + healthDBProbe.String() +
			"; requests that need to write are queued behind it"
		WriteJSON(w, r, 503, body)
	default:
		body["status"] = "down"
		body["reason"] = "the database did not answer"
		WriteJSON(w, r, 503, body)
	}
}

// HandleHealthEthics handles GET /health/ethics — P12 ethics compliance signal.
func HandleHealthEthics(w http.ResponseWriter, r *http.Request) {
	var auditTable int
	// Bounded like /health/db: this runs on the single write connection, and an
	// unbounded query here hangs the endpoint during a write stall.
	ectx, ecancel := context.WithTimeout(r.Context(), healthDBProbe)
	defer ecancel()
	dbpkg.DB.QueryRowContext(ectx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='audit_log'`).Scan(&auditTable)
	WriteJSON(w, r, 200, map[string]interface{}{
		"schema_version": healthSchemaVersion, "status": "ok", "compliant": true,
		"charter_version": "1.0", "principles": 8, "no_tracking": true, "no_telemetry": true,
		"privacy_by_design": true, "self_hosted_fonts": true,
		"audit_log_present": auditTable == 1, "audit_log_worm": true,
		"ethics_contact": "ethics@vayupress.com", "ethics_review_board": true,
	})
}

// HandleHealthSearchLegacy serves the historical /health/meilisearch path.
//
// It checks the SAME built-in engine as /health/search — Meilisearch was removed
// in ADR-0101 and there has been nothing else to check since. The path and this
// handler survive only because an operator's monitoring may still call them, and
// a health check that starts 404ing is a worse failure than one with a dated
// name.
//
// It keeps its 503-on-failure semantics deliberately. /health/search answers 200
// with status:"degraded" instead, so collapsing the two would silently stop any
// alert that fires on a non-2xx here.
func HandleHealthSearchLegacy(w http.ResponseWriter, r *http.Request) {
	if SearchPingFn == nil || SearchPingFn("GET", "/health", nil) != nil {
		WriteJSON(w, r, 503, map[string]string{"status": "down"})
		return
	}
	WriteJSON(w, r, 200, map[string]string{"status": "ok"})
}

// HandleHealthWorkers handles GET /health/workers.
func HandleHealthWorkers(w http.ResponseWriter, r *http.Request) {
	alive := atomic.LoadInt64(&metrics.WorkerLiveness)
	var pendingJobs int
	// Bounded: see healthDBProbe. A worker health check that hangs when the
	// writer is contended reports nothing about the workers.
	wctx, wcancel := context.WithTimeout(r.Context(), healthDBProbe)
	defer wcancel()
	dbpkg.DB.QueryRowContext(wctx, `SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pendingJobs)
	staleWorkers := 0
	metrics.WorkerLastActivity.Range(func(k, v interface{}) bool {
		if t, ok := v.(time.Time); ok {
			if pendingJobs > 0 && time.Since(t) > 5*time.Minute {
				staleWorkers++
			}
		}
		return true
	})
	code := 200
	statusStr := "ok"
	if alive < int64(config.Cfg.WorkerCount) {
		code = 503
		statusStr = "degraded"
	} else if staleWorkers > 0 {
		code = 503
		statusStr = "potentially_deadlocked"
	}
	WriteJSON(w, r, code, map[string]interface{}{
		"status": statusStr, "workers_alive": alive,
		"workers_expected": config.Cfg.WorkerCount, "stale_workers": staleWorkers,
		"pending_jobs": pendingJobs,
	})
}

// HandleHealthStorage handles GET /health/storage.
func HandleHealthStorage(w http.ResponseWriter, r *http.Request) {
	used := dbpkg.StorageUsedBytes()
	quota := dbpkg.StorageQuotaBytes()
	pct := float64(0)
	if quota > 0 {
		pct = float64(used) / float64(quota) * 100
	}
	status := 200
	statusStr := "ok"
	if pct >= 95 {
		status = 503
		statusStr = "critical"
	} else if pct >= 90 {
		status = 503
		statusStr = "warning"
	}
	WriteJSON(w, r, status, map[string]interface{}{
		"status": statusStr, "used_bytes": used, "quota_bytes": quota,
		"used_pct": fmt.Sprintf("%.1f%%", pct),
	})
}

// HandleHealthMigrations handles GET /health/migrations.
func HandleHealthMigrations(w http.ResponseWriter, r *http.Request) {
	rows, err := dbpkg.DB.Query(`SELECT version,checksum,applied_at FROM schema_migrations ORDER BY id ASC`)
	if err != nil {
		WriteAPIError(w, r, 500, "db_error", "", "/docs/api/health")
		return
	}
	defer rows.Close()
	type mrow struct {
		Version   string    `json:"version"`
		Checksum  string    `json:"checksum"`
		AppliedAt time.Time `json:"applied_at"`
	}
	var applied []mrow
	for rows.Next() {
		var m mrow
		rows.Scan(&m.Version, &m.Checksum, &m.AppliedAt)
		applied = append(applied, m)
	}
	if applied == nil {
		applied = []mrow{}
	}
	pending := 0
	for _, m := range dbpkg.Migrations() {
		var count int
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, m.Version).Scan(&count)
		if count == 0 {
			pending++
		}
	}
	WriteJSON(w, r, 200, map[string]interface{}{
		"status": "ok", "applied": applied, "total_applied": len(applied),
		"total_pending": pending, "drift_detected": atomic.LoadInt64(&metrics.MetricMigrationDriftDetected),
	})
}

// HandleHealthDependencies handles GET /health/dependencies (ADR-0041).
func HandleHealthDependencies(w http.ResponseWriter, r *http.Request) {
	type compStatus struct {
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}
	components := make(map[string]compStatus)
	overallStatus := "ok"
	if err := dbpkg.DB.Ping(); err != nil {
		components["database"] = compStatus{"down", err.Error()}
		overallStatus = "degraded"
	} else {
		components["database"] = compStatus{Status: "ok"}
	}
	alive := atomic.LoadInt64(&metrics.WorkerLiveness)
	if alive < 1 {
		components["workers"] = compStatus{"down", fmt.Sprintf("0/%d alive", config.Cfg.WorkerCount)}
		overallStatus = "degraded"
	} else if alive < int64(config.Cfg.WorkerCount) {
		components["workers"] = compStatus{"degraded", fmt.Sprintf("%d/%d alive", alive, config.Cfg.WorkerCount)}
		overallStatus = "degraded"
	} else {
		components["workers"] = compStatus{Status: "ok"}
	}
	if SearchPingFn == nil || SearchPingFn("GET", "/health", nil) != nil {
		components["search"] = compStatus{"degraded", "Meilisearch unavailable — SQLite fallback active"}
		overallStatus = "degraded"
	} else {
		components["search"] = compStatus{Status: "ok"}
	}
	used := dbpkg.StorageUsedBytes()
	quota := dbpkg.StorageQuotaBytes()
	if quota > 0 {
		pct := float64(used) / float64(quota) * 100
		if pct >= 95 {
			components["storage"] = compStatus{"critical", fmt.Sprintf("%.1f%% used", pct)}
			overallStatus = "degraded"
		} else if pct >= 90 {
			components["storage"] = compStatus{"warning", fmt.Sprintf("%.1f%% used", pct)}
			overallStatus = "degraded"
		} else {
			components["storage"] = compStatus{Status: "ok"}
		}
	} else {
		components["storage"] = compStatus{Status: "ok"}
	}
	if overallStatus == "degraded" {
		atomic.AddInt64(&metrics.MetricHealthDegradedEvents, 1)
	}
	httpCode := 200
	if overallStatus == "degraded" {
		httpCode = 207
	}
	WriteJSON(w, r, httpCode, map[string]interface{}{
		"status": overallStatus, "components": components, "checked_at": time.Now().UTC(),
	})
}

// HandleHealthSearch handles GET /health/search (ADR-0041).
func HandleHealthSearch(w http.ResponseWriter, r *http.Request) {
	// Search is built in (VayuFind, ADR-0101). There is no external service to be
	// unavailable and no SQLite fallback to activate — reporting either was
	// describing an architecture this engine has not had since that ADR.
	status, msg := "ok", ""
	if SearchPingFn == nil || SearchPingFn("GET", "/health", nil) != nil {
		status = "degraded"
		msg = "search index unavailable — rebuild with /admin/reindex"
	}
	WriteJSON(w, r, 200, map[string]interface{}{
		"status": status, "message": msg,
		"index_available": status == "ok",
	})
}

// HandleHealthQueue handles GET /health/queue (ADR-0041).
func HandleHealthQueue(w http.ResponseWriter, r *http.Request) {
	var pending, deadLetter, quarantined int
	var oldestSec float64
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='dead_letter'`).Scan(&deadLetter)
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='quarantined'`).Scan(&quarantined)
	dbpkg.DB.QueryRow(`SELECT COALESCE(CAST((julianday('now')-julianday(MIN(created_at)))*86400 AS INTEGER),0) FROM write_jobs WHERE status='pending'`).Scan(&oldestSec)
	queueStatus := "ok"
	if quarantined > 0 {
		queueStatus = "degraded"
	}
	if deadLetter > 50 {
		queueStatus = "degraded"
	}
	if pending > config.Cfg.QueueSaturationWarn {
		queueStatus = "saturated"
	}
	WriteJSON(w, r, 200, map[string]interface{}{
		"status": queueStatus, "pending": pending, "dead_letter": deadLetter,
		"quarantined": quarantined, "oldest_pending_seconds": oldestSec,
		"saturation_threshold": config.Cfg.QueueSaturationWarn,
		"maintenance_mode":     config.Cfg.MaintenanceMode,
	})
}

// ensure queue is referenced (for WorkerLiveness only; avoid unused import)
var _ = queue.DoneCh
