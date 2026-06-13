package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Config holds SQLite connection parameters.
type Config struct {
	Path            string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	BusyTimeout     time.Duration
	WALCheckpoint   int
}

// DefaultConfig returns production-safe SQLite defaults.
func DefaultConfig(path string) Config {
	return Config{
		Path:            path,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: 30 * time.Minute,
		BusyTimeout:     5 * time.Second,
		WALCheckpoint:   1000,
	}
}

// Open opens a SQLite database with governance PRAGMAs applied.
func Open(cfg Config) (*sql.DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_busy_timeout=%d&_synchronous=NORMAL&_foreign_keys=ON&_cache_size=10000",
		cfg.Path,
		cfg.BusyTimeout.Milliseconds(),
	)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("db.Open: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	if err := applyPragmas(db, cfg); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func applyPragmas(db *sql.DB, cfg Config) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		fmt.Sprintf("PRAGMA busy_timeout=%d", cfg.BusyTimeout.Milliseconds()),
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA temp_store=MEMORY",
		fmt.Sprintf("PRAGMA wal_autocheckpoint=%d", cfg.WALCheckpoint),
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("db: pragma %q: %w", p, err)
		}
	}
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("db: integrity_check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("db: integrity_check failed: %s", result)
	}
	return nil
}

// WALStats returns current WAL size in pages for monitoring.
func WALStats(db *sql.DB) (walPages int, err error) {
	err = db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(new(int), new(int), &walPages)
	return
}
