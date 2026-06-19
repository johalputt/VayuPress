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

type Article struct {
	ID        string
	Title     string
	Slug      string
	Content   string
	Tags      []string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Writer struct {
	db *sql.DB
}

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
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS substack2vayu_checkpoint(
		key TEXT PRIMARY KEY,
		val TEXT NOT NULL
	)`); err != nil {
		return nil, fmt.Errorf("vayu checkpoint table: %w", err)
	}
	return &Writer{db: db}, nil
}

func (w *Writer) Close() { w.db.Close() }

func (w *Writer) Insert(ctx context.Context, a Article) (bool, error) {
	if a.ID == "" {
		a.ID = newID()
	}
	res, err := w.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO articles(id,title,slug,content,tags,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?)`,
		a.ID, a.Title, a.Slug, a.Content,
		strings.Join(a.Tags, ","),
		a.CreatedAt.UTC().Format(time.RFC3339),
		a.UpdatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return false, fmt.Errorf("insert %q: %w", a.Slug, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected %q: %w", a.Slug, err)
	}
	return n > 0, nil
}

func (w *Writer) SaveCheckpoint(ctx context.Context, lastID string) error {
	_, err := w.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO substack2vayu_checkpoint(key,val) VALUES('last_id',?)`,
		lastID)
	return err
}

func (w *Writer) LoadCheckpoint(ctx context.Context) (string, error) {
	var val string
	err := w.db.QueryRowContext(ctx,
		`SELECT val FROM substack2vayu_checkpoint WHERE key='last_id'`).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
