// Package wpdb reads posts from a WordPress MySQL database.
package wpdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Post represents a single WordPress post with resolved tags and feature image.
type Post struct {
	ID           string
	Title        string
	Slug         string
	Content      string // HTML
	Tags         []string
	FeatureImage string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Status       string
	PostType     string
}

// Reader fetches posts from a WordPress MySQL database.
type Reader struct {
	db *sql.DB
}

// New opens a connection to the WordPress MySQL database using dsn.
func New(dsn string) (*Reader, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("wpdb open: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("wpdb ping: %w", err)
	}
	return &Reader{db: db}, nil
}

// Close releases the database connection.
func (r *Reader) Close() error { return r.db.Close() }

// Count returns the number of posts matching status and postType.
// status: "publish", "draft", or "all"
// postType: "post", "page", or "both"
func (r *Reader) Count(ctx context.Context, status, postType, prefix string) (int64, error) {
	query, args := buildBaseQuery(prefix, status, postType, true, "", 0)
	var count int64
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return count, nil
}

// Fetch retrieves up to limit posts with ID > afterID, ordered by ID ascending.
// Tags and feature images are resolved in batch for the returned posts.
func (r *Reader) Fetch(ctx context.Context, status, postType, prefix string, limit int, afterID string) ([]Post, error) {
	query, args := buildBaseQuery(prefix, status, postType, false, afterID, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		var slug sql.NullString
		if err := rows.Scan(&p.ID, &p.Title, &slug, &p.Content, &p.Status, &p.PostType, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		if slug.Valid && slug.String != "" {
			p.Slug = slug.String
		} else {
			p.Slug = slugify(p.Title)
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	if len(posts) == 0 {
		return posts, nil
	}

	// Collect IDs for batch queries.
	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}

	// Batch fetch tags.
	tagMap, err := r.fetchTags(ctx, prefix, ids)
	if err != nil {
		return nil, err
	}

	// Batch fetch feature images.
	imgMap, err := r.fetchFeatureImages(ctx, prefix, ids)
	if err != nil {
		return nil, err
	}

	for i := range posts {
		posts[i].Tags = tagMap[posts[i].ID]
		posts[i].FeatureImage = imgMap[posts[i].ID]
	}
	return posts, nil
}

func (r *Reader) fetchTags(ctx context.Context, prefix string, ids []string) (map[string][]string, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT tr.object_id, t.name
		FROM %sterm_relationships tr
		JOIN %sterm_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id
		JOIN %sterms t ON t.term_id = tt.term_id
		WHERE tt.taxonomy IN ('category','post_tag')
		  AND tr.object_id IN (%s)
		ORDER BY tr.object_id, t.name`,
		prefix, prefix, prefix, placeholders)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("fetch tags: %w", err)
	}
	defer rows.Close()

	tagMap := make(map[string][]string)
	for rows.Next() {
		var objectID, name string
		if err := rows.Scan(&objectID, &name); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		tagMap[objectID] = append(tagMap[objectID], name)
	}
	return tagMap, rows.Err()
}

func (r *Reader) fetchFeatureImages(ctx context.Context, prefix string, ids []string) (map[string]string, error) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT pm.post_id, p.guid
		FROM %spostmeta pm
		JOIN %sposts p ON p.ID = pm.meta_value
		WHERE pm.meta_key = '_thumbnail_id'
		  AND pm.post_id IN (%s)`,
		prefix, prefix, placeholders)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("fetch feature images: %w", err)
	}
	defer rows.Close()

	imgMap := make(map[string]string)
	for rows.Next() {
		var postID, guid string
		if err := rows.Scan(&postID, &guid); err != nil {
			return nil, fmt.Errorf("scan feature image: %w", err)
		}
		imgMap[postID] = guid
	}
	return imgMap, rows.Err()
}

// buildBaseQuery constructs the SELECT or COUNT query with appropriate filters.
func buildBaseQuery(prefix, status, postType string, count bool, afterID string, limit int) (string, []interface{}) {
	var sb strings.Builder
	var args []interface{}

	if count {
		sb.WriteString(fmt.Sprintf("SELECT COUNT(*) FROM %sposts WHERE 1=1", prefix))
	} else {
		sb.WriteString(fmt.Sprintf(
			"SELECT ID, post_title, post_name, post_content, post_status, post_type, post_date, post_modified FROM %sposts WHERE 1=1",
			prefix))
	}

	// Status filter.
	switch status {
	case "all":
		sb.WriteString(" AND post_status IN ('publish','draft')")
	default:
		sb.WriteString(" AND post_status = ?")
		args = append(args, status)
	}

	// Post type filter.
	switch postType {
	case "both":
		sb.WriteString(" AND post_type IN ('post','page')")
	default:
		sb.WriteString(" AND post_type = ?")
		args = append(args, postType)
	}

	if !count {
		if afterID != "" {
			sb.WriteString(" AND ID > ?")
			args = append(args, afterID)
		}
		sb.WriteString(" ORDER BY ID LIMIT ?")
		args = append(args, limit)
	}

	return sb.String(), args
}

// slugify converts a title to a URL-safe slug.
func slugify(title string) string {
	slug := strings.ToLower(title)
	var sb strings.Builder
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			sb.WriteRune(r)
		case r == ' ':
			sb.WriteRune('-')
		}
	}
	result := sb.String()
	// Collapse multiple hyphens.
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	return strings.Trim(result, "-")
}
