package queue

import (
	"context"
	"database/sql"
	"encoding/json"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// Writer is the enqueue contract. ArticleService depends on this interface
// rather than on a concrete DB function, enabling test doubles and future
// queue backend replacements.
type Writer interface {
	Enqueue(ctx context.Context, art dbpkg.Article, op string) error
}

// SQLiteWriter implements Writer by inserting jobs into the write_jobs table.
type SQLiteWriter struct {
	db *sql.DB
}

// NewSQLiteWriter returns a Writer backed by the given DB.
func NewSQLiteWriter(db *sql.DB) Writer {
	return &SQLiteWriter{db: db}
}

func (w *SQLiteWriter) Enqueue(_ context.Context, art dbpkg.Article, op string) error {
	payload, err := json.Marshal(art)
	if err != nil {
		return err
	}
	_, err = w.db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,?)`, payload, op)
	return err
}
