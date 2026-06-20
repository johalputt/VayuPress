// Package newsletter manages email subscribers for VayuPress.
// It provides subscriber management only — actual email delivery is the operator's responsibility
// (use any SMTP library or service with the exported subscriber list).
package newsletter

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// Subscriber represents a newsletter subscriber.
type Subscriber struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	Status         string     `json:"status"`
	Confirmed      bool       `json:"confirmed"`
	Token          string     `json:"-"`
	SubscribedAt   time.Time  `json:"subscribed_at"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at,omitempty"`
}

// Store manages newsletter subscribers in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Subscribe adds a new subscriber. Returns (sub, true, nil) when new, (sub, false, nil) when already subscribed.
func (s *Store) Subscribe(ctx context.Context, email string) (*Subscriber, bool, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, false, fmt.Errorf("invalid email: %w", err)
	}

	// Check if already exists.
	var existing Subscriber
	var subsRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,email,status,confirmed,subscribed_at FROM newsletter_subscribers WHERE email=?`, email).
		Scan(&existing.ID, &existing.Email, &existing.Status, &existing.Confirmed, &subsRaw)
	if err == nil {
		existing.SubscribedAt, _ = time.Parse("2006-01-02 15:04:05", subsRaw)
		return &existing, false, nil
	}
	if err != sql.ErrNoRows {
		return nil, false, err
	}

	id := newID()
	token := newToken()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO newsletter_subscribers(id,email,token) VALUES(?,?,?)`, id, email, token)
	if err != nil {
		return nil, false, fmt.Errorf("newsletter subscribe: %w", err)
	}
	return &Subscriber{ID: id, Email: email, Status: "active", Token: token, SubscribedAt: time.Now()}, true, nil
}

// Confirm marks a subscriber as confirmed using their token.
func (s *Store) Confirm(ctx context.Context, token string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE newsletter_subscribers SET confirmed=1 WHERE token=? AND confirmed=0`, token)
	if err != nil {
		return fmt.Errorf("newsletter confirm: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("token not found or already confirmed")
	}
	return nil
}

// Unsubscribe marks a subscriber inactive via their token.
func (s *Store) Unsubscribe(ctx context.Context, token string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE newsletter_subscribers SET status='inactive',unsubscribed_at=? WHERE token=?`, now, token)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("token not found")
	}
	return nil
}

// ListActive returns all active, confirmed subscribers.
func (s *Store) ListActive(ctx context.Context) ([]Subscriber, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,email,status,confirmed,token,subscribed_at FROM newsletter_subscribers WHERE status='active' AND confirmed=1 ORDER BY subscribed_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscriber
	for rows.Next() {
		var sub Subscriber
		var subsRaw string
		if err := rows.Scan(&sub.ID, &sub.Email, &sub.Status, &sub.Confirmed, &sub.Token, &subsRaw); err != nil {
			return nil, err
		}
		sub.SubscribedAt, _ = time.Parse("2006-01-02 15:04:05", subsRaw)
		out = append(out, sub)
	}
	return out, rows.Err()
}

// Count returns subscriber counts by status.
func (s *Store) Count(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status,COUNT(*) FROM newsletter_subscribers GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int64{}
	for rows.Next() {
		var st string
		var n int64
		rows.Scan(&st, &n)
		m[st] = n
	}
	return m, rows.Err()
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func newToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}
