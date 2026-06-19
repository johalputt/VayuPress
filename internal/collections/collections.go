// Package collections manages Series / Collections of VayuPress articles.
// A collection is an ordered list of articles with a title and description.
package collections

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Collection groups related articles into an ordered series.
type Collection struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	Articles    []string  `json:"articles,omitempty"` // article IDs in order
}

// Store manages collections in SQLite.
type Store struct{ db *sql.DB }

// New creates a Store.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Create creates a new collection.
func (s *Store) Create(ctx context.Context, title, slug, description string) (*Collection, error) {
	id := newID()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO collections(id,title,slug,description) VALUES(?,?,?,?)`,
		id, title, slug, description)
	if err != nil {
		return nil, fmt.Errorf("collections create: %w", err)
	}
	return &Collection{ID: id, Title: title, Slug: slug, Description: description, CreatedAt: time.Now()}, nil
}

// AddArticle appends an article to a collection at the given position.
func (s *Store) AddArticle(ctx context.Context, collectionID, articleID string, position int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO collection_articles(collection_id,article_id,position) VALUES(?,?,?)`,
		collectionID, articleID, position)
	return err
}

// RemoveArticle removes an article from a collection.
func (s *Store) RemoveArticle(ctx context.Context, collectionID, articleID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM collection_articles WHERE collection_id=? AND article_id=?`,
		collectionID, articleID)
	return err
}

// Get returns a collection with its ordered article IDs.
func (s *Store) Get(ctx context.Context, id string) (*Collection, error) {
	c := &Collection{}
	var createdRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,title,slug,description,created_at FROM collections WHERE id=?`, id).
		Scan(&c.ID, &c.Title, &c.Slug, &c.Description, &createdRaw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("collections get: %w", err)
	}
	c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdRaw)
	rows, err := s.db.QueryContext(ctx,
		`SELECT article_id FROM collection_articles WHERE collection_id=? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var aid string
		if err := rows.Scan(&aid); err != nil {
			return nil, err
		}
		c.Articles = append(c.Articles, aid)
	}
	return c, rows.Err()
}

// GetBySlug looks up a collection by slug.
func (s *Store) GetBySlug(ctx context.Context, slug string) (*Collection, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM collections WHERE slug=?`, slug).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// List returns all collections (without article IDs for brevity).
func (s *Store) List(ctx context.Context) ([]Collection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,slug,description,created_at FROM collections ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("collections list: %w", err)
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		var c Collection
		var createdRaw string
		if err := rows.Scan(&c.ID, &c.Title, &c.Slug, &c.Description, &createdRaw); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdRaw)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes a collection and its article memberships.
func (s *Store) Delete(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM collection_articles WHERE collection_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM collections WHERE id=?`, id)
	return err
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
