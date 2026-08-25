// SPDX-License-Identifier: Apache-2.0

// Package store is the SQLite persistence layer for VayuAnalytics engagement
// sessions. It owns both the write path (page-enter + engagement beacon) and the
// dashboard read path (overview, source breakdown, AI-traffic comparison, top
// pages, realtime). Every stored field is anonymous by construction — see the
// migration 056 header and internal/vayuanalytics/session for the GDPR rationale.
//
// It imports only database/sql from outside stdlib, so the package builds and
// its query logic type-checks without the SQLite driver; the driver is required
// only at runtime and by the DB-backed tests.
package store

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/vayuanalytics/classifier"
)

// execer is satisfied by both *sql.DB and *sql.Tx, so the row-level write helpers
// can run either directly (synchronous path) or inside a batched transaction
// (async ingest path).
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store wraps an open *sql.DB whose schema includes vayuanalytics_sessions.
//
// Ingestion (page-enter + engagement beacons) is fired on EVERY public page
// view. Writing each event synchronously on the single SQLite writer connection
// turned normal traffic into a storm of tiny fsync'd transactions that, after a
// cold restart, saturated the writer and starved the dynamic VayuOS admin pages
// (public pages are served from the HTML cache and were unaffected). So the hot
// path now enqueues events on a bounded buffer and a single background goroutine
// flushes them in BATCHED transactions (~1/sec), collapsing hundreds of tiny
// writes into a handful and keeping the writer free. Telemetry is best-effort:
// if the buffer is full (an extreme flood) events are dropped rather than
// allowed to block a request or melt the database.
type Store struct {
	db      *sql.DB
	reader  *sql.DB          // dashboard read pool; falls back to db. Set via UseReader.
	ingest  chan ingestEvent // nil until StartIngest; then the async batch path
	dropped atomic.Int64
}

// New constructs a Store. Until StartIngest is called, QueueEnter/QueueBeacon
// fall back to synchronous writes (so tests and one-off callers still persist).
func New(db *sql.DB) *Store { return &Store{db: db, reader: db} }

// UseReader routes the dashboard read queries (all of query.go) at a dedicated
// read pool instead of the single writer connection. The analytics dashboards
// run a dozen heavy aggregate scans over the ever-growing vayuanalytics_sessions
// table; on the writer they serialise behind the beacon write stream and each
// other, so opening the Analytics / VayuShield panel could exceed the 30s server
// timeout and 502. Writes (beacon ingest, purge) stay on the writer; WAL gives
// the reader read-your-writes (a ~1s batch-flush lag is fine for a dashboard). A
// nil reader is ignored.
func (s *Store) UseReader(reader *sql.DB) {
	if reader != nil {
		s.reader = reader
	}
}

// readDB returns the handle for dashboard reads: the dedicated read pool when
// set, otherwise the writer.
func (s *Store) readDB() *sql.DB {
	if s.reader != nil {
		return s.reader
	}
	return s.db
}

// EnterInput is a page-enter event (fired immediately on page load).
type EnterInput struct {
	SessionHash string
	// VisitorHash is the day-stable anonymous visitor identity (session.Visitor).
	// Empty falls back to SessionHash so pre-090 callers and tests keep working.
	VisitorHash string
	PagePath    string
	Class       classifier.Result
	Country     string
	ClientType  string // human | GoodBot | BadBot | AIAgent | Headless | Unknown
	BotScore    float64
	Now         time.Time
}

// RecordEnter inserts a new engagement row for a page view. is_new_session is
// true when this VISITOR has not been seen today — with the stable daily
// visitor hash (migration 090) that finally means what it says; the old
// session-hash probe reset every UTC day and made the metric read ~zero.
func (s *Store) RecordEnter(ctx context.Context, in EnterInput) error {
	if s == nil || s.db == nil {
		return nil
	}
	return recordEnter(ctx, s.db, in)
}

