// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrNotFound is returned by the repository when a requested article does not exist.
var ErrNotFound = errors.New("article not found")

// ScopeAll is the domain scope that disables per-domain filtering: the admin
// JSON API and VayuOS see every article regardless of its owning domain. Public
// pages instead pass the active domain's scope ("" for the primary domain, or a
// secondary domain's id) so each site serves only its own content (ADR-0132,
// VayuDomains Stage 2). The sentinel is a byte no real domain id can hold.
const ScopeAll = "\x00*all*"

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
	isPage := 0
	if art.IsPage {
		isPage = 1
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at,status,author_id,domain_id,is_page) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		art.ID, art.Title, art.Slug, art.Content,
		strings.Join(art.Tags, ","), art.CreatedAt, art.UpdatedAt, status, art.AuthorID, art.DomainID, isPage,
	)
	return err
}

// ListPages returns up to limit standalone pages (is_page=1), newest-updated
// first. Pages are excluded from the blog feed, so they need their own listing;
// this is the read behind the list_pages tool and the VayuOS Pages manager.
func (r *sqliteArticleRepo) ListPages(ctx context.Context, limit int) ([]Article, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := r.reader().QueryContext(ctx,
		`SELECT id,title,slug,content,tags,created_at,updated_at,COALESCE(status,'published'),domain_id
		 FROM articles WHERE is_page=1 ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Article
	for rows.Next() {
		var a Article
		var tagsCSV string
		if err := rows.Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsCSV, &a.CreatedAt, &a.UpdatedAt, &a.Status, &a.DomainID); err != nil {
			continue
		}
		a.Tags = splitCSV(tagsCSV)
		a.IsPage = true
		out = append(out, a)
	}
	return out, rows.Err()
}

// Get returns an article by slug regardless of owning domain (admin path).
func (r *sqliteArticleRepo) Get(ctx context.Context, slug string) (Article, error) {
	return r.GetScoped(ctx, ScopeAll, slug)
}

// GetScoped returns an article by slug within a domain scope. ScopeAll matches
// any domain; otherwise only an article whose domain_id equals scope is
// returned (so a secondary domain cannot reach another domain's content, and
// the primary domain — scope "" — serves the historic unassigned rows).
func (r *sqliteArticleRepo) GetScoped(ctx context.Context, scope, slug string) (Article, error) {
	var art Article
	var tagsCSV string
	const cols = `id,title,slug,content,tags,created_at,updated_at,COALESCE(status,'published'),COALESCE(author_id,''),domain_id`
	var row *sql.Row
	if scope == ScopeAll {
		row = r.reader().QueryRowContext(ctx, `SELECT `+cols+` FROM articles WHERE slug=?`, slug)
	} else {
		row = r.reader().QueryRowContext(ctx, `SELECT `+cols+` FROM articles WHERE slug=? AND domain_id=?`, slug, scope)
	}
	err := row.Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsCSV, &art.CreatedAt, &art.UpdatedAt, &art.Status, &art.AuthorID, &art.DomainID)
	if err == sql.ErrNoRows {
		return art, ErrNotFound
	}
	if err != nil {
		return art, err
	}
	art.Tags = splitCSV(tagsCSV)
	return art, nil
}

// SetDomain reassigns an article to a domain (empty = the primary domain).
// ListOwnedBy returns everything a domain owns, drafts and pages included,
// newest first.
//
// ListScoped cannot serve this. It is the PUBLIC listing and excludes drafts by
// design — and an operator opening a client's site to see what is on it needs
// the drafts most of all, because an unpublished post is the one that needs
// attention. Bodies are deliberately not selected: this is a listing.
//
// The domainID is used verbatim, including "" for the primary. There is no
// "everything" mode here on purpose: this method exists to answer "what does
// THIS site have", and a scope that could mean every site is how a per-site page
// comes to show another site's rows.
func (r *sqliteArticleRepo) ListOwnedBy(ctx context.Context, domainID string, limit int) ([]Article, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.reader().QueryContext(ctx,
		`SELECT id,title,slug,created_at,updated_at,COALESCE(status,'published'),COALESCE(is_page,0),domain_id
		 FROM articles WHERE domain_id=? ORDER BY updated_at DESC, created_at DESC LIMIT ?`,
		domainID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Article
	for rows.Next() {
		var a Article
		var isPage int
		if err := rows.Scan(&a.ID, &a.Title, &a.Slug, &a.CreatedAt, &a.UpdatedAt, &a.Status, &isPage, &a.DomainID); err != nil {
			return nil, err
		}
		a.IsPage = isPage == 1
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetDomain reassigns an article to a domain ("" = the primary).
//
// A slug matching nothing is an ERROR, not a silent no-op. Without the
// RowsAffected check a typo returned nil, the console reported "Moved ✓" and
// reloaded, and the operator saw the post missing from the list with no way to
// tell whether the move had failed or the listing was wrong. A write that
// changed nothing must not report that it did.
func (r *sqliteArticleRepo) SetDomain(ctx context.Context, slug, domainID string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE articles SET domain_id=? WHERE slug=?`, domainID, slug)
	if err != nil {
		return err
	}
	// A driver that cannot report the count is not evidence of failure, so a
	// RowsAffected error is not treated as one — it just cannot confirm.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountsByDomain returns the number of articles owned by each domain id
// (the primary/unassigned bucket is the empty-string key).
func (r *sqliteArticleRepo) CountsByDomain(ctx context.Context) (map[string]int, error) {
	rows, err := r.reader().QueryContext(ctx, `SELECT domain_id, COUNT(1) FROM articles GROUP BY domain_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var d string
		var n int
		if rows.Scan(&d, &n) == nil {
			out[d] = n
		}
	}
	return out, rows.Err()
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

// List returns the published feed across all domains (admin / JSON API).
func (r *sqliteArticleRepo) List(ctx context.Context, page, limit int, tag string) ([]Article, int, error) {
	return r.ListScoped(ctx, ScopeAll, page, limit, tag)
}

// ListScoped is List restricted to a domain scope. ScopeAll lists every
// published article; any other scope lists only articles whose domain_id equals
// it, so the primary domain (scope "") serves its historic content and a
// secondary domain serves only its own (ADR-0132, VayuDomains Stage 2). Drafts
// are never included on either path.
func (r *sqliteArticleRepo) ListScoped(ctx context.Context, scope string, page, limit int, tag string) ([]Article, int, error) {
	var total int
	var rows *sql.Rows
	var err error
	scoped := scope != ScopeAll
	if tag != "" {
		// Resolve membership through the indexed article_tags join table rather
		// than a `tags LIKE '%..%'` scan, so tag-filtered listings stay fast at
		// scale. CROSS JOIN pins the tag table as the driver — an always-indexed
		// lookup, never a full articles scan even when the tag is very common.
		norm := strings.ToLower(strings.TrimSpace(tag))
		where := `t.tag_norm=? AND COALESCE(a.status,'published')='published'`
		cargs := []any{norm}
		if scoped {
			where += ` AND a.domain_id=?`
			cargs = append(cargs, scope)
		}
		r.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE `+where, cargs...).Scan(&total)
		qargs := append(append([]any{}, cargs...), limit, (page-1)*limit)
		rows, err = r.reader().QueryContext(ctx,
			`SELECT a.id,a.title,a.slug,a.content,a.tags,a.created_at,a.updated_at,COALESCE(a.status,'published'),a.domain_id FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE `+where+` ORDER BY t.created_at DESC LIMIT ? OFFSET ?`,
			qargs...,
		)
	} else {
		where := `COALESCE(status,'published')='published'`
		cargs := []any{}
		if scoped {
			where += ` AND domain_id=?`
			cargs = append(cargs, scope)
		}
		r.reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE `+where, cargs...).Scan(&total)
		qargs := append(append([]any{}, cargs...), limit, (page-1)*limit)
		rows, err = r.reader().QueryContext(ctx,
			`SELECT id,title,slug,content,tags,created_at,updated_at,COALESCE(status,'published'),domain_id FROM articles WHERE `+where+` ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			qargs...,
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
		rows.Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsCSV, &a.CreatedAt, &a.UpdatedAt, &a.Status, &a.DomainID)
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
