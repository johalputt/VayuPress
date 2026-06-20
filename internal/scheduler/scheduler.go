// Package scheduler provides scheduled (future-dated) article publishing for
// VayuPress. Posts are staged in the scheduled_posts table with a publish_at
// timestamp; a background ticker promotes them to live articles when due by
// invoking the normal article-create pipeline (queue → render → index → cache),
// so scheduled posts reuse every existing durability and consistency guarantee.
//
// Durability: staged rows live in SQLite, so a crash before publish time simply
// resumes on the next startup tick. Promotion is idempotent — a row is marked
// 'published' only after the publish callback succeeds.
package scheduler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Status values for a staged post.
const (
	StatusScheduled = "scheduled"
	StatusPublished = "published"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// Post is a future-dated article awaiting publication.
type Post struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Content     string     `json:"content"`
	Tags        []string   `json:"tags"`
	PublishAt   time.Time  `json:"publish_at"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

// Store persists scheduled posts in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Schedule stages a post for future publication. publishAt is stored in UTC.
func (s *Store) Schedule(ctx context.Context, slug, title, content string, tags []string, publishAt time.Time) (*Post, error) {
	slug = strings.TrimSpace(slug)
	title = strings.TrimSpace(title)
	if slug == "" || title == "" {
		return nil, fmt.Errorf("slug and title are required")
	}
	if content == "" {
		return nil, fmt.Errorf("content is required")
	}
	id := newID()
	tagStr := strings.Join(tags, ",")
	pa := publishAt.UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_posts(id,slug,title,content,tags,publish_at,status) VALUES(?,?,?,?,?,?,?)`,
		id, slug, title, content, tagStr, pa.Format("2006-01-02 15:04:05"), StatusScheduled)
	if err != nil {
		return nil, fmt.Errorf("schedule: %w", err)
	}
	return &Post{ID: id, Slug: slug, Title: title, Content: content, Tags: tags,
		PublishAt: pa, Status: StatusScheduled, CreatedAt: time.Now().UTC()}, nil
}

// Due returns scheduled posts whose publish_at is at or before now (UTC),
// oldest first, up to limit.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,slug,title,content,tags,publish_at,status,error,created_at FROM scheduled_posts
		 WHERE status=? AND publish_at<=? ORDER BY publish_at LIMIT ?`,
		StatusScheduled, now.UTC().Format("2006-01-02 15:04:05"), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// List returns the most recent staged posts regardless of status, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Post, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,slug,title,content,tags,publish_at,status,error,created_at FROM scheduled_posts
		 ORDER BY publish_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPosts(rows)
}

// MarkPublished flips a row to published and records the publish time.
func (s *Store) MarkPublished(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_posts SET status=?,published_at=?,error='' WHERE id=?`,
		StatusPublished, time.Now().UTC().Format("2006-01-02 15:04:05"), id)
	return err
}

// MarkFailed records a publish failure, leaving the row for operator inspection.
func (s *Store) MarkFailed(ctx context.Context, id, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_posts SET status=?,error=? WHERE id=?`, StatusFailed, reason, id)
	return err
}

// Cancel marks a still-scheduled post as canceled. Returns an error if the row
// is not found or already left the scheduled state.
func (s *Store) Cancel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_posts SET status=? WHERE id=? AND status=?`, StatusCanceled, id, StatusScheduled)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("not found or no longer scheduled")
	}
	return nil
}

// PendingCount returns the number of posts still awaiting publication.
func (s *Store) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM scheduled_posts WHERE status=?`, StatusScheduled).Scan(&n)
	return n, err
}

func scanPosts(rows *sql.Rows) ([]Post, error) {
	var out []Post
	for rows.Next() {
		var p Post
		var tagStr string
		// expires/created columns are DATETIME; scan directly into time.Time so
		// go-sqlite3 handles the stored format (it returns RFC3339 to strings).
		if err := rows.Scan(&p.ID, &p.Slug, &p.Title, &p.Content, &tagStr, &p.PublishAt, &p.Status, &p.Error, &p.CreatedAt); err != nil {
			return nil, err
		}
		if tagStr != "" {
			p.Tags = strings.Split(tagStr, ",")
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
