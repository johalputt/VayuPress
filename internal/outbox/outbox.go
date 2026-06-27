// Package outbox implements the transactional outbox relay pattern (ADR-0051).
// The relay polls the event_outbox table, dispatches events via a caller-provided
// function, marks them delivered, and retries failures with exponential backoff.
// Idempotent delivery is enforced via the delivered_events deduplication table (ADR-0052).
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/johalputt/vayupress/internal/events"
	"github.com/johalputt/vayupress/internal/logging"
)

const (
	maxRetries        = 3
	maxBackoffSeconds = 120
	pollInterval      = 200 * time.Millisecond
)

// ErrPoisonEvent is returned by a DispatchFn to signal an unrecoverable event
// that should be dead-lettered immediately without incrementing retries.
var ErrPoisonEvent = errors.New("poison event: unrecoverable")

// DispatchFn is called for each pending outbox event. It receives the event
// type string and raw JSON payload. Return a non-nil error to trigger a retry.
// Return ErrPoisonEvent to dead-letter immediately.
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

	// Parse the envelope to get the stable event_id for deduplication.
	var env events.Envelope
	if parseErr := parseEnvelope(rec.payload, &env); parseErr == nil && env.EventID != "" {
		// Check if this event_id has already been delivered.
		var count int
		// Best-effort dedup probe: on Scan error count stays 0 and we fall
		// through to dispatch (the delivered_events INSERT below is idempotent).
		_ = r.db.QueryRow(`SELECT COUNT(1) FROM delivered_events WHERE event_id=?`, env.EventID).Scan(&count)
		if count > 0 {
			// Already delivered — mark outbox record as delivered and skip dispatch.
			// Status-update failures are safe: the record simply stays pending and
			// is retried, then re-caught by this dedup guard.
			_, _ = r.db.Exec(
				`UPDATE event_outbox SET status='delivered', delivered_at=datetime('now') WHERE id=?`,
				rec.id,
			)
			return false
		}
	}

	// Poison-event fast-path: dead-letter immediately on ErrPoisonEvent.
	dispCtx, dispCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dispCancel()
	dispErr := r.dispatch(dispCtx, rec.eventType, rec.payload)
	if dispErr != nil && errors.Is(dispErr, ErrPoisonEvent) {
		logging.LogError("outbox", "poison event dead-lettered for "+rec.eventType, dispErr.Error())
		_, _ = r.db.Exec(
			`UPDATE event_outbox SET status='dead_letter', dead_reason=? WHERE id=?`,
			dispErr.Error(), rec.id,
		)
		return false
	}

	if dispErr == nil {
		_, _ = r.db.Exec(
			`UPDATE event_outbox SET status='delivered', delivered_at=datetime('now') WHERE id=?`,
			rec.id,
		)
		// Record in delivered_events for idempotency (best effort).
		if env.EventID != "" {
			if _, insErr := r.db.Exec(
				`INSERT OR IGNORE INTO delivered_events(event_id, event_type) VALUES(?, ?)`,
				env.EventID, rec.eventType,
			); insErr != nil {
				logging.LogError("outbox", "delivered_events insert failed for "+env.EventID, insErr.Error())
			}
		}
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
		// Best-effort: a failed status write leaves the row pending and it is
		// retried on the next sweep, so the outcome is self-healing.
		_, _ = r.db.Exec(
			`UPDATE event_outbox SET retries=retries+1, retry_at=? WHERE id=?`,
			retryAt, rec.id,
		)
	} else {
		_, _ = r.db.Exec(
			`UPDATE event_outbox SET status='dead_letter', dead_reason=? WHERE id=?`,
			dispErr.Error(), rec.id,
		)
	}
	return false
}

// parseEnvelope attempts to parse payload as an events.Envelope.
// It is a best-effort parse: if the payload is not an envelope (legacy format),
// the env will have an empty EventID and deduplication is skipped.
func parseEnvelope(payload []byte, env *events.Envelope) error {
	return json.Unmarshal(payload, env)
}
