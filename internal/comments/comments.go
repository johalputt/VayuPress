// SPDX-License-Identifier: Apache-2.0

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
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/metrics"
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
	Country   string    `json:"country,omitempty"` // ISO alpha-2 (GDPR-safe; no IP stored)
	Region    string    `json:"region,omitempty"`
	City      string    `json:"city,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Store manages comments in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Submit creates a new top-level comment in pending status. Location is coarse
// (country/region/city) and GDPR-safe — the caller resolves it without storing
// any IP.
func (s *Store) Submit(ctx context.Context, articleID, author, email, body, country, region, city string) (*Comment, error) {
	return s.SubmitReply(ctx, articleID, "", author, email, body, country, region, city)
}

// SubmitReply creates a new comment in pending status, optionally as a threaded
// reply to parentID (empty for a top-level comment). country/region/city are the
// coarse, GDPR-safe location captured at submit time (no IP is stored).
func (s *Store) SubmitReply(ctx context.Context, articleID, parentID, author, email, body, country, region, city string) (*Comment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("comment body is empty")
	}
	if strings.TrimSpace(author) == "" {
		return nil, fmt.Errorf("author name is required")
	}
	id := newID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO comments(id,article_id,parent_id,author,email,body,country,region,city) VALUES(?,?,?,?,?,?,?,?,?)`,
		id, articleID, parentID, author, email, body, country, region, city)
	if err != nil {
		return nil, fmt.Errorf("comments submit: %w", err)
	}
	return &Comment{
		ID: id, ArticleID: articleID, ParentID: parentID, Author: author, Email: email,
		Body: body, Status: StatusPending, Country: country, Region: region, City: city, CreatedAt: time.Now(),
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
		c.CreatedAt = parseDBTime(createdRaw)
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
	// Count every moderation action here (the single chokepoint), so the metric
	// reflects the HTMX fragment, the JSON API and any future path uniformly.
	atomic.AddInt64(&metrics.MetricCommentsModerated, 1)
	return nil
}

// Delete removes a comment permanently.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM comments WHERE id=?`, id)
	return err
}

// UpdateBody replaces a comment's body text, leaving its status and thread
// position unchanged so an edited comment stays exactly where it was. The caller
// authorises ownership; an empty body is rejected. Not-found is reported so a
// stale edit against a deleted comment fails cleanly rather than silently.
func (s *Store) UpdateBody(ctx context.Context, id, body string) error {
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("comment body is empty")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE comments SET body=? WHERE id=?`, body, id)
	if err != nil {
		return fmt.Errorf("comments update: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("comment %q not found", id)
	}
	return nil
}

// DeleteThread removes a comment together with any direct replies to it, so
// deleting a top-level comment never leaves replies orphaned behind a missing
// parent (they would otherwise persist yet never render). Deleting a reply
// removes just that reply — nothing points at it as a parent.
func (s *Store) DeleteThread(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM comments WHERE id=? OR parent_id=?`, id, id)
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
		`SELECT id,article_id,parent_id,author,email,body,status,country,region,city,created_at FROM comments `+whereClause,
		args...)
	if err != nil {
		return nil, fmt.Errorf("comments list: %w", err)
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		var createdRaw string
		if err := rows.Scan(&c.ID, &c.ArticleID, &c.ParentID, &c.Author, &c.Email, &c.Body, &c.Status, &c.Country, &c.Region, &c.City, &createdRaw); err != nil {
			return nil, err
		}
		c.CreatedAt = parseDBTime(createdRaw)
		out = append(out, c)
	}
	return out, rows.Err()
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// parseDBTime parses a created_at value in whatever textual form the SQLite
// driver hands back. The column is a DATETIME defaulting to CURRENT_TIMESTAMP
// ("YYYY-MM-DD HH:MM:SS" in UTC), but database/sql may also stringify a parsed
// time.Time as RFC3339 — so a single fixed layout silently failed and produced
// the zero time (rendered as "1 Jan 1"). Try the known shapes, treating a naive
// timestamp as UTC (matching SQLite), and fall back to zero only if none match.
func parseDBTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
