// Package vacuum writes converted Ghost posts into a VayuPress SQLite database.
package vacuum

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Article mirrors the VayuPress articles table.
type Article struct {
	ID        string
	Title     string
	Slug      string
	Content   string
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Writer inserts articles into the VayuPress SQLite database.
type Writer struct {
	db *sql.DB
}

// Open opens (or creates) the VayuPress SQLite database at path.
// It applies the minimal schema needed if the articles table doesn't exist yet.
func Open(path string) (*Writer, error) {
	dsn := path + "?_journal_mode=WAL&_busy_timeout=10000&_foreign_keys=on&_synchronous=NORMAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("vayu open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("vayu ping: %w", err)
	}
	// Ensure articles table exists (mirrors VayuPress migration 001-baseline)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS articles(
		id        TEXT PRIMARY KEY,
		title     TEXT NOT NULL,
		slug      TEXT UNIQUE NOT NULL,
		content   TEXT NOT NULL,
		tags      TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		return nil, fmt.Errorf("vayu schema: %w", err)
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_slug    ON articles(slug)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_created ON articles(created_at DESC)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_tags    ON articles(tags)`)
	return &Writer{db: db}, nil
}

// Close releases the database connection.
func (w *Writer) Close() { w.db.Close() }

// SlugExists returns true if a post with this slug already exists.
func (w *Writer) SlugExists(ctx context.Context, slug string) (bool, error) {
	var n int
	err := w.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE slug=?`, slug).Scan(&n)
	return n > 0, err
}

// Insert writes an article. Returns (true, nil) on success, (false, nil) if slug already exists.
func (w *Writer) Insert(ctx context.Context, a Article) (bool, error) {
	exists, err := w.SlugExists(ctx, a.Slug)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if a.ID == "" {
		a.ID = newID()
	}
	_, err = w.db.ExecContext(ctx,
		`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.Title, a.Slug, a.Content,
		strings.Join(a.Tags, ","),
		a.CreatedAt.UTC().Format(time.RFC3339),
		a.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return false, nil
		}
		return false, fmt.Errorf("insert %q: %w", a.Slug, err)
	}
	return true, nil
}

// Checkpoint writes progress to a simple key-value table so the migration
// can resume after interruption.
func (w *Writer) SaveCheckpoint(ctx context.Context, offset int64) error {
	_, err := w.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ghost2vayu_checkpoint(
			key TEXT PRIMARY KEY,
			val TEXT NOT NULL
		)`)
	if err != nil {
		return err
	}
	_, err = w.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO ghost2vayu_checkpoint(key,val) VALUES('offset',?)`,
		fmt.Sprintf("%d", offset))
	return err
}

// LoadCheckpoint returns the last saved offset (0 if none).
func (w *Writer) LoadCheckpoint(ctx context.Context) (int64, error) {
	_, err := w.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ghost2vayu_checkpoint(
			key TEXT PRIMARY KEY,
			val TEXT NOT NULL
		)`)
	if err != nil {
		return 0, err
	}
	var val string
	err = w.db.QueryRowContext(ctx,
		`SELECT val FROM ghost2vayu_checkpoint WHERE key='offset'`).Scan(&val)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int64
	fmt.Sscanf(val, "%d", &n)
	return n, nil
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
