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

// Post is a single Ghost post row with its tag slugs.
type Post struct {
	ID           string
	Title        string
	Slug         string
	HTML         string // server-rendered HTML (preferred)
	Mobiledoc    string // Ghost 1.x–4.x editor JSON (fallback)
	Lexical      string // Ghost 5.x editor JSON (fallback)
	PublishedAt  time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Status       string   // published | draft | scheduled
	Tags         []string // tag slugs, in Ghost sort order
	FeatureImage string
}

// Reader fetches Ghost posts in batches using keyset pagination on the primary key.
type Reader struct {
	db     *sql.DB
	driver string
}

// New opens the Ghost database. dsn format:
//
//	mysql:   "user:pass@tcp(host:3306)/ghost_db"
//	sqlite3: "/var/lib/ghost/content/data/ghost.db"
//
// For MySQL, parseTime=true is required so DATETIME columns scan into time.Time;
// it is appended automatically if absent.
func New(driver, dsn string) (*Reader, error) {
	if driver == "mysql" {
		dsn = ensureMySQLParseTime(dsn)
	}
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

// ensureMySQLParseTime appends parseTime=true to a MySQL DSN if it is missing.
// Without it, the driver returns DATETIME as []byte and scanning into time.Time
// fails. Existing query parameters are preserved.
func ensureMySQLParseTime(dsn string) string {
	if strings.Contains(dsn, "parseTime=") {
		return dsn
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&parseTime=true"
	}
	return dsn + "?parseTime=true"
}

// Close releases the database connection.
func (r *Reader) Close() { r.db.Close() }

// timeLayouts covers the datetime string formats Ghost databases produce across
// MySQL and SQLite storage.
var timeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05.999999999-07:00",
	time.RFC3339Nano,
}

// toTime converts a scanned datetime value (time.Time, []byte, string, or nil)
// into a time.Time. The bool is false when the value is NULL or unparseable.
func toTime(v interface{}) (time.Time, bool) {
	switch t := v.(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		if t.IsZero() {
			return time.Time{}, false
		}
		return t, true
	case []byte:
		return parseTimeString(string(t))
	case string:
		return parseTimeString(t)
	default:
		return time.Time{}, false
	}
}

func parseTimeString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "0000-00-00") {
		return time.Time{}, false
	}
	for _, layout := range timeLayouts {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

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

// Fetch retrieves up to limit posts whose id sorts after afterID, ordered by id.
// Pass an empty afterID for the first batch. Keyset pagination keeps every batch
// fast and gentle on the database, even deep into a 200k+ post table (unlike
// OFFSET, which forces the server to scan and discard all preceding rows).
//
// The returned posts have their Tags populated via a single batched query.
func (r *Reader) Fetch(ctx context.Context, status string, limit int, afterID string) ([]Post, error) {
	q := `
SELECT
  p.id,
  p.title,
  p.slug,
  COALESCE(p.html,'')       AS html,
  COALESCE(p.mobiledoc,'')  AS mobiledoc,
  COALESCE(p.lexical,'')    AS lexical,
  p.published_at,
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
	if afterID != "" {
		q += ` AND p.id > ?`
		args = append(args, afterID)
	}
	q += ` ORDER BY p.id LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ghostdb fetch: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		// Scan the datetime columns into interface{} so we are agnostic to how
		// each driver returns them: go-sqlite3 yields time.Time or string, and
		// go-sql-driver/mysql yields time.Time (parseTime=true) or []byte.
		var pubRaw, createdRaw, updatedRaw interface{}
		err := rows.Scan(
			&p.ID, &p.Title, &p.Slug,
			&p.HTML, &p.Mobiledoc, &p.Lexical,
			&pubRaw, &createdRaw, &updatedRaw,
			&p.Status, &p.FeatureImage,
		)
		if err != nil {
			return nil, fmt.Errorf("ghostdb scan: %w", err)
		}
		created, hasCreated := toTime(createdRaw)
		pub, hasPub := toTime(pubRaw)
		updated, hasUpdated := toTime(updatedRaw)

		switch {
		case hasCreated:
			p.CreatedAt = created
		case hasPub:
			p.CreatedAt = pub
		default:
			p.CreatedAt = time.Now()
		}
		if hasPub {
			p.PublishedAt = pub
		} else {
			p.PublishedAt = p.CreatedAt
		}
		if hasUpdated {
			p.UpdatedAt = updated
		} else {
			p.UpdatedAt = p.CreatedAt
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := r.attachTags(ctx, posts); err != nil {
		return nil, err
	}
	return posts, nil
}

// attachTags fills in Tags for every post in the batch using one query, keyed by
// post id. This avoids the N+1 query pattern of fetching tags per post.
func (r *Reader) attachTags(ctx context.Context, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}
	idIndex := make(map[string]int, len(posts))
	placeholders := make([]string, len(posts))
	args := make([]interface{}, len(posts))
	for i, p := range posts {
		idIndex[p.ID] = i
		placeholders[i] = "?"
		args[i] = p.ID
	}

	q := `
		SELECT pt.post_id, t.slug
		FROM posts_tags pt
		JOIN tags t ON t.id = pt.tag_id
		WHERE pt.post_id IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY pt.post_id, pt.sort_order`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("attachTags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var postID, slug string
		if err := rows.Scan(&postID, &slug); err != nil {
			return fmt.Errorf("attachTags scan: %w", err)
		}
		if slug == "" {
			continue
		}
		if i, ok := idIndex[postID]; ok {
			posts[i].Tags = append(posts[i].Tags, slug)
		}
	}
	return rows.Err()
}
