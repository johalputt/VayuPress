// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayuflow"
)

// flowContent is the storage VayuFlow's content actions write through.
//
// It exists rather than reusing api.ArticleService for one specific reason:
// ArticleService.Create builds its Article WITHOUT setting Status, and
// article_repo.Create turns an empty status into "published". That is correct
// for the editor — an author pressing Publish — and exactly wrong for an
// automation whose entire capability is capped at draft. Reusing it would have
// made every automated "draft" a live post.
type flowContent struct {
	repo interface {
		Create(ctx context.Context, art dbpkg.Article) error
		Update(ctx context.Context, art dbpkg.Article) error
		Get(ctx context.Context, slug string) (dbpkg.Article, error)
		SlugExists(ctx context.Context, slug string) (bool, error)
	}
}

// CreateDraft writes a new article with the status set EXPLICITLY. The status
// is never taken from the caller — vayuflow.Draft has no field for one.
func (c flowContent) CreateDraft(ctx context.Context, d vayuflow.Draft) error {
	exists, err := c.repo.SlugExists(ctx, d.Slug)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("vayuflow: a post already exists at %q", d.Slug)
	}
	now := time.Now().UTC()
	return c.repo.Create(ctx, dbpkg.Article{
		ID: newFlowArticleID(), Title: d.Title, Slug: d.Slug, Content: d.Content, Tags: d.Tags,
		// Load-bearing. An empty status is read as published.
		Status:    vayuflow.DraftStatus,
		CreatedAt: now, UpdatedAt: now,
	})
}

// UpdateDraft edits an existing article, and REFUSES if that article is not
// itself a draft.
//
// This guard is not defensive decoration. article_repo.Update sets only title,
// content and tags — it never touches status — so without this check an action
// capped at writeDraft could rewrite the body of a PUBLISHED post, which is a
// live write reaching every reader. The capability ceiling would have been
// satisfied while the effect it exists to prevent happened anyway.
func (c flowContent) UpdateDraft(ctx context.Context, d vayuflow.Draft) error {
	existing, err := c.repo.Get(ctx, d.Slug)
	if err != nil {
		return fmt.Errorf("vayuflow: no post at %q to update: %w", d.Slug, err)
	}
	// An empty status means published, so it must be compared explicitly rather
	// than treated as "unset and therefore harmless".
	if status := strings.TrimSpace(existing.Status); status != vayuflow.DraftStatus {
		if status == "" {
			status = "published"
		}
		return fmt.Errorf("vayuflow: %q is %s, not a draft — editing it would be a live write, "+
			"which no content action is permitted to perform", d.Slug, status)
	}
	existing.Title, existing.Content, existing.Tags = d.Title, d.Content, d.Tags
	existing.UpdatedAt = time.Now().UTC()
	return c.repo.Update(ctx, existing)
}

// newFlowArticleID mints an article ID. It panics rather than falling back to a
// timestamp: a colliding article ID would attach an automated draft to an
// existing post, and there is no safe degraded behaviour for that.
func newFlowArticleID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("vayuflow: system entropy unavailable: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