// recordEnter performs the page-enter write against any execer (the writer for
// the synchronous path, or a batched *sql.Tx for the async ingest path).
func recordEnter(ctx context.Context, e execer, in EnterInput) error {
	if in.SessionHash == "" || in.PagePath == "" {
		return nil
	}
	now := in.Now.UTC()
	if in.Now.IsZero() {
		now = time.Now().UTC()
	}
	day := now.Format("2006-01-02")
	visitor := in.VisitorHash
	if visitor == "" {
		visitor = in.SessionHash // legacy fallback (pre-090 rows/tests)
	}
	var seen int
	_ = e.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM vayuanalytics_sessions WHERE visitor_hash=? AND date(entry_time)=?`,
		visitor, day).Scan(&seen)
	isNew := 1
	if seen > 0 {
		isNew = 0
	}
	ct := in.ClientType
	if ct == "" {
		ct = "human"
	}
	_, err := e.ExecContext(ctx, `INSERT INTO vayuanalytics_sessions
(session_hash,visitor_hash,page_path,source_category,source_detail,referrer_domain,referrer_path,entry_time,country_code,client_type,bot_score,is_new_session)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.SessionHash, visitor, in.PagePath, string(in.Class.Category), in.Class.Detail,
		in.Class.ReferrerDomain, in.Class.ReferrerPath, now, in.Country, ct, in.BotScore, isNew)
	return err
}

// BeaconInput is an engagement update sent as the reader leaves or hides the tab.
type BeaconInput struct {
	SessionHash  string
	PagePath     string
	TimeOnPage   int // seconds
	ScrollDepth  int // percent 0..100
	Interactions int
	Now          time.Time
}

// engagedThresholdSec / engagedThresholdScroll define an "engaged read".
const (
	engagedThresholdSec    = 30
	engagedThresholdScroll = 25
	bounceMaxSec           = 10
)

// RecordBeacon folds an engagement beacon into the most recent enter row for the
// same (session, page). Metrics accumulate with MAX so multiple beacons (the
// visibilitychange beacon then the unload beacon) converge on the largest
// observed values. engaged/bounce are recomputed from the accumulated values.
func (s *Store) RecordBeacon(ctx context.Context, in BeaconInput) error {
	if s == nil || s.db == nil {
		return nil
	}
	return recordBeacon(ctx, s.db, in)
}

// recordBeacon folds an engagement beacon into the latest matching enter row,
// against any execer (writer or batched *sql.Tx).
func recordBeacon(ctx context.Context, e execer, in BeaconInput) error {
	if in.SessionHash == "" || in.PagePath == "" {
		return nil
	}
	now := in.Now.UTC()
	if in.Now.IsZero() {
		now = time.Now().UTC()
	}
	if in.ScrollDepth < 0 {
		in.ScrollDepth = 0
	}
	if in.ScrollDepth > 100 {
		in.ScrollDepth = 100
	}
	if in.TimeOnPage < 0 {
		in.TimeOnPage = 0
	}
	_, err := e.ExecContext(ctx, `UPDATE vayuanalytics_sessions SET
exit_time=?,
time_on_page_seconds=MAX(time_on_page_seconds,?),
scroll_depth_percent=MAX(scroll_depth_percent,?),
interaction_count=MAX(interaction_count,?),
engaged=CASE WHEN MAX(time_on_page_seconds,?)>=? AND MAX(scroll_depth_percent,?)>=? THEN 1 ELSE 0 END,
bounce=CASE WHEN MAX(time_on_page_seconds,?)<? AND MAX(scroll_depth_percent,?)<? THEN 1 ELSE 0 END
WHERE id=(SELECT id FROM vayuanalytics_sessions WHERE session_hash=? AND page_path=? ORDER BY entry_time DESC LIMIT 1)`,
		now,
		in.TimeOnPage,
		in.ScrollDepth,
		in.Interactions,
		in.TimeOnPage, engagedThresholdSec, in.ScrollDepth, engagedThresholdScroll,
		in.TimeOnPage, bounceMaxSec, in.ScrollDepth, engagedThresholdScroll,
		in.SessionHash, in.PagePath)
	return err
}

