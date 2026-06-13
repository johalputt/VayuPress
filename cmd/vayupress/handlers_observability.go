package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/events"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/resource"
	"github.com/johalputt/vayupress/internal/trace"
)

// handleTraceSpans returns recent finished spans from the in-memory ring buffer.
// GET /api/v1/admin/traces?limit=100
func (a *App) handleTraceSpans(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 2000 {
		limit = n
	}
	spans := trace.Global.Recorder.Recent(limit)
	writeJSON(w, r, 200, map[string]interface{}{
		"spans":          spans,
		"count":          len(spans),
		"correlation_id": trace.CorrelationID(r.Context()),
	})
}

// handleResourceStats returns current resource limiter and goroutine telemetry.
// GET /api/v1/admin/resource/stats
func (a *App) handleResourceStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, 200, map[string]interface{}{
		"goroutines":     resource.GoroutineCount(),
		"limiters":       resource.AllStats(),
		"correlation_id": trace.CorrelationID(r.Context()),
	})
}

// handleTraceByID returns all spans for a specific trace (correlation) ID.
// GET /api/v1/admin/traces/{trace_id}
func (a *App) handleTraceByID(w http.ResponseWriter, r *http.Request) {
	traceID := chi.URLParam(r, "trace_id")
	if traceID == "" {
		writeAPIError(w, r, 400, "missing_param", "trace_id required", "")
		return
	}
	spans := trace.Global.Recorder.ByTraceID(traceID)
	writeJSON(w, r, 200, map[string]interface{}{
		"trace_id": traceID,
		"spans":    spans,
		"count":    len(spans),
	})
}

// outboxEventRow is the DB projection of event_outbox for the inspection API.
type outboxEventRow struct {
	ID          int64            `json:"id"`
	EventType   string           `json:"event_type"`
	Status      string           `json:"status"`
	Retries     int              `json:"retries"`
	CreatedAt   time.Time        `json:"created_at"`
	DeliveredAt *time.Time       `json:"delivered_at,omitempty"`
	RetryAt     *time.Time       `json:"retry_at,omitempty"`
	DeadReason  string           `json:"dead_reason,omitempty"`
	Envelope    *events.Envelope `json:"envelope,omitempty"`
}

