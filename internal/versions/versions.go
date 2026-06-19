// Package versions stores and retrieves article revision history.
// A snapshot is taken automatically before each update so content can be rolled back.
package versions

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Version is a point-in-time snapshot of an article.
type Version struct {
	ID        int64     `json:"id"`
	ArticleID string    `json:"article_id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	SavedAt   time.Time `json:"saved_at"`
	Label     string    `json:"label"`
}

// Store manages article version history.
type Store struct{ db *sql.DB }

// New creates a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Save stores a snapshot of the article before it is modified.
func (s *Store) Save(ctx context.Context, articleID, slug, title, content, tags, label string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO article_versions(article_id,slug,title,content,tags,label) VALUES(?,?,?,?,?,?)`,
		articleID, slug, title, content, tags, label)
	if err != nil {
		return 0, fmt.Errorf("versions save: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// List returns the N most recent versions for an article (newest first).
func (s *Store) List(ctx context.Context, articleID string, limit int) ([]Version, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,article_id,slug,title,content,tags,saved_at,label
		 FROM article_versions WHERE article_id=? ORDER BY saved_at DESC LIMIT ?`,
		articleID, limit)
	if err != nil {
		return nil, fmt.Errorf("versions list: %w", err)
	}
	defer rows.Close()
	var vs []Version
	for rows.Next() {
		var v Version
		var savedRaw, tagsStr string
		if err := rows.Scan(&v.ID, &v.ArticleID, &v.Slug, &v.Title, &v.Content, &tagsStr, &savedRaw, &v.Label); err != nil {
			return nil, err
		}
		v.SavedAt, _ = time.Parse(time.RFC3339, savedRaw)
		if tagsStr != "" {
			v.Tags = strings.Split(tagsStr, ",")
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}

// Get returns a single version by ID.
func (s *Store) Get(ctx context.Context, id int64) (*Version, error) {
	var v Version
	var savedRaw, tagsStr string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,article_id,slug,title,content,tags,saved_at,label FROM article_versions WHERE id=?`, id).
		Scan(&v.ID, &v.ArticleID, &v.Slug, &v.Title, &v.Content, &tagsStr, &savedRaw, &v.Label)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("versions get: %w", err)
	}
	v.SavedAt, _ = time.Parse(time.RFC3339, savedRaw)
	if tagsStr != "" {
		v.Tags = strings.Split(tagsStr, ",")
	}
	return &v, nil
}

// Purge deletes versions older than maxAge for an article, keeping at least keepMin recent ones.
func (s *Store) Purge(ctx context.Context, articleID string, maxAge time.Duration, keepMin int) (int64, error) {
	cutoff := time.Now().Add(-maxAge).UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM article_versions
		WHERE article_id=? AND saved_at < ?
		  AND id NOT IN (
		    SELECT id FROM article_versions WHERE article_id=?
		    ORDER BY saved_at DESC LIMIT ?
		  )`, articleID, cutoff, articleID, keepMin)
	if err != nil {
		return 0, fmt.Errorf("versions purge: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
