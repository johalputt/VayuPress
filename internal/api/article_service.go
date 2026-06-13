package api

import (
	"context"
	"fmt"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/queue"
)

// ArticleService owns all business logic for article CRUD. Handlers call
// service methods; the service owns validation, quota checks, and queue dispatch.
// Persistence is delegated to the injected ArticleRepository and queue.Writer.
type ArticleService struct {
	Repo           ArticleRepository
	Queue          queue.Writer
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

// BulkCreateItem is the input type for a single item in a bulk create.
type BulkCreateItem struct {
	Title, Slug, Content string
	Tags                 []string `json:"tags"`
}

// Create validates and enqueues a new article.
func (s *ArticleService) Create(ctx context.Context, title, slug, content string, tags []string) (CreateResult, error) {
	if err := ValidateArticleInput(title, slug, content, tags); err != nil {
		return CreateResult{}, err
	}
	if s.StorageCheckFn != nil {
		used, quota := s.StorageCheckFn()
		if used >= quota {
			return CreateResult{}, ErrStorageQuota
		}
	}
	exists, err := s.Repo.SlugExists(ctx, slug)
	if err != nil {
		return CreateResult{}, err
	}
	if exists {
		return CreateResult{}, ErrSlugConflict
	}
	art := dbpkg.Article{
		ID: newID(), Title: title, Slug: slug,
		Content: content, Tags: tags,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.Queue.Enqueue(ctx, art, "insert"); err != nil {
		return CreateResult{}, fmt.Errorf("queue: %w", err)
	}
	return CreateResult{ID: art.ID, Slug: art.Slug}, nil
}

// BulkCreate creates multiple articles, skipping those that fail validation or
// have duplicate slugs.
func (s *ArticleService) BulkCreate(ctx context.Context, items []BulkCreateItem) (BulkResult, error) {
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
		exists, _ := s.Repo.SlugExists(ctx, in.Slug)
		if exists {
			res.Skipped++
			res.SkipReasons = append(res.SkipReasons, in.Slug+": duplicate slug")
			continue
		}
		art := dbpkg.Article{
			ID: newID(), Title: in.Title, Slug: in.Slug,
			Content: in.Content, Tags: in.Tags,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		s.Queue.Enqueue(ctx, art, "insert") //nolint:errcheck
		res.Queued++
	}
	return res, nil
}

// Update fetches the article by slug, applies the partial update, and enqueues.
func (s *ArticleService) Update(ctx context.Context, slug string, title, content *string, tags []string) (dbpkg.Article, error) {
	art, err := s.Repo.Get(ctx, slug)
	if err != nil {
		return art, mapRepoErr(err)
	}
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
	if err := s.Queue.Enqueue(ctx, art, "update"); err != nil {
		return art, fmt.Errorf("queue: %w", err)
	}
	return art, nil
}

// Delete fetches the article by slug and enqueues a delete operation.
func (s *ArticleService) Delete(ctx context.Context, slug string) (dbpkg.Article, error) {
	art, err := s.Repo.Get(ctx, slug)
	if err != nil {
		return art, mapRepoErr(err)
	}
	if err := s.Queue.Enqueue(ctx, art, "delete"); err != nil {
		return art, fmt.Errorf("queue: %w", err)
	}
	return art, nil
}

// Get returns the article for the given slug.
func (s *ArticleService) Get(ctx context.Context, slug string) (dbpkg.Article, error) {
	if !IsValidSlug(slug) {
		return dbpkg.Article{}, ErrInvalidSlug
	}
	art, err := s.Repo.Get(ctx, slug)
	return art, mapRepoErr(err)
}

// List returns a paginated, optionally tag-filtered, article summary list.
func (s *ArticleService) List(ctx context.Context, page, limit int, tag string) (ListResult, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	articles, total, err := s.Repo.List(ctx, page, limit, tag)
	if err != nil {
		return ListResult{}, err
	}
	summaries := make([]ArticleSummary, 0, len(articles))
	for _, a := range articles {
		summaries = append(summaries, ArticleSummary{
			ID: a.ID, Title: a.Title, Slug: a.Slug, Tags: a.Tags,
			CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		})
	}
	pages := (total + limit - 1) / limit
	return ListResult{Articles: summaries, Page: page, Limit: limit, Total: total, Pages: pages}, nil
}

// ListTags returns all distinct tag names with counts across all articles.
func (s *ArticleService) ListTags(ctx context.Context) ([]TagCount, error) {
	csvs, err := s.Repo.AllTagCSVs(ctx)
	if err != nil {
		return nil, err
	}
	tagCount := make(map[string]int)
	for _, csv := range csvs {
		for _, t := range SplitTags(csv) {
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

// mapRepoErr translates db-level sentinel errors to api-level sentinels so
// handlers always work with api.Err* values.
func mapRepoErr(err error) error {
	if err == nil {
		return nil
	}
	if err == dbpkg.ErrNotFound {
		return ErrNotFound
	}
	return err
}
