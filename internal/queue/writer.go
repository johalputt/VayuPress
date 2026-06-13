package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/trace"
)

// ErrQueueSaturated is returned when the write queue has reached the configured
// hard limit and cannot accept new jobs. Callers should respond with HTTP 429.
var ErrQueueSaturated = errors.New("queue: at capacity — try again later")

// Writer is the enqueue contract. ArticleService depends on this interface
// rather than on a concrete DB function, enabling test doubles and future
// queue backend replacements.
type Writer interface {
	Enqueue(ctx context.Context, art dbpkg.Article, op string) error
}

// SQLiteWriter implements Writer by inserting jobs into the write_jobs table,
// persisting the correlation ID from ctx so the outbox relay can thread it
// through to the event envelope (ADR-0053).
// It enforces a configurable queue depth hard limit (ADR-0055).
type SQLiteWriter struct {
	db        *sql.DB
	hardLimit int // 0 = unlimited
}

// NewSQLiteWriter returns a Writer backed by the given DB.
// hardLimit is the maximum allowed pending job count before ErrQueueSaturated is returned.
// Pass 0 to disable the limit.
func NewSQLiteWriter(db *sql.DB, hardLimit int) Writer {
	return &SQLiteWriter{db: db, hardLimit: hardLimit}
}

func (w *SQLiteWriter) Enqueue(ctx context.Context, art dbpkg.Article, op string) error {
	if w.hardLimit > 0 {
		var depth int
		if err := w.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&depth); err == nil {
			if depth >= w.hardLimit {
				return fmt.Errorf("%w (depth=%d limit=%d)", ErrQueueSaturated, depth, w.hardLimit)
			}
		}
	}
	payload, err := json.Marshal(art)
	if err != nil {
		return err
	}
	corrID := trace.CorrelationID(ctx)
	_, err = w.db.Exec(
		`INSERT INTO write_jobs(article_json,op,correlation_id) VALUES(?,?,?)`,
		payload, op, corrID,
	)
	return err
}
