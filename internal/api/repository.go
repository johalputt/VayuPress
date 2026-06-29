package api

import (
	"context"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// ArticleRepository is the persistence contract for articles. Concrete
// implementations live in internal/db; this interface is defined here so the
// service layer depends on an abstraction, not on SQLite directly.
type ArticleRepository interface {
	SlugExists(ctx context.Context, slug string) (bool, error)
	Create(ctx context.Context, art dbpkg.Article) error
	Get(ctx context.Context, slug string) (dbpkg.Article, error)
	Update(ctx context.Context, art dbpkg.Article) error
	Delete(ctx context.Context, slug string) error
	// List returns up to limit articles starting at page (1-indexed), optionally
	// filtered by tag, plus the total count.
	List(ctx context.Context, page, limit int, tag string) (articles []dbpkg.Article, total int, err error)
	// TagCounts returns each distinct tag mapped to the number of articles using
	// it, resolved from the indexed article_tags join table so it never has to
	// scan or load the whole articles table.
	TagCounts(ctx context.Context) (map[string]int, error)
}
