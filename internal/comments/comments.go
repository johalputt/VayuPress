// Package comments provides a local comment system for VayuPress articles.
// Comments are stored in SQLite with a moderation workflow (pending → approved/rejected).
package comments

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Status constants for comment moderation.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusRejected = "rejected"
	StatusSpam     = "spam"
)

// Comment is a reader reply on an article.
type Comment struct {
	ID        string    `json:"id"`
	ArticleID string    `json:"article_id"`
	Author    string    `json:"author"`
	Email     string    `json:"email,omitempty"`
	Body      string    `json:"body"`
	Status    string    `json:"status"`
	IP        string    `json:"ip,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store manages comments in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Submit creates a new comment in pending status.
func (s *Store) Submit(ctx context.Context, articleID, author, email, body, ip string) (*Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("comment body is empty")
	}
	if strings.TrimSpace(author) == "" {
		return nil, fmt.Errorf("author name is required")
	}
	id := newID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO comments(id,article_id,author,email,body,ip) VALUES(?,?,?,?,?,?)`,
		id, articleID, author, email, body, ip)
	if err != nil {
		return nil, fmt.Errorf("comments submit: %w", err)
	}
	return &Comment{
		ID: id, ArticleID: articleID, Author: author, Email: email,
		Body: body, Status: StatusPending, IP: ip, CreatedAt: time.Now(),
	}, nil
}

// ListApproved returns approved comments for an article ordered by creation time.
func (s *Store) ListApproved(ctx context.Context, articleID string) ([]Comment, error) {
	return s.list(ctx, `WHERE article_id=? AND status='approved' ORDER BY created_at`, articleID)
}

// ListAll returns all comments for admin moderation (newest first).
func (s *Store) ListAll(ctx context.Context, status string, limit int) ([]Comment, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `WHERE 1=1`
	var args []interface{}
	if status != "" && status != "all" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	return s.list(ctx, q, args...)
}

// Moderate updates the status of a comment.
func (s *Store) Moderate(ctx context.Context, id, status string) error {
	if status != StatusApproved && status != StatusRejected && status != StatusSpam {
		return fmt.Errorf("invalid status %q", status)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE comments SET status=? WHERE id=?`, status, id)
	if err != nil {
		return fmt.Errorf("comments moderate: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("comment %q not found", id)
	}
	return nil
}

// Delete removes a comment permanently.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM comments WHERE id=?`, id)
	return err
}

// Count returns comment counts by status.
func (s *Store) Count(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status,COUNT(*) FROM comments GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int64{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		m[st] = n
	}
	return m, rows.Err()
}

func (s *Store) list(ctx context.Context, whereClause string, args ...interface{}) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,article_id,author,email,body,status,ip,created_at FROM comments `+whereClause,
		args...)
	if err != nil {
		return nil, fmt.Errorf("comments list: %w", err)
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		var createdRaw string
		if err := rows.Scan(&c.ID, &c.ArticleID, &c.Author, &c.Email, &c.Body, &c.Status, &c.IP, &createdRaw); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdRaw)
		out = append(out, c)
	}
	return out, rows.Err()
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
