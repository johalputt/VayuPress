// Package vacuum writes parsed Hugo documents into a VayuPress SQLite database.
package vacuum

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/hugo2vayu/internal/hugoparse"
)

// Writer inserts articles into the VayuPress SQLite database.
type Writer struct {
	db *sql.DB
}

// Open opens (or creates) the VayuPress SQLite database at dbPath. It ensures
// the articles table and checkpoint table exist so the tool works against a
// brand-new database file.
func Open(dbPath string) (*Writer, error) {
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=10000&_foreign_keys=on&_synchronous=NORMAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("vayu open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("vayu ping: %w", err)
	}

	// Create articles table (mirrors VayuPress schema).
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS articles(
		id        TEXT PRIMARY KEY,
		title     TEXT NOT NULL,
		slug      TEXT UNIQUE NOT NULL,
		content   TEXT NOT NULL,
		tags      TEXT DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("vayu schema: %w", err)
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_slug    ON articles(slug)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_created ON articles(created_at DESC)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_articles_tags    ON articles(tags)`)

	// Create checkpoint table for resume support.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS hugo2vayu_checkpoint(
		key TEXT PRIMARY KEY,
		val TEXT NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("vayu checkpoint table: %w", err)
	}

	return &Writer{db: db}, nil
}

// Close releases the database connection.
func (w *Writer) Close() { w.db.Close() }

// InsertArticle inserts a Document into the articles table using INSERT OR IGNORE
// so that re-running after interruption safely skips already-inserted rows.
// Returns (true, nil) when a new row was inserted, (false, nil) when skipped.
func (w *Writer) InsertArticle(doc *hugoparse.Document) (bool, error) {
	id := newID()
	now := time.Now().UTC().Format(time.RFC3339)
	dateStr := doc.Date.UTC().Format(time.RFC3339)

	res, err := w.db.Exec(
		`INSERT OR IGNORE INTO articles(id,title,slug,content,tags,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?)`,
		id,
		doc.Title,
		doc.Slug,
		doc.HTML,
		strings.Join(doc.Tags, ","),
		dateStr,
		now,
	)
	if err != nil {
		return false, fmt.Errorf("insert %q: %w", doc.Slug, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected %q: %w", doc.Slug, err)
	}
	return n > 0, nil
}

// GetCheckpoint returns the last processed file path ("" if none).
func (w *Writer) GetCheckpoint() (string, error) {
	var val string
	err := w.db.QueryRow(
		`SELECT val FROM hugo2vayu_checkpoint WHERE key='last_file'`).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get checkpoint: %w", err)
	}
	return val, nil
}

// SetCheckpoint records the last successfully processed file path.
func (w *Writer) SetCheckpoint(path string) error {
	_, err := w.db.Exec(
		`INSERT OR REPLACE INTO hugo2vayu_checkpoint(key,val) VALUES('last_file',?)`,
		path)
	if err != nil {
		return fmt.Errorf("set checkpoint: %w", err)
	}
	return nil
}

// newID generates a random hex UUID-like identifier using crypto/rand.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	// Format as UUID: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
