// Package outbox implements the transactional outbox relay pattern (ADR-0051).
// The relay polls the event_outbox table, dispatches events via a caller-provided
// function, marks them delivered, and retries failures with exponential backoff.
package outbox

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

const (
	maxRetries         = 3
	maxBackoffSeconds  = 120
	pollInterval       = 200 * time.Millisecond
)

// DispatchFn is called for each pending outbox event. It receives the event
// type string and raw JSON payload. Return a non-nil error to trigger a retry.
type DispatchFn func(ctx context.Context, eventType string, payload []byte) error

// Relay reads pending events from the event_outbox table and dispatches them.
type Relay struct {
	db       *sql.DB
	dispatch DispatchFn
	doneCh   <-chan struct{}
}

// NewRelay returns a Relay. Call Start() to begin polling.
func NewRelay(db *sql.DB, dispatch DispatchFn, doneCh <-chan struct{}) *Relay {
	return &Relay{db: db, dispatch: dispatch, doneCh: doneCh}
}

// Start launches the relay goroutine.
func (r *Relay) Start() {
	go r.run()
}

func (r *Relay) run() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.doneCh:
			// Drain remaining events on graceful shutdown.
			for !r.processOne() {
			}
			return
		case <-ticker.C:
			r.processOne()
		}
	}
}

type outboxRecord struct {
	id        int64
	eventType string
	payload   []byte
	retries   int
}

func (r *Relay) processOne() (empty bool) {
	var rec outboxRecord
	err := r.db.QueryRow(
		`SELECT id,event_type,payload,retries FROM event_outbox
		 WHERE status='pending' AND (retry_at IS NULL OR retry_at <= datetime('now'))
		 ORDER BY id ASC LIMIT 1`,
	).Scan(&rec.id, &rec.eventType, &rec.payload, &rec.retries)
	if err != nil {
		return true
	}

	// Claim the record by marking it processing isn't feasible with SQLite
	// single-writer; isolation is handled by the serialised worker.

	dispErr := r.dispatch(context.Background(), rec.eventType, rec.payload)
	if dispErr == nil {
		r.db.Exec(
			`UPDATE event_outbox SET status='delivered', delivered_at=datetime('now') WHERE id=?`,
			rec.id,
		)
		return false
	}

	// Dispatch failed — schedule retry or dead-letter.
	logging.LogError("outbox", "dispatch failed for "+rec.eventType, dispErr.Error())
	if rec.retries < maxRetries {
		backoff := int(math.Pow(2, float64(rec.retries+1))) * 5
		if backoff > maxBackoffSeconds {
			backoff = maxBackoffSeconds
		}
		retryAt := time.Now().Add(time.Duration(backoff) * time.Second).UTC().Format("2006-01-02T15:04:05Z")
		r.db.Exec(
			`UPDATE event_outbox SET retries=retries+1, retry_at=? WHERE id=?`,
			retryAt, rec.id,
		)
	} else {
		r.db.Exec(
			`UPDATE event_outbox SET status='dead_letter', dead_reason=? WHERE id=?`,
			dispErr.Error(), rec.id,
		)
	}
	return false
}
