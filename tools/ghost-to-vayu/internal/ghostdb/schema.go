// Package ghostdb reads posts directly from a Ghost CMS database (MySQL or SQLite).
// It requires NO Ghost admin access — direct DB connection only.
package ghostdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Post is a single Ghost post row joined with tags and primary author.
type Post struct {
	ID           string
	Title        string
	Slug         string
	HTML         string // rendered HTML (preferred over mobiledoc)
	Mobiledoc    string // fallback if HTML is empty
	Lexical      string // Ghost 5.x editor format
	PublishedAt  time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Status       string   // published | draft | scheduled
	Tags         []string // slugs
	AuthorName   string
	FeatureImage string
}

// Reader fetches Ghost posts in batches.
type Reader struct {
	db     *sql.DB
	driver string // "mysql" or "sqlite3"
}

// New opens the Ghost database. dsn format:
//
//	mysql:   "user:pass@tcp(host:3306)/ghost_db"
//	sqlite3: "/var/lib/ghost/content/data/ghost.db"
func New(driver, dsn string) (*Reader, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("ghostdb open: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ghostdb ping: %w", err)
	}
	return &Reader{db: db, driver: driver}, nil
}

// Close releases the database connection.
func (r *Reader) Close() { r.db.Close() }

// Count returns the number of posts matching the status filter.
func (r *Reader) Count(ctx context.Context, status string) (int64, error) {
	q := `SELECT COUNT(*) FROM posts WHERE type='post'`
	var args []interface{}
	if status != "all" {
		q += ` AND status=?`
		args = append(args, status)
	}
	var n int64
	err := r.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// Fetch retrieves a batch of posts starting at offset, ordered by id.
// Each Post's Tags slice is populated via a secondary query.
func (r *Reader) Fetch(ctx context.Context, status string, limit, offset int) ([]Post, error) {
	q := `
SELECT
  p.id,
  p.title,
  p.slug,
  COALESCE(p.html,'')       AS html,
  COALESCE(p.mobiledoc,'')  AS mobiledoc,
  COALESCE(p.lexical,'')    AS lexical,
  COALESCE(p.published_at, p.created_at) AS published_at,
  p.created_at,
  p.updated_at,
  p.status,
  COALESCE(p.feature_image,'') AS feature_image
FROM posts p
WHERE p.type='post'`

	var args []interface{}
	if status != "all" {
		q += ` AND p.status=?`
		args = append(args, status)
	}
	q += ` ORDER BY p.id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ghostdb fetch: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		var pubAt sql.NullTime
		var createdAt, updatedAt sql.NullTime
		err := rows.Scan(
			&p.ID, &p.Title, &p.Slug,
			&p.HTML, &p.Mobiledoc, &p.Lexical,
			&pubAt, &createdAt, &updatedAt,
			&p.Status, &p.FeatureImage,
		)
		if err != nil {
			return nil, fmt.Errorf("ghostdb scan: %w", err)
		}
		if pubAt.Valid {
			p.PublishedAt = pubAt.Time
		}
		if createdAt.Valid {
			p.CreatedAt = createdAt.Time
		} else {
			p.CreatedAt = time.Now()
		}
		if updatedAt.Valid {
			p.UpdatedAt = updatedAt.Time
		} else {
			p.UpdatedAt = p.CreatedAt
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Populate tags and author for each post
	for i := range posts {
		posts[i].Tags, err = r.fetchTags(ctx, posts[i].ID)
		if err != nil {
			return nil, err
		}
		posts[i].AuthorName, err = r.fetchAuthor(ctx, posts[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return posts, nil
}

func (r *Reader) fetchTags(ctx context.Context, postID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.slug FROM tags t
		JOIN posts_tags pt ON pt.tag_id = t.id
		WHERE pt.post_id = ?
		ORDER BY pt.sort_order`, postID)
	if err != nil {
		return nil, fmt.Errorf("fetchTags: %w", err)
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		if s != "" {
			tags = append(tags, s)
		}
	}
	return tags, rows.Err()
}

func (r *Reader) fetchAuthor(ctx context.Context, postID string) (string, error) {
	var name string
	err := r.db.QueryRowContext(ctx, `
		SELECT u.name FROM users u
		JOIN posts_authors pa ON pa.author_id = u.id
		WHERE pa.post_id = ?
		ORDER BY pa.sort_order LIMIT 1`, postID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		// older Ghost versions use posts_authors differently; try posts table
		r.db.QueryRowContext(ctx,
			`SELECT COALESCE(author_id,'') FROM posts WHERE id=?`, postID).Scan(&name)
		return name, nil
	}
	return strings.TrimSpace(name), nil
}
