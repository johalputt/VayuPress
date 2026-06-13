package migrations

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"time"
)

// DryRunResult describes what a migration would do without applying it.
type DryRunResult struct {
	Version   string
	Direction string
	SQL       string
	Checksum  string
	WouldSkip bool // already applied
}

// DryRun returns what Up() would execute without making any changes.
func (m *Migrator) DryRun() ([]DryRunResult, error) {
	if err := m.ensureTable(); err != nil {
		return nil, err
	}
	applied, err := m.applied()
	if err != nil {
		return nil, err
	}
	files, err := fs.Glob(sqlFiles, "sql/*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("migrations: glob: %w", err)
	}
	sort.Strings(files)

	results := make([]DryRunResult, 0, len(files))
	for _, f := range files {
		version := versionOf(f)
		blob, err := sqlFiles.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("migrations: read %s: %w", f, err)
		}
		results = append(results, DryRunResult{
			Version:   version,
			Direction: "up",
			SQL:       string(blob),
			Checksum:  sha256Hex(string(blob)),
			WouldSkip: applied[version],
		})
	}
	return results, nil
}

// VerifyChecksums checks that every applied migration's stored checksum still
// matches the embedded SQL file. Returns a list of drift violations.
type ChecksumDrift struct {
	Version  string
	Stored   string
	Computed string
}

func (m *Migrator) VerifyChecksums() ([]ChecksumDrift, error) {
	rows, err := m.db.Query(`SELECT version, checksum FROM schema_migrations WHERE direction='up'`)
	if err != nil {
		return nil, fmt.Errorf("migrations: query checksums: %w", err)
	}
	defer rows.Close()

	type record struct{ version, checksum string }
	var records []record
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.version, &r.checksum); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var drifts []ChecksumDrift
	for _, rec := range records {
		f := fmt.Sprintf("sql/%s.up.sql", rec.version)
		blob, err := sqlFiles.ReadFile(f)
		if err != nil {
			// File removed from embed — always a drift.
			drifts = append(drifts, ChecksumDrift{
				Version:  rec.version,
				Stored:   rec.checksum,
				Computed: "(file missing)",
			})
			continue
		}
		computed := sha256Hex(string(blob))
		if computed != rec.checksum {
			drifts = append(drifts, ChecksumDrift{
				Version:  rec.version,
				Stored:   rec.checksum,
				Computed: computed,
			})
		}
	}
	return drifts, nil
}

// JournalEntry records every migration attempt (success or failure) for audit.
type JournalEntry struct {
	ID          int64
	Version     string
	Direction   string
	Checksum    string
	AttemptedAt time.Time
	Succeeded   bool
	ErrorMsg    string
}

// EnsureJournal creates the migration_journal table if it does not exist.
func (m *Migrator) EnsureJournal() error {
	_, err := m.db.Exec(`CREATE TABLE IF NOT EXISTS migration_journal (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		version      TEXT NOT NULL,
		direction    TEXT NOT NULL,
		checksum     TEXT NOT NULL,
		attempted_at TEXT NOT NULL DEFAULT (datetime('now','utc')),
		succeeded    INTEGER NOT NULL DEFAULT 0,
		error_msg    TEXT NOT NULL DEFAULT ''
	)`)
	return err
}

// RecordJournalEntry appends an audit row to migration_journal.
func (m *Migrator) RecordJournalEntry(version, direction, checksum string, succeeded bool, errMsg string) error {
	_, err := m.db.Exec(
		`INSERT INTO migration_journal(version,direction,checksum,succeeded,error_msg) VALUES(?,?,?,?,?)`,
		version, direction, checksum, boolInt(succeeded), errMsg,
	)
	return err
}

// Journal returns all journal entries ordered oldest-first.
func (m *Migrator) Journal(db *sql.DB) ([]JournalEntry, error) {
	rows, err := db.Query(`SELECT id,version,direction,checksum,attempted_at,succeeded,error_msg
		FROM migration_journal ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []JournalEntry
	for rows.Next() {
		var e JournalEntry
		var succeeded int
		var ts string
		if err := rows.Scan(&e.ID, &e.Version, &e.Direction, &e.Checksum, &ts, &succeeded, &e.ErrorMsg); err != nil {
			return nil, err
		}
		e.Succeeded = succeeded == 1
		e.AttemptedAt, _ = time.Parse("2006-01-02 15:04:05", ts)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
