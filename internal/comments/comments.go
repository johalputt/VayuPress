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

// Comment is a reader reply on an article. A non-empty ParentID marks this as a
// threaded reply to another comment; top-level comments leave it empty.
type Comment struct {
	ID        string    `json:"id"`
	ArticleID string    `json:"article_id"`
	ParentID  string    `json:"parent_id,omitempty"`
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

// Submit creates a new top-level comment in pending status.
func (s *Store) Submit(ctx context.Context, articleID, author, email, body, ip string) (*Comment, error) {
	return s.SubmitReply(ctx, articleID, "", author, email, body, ip)
}

// SubmitReply creates a new comment in pending status, optionally as a threaded
// reply to parentID (empty for a top-level comment).
func (s *Store) SubmitReply(ctx context.Context, articleID, parentID, author, email, body, ip string) (*Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("comment body is empty")
	}
	if strings.TrimSpace(author) == "" {
		return nil, fmt.Errorf("author name is required")
	}
	id := newID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO comments(id,article_id,parent_id,author,email,body,ip) VALUES(?,?,?,?,?,?,?)`,
		id, articleID, parentID, author, email, body, ip)
	if err != nil {
		return nil, fmt.Errorf("comments submit: %w", err)
	}
	return &Comment{
		ID: id, ArticleID: articleID, ParentID: parentID, Author: author, Email: email,
		Body: body, Status: StatusPending, IP: ip, CreatedAt: time.Now(),
	}, nil
}

// Get returns a single comment by ID (any status), or an error if not found.
func (s *Store) Get(ctx context.Context, id string) (*Comment, error) {
	cs, err := s.list(ctx, `WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, fmt.Errorf("comment %q not found", id)
	}
	return &cs[0], nil
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

// MemberComment is one of a member's own comments, joined to its article's slug
// and title so the reader-facing activity view can link back to each thread.
type MemberComment struct {
	Comment
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

// ListByEmail returns a member's own comments (any status) newest-first, joined
// to the article slug/title for linking. The read pool is passed in explicitly
// so this SELECT never runs on the single writer connection (scale rule). The
// comments table is small, so the email filter scan is cheap; the article join
// is an indexed primary-key lookup.
func (s *Store) ListByEmail(ctx context.Context, reader *sql.DB, email string, limit int) ([]MemberComment, error) {
	if strings.TrimSpace(email) == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := reader.QueryContext(ctx,
		`SELECT c.id,c.article_id,c.parent_id,c.author,c.email,c.body,c.status,c.created_at,
		        COALESCE(a.slug,''),COALESCE(a.title,'')
		   FROM comments c LEFT JOIN articles a ON a.id=c.article_id
		  WHERE c.email=? ORDER BY c.created_at DESC LIMIT ?`,
		strings.ToLower(strings.TrimSpace(email)), limit)
	if err != nil {
		return nil, fmt.Errorf("comments by email: %w", err)
	}
	defer rows.Close()
	var out []MemberComment
	for rows.Next() {
		var c MemberComment
		var createdRaw string
		if err := rows.Scan(&c.ID, &c.ArticleID, &c.ParentID, &c.Author, &c.Email, &c.Body, &c.Status, &createdRaw, &c.Slug, &c.Title); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdRaw)
		out = append(out, c)
	}
	return out, rows.Err()
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
		`SELECT id,article_id,parent_id,author,email,body,status,ip,created_at FROM comments `+whereClause,
		args...)
	if err != nil {
		return nil, fmt.Errorf("comments list: %w", err)
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		var createdRaw string
		if err := rows.Scan(&c.ID, &c.ArticleID, &c.ParentID, &c.Author, &c.Email, &c.Body, &c.Status, &c.IP, &createdRaw); err != nil {
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
