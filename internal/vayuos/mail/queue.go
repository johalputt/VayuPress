package mail

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/mail"
	"time"
)

// DeliverFunc performs the actual SMTP delivery of a queued message. It is
// injected so the queue is unit-testable without a network.
type DeliverFunc func(ctx context.Context, from string, to []string, raw []byte) error

// Queue is a durable, SQLite-backed outbound mail queue with exponential
// backoff retry. It is the single writer path for outgoing mail.
type Queue struct {
	db      *sql.DB
	cfg     Config
	deliver DeliverFunc
}

// NewQueue creates a queue over db, creating its table if needed.
func NewQueue(db *sql.DB, cfg Config, deliver DeliverFunc) (*Queue, error) {
	q := &Queue{db: db, cfg: cfg, deliver: deliver}
	if err := q.init(); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *Queue) init() error {
	_, err := q.db.Exec(`
CREATE TABLE IF NOT EXISTS vayumail_queue(
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_addr   TEXT NOT NULL,
    to_json     TEXT NOT NULL,
    raw         BLOB NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 12,
    state       TEXT NOT NULL DEFAULT 'pending',
    last_error  TEXT NOT NULL DEFAULT '',
    next_attempt DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_vmq_state_next ON vayumail_queue(state, next_attempt);`)
	return err
}

// Enqueue adds a message for delivery and returns its queue id.
func (q *Queue) Enqueue(ctx context.Context, from string, to []string, raw []byte) (int64, error) {
	toJSON, err := json.Marshal(to)
	if err != nil {
		return 0, err
	}
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO vayumail_queue(from_addr,to_json,raw,max_attempts,state,next_attempt,created_at) VALUES(?,?,?,?,'pending',?,?)`,
		from, string(toJSON), raw, q.cfg.QueueMaxAttempts, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type queueItem struct {
	id       int64
	from     string
	to       []string
	raw      []byte
	attempts int
	maxAtt   int
}

// ProcessDue attempts delivery of every message whose next_attempt has arrived.
// It returns the number of messages delivered and the number that failed
// (permanently or transiently) on this pass.
func (q *Queue) ProcessDue(ctx context.Context, now time.Time) (delivered, failed int, err error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id,from_addr,to_json,raw,attempts,max_attempts FROM vayumail_queue WHERE state='pending' AND next_attempt<=? ORDER BY next_attempt LIMIT 100`,
		now.UTC())
	if err != nil {
		return 0, 0, err
	}
	var items []queueItem
	for rows.Next() {
		var it queueItem
		var toJSON string
		if err := rows.Scan(&it.id, &it.from, &toJSON, &it.raw, &it.attempts, &it.maxAtt); err != nil {
			rows.Close()
			return delivered, failed, err
		}
		_ = json.Unmarshal([]byte(toJSON), &it.to)
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return delivered, failed, err
	}

	for _, it := range items {
		derr := error(nil)
		if q.deliver != nil {
			derr = q.deliver(ctx, it.from, it.to, it.raw)
		} else {
			derr = fmt.Errorf("no delivery transport configured")
		}
		if derr == nil {
			_, _ = q.db.ExecContext(ctx, `UPDATE vayumail_queue SET state='delivered', last_error='' WHERE id=?`, it.id)
			delivered++
			continue
		}
		failed++
		attempts := it.attempts + 1
		if attempts >= it.maxAtt {
			_, _ = q.db.ExecContext(ctx,
				`UPDATE vayumail_queue SET state='failed', attempts=?, last_error=? WHERE id=?`,
				attempts, derr.Error(), it.id)
			continue
		}
		// Exponential backoff capped to keep next_attempt sane.
		backoff := q.cfg.QueueBaseBackoff
		for i := 1; i < attempts && backoff < 6*time.Hour; i++ {
			backoff *= 2
		}
		next := now.UTC().Add(backoff)
		_, _ = q.db.ExecContext(ctx,
			`UPDATE vayumail_queue SET attempts=?, last_error=?, next_attempt=? WHERE id=?`,
			attempts, derr.Error(), next, it.id)
	}
	return delivered, failed, nil
}

// SentInfo summarises an outbound queue message for the panel.
type SentInfo struct {
	ID        int64    `json:"id"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	Subject   string   `json:"subject"`
	State     string   `json:"state"`
	Attempts  int      `json:"attempts"`
	LastError string   `json:"last_error"`
	CreatedAt string   `json:"created_at"`
}

// Recent returns the most recent outbound messages (the "Sent" view).
func (q *Queue) Recent(ctx context.Context, limit int) ([]SentInfo, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT id,from_addr,to_json,raw,state,attempts,last_error,created_at FROM vayumail_queue ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SentInfo{}
	for rows.Next() {
		var si SentInfo
		var toJSON string
		var raw []byte
		if err := rows.Scan(&si.ID, &si.From, &toJSON, &raw, &si.State, &si.Attempts, &si.LastError, &si.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(toJSON), &si.To)
		if msg, perr := mail.ReadMessage(bytes.NewReader(raw)); perr == nil {
			si.Subject = msg.Header.Get("Subject")
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// Status returns counters for the VayuOS panel.
func (q *Queue) Status(ctx context.Context) (*QueueStatus, *SMTPStats, error) {
	st := &QueueStatus{CheckedAt: time.Now().UTC()}
	stats := &SMTPStats{}
	_ = q.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_queue WHERE state='pending'`).Scan(&st.Pending)
	_ = q.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_queue WHERE state='failed'`).Scan(&st.Failed)
	_ = q.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_queue WHERE state='delivered'`).Scan(&stats.Delivered)
	stats.Queued = st.Pending
	stats.Failed = st.Failed
	var oldest sql.NullString
	_ = q.db.QueryRowContext(ctx, `SELECT MIN(created_at) FROM vayumail_queue WHERE state='pending'`).Scan(&oldest)
	if oldest.Valid && oldest.String != "" {
		if t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", oldest.String); err == nil {
			st.OldestAge = time.Since(t).Truncate(time.Second).String()
		} else if t, err := time.Parse(time.RFC3339, oldest.String); err == nil {
			st.OldestAge = time.Since(t).Truncate(time.Second).String()
		}
	}
	return st, stats, nil
}
