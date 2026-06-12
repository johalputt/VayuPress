package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// ArticleService owns all business logic for article CRUD. Handlers call
// service methods; the service owns DB access and queue dispatch.
type ArticleService struct {
	DB             *sql.DB
	Enqueue        func(art dbpkg.Article, op string) error
	StorageCheckFn func() (used, quota int64) // nil = skip quota check
}

// CreateResult is returned after a successful create operation.
type CreateResult struct {
	ID   string
	Slug string
}

// BulkResult is returned after a bulk create.
type BulkResult struct {
	Queued      int
	Skipped     int
	SkipReasons []string
}

// ListResult is the paginated article list response.
type ListResult struct {
	Articles []ArticleSummary
	Page     int
	Limit    int
	Total    int
	Pages    int
}

// ArticleSummary is the list-view projection of an article (no content).
type ArticleSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TagCount pairs a tag name with the number of articles using it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// Create validates and enqueues a new article. Returns (result, error).
func (s *ArticleService) Create(title, slug, content string, tags []string) (CreateResult, error) {
	if err := ValidateArticleInput(title, slug, content, tags); err != nil {
		return CreateResult{}, err
	}
	if s.StorageCheckFn != nil {
		used, quota := s.StorageCheckFn()
		if used >= quota {
			return CreateResult{}, ErrStorageQuota
		}
	}
	var count int
	s.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, slug).Scan(&count)
	if count > 0 {
		return CreateResult{}, ErrSlugConflict
	}
	art := dbpkg.Article{
		ID: newID(), Title: title, Slug: slug,
		Content: content, Tags: tags,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Enqueue(art, "insert"); err != nil {
		return CreateResult{}, fmt.Errorf("queue: %w", err)
	}
	return CreateResult{ID: art.ID, Slug: art.Slug}, nil
}

// BulkCreate creates multiple articles, skipping those that fail validation or
// have duplicate slugs. Returns counts and per-item skip reasons.
func (s *ArticleService) BulkCreate(items []BulkCreateItem) (BulkResult, error) {
	if len(items) > 1000 {
		return BulkResult{}, ErrBulkLimit
	}
	res := BulkResult{}
	for _, in := range items {
		if err := ValidateArticleInput(in.Title, in.Slug, in.Content, in.Tags); err != nil {
			res.Skipped++
			res.SkipReasons = append(res.SkipReasons, in.Slug+": "+err.Error())
			continue
		}
		var count int
		s.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, in.Slug).Scan(&count)
		if count > 0 {
			res.Skipped++
			res.SkipReasons = append(res.SkipReasons, in.Slug+": duplicate slug")
			continue
		}
		art := dbpkg.Article{
			ID: newID(), Title: in.Title, Slug: in.Slug,
			Content: in.Content, Tags: in.Tags,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		s.Enqueue(art, "insert") //nolint:errcheck
		res.Queued++
	}
	return res, nil
}

// BulkCreateItem is the input type for a single item in a bulk create.
type BulkCreateItem struct {
	Title, Slug, Content string
	Tags                 []string
}

// Update fetches the article by slug, applies the partial update, and enqueues.
func (s *ArticleService) Update(slug string, title, content *string, tags []string) (dbpkg.Article, error) {
	var art dbpkg.Article
	var tagsStr string
	err := s.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).
		Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt)
	if err == sql.ErrNoRows {
		return art, ErrNotFound
	}
	if err != nil {
		return art, err
	}
	art.Tags = SplitTags(tagsStr)
	if title != nil {
		art.Title = *title
	}
	if content != nil {
		art.Content = *content
	}
	if tags != nil {
		art.Tags = tags
	}
	art.UpdatedAt = time.Now().UTC()
	if err := s.Enqueue(art, "update"); err != nil {
		return art, fmt.Errorf("queue: %w", err)
	}
	return art, nil
}

// Delete fetches the article by slug and enqueues a delete operation.
func (s *ArticleService) Delete(slug string) (dbpkg.Article, error) {
	var art dbpkg.Article
	var tagsStr string
	err := s.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).
		Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt)
	if err == sql.ErrNoRows {
		return art, ErrNotFound
	}
	if err != nil {
		return art, err
	}
	art.Tags = SplitTags(tagsStr)
	if err := s.Enqueue(art, "delete"); err != nil {
		return art, fmt.Errorf("queue: %w", err)
	}
	return art, nil
}

// Get returns the article for the given slug.
func (s *ArticleService) Get(slug string) (dbpkg.Article, error) {
	if !IsValidSlug(slug) {
		return dbpkg.Article{}, ErrInvalidSlug
	}
	var art dbpkg.Article
	var tagsStr string
	err := s.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).
		Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt)
	if err == sql.ErrNoRows {
		return art, ErrNotFound
	}
	if err != nil {
		return art, err
	}
	art.Tags = SplitTags(tagsStr)
	return art, nil
}

// List returns a paginated, optionally tag-filtered, article summary list.
func (s *ArticleService) List(page, limit int, tag string) (ListResult, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var (
		rows  *sql.Rows
		err   error
		total int
	)
	if tag != "" {
		s.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE tags LIKE ?`, "%"+tag+"%").Scan(&total)
		rows, err = s.DB.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles WHERE tags LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?`, "%"+tag+"%", limit, offset)
	} else {
		s.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&total)
		rows, err = s.DB.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	result := make([]ArticleSummary, 0)
	for rows.Next() {
		var s ArticleSummary
		var tagsStr string
		rows.Scan(&s.ID, &s.Title, &s.Slug, &tagsStr, &s.CreatedAt, &s.UpdatedAt)
		s.Tags = SplitTags(tagsStr)
		result = append(result, s)
	}
	pages := (total + limit - 1) / limit
	return ListResult{Articles: result, Page: page, Limit: limit, Total: total, Pages: pages}, nil
}

// ListTags returns all distinct tag names with counts across all articles.
func (s *ArticleService) ListTags() ([]TagCount, error) {
	rows, err := s.DB.Query(`SELECT tags FROM articles WHERE tags != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tagCount := make(map[string]int)
	for rows.Next() {
		var tagsStr string
		rows.Scan(&tagsStr)
		for _, t := range SplitTags(tagsStr) {
			if t != "" {
				tagCount[t]++
			}
		}
	}
	result := make([]TagCount, 0, len(tagCount))
	for t, c := range tagCount {
		result = append(result, TagCount{Tag: t, Count: c})
	}
	return result, nil
}

// MakeEnqueueFn creates an Enqueue function backed by a sql.DB write_jobs table.
func MakeEnqueueFn(db *sql.DB) func(art dbpkg.Article, op string) error {
	return func(art dbpkg.Article, op string) error {
		payload, err := json.Marshal(art)
		if err != nil {
			return err
		}
		_, err = db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,?)`, payload, op)
		return err
	}
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x%s", time.Now().UnixNano(), strings.Repeat("0", 16))
	}
	return hex.EncodeToString(b)
}
