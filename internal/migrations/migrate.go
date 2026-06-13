// Package migrations provides a full-file SQL migration engine for SQLite.
// Each migration is a .sql file executed atomically in a single transaction.
package migrations

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

// Migrator applies SQL migrations to a database.
type Migrator struct {
	db *sql.DB
}

// New creates a Migrator backed by db.
func New(db *sql.DB) *Migrator {
	return &Migrator{db: db}
}

// MigrationRecord represents a row in schema_migrations.
type MigrationRecord struct {
	Version   string
	AppliedAt string
	Checksum  string
	Direction string
}

// Up applies all pending UP migrations in version order.
func (m *Migrator) Up() error {
	if err := m.ensureTable(); err != nil {
		return err
	}
	applied, err := m.applied()
	if err != nil {
		return err
	}
	files, err := fs.Glob(sqlFiles, "sql/*.up.sql")
	if err != nil {
		return fmt.Errorf("migrations: glob: %w", err)
	}
	sort.Strings(files)
	for _, f := range files {
		version := versionOf(f)
		if applied[version] {
			continue
		}
		blob, err := sqlFiles.ReadFile(f)
		if err != nil {
			return fmt.Errorf("migrations: read %s: %w", f, err)
		}
		if err := m.apply(version, string(blob), "up"); err != nil {
			return fmt.Errorf("migrations: apply %s: %w", f, err)
		}
	}
	return nil
}

// Down rolls back the last applied migration.
func (m *Migrator) Down() error {
	if err := m.ensureTable(); err != nil {
		return err
	}
	last, err := m.lastApplied()
	if err != nil {
		return err
	}
	if last == "" {
		return nil
	}
	f := fmt.Sprintf("sql/%s.down.sql", last)
	blob, err := sqlFiles.ReadFile(f)
	if err != nil {
		return fmt.Errorf("migrations: read down %s: %w", f, err)
	}
	return m.apply(last, string(blob), "down")
}

// Applied returns all applied migration records.
func (m *Migrator) Applied() ([]MigrationRecord, error) {
	if err := m.ensureTable(); err != nil {
		return nil, err
	}
	rows, err := m.db.Query(`SELECT version, applied_at, checksum, direction FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("migrations: query: %w", err)
	}
	defer rows.Close()
	var records []MigrationRecord
	for rows.Next() {
		var r MigrationRecord
		if err := rows.Scan(&r.Version, &r.AppliedAt, &r.Checksum, &r.Direction); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func (m *Migrator) ensureTable() error {
	_, err := m.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version     TEXT PRIMARY KEY,
		applied_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
		checksum    TEXT NOT NULL,
		direction   TEXT NOT NULL DEFAULT 'up'
	)`)
	return err
}

func (m *Migrator) applied() (map[string]bool, error) {
	rows, err := m.db.Query(`SELECT version FROM schema_migrations WHERE direction='up'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func (m *Migrator) lastApplied() (string, error) {
	var v string
	err := m.db.QueryRow(`SELECT version FROM schema_migrations WHERE direction='up' ORDER BY version DESC LIMIT 1`).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (m *Migrator) apply(version, sqlBlob, direction string) error {
	sum := sha256Hex(sqlBlob)
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(sqlBlob); err != nil {
		return fmt.Errorf("exec migration %s: %w", version, err)
	}
	if direction == "up" {
		_, err = tx.Exec(`INSERT OR REPLACE INTO schema_migrations(version,checksum,direction) VALUES(?,?,'up')`, version, sum)
	} else {
		_, err = tx.Exec(`DELETE FROM schema_migrations WHERE version=?`, version)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func versionOf(path string) string {
	base := path[len("sql/"):]
	base = strings.TrimSuffix(base, ".up.sql")
	base = strings.TrimSuffix(base, ".down.sql")
	return base
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