// ── Async batched ingestion ───────────────────────────────────────────────────

// ingestEvent is one buffered write (exactly one of enter/beacon is set).
type ingestEvent struct {
	enter  *EnterInput
	beacon *BeaconInput
}

const (
	ingestBuffer     = 4096            // bounded backlog; full ⇒ drop (best-effort)
	ingestMaxBatch   = 256             // flush early once this many events accrue
	ingestFlushEvery = 1 * time.Second // otherwise flush on this cadence
)

// StartIngest launches the background batched-write loop. Call once at boot with
// the process shutdown channel. It is safe to call on a nil/dbless store (no-op)
// and idempotent. Until it runs, QueueEnter/QueueBeacon write synchronously.
func (s *Store) StartIngest(done <-chan struct{}) {
	if s == nil || s.db == nil || s.ingest != nil {
		return
	}
	s.ingest = make(chan ingestEvent, ingestBuffer)
	go s.ingestLoop(done)
}

// Dropped reports how many telemetry events were shed because the ingest buffer
// was full (an extreme flood). Exposed for observability.
func (s *Store) Dropped() int64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

// QueueEnter enqueues a page-enter for batched persistence, never blocking the
// caller. Falls back to a synchronous write if ingestion has not been started.
func (s *Store) QueueEnter(in EnterInput) {
	if s == nil || s.db == nil || in.SessionHash == "" || in.PagePath == "" {
		return
	}
	if s.ingest == nil {
		_ = s.RecordEnter(context.Background(), in)
		return
	}
	select {
	case s.ingest <- ingestEvent{enter: &in}:
	default:
		s.dropped.Add(1)
	}
}

// QueueBeacon enqueues an engagement beacon for batched persistence.
func (s *Store) QueueBeacon(in BeaconInput) {
	if s == nil || s.db == nil || in.SessionHash == "" || in.PagePath == "" {
		return
	}
	if s.ingest == nil {
		_ = s.RecordBeacon(context.Background(), in)
		return
	}
	select {
	case s.ingest <- ingestEvent{beacon: &in}:
	default:
		s.dropped.Add(1)
	}
}

func (s *Store) ingestLoop(done <-chan struct{}) {
	ticker := time.NewTicker(ingestFlushEvery)
	defer ticker.Stop()
	batch := make([]ingestEvent, 0, ingestMaxBatch)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.flushBatch(batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-done:
			// Drain whatever is buffered, then flush and exit.
			for {
				select {
				case ev := <-s.ingest:
					batch = append(batch, ev)
					if len(batch) >= ingestMaxBatch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case ev := <-s.ingest:
			batch = append(batch, ev)
			if len(batch) >= ingestMaxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// flushBatch persists a batch of events in a SINGLE transaction — one fsync for
// the whole batch instead of one per event. Best-effort: a failed batch is
// dropped (the process must never stall or crash on telemetry). Events are
// applied in arrival order so a beacon that shares a batch with its enter still
// folds into the just-inserted row.
func (s *Store) flushBatch(batch []ingestEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	for _, ev := range batch {
		switch {
		case ev.enter != nil:
			_ = recordEnter(ctx, tx, *ev.enter)
		case ev.beacon != nil:
			_ = recordBeacon(ctx, tx, *ev.beacon)
		}
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
	}
}

// ── GDPR retention ────────────────────────────────────────────────────────────

// Purge deletes engagement rows older than retainDays (measured from entry_time).
// Returns the number of rows removed. A retainDays <= 0 is a no-op ("forever").
func (s *Store) Purge(ctx context.Context, retainDays int) (int64, error) {
	if s == nil || s.db == nil || retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays)
	res, err := s.db.ExecContext(ctx, `DELETE FROM vayuanalytics_sessions WHERE entry_time<?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