// handleOutboxStats returns aggregated counts for the event_outbox table.
// GET /api/v1/admin/outbox/stats
func (a *App) handleOutboxStats(w http.ResponseWriter, r *http.Request) {
	rows, err := dbpkg.DB.Query(`SELECT status, COUNT(1) FROM event_outbox GROUP BY status`)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", err.Error(), "")
		return
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		rows.Scan(&status, &count)
		counts[status] = count
	}

	var totalDelivered int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM delivered_events`).Scan(&totalDelivered)

	var oldestPendingSec float64
	dbpkg.DB.QueryRow(`SELECT COALESCE(CAST((julianday('now')-julianday(MIN(created_at)))*86400 AS INTEGER),0) FROM event_outbox WHERE status='pending'`).Scan(&oldestPendingSec)

	writeJSON(w, r, 200, map[string]interface{}{
		"pending":             counts["pending"],
		"delivered":           counts["delivered"],
		"dead_letter":         counts["dead_letter"],
		"dedup_table_size":    totalDelivered,
		"oldest_pending_secs": oldestPendingSec,
		"correlation_id":      trace.CorrelationID(r.Context()),
	})
}

// handleOutboxEvents lists recent outbox events with envelope metadata.
// GET /api/v1/admin/outbox/events?status=pending&limit=50
func (a *App) handleOutboxEvents(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 500 {
		limit = n
	}

	query := `SELECT id,event_type,status,retries,created_at,delivered_at,retry_at,dead_reason,payload
	          FROM event_outbox`
	args := []interface{}{}
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := dbpkg.DB.Query(query, args...)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", err.Error(), "")
		return
	}
	defer rows.Close()

	result := make([]outboxEventRow, 0, limit)
	for rows.Next() {
		var row outboxEventRow
		var deliveredAt, retryAt sql.NullTime
		var deadReason sql.NullString
		var payloadJSON string
		if err := rows.Scan(&row.ID, &row.EventType, &row.Status, &row.Retries,
			&row.CreatedAt, &deliveredAt, &retryAt, &deadReason, &payloadJSON); err != nil {
			continue
		}
		if deliveredAt.Valid {
			row.DeliveredAt = &deliveredAt.Time
		}
		if retryAt.Valid {
			row.RetryAt = &retryAt.Time
		}
		if deadReason.Valid {
			row.DeadReason = deadReason.String
		}
		var env events.Envelope
		if err := json.Unmarshal([]byte(payloadJSON), &env); err == nil {
			row.Envelope = &env
		}
		result = append(result, row)
	}

	writeJSON(w, r, 200, map[string]interface{}{
		"events":         result,
		"count":          len(result),
		"correlation_id": trace.CorrelationID(r.Context()),
	})
}

// handleOutboxEvent returns the full envelope for a single event_outbox record.
// GET /api/v1/admin/outbox/events/{id}
func (a *App) handleOutboxEvent(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeAPIError(w, r, 400, "invalid_id", "id must be an integer", "")
		return
	}

	var row outboxEventRow
	var deliveredAt, retryAt sql.NullTime
	var deadReason sql.NullString
	var payloadJSON string

	err = dbpkg.DB.QueryRow(
		`SELECT id,event_type,status,retries,created_at,delivered_at,retry_at,dead_reason,payload
		 FROM event_outbox WHERE id=?`, id,
	).Scan(&row.ID, &row.EventType, &row.Status, &row.Retries,
		&row.CreatedAt, &deliveredAt, &retryAt, &deadReason, &payloadJSON)

	if err != nil {
		writeAPIError(w, r, 404, "not_found", "event not found", "")
		return
	}
	if deliveredAt.Valid {
		row.DeliveredAt = &deliveredAt.Time
	}
	if retryAt.Valid {
		row.RetryAt = &retryAt.Time
	}
	if deadReason.Valid {
		row.DeadReason = deadReason.String
	}
	var env events.Envelope
	if err := json.Unmarshal([]byte(payloadJSON), &env); err == nil {
		row.Envelope = &env
	}

	writeJSON(w, r, 200, row)
}

// handleCorrelationTrace returns all outbox events that share a correlation ID.
// GET /api/v1/admin/trace/{correlation_id}
func (a *App) handleCorrelationTrace(w http.ResponseWriter, r *http.Request) {
	corrID := chi.URLParam(r, "correlation_id")
	if corrID == "" {
		writeAPIError(w, r, 400, "missing_param", "correlation_id required", "")
		return
	}

	// Search outbox payloads for the correlation ID embedded in the envelope JSON.
	rows, err := dbpkg.DB.Query(
		`SELECT id,event_type,status,retries,created_at,delivered_at,payload
		 FROM event_outbox
		 WHERE json_extract(payload,'$.correlation_id')=?
		 ORDER BY id ASC LIMIT 200`, corrID,
	)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", err.Error(), "")
		return
	}
	defer rows.Close()

	type traceEvent struct {
		ID          int64            `json:"id"`
		EventType   string           `json:"event_type"`
		Status      string           `json:"status"`
		Retries     int              `json:"retries"`
		CreatedAt   time.Time        `json:"created_at"`
		DeliveredAt *time.Time       `json:"delivered_at,omitempty"`
		Envelope    *events.Envelope `json:"envelope,omitempty"`
	}

	result := []traceEvent{}
	for rows.Next() {
		var ev traceEvent
		var deliveredAt sql.NullTime
		var payloadJSON string
		if err := rows.Scan(&ev.ID, &ev.EventType, &ev.Status, &ev.Retries, &ev.CreatedAt, &deliveredAt, &payloadJSON); err != nil {
			continue
		}
		if deliveredAt.Valid {
			ev.DeliveredAt = &deliveredAt.Time
		}
		var env events.Envelope
		if json.Unmarshal([]byte(payloadJSON), &env) == nil {
			ev.Envelope = &env
		}
		result = append(result, ev)
	}

	writeJSON(w, r, 200, map[string]interface{}{
		"correlation_id": corrID,
		"events":         result,
		"count":          len(result),
	})
}

// handleSandboxStats returns health snapshots for all registered subprocess plugin pools.
// GET /api/v1/admin/sandbox/stats
func (a *App) handleSandboxStats(w http.ResponseWriter, r *http.Request) {
	stats := plugins.SubprocessStats()
	writeJSON(w, r, 200, map[string]interface{}{
		"subprocess_plugins": stats,
		"count":              len(stats),
		"correlation_id":     trace.CorrelationID(r.Context()),
	})
}
