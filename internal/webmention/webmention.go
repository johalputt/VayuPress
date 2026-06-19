// Package webmention implements a Webmention receiver for VayuPress.
// Webmentions are a W3C standard for cross-site notification when one page links to another.
// This package receives incoming mentions and stores them for moderation.
package webmention

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Mention is an incoming webmention notification.
type Mention struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Target     string    `json:"target"`
	Type       string    `json:"type"`
	Author     string    `json:"author"`
	Title      string    `json:"title"`
	Excerpt    string    `json:"excerpt"`
	Status     string    `json:"status"`
	ReceivedAt time.Time `json:"received_at"`
}

const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
)

// Store manages webmentions in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Receive validates and stores an incoming webmention.
// It returns ErrSelf when source and target have the same host (self-ping).
func (s *Store) Receive(ctx context.Context, sourceURL, targetURL string) (*Mention, error) {
	src, err := url.Parse(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("invalid source URL: %w", err)
	}
	tgt, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}
	if !strings.HasPrefix(src.Scheme, "http") || !strings.HasPrefix(tgt.Scheme, "http") {
		return nil, fmt.Errorf("source and target must be HTTP/HTTPS URLs")
	}
	if strings.EqualFold(src.Hostname(), tgt.Hostname()) {
		return nil, fmt.Errorf("self-ping rejected: source and target share the same host")
	}

	id := newID()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO webmentions(id,source,target) VALUES(?,?,?)`, id, sourceURL, targetURL)
	if err != nil {
		return nil, fmt.Errorf("webmention receive: %w", err)
	}
	return &Mention{
		ID: id, Source: sourceURL, Target: targetURL,
		Status: StatusPending, ReceivedAt: time.Now(),
	}, nil
}

// ListForTarget returns approved webmentions for a target URL.
func (s *Store) ListForTarget(ctx context.Context, targetURL string) ([]Mention, error) {
	return s.list(ctx, `WHERE target=? AND status='approved' ORDER BY received_at DESC`, targetURL)
}

// ListAll returns all webmentions for admin review.
func (s *Store) ListAll(ctx context.Context, status string, limit int) ([]Mention, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `WHERE 1=1`
	var args []interface{}
	if status != "" && status != "all" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY received_at DESC LIMIT ?`
	args = append(args, limit)
	return s.list(ctx, q, args...)
}

// Moderate updates the status of a webmention.
func (s *Store) Moderate(ctx context.Context, id, status string) error {
	if status != StatusApproved && status != StatusRejected {
		return fmt.Errorf("invalid status %q", status)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE webmentions SET status=? WHERE id=?`, status, id)
	return err
}

// UpdateMeta updates the author, title, and excerpt after fetching the source page.
func (s *Store) UpdateMeta(ctx context.Context, id, author, title, excerpt string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE webmentions SET author=?,title=?,type='mention' WHERE id=?`,
		author, title, id)
	return err
}

func (s *Store) list(ctx context.Context, where string, args ...interface{}) ([]Mention, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,source,target,type,author,title,excerpt,status,received_at FROM webmentions `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mention
	for rows.Next() {
		var m Mention
		var recvRaw string
		if err := rows.Scan(&m.ID, &m.Source, &m.Target, &m.Type, &m.Author, &m.Title, &m.Excerpt, &m.Status, &recvRaw); err != nil {
			return nil, err
		}
		m.ReceivedAt, _ = time.Parse("2006-01-02 15:04:05", recvRaw)
		out = append(out, m)
	}
	return out, rows.Err()
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
