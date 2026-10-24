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
	"time"

	"github.com/johalputt/vayupress/internal/vayuanalytics/classifier"

	"github.com/johalputt/vayupress/internal/dbbatch"
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
// allowed to block a request or melt the database. The batching is
// dbbatch.Writer's, so a batch the database refuses is kept and tried again,
// and held while the install refuses writes, as every telemetry queue is.
type Store struct {
	db     *sql.DB
	reader *sql.DB         // dashboard read pool; falls back to db. Set via UseReader.
	ingest *dbbatch.Writer // nil until StartIngest; then the async batch path
	hold   func() bool     // see HoldWhile
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
	// DomainID is the site this install served the page for ("" the primary):
	// resolved from the request by the handler, never read from the beacon,
	// which anyone can send naming any site.
	DomainID   string
	Class      classifier.Result
	Country    string
	ClientType string // human | GoodBot | BadBot | AIAgent | Headless | Unknown
	BotScore   float64
	Now        time.Time
	// Real-user Core Web Vitals as reported by the page's PerformanceObserver
	// (migration 091). Zero means "not reported yet"; the unload beacon
	// overwrites with MAX semantics, so late INP/CLS still land on the row.
	LCPMs   int // Largest Contentful Paint, milliseconds
	INPMs   int // Interaction to Next Paint, milliseconds
	CLSX100 int // Cumulative Layout Shift × 100 (integer-exact)
	// CLSKnown says CLSX100 was measured: the browser observes layout shifts
	// and the page said so. Its 0 is then no shift, not "not reported".
	CLSKnown bool
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
	lcp, inp, clsx := clampCWV(in.LCPMs, in.INPMs, in.CLSX100)
	_, err := e.ExecContext(ctx, `INSERT INTO vayuanalytics_sessions
(session_hash,visitor_hash,page_path,source_category,source_detail,referrer_domain,referrer_path,entry_time,country_code,client_type,bot_score,is_new_session,lcp_ms,inp_ms,cls_x100,cls_seen,domain_id)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.SessionHash, visitor, in.PagePath, string(in.Class.Category), in.Class.Detail,
		in.Class.ReferrerDomain, in.Class.ReferrerPath, now, in.Country, ct, in.BotScore, isNew,
		lcp, inp, clsx, clsSeen(in.CLSKnown, in.CLSX100), in.DomainID)
	return err
}

// clampCWV bounds client-reported vitals before storage. A beacon is
// unauthenticated input: negative or absurd values are lies, and a lie that
// reaches an aggregate is a lie the operator reads. Ceilings are far above any
// real observation (a 60s LCP means the tab was backgrounded, not that paint
// took a minute) while still letting genuine slow-network data through.
func clampCWV(lcp, inp, clsx int) (int, int, int) {
	if lcp < 0 || lcp > 60000 {
		lcp = 0
	}
	if inp < 0 || inp > 10000 {
		inp = 0
	}
	if clsx < 0 || clsx > 4000 { // CLS > 40 is not a layout shift, it's a seizure
		clsx = 0
	}
	return lcp, inp, clsx
}

// BeaconInput is an engagement update sent as the reader leaves or hides the tab.
type BeaconInput struct {
	SessionHash  string
	PagePath     string
	TimeOnPage   int // seconds
	ScrollDepth  int // percent 0..100
	Interactions int
	Now          time.Time
	// Late-reported Core Web Vitals (migration 091): INP and CLS often only
	// exist by unload time. Zero = not reported; the UPDATE folds with MAX so
	// the enter row's earlier value never regresses.
	LCPMs    int
	INPMs    int
	CLSX100  int
	CLSKnown bool // as EnterInput's
}

// clsSeen is 1 when a reported layout shift was measured and is in range:
// one out of range is a lie clampCWV zeroed, not a measurement of 0.
func clsSeen(known bool, clsx int) int {
	if known && clsx >= 0 && clsx <= 4000 {
		return 1
	}
	return 0
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
	lcp, inp, clsx := clampCWV(in.LCPMs, in.INPMs, in.CLSX100)
	_, err := e.ExecContext(ctx, `UPDATE vayuanalytics_sessions SET
exit_time=?,
time_on_page_seconds=MAX(time_on_page_seconds,?),
scroll_depth_percent=MAX(scroll_depth_percent,?),
interaction_count=MAX(interaction_count,?),
lcp_ms=CASE WHEN ?=0 THEN lcp_ms ELSE MAX(lcp_ms,?) END,
inp_ms=CASE WHEN ?=0 THEN inp_ms ELSE MAX(inp_ms,?) END,
cls_x100=CASE WHEN ?=0 THEN cls_x100 ELSE MAX(cls_x100,?) END,
cls_seen=MAX(cls_seen,?),
engaged=CASE WHEN MAX(time_on_page_seconds,?)>=? AND MAX(scroll_depth_percent,?)>=? THEN 1 ELSE 0 END,
bounce=CASE WHEN MAX(time_on_page_seconds,?)<? AND MAX(scroll_depth_percent,?)<? THEN 1 ELSE 0 END
WHERE id=(SELECT id FROM vayuanalytics_sessions WHERE session_hash=? AND page_path=? ORDER BY entry_time DESC LIMIT 1)`,
		now,
		in.TimeOnPage,
		in.ScrollDepth,
		in.Interactions,
		lcp, lcp,
		inp, inp,
		clsx, clsx,
		clsSeen(in.CLSKnown, in.CLSX100),
		in.TimeOnPage, engagedThresholdSec, in.ScrollDepth, engagedThresholdScroll,
		in.TimeOnPage, bounceMaxSec, in.ScrollDepth, engagedThresholdScroll,
		in.SessionHash, in.PagePath)
	return err
}

// ── Async batched ingestion ───────────────────────────────────────────────────

const (
	ingestBuffer     = 4096            // bounded backlog; full ⇒ drop (best-effort)
	ingestMaxBatch   = 256             // flush early once this many events accrue
	ingestFlushEvery = 1 * time.Second // otherwise flush on this cadence
)

// HoldWhile holds the ingest's batches, not writing them, while held()
// reports true (the install's mode refuses writes). Call before StartIngest.
func (s *Store) HoldWhile(held func() bool) {
	if s != nil {
		s.hold = held
	}
}

// StartIngest launches the background batched-write loop. Call once at boot with
// the process shutdown channel. It is safe to call on a nil/dbless store (no-op)
// and idempotent. Until it runs, QueueEnter/QueueBeacon write synchronously.
// Events are applied in arrival order, so a beacon that shares a batch with
// its enter still folds into the just-inserted row.
func (s *Store) StartIngest(done <-chan struct{}) {
	if s == nil || s.db == nil || s.ingest != nil {
		return
	}
	s.ingest = dbbatch.New(s.db, ingestBuffer, ingestMaxBatch, ingestFlushEvery)
	s.ingest.HoldWhile(s.hold)
	go s.ingest.Run(done)
}

// IngestStopped is closed once the ingest has written (or counted lost) its
// last batch after the shutdown channel closed; closed at once if it never
// started.
func (s *Store) IngestStopped() <-chan struct{} {
	if s == nil {
		return (*dbbatch.Writer)(nil).Stopped()
	}
	return s.ingest.Stopped()
}

// IngestStats is the ingest queue's state for the console: events waiting and
// the buffer's size, those dropped because it was full, and those lost
// because the database refused them (dbbatch.Writer.Lost).
type IngestStats struct {
	Buffered, Cap int
	Dropped, Lost int64
}

// Stats reports the ingest queue. Zero before StartIngest.
func (s *Store) Stats() IngestStats {
	if s == nil || s.ingest == nil {
		return IngestStats{}
	}
	n, c := s.ingest.Buffered()
	return IngestStats{Buffered: n, Cap: c, Dropped: s.ingest.Dropped(), Lost: s.ingest.Lost()}
}

// Dropped reports how many telemetry events were shed because the ingest buffer
// was full (an extreme flood). Exposed for observability.
func (s *Store) Dropped() int64 {
	if s == nil {
		return 0
	}
	return s.ingest.Dropped()
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
	s.ingest.Submit(func(ctx context.Context, tx *sql.Tx) error { return recordEnter(ctx, tx, in) })
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
	s.ingest.Submit(func(ctx context.Context, tx *sql.Tx) error { return recordBeacon(ctx, tx, in) })
}

// ── GDPR retention ────────────────────────────────────────────────────────────

// purgeChunk bounds each DELETE transaction. One giant DELETE on the single
// SQLite writer held its lock (and rebuilt the rollback journal) for as long
// as the expired range was deep — minutes on large tables — stalling every
// beacon flush behind it (2025 audit). Chunked deletes keep each transaction
// short so the writer interleaves with live ingest.
const purgeChunk = 5000

// PurgeSite deletes one hosted site's engagement rows older than retainDays,
// chunked as Purge is. site must name a hosted site.
func (s *Store) PurgeSite(ctx context.Context, site string, retainDays int) (int64, error) {
	if s == nil || s.db == nil || site == "" || retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays)
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM vayuanalytics_sessions WHERE id IN (SELECT id FROM vayuanalytics_sessions WHERE domain_id=? AND entry_time<? LIMIT ?)`,
			site, cutoff, purgeChunk)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeChunk {
			return total, nil
		}
	}
}

// Purge deletes engagement rows older than retainDays (measured from entry_time).
// Returns the number of rows removed. A retainDays <= 0 is a no-op ("forever").
// Deletion runs in rowid-bounded chunks; between chunks the writer lock is
// released, WAL is checkpoint-truncated once at the end.
func (s *Store) Purge(ctx context.Context, retainDays int) (int64, error) {
	if s == nil || s.db == nil || retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays)
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM vayuanalytics_sessions WHERE id IN (SELECT id FROM vayuanalytics_sessions WHERE entry_time<? LIMIT ?)`,
			cutoff, purgeChunk)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < purgeChunk {
			break // range exhausted
		}
	}
	// Fold the WAL back after a big removal so readers do not churn through
	// dead pages for hours afterwards.
	_, _ = s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return total, nil
}
