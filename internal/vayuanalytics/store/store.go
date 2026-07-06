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
)

// Store wraps an open *sql.DB whose schema includes vayuanalytics_sessions.
type Store struct{ db *sql.DB }

// New constructs a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// EnterInput is a page-enter event (fired immediately on page load).
type EnterInput struct {
	SessionHash string
	PagePath    string
	Class       classifier.Result
	Country     string
	ClientType  string // human | GoodBot | BadBot | AIAgent | Headless | Unknown
	BotScore    float64
	Now         time.Time
}

// RecordEnter inserts a new engagement row for a page view. is_new_session is
// true when this session hash has not been seen today, giving new-vs-returning
// analytics without any cross-day identifier.
func (s *Store) RecordEnter(ctx context.Context, in EnterInput) error {
	if s == nil || s.db == nil || in.SessionHash == "" || in.PagePath == "" {
		return nil
	}
	now := in.Now.UTC()
	if in.Now.IsZero() {
		now = time.Now().UTC()
	}
	day := now.Format("2006-01-02")
	var seen int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM vayuanalytics_sessions WHERE session_hash=? AND date(entry_time)=?`,
		in.SessionHash, day).Scan(&seen)
	isNew := 1
	if seen > 0 {
		isNew = 0
	}
	ct := in.ClientType
	if ct == "" {
		ct = "human"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO vayuanalytics_sessions
(session_hash,page_path,source_category,source_detail,referrer_domain,referrer_path,entry_time,country_code,client_type,bot_score,is_new_session)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		in.SessionHash, in.PagePath, string(in.Class.Category), in.Class.Detail,
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
	if s == nil || s.db == nil || in.SessionHash == "" || in.PagePath == "" {
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
	_, err := s.db.ExecContext(ctx, `UPDATE vayuanalytics_sessions SET
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
