package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrNotFound is returned by the repository when a requested article does not exist.
var ErrNotFound = errors.New("article not found")

// sqliteArticleRepo is the SQLite implementation of api.ArticleRepository.
// The interface is defined in internal/api; this concrete type satisfies it
// via duck typing — no import of internal/api required.
//
// Writes go to the serialized writer (db); read-only methods use reader() so
// they fan across the WAL read pool instead of queuing behind writes (F-4).
type sqliteArticleRepo struct {
	db  *sql.DB
	rdb *sql.DB
}

// NewArticleRepo returns a concrete article repository backed by the given DB.
// The returned value satisfies api.ArticleRepository.
func NewArticleRepo(db *sql.DB) *sqliteArticleRepo {
	return &sqliteArticleRepo{db: db, rdb: Reader()}
}

// reader returns the read pool when configured, falling back to the writer.
func (r *sqliteArticleRepo) reader() *sql.DB {
	if r.rdb != nil {
		return r.rdb
	}
	return r.db
}

func (r *sqliteArticleRepo) SlugExists(ctx context.Context, slug string) (bool, error) {
	var n int
	err := r.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE slug=?`, slug).Scan(&n)
	return n > 0, err
}

func (r *sqliteArticleRepo) Create(ctx context.Context, art Article) error {
	status := art.Status
	if status == "" {
		status = "published"
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at,status) VALUES(?,?,?,?,?,?,?,?)`,
		art.ID, art.Title, art.Slug, art.Content,
		strings.Join(art.Tags, ","), art.CreatedAt, art.UpdatedAt, status,
	)
	return err
}

func (r *sqliteArticleRepo) Get(ctx context.Context, slug string) (Article, error) {
	var art Article
	var tagsCSV string
	err := r.reader().QueryRowContext(ctx,
		`SELECT id,title,slug,content,tags,created_at,updated_at,COALESCE(status,'published') FROM articles WHERE slug=?`, slug,
	).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsCSV, &art.CreatedAt, &art.UpdatedAt, &art.Status)
	if err == sql.ErrNoRows {
		return art, ErrNotFound
	}
	if err != nil {
		return art, err
	}
	art.Tags = splitCSV(tagsCSV)
	return art, nil
}

func (r *sqliteArticleRepo) Update(ctx context.Context, art Article) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE articles SET title=?,content=?,tags=?,updated_at=? WHERE slug=?`,
		art.Title, art.Content, strings.Join(art.Tags, ","), art.UpdatedAt, art.Slug,
	)
	return err
}

func (r *sqliteArticleRepo) Delete(ctx context.Context, slug string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM articles WHERE slug=?`, slug)
	return err
}

func (r *sqliteArticleRepo) List(ctx context.Context, page, limit int, tag string) ([]Article, int, error) {
	var total int
	var rows *sql.Rows
	var err error
	// List is the public-facing listing (JSON API): drafts are never included.
	if tag != "" {
		// Resolve membership through the indexed article_tags join table rather
		// than a `tags LIKE '%..%'` scan, so tag-filtered listings stay fast at
		// scale. tag_norm is the lower-cased form; match case-insensitively.
		// CROSS JOIN pins the tag table as the driver — an always-indexed lookup,
		// never a full articles scan even when the tag is very common.
		norm := strings.ToLower(strings.TrimSpace(tag))
		r.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm=? AND COALESCE(a.status,'published')='published'`, norm).Scan(&total)
		rows, err = r.reader().QueryContext(ctx,
			`SELECT a.id,a.title,a.slug,a.content,a.tags,a.created_at,a.updated_at,COALESCE(a.status,'published') FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm=? AND COALESCE(a.status,'published')='published' ORDER BY t.created_at DESC LIMIT ? OFFSET ?`,
			norm, limit, (page-1)*limit,
		)
	} else {
		r.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE COALESCE(status,'published')='published'`).Scan(&total)
		rows, err = r.reader().QueryContext(ctx,
			`SELECT id,title,slug,content,tags,created_at,updated_at,COALESCE(status,'published') FROM articles WHERE COALESCE(status,'published')='published' ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			limit, (page-1)*limit,
		)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var result []Article
	for rows.Next() {
		var a Article
		var tagsCSV string
		rows.Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsCSV, &a.CreatedAt, &a.UpdatedAt, &a.Status)
		a.Tags = splitCSV(tagsCSV)
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return result, total, nil
}

func (r *sqliteArticleRepo) TagCounts(ctx context.Context) (map[string]int, error) {
	rows, err := r.reader().QueryContext(ctx, `SELECT tag, COUNT(1) FROM article_tags GROUP BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var tag string
		var n int
		if err := rows.Scan(&tag, &n); err != nil {
			continue
		}
		if tag != "" {
			out[tag] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// splitCSV parses a comma-separated tag string, trimming whitespace.
func splitCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
