// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func setupRelatedTestDB(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	os.Setenv("DB_PATH", filepath.Join(dir, "related.db"))
	os.Setenv("API_KEY", "test-key")
	os.Setenv("DOMAIN", "example.test")
	os.Setenv("CACHE_DIR", dir)
	os.Setenv("STORAGE_QUOTA_GB", "10")
	config.Load()
	if err := dbpkg.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	t.Cleanup(func() {
		dbpkg.ClosePools()
		_ = dbpkg.DB.Close()
	})
}

// Related posts are the newest published posts sharing any tag, across all the
// tags, each once, never the current post and never a draft; the recent posts
// fill what tags do not.
func TestRelatedArticlesAreTheNewestPublishedTagMatches(t *testing.T) {
	setupRelatedTestDB(t)
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mk := func(slug string, hoursAgo int, status string, tags ...string) {
		at := base.Add(-time.Duration(hoursAgo) * time.Hour)
		if err := repo.Create(ctx, dbpkg.Article{ID: slug, Title: "T " + slug, Slug: slug, Content: "<p>x</p>",
			Tags: tags, Status: status, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		// Production writes the tag rows in the article's own transaction.
		if err := dbpkg.RunInTx(ctx, dbpkg.DB, func(tx *sql.Tx) error { return dbpkg.SyncArticleTagsByIDTx(tx, slug, at, tags) }); err != nil {
			t.Fatalf("tags %s: %v", slug, err)
		}
	}
	mk("current", 0, "published", "go", "sqlite")
	mk("go-new", 1, "published", "go")
	mk("both", 2, "published", "go", "sqlite") // carries two of the tags: listed once
	mk("draft-newest", 0, "draft", "sqlite")   // a draft is never suggested
	mk("sqlite-old", 5, "published", "SQLite") // matched case-insensitively
	mk("go-oldest", 9, "published", "go")
	mk("untagged-recent", 3, "published")

	var n int
	_ = dbpkg.DB.QueryRow(`SELECT count(*) FROM article_tags`).Scan(&n)
	if n == 0 {
		t.Fatal("article_tags is empty: the fixture does not exercise the tag path")
	}

	got := (&App{}).relatedArticles(ctx, "current", []string{"go", "SQLite"}, 3)
	var slugs []string
	for _, ra := range got {
		slugs = append(slugs, ra.Slug)
	}
	if want := "go-new,both,sqlite-old"; strings.Join(slugs, ",") != want {
		t.Fatalf("related = %v, want %s (newest tag matches, once each, no draft, not the current post)", slugs, want)
	}

	// With tags exhausted, the most recent posts fill the remaining slots.
	got = (&App{}).relatedArticles(ctx, "current", []string{"sqlite"}, 4)
	slugs = slugs[:0]
	for _, ra := range got {
		slugs = append(slugs, ra.Slug)
	}
	if want := "both,sqlite-old,go-new,untagged-recent"; strings.Join(slugs, ",") != want {
		t.Fatalf("related with top-up = %v, want %s", slugs, want)
	}
}

// A tag's candidates are read from the (tag_norm, created_at, article_id) index
// alone, in its order, so the read stops at the LIMIT however many posts carry
// the tag. A temporary sort or a join to articles here means every post with
// the tag is read on every render: the 30-second pages behind the 502s.
func TestRelatedCandidatesComeFromTheIndexAlone(t *testing.T) {
	setupRelatedTestDB(t)
	rows, err := dbpkg.DB.Query(`EXPLAIN QUERY PLAN `+relatedPerTagSQL, "go", 16)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	p := strings.Join(plan, " | ")
	if !strings.Contains(p, "COVERING INDEX idx_article_tags_norm_created") || strings.Contains(p, "TEMP B-TREE") ||
		strings.Contains(p, "autoindex_articles") || strings.Contains(p, "SCAN") {
		t.Fatalf("per-tag candidates must be a covering index read with no sort, got: %s", p)
	}
}
