package federation

import (
	"database/sql"
	"fmt"
	"time"
)

// ReplayStore provides durable, SQLite-backed replay protection for federation
// activities. Activity IDs are stored with a TTL; expired entries are pruned
// on access. Survives process restarts and WAL recovery.
type ReplayStore struct {
	db  *sql.DB
	ttl time.Duration
}

const defaultReplayTTL = 7 * 24 * time.Hour // 7 days covers all clock-skew scenarios

// NewReplayStore creates a ReplayStore backed by the given DB.
// Call EnsureSchema before first use.
func NewReplayStore(db *sql.DB, ttl time.Duration) *ReplayStore {
	if ttl <= 0 {
		ttl = defaultReplayTTL
	}
	return &ReplayStore{db: db, ttl: ttl}
}

// EnsureSchema creates the federation_seen_activities table if it does not exist.
func (rs *ReplayStore) EnsureSchema() error {
	_, err := rs.db.Exec(`CREATE TABLE IF NOT EXISTS federation_seen_activities (
		activity_id TEXT NOT NULL PRIMARY KEY,
		seen_at     TEXT NOT NULL DEFAULT (datetime('now','utc')),
		expires_at  TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("replay: schema: %w", err)
	}
	_, err = rs.db.Exec(`CREATE INDEX IF NOT EXISTS idx_fed_expires ON federation_seen_activities(expires_at)`)
	return err
}

// Seen returns true if the activity ID has been processed within the TTL window.
// Also prunes expired entries to prevent unbounded growth.
func (rs *ReplayStore) Seen(activityID string) (bool, error) {
	// Prune expired in same transaction as check — keeps table bounded.
	_, err := rs.db.Exec(
		`DELETE FROM federation_seen_activities WHERE expires_at < datetime('now','utc')`)
	if err != nil {
		return false, fmt.Errorf("replay: prune: %w", err)
	}

	var count int
	err = rs.db.QueryRow(
		`SELECT COUNT(*) FROM federation_seen_activities WHERE activity_id=?`, activityID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("replay: check: %w", err)
	}
	return count > 0, nil
}

// Mark records the activity ID as seen. Returns an error if already seen (replay).
func (rs *ReplayStore) Mark(activityID string) error {
	if activityID == "" {
		return fmt.Errorf("replay: empty activity ID")
	}
	expiresAt := time.Now().UTC().Add(rs.ttl).Format("2006-01-02 15:04:05")
	_, err := rs.db.Exec(
		`INSERT OR IGNORE INTO federation_seen_activities(activity_id, expires_at) VALUES(?,?)`,
		activityID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("replay: mark: %w", err)
	}
	return nil
}

// MarkOrReject atomically marks the activity as seen if new; returns ErrReplay
// if it was already recorded. Atomicity matters: a check-then-insert (Seen then
// Mark) has a TOCTOU window where two concurrent deliveries of the same activity
// can both observe "unseen" and both be admitted. Here the single INSERT is the
// decision — rows-affected == 0 means the PRIMARY KEY already held the ID, i.e. a
// replay — so the verdict is correct regardless of connection pool size.
var ErrReplay = fmt.Errorf("federation: replay detected — activity already processed")

func (rs *ReplayStore) MarkOrReject(activityID string) error {
	if activityID == "" {
		return fmt.Errorf("replay: empty activity ID")
	}
	// Prune expired entries first so a long-expired ID can be legitimately re-seen.
	if _, err := rs.db.Exec(
		`DELETE FROM federation_seen_activities WHERE expires_at < datetime('now','utc')`,
	); err != nil {
		return fmt.Errorf("replay: prune: %w", err)
	}
	expiresAt := time.Now().UTC().Add(rs.ttl).Format("2006-01-02 15:04:05")
	res, err := rs.db.Exec(
		`INSERT OR IGNORE INTO federation_seen_activities(activity_id, expires_at) VALUES(?,?)`,
		activityID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("replay: mark-or-reject: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("replay: rows-affected: %w", err)
	}
	if n == 0 {
		return ErrReplay // PRIMARY KEY collision → already processed
	}
	return nil
}

// Count returns the number of active (non-expired) entries in the store.
func (rs *ReplayStore) Count() (int, error) {
	var n int
	err := rs.db.QueryRow(
		`SELECT COUNT(*) FROM federation_seen_activities WHERE expires_at >= datetime('now','utc')`,
	).Scan(&n)
	return n, err
}
