// Package mode — journal persists mode transitions to SQLite for cross-restart audit.
package mode

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Journal persists mode transitions durably to SQLite.
// It registers an OnTransition hook with the provided Manager and replays
// existing history on Open so the in-memory Manager reflects prior state.
type Journal struct {
	db  *sql.DB
	mgr *Manager
}

// OpenJournal opens (or creates) the mode journal in the given SQLite file.
// On success it registers a write hook on mgr and returns past transitions
// stored in the DB — the caller may inspect them for audit or replay.
func OpenJournal(path string, mgr *Manager) (*Journal, []Transition, error) {
	db, err := sql.Open("sqlite3", path+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, nil, fmt.Errorf("mode journal open: %w", err)
	}

	if err := migrateJournal(db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("mode journal migrate: %w", err)
	}

	past, err := loadHistory(db)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("mode journal load: %w", err)
	}

	j := &Journal{db: db, mgr: mgr}
	mgr.OnTransition(j.write)
	return j, past, nil
}

// Close releases the database connection.
func (j *Journal) Close() error {
	return j.db.Close()
}

// History returns all transitions stored in the journal, oldest first.
func (j *Journal) History() ([]Transition, error) {
	return loadHistory(j.db)
}

func (j *Journal) write(t Transition) {
	_, _ = j.db.Exec(
		`INSERT INTO mode_transitions(from_mode,to_mode,reason,cause,occurred_at) VALUES(?,?,?,?,?)`,
		string(t.From), string(t.To), t.Reason, t.Cause, t.OccurredAt.UTC().Format(time.RFC3339Nano),
	)
}

func migrateJournal(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS mode_transitions (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		from_mode   TEXT    NOT NULL,
		to_mode     TEXT    NOT NULL,
		reason      TEXT    NOT NULL DEFAULT '',
		cause       TEXT    NOT NULL DEFAULT '',
		occurred_at TEXT    NOT NULL
	)`)
	return err
}

func loadHistory(db *sql.DB) ([]Transition, error) {
	rows, err := db.Query(
		`SELECT from_mode,to_mode,reason,cause,occurred_at FROM mode_transitions ORDER BY id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Transition
	for rows.Next() {
		var t Transition
		var ts string
		if err := rows.Scan(&t.From, &t.To, &t.Reason, &t.Cause, &ts); err != nil {
			return nil, err
		}
		t.OccurredAt, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, t)
	}
	return out, rows.Err()
}
