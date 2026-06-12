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
type sqliteArticleRepo struct{ db *sql.DB }

// NewArticleRepo returns a concrete article repository backed by the given DB.
// The returned value satisfies api.ArticleRepository.
func NewArticleRepo(db *sql.DB) *sqliteArticleRepo {
	return &sqliteArticleRepo{db: db}
}

func (r *sqliteArticleRepo) SlugExists(ctx context.Context, slug string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE slug=?`, slug).Scan(&n)
	return n > 0, err
}

func (r *sqliteArticleRepo) Create(ctx context.Context, art Article) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		art.ID, art.Title, art.Slug, art.Content,
		strings.Join(art.Tags, ","), art.CreatedAt, art.UpdatedAt,
	)
	return err
}

func (r *sqliteArticleRepo) Get(ctx context.Context, slug string) (Article, error) {
	var art Article
	var tagsCSV string
	err := r.db.QueryRowContext(ctx,
		`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug,
	).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsCSV, &art.CreatedAt, &art.UpdatedAt)
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
	if tag != "" {
		like := "%" + tag + "%"
		r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE tags LIKE ?`, like).Scan(&total)
		rows, err = r.db.QueryContext(ctx,
			`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE tags LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			like, limit, (page-1)*limit,
		)
	} else {
		r.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM articles`).Scan(&total)
		rows, err = r.db.QueryContext(ctx,
			`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles ORDER BY created_at DESC LIMIT ? OFFSET ?`,
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
		rows.Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsCSV, &a.CreatedAt, &a.UpdatedAt)
		a.Tags = splitCSV(tagsCSV)
		result = append(result, a)
	}
	return result, total, nil
}

func (r *sqliteArticleRepo) AllTagCSVs(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT tags FROM articles WHERE tags != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
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
