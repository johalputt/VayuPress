package update

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store is the SQLite-backed audit trail for self-update attempts.
type Store struct {
	db *sql.DB
}

// Record is a single update_history row.
type Record struct {
	ID          int64
	FromVersion string
	ToVersion   string
	Status      string // checked, started, success, failed, rolled_back
	BackupPath  string
	Detail      string
	StartedAt   time.Time
	CompletedAt *time.Time
}

// New returns a Store, ensuring the update_history table exists.
func New(db *sql.DB) *Store {
	if db != nil {
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS update_history(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  from_version TEXT,
  to_version TEXT,
  status TEXT NOT NULL,
  backup_path TEXT DEFAULT '',
  detail TEXT DEFAULT '',
  started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at DATETIME
)`)
	}
	return &Store{db: db}
}

// Log records an update attempt and returns the new row ID.
func (s *Store) Log(ctx context.Context, rec Record) (int64, error) {
	if rec.StartedAt.IsZero() {
		rec.StartedAt = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO update_history(from_version,to_version,status,backup_path,detail,started_at,completed_at)
		 VALUES(?,?,?,?,?,?,?)`,
		rec.FromVersion, rec.ToVersion, rec.Status, rec.BackupPath, rec.Detail, rec.StartedAt, rec.CompletedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("update: log insert: %w", err)
	}
	return res.LastInsertId()
}

// List returns the most recent update_history rows, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,from_version,to_version,status,backup_path,detail,started_at,completed_at
		 FROM update_history ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("update: list: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var completed sql.NullTime
		if err := rows.Scan(&r.ID, &r.FromVersion, &r.ToVersion, &r.Status,
			&r.BackupPath, &r.Detail, &r.StartedAt, &completed); err != nil {
			return nil, fmt.Errorf("update: scan: %w", err)
		}
		if completed.Valid {
			t := completed.Time
			r.CompletedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkComplete sets the terminal status, detail, and completion timestamp.
func (s *Store) MarkComplete(ctx context.Context, id int64, status, detail string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE update_history SET status=?, detail=?, completed_at=? WHERE id=?`,
		status, detail, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update: mark complete: %w", err)
	}
	return nil
}
