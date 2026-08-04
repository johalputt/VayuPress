// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayuflow"
)

// fakeArticleRepo records what the adapter asked storage to do, so the tests
// assert on the Article that would actually be written rather than on the
// adapter's intent.
type fakeArticleRepo struct {
	created  []dbpkg.Article
	updated  []dbpkg.Article
	existing map[string]dbpkg.Article
	exists   bool
	getErr   error
}

func (f *fakeArticleRepo) Create(_ context.Context, a dbpkg.Article) error {
	f.created = append(f.created, a)
	return nil
}
func (f *fakeArticleRepo) Update(_ context.Context, a dbpkg.Article) error {
	f.updated = append(f.updated, a)
	return nil
}
func (f *fakeArticleRepo) Get(_ context.Context, slug string) (dbpkg.Article, error) {
	if f.getErr != nil {
		return dbpkg.Article{}, f.getErr
	}
	a, ok := f.existing[slug]
	if !ok {
		return dbpkg.Article{}, errors.New("not found")
	}
	return a, nil
}
func (f *fakeArticleRepo) SlugExists(_ context.Context, _ string) (bool, error) {
	return f.exists, nil
}

// The whole point of the adapter. article_repo.Create turns an EMPTY status
// into "published", so an automated draft written without an explicit status
// would go live to every reader.
func TestAnAutomatedDraftIsWrittenWithAnExplicitDraftStatus(t *testing.T) {
	repo := &fakeArticleRepo{}
	c := flowContent{repo: repo}
	if err := c.CreateDraft(context.Background(), vayuflow.Draft{
		Slug: "weekly", Title: "Weekly digest", Content: "body", Tags: []string{"digest"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("expected one create, got %d", len(repo.created))
	}
	got := repo.created[0]
	if got.Status != vayuflow.DraftStatus {
		t.Fatalf("the article was written with status %q; an empty or non-draft status is "+
			"published by article_repo.Create, so this flow would have gone live", got.Status)
	}
	if got.ID == "" || got.Slug != "weekly" || got.Title != "Weekly digest" {
		t.Errorf("the article did not carry its content: %+v", got)
	}
}

// article_repo.Update sets only title, content and tags — it never touches
// status. So without an explicit check, an action capped at writeDraft could
// rewrite the body of a PUBLISHED post: the capability ceiling satisfied while
// the effect it exists to prevent happens anyway.
func TestUpdatingAPublishedPostIsRefused(t *testing.T) {
	for _, status := range []string{"published", ""} {
		t.Run("status="+status, func(t *testing.T) {
			repo := &fakeArticleRepo{existing: map[string]dbpkg.Article{
				"live-post": {Slug: "live-post", Title: "Live", Status: status},
			}}
			c := flowContent{repo: repo}
			err := c.UpdateDraft(context.Background(), vayuflow.Draft{
				Slug: "live-post", Title: "Rewritten", Content: "new body",
			})
			if err == nil {
				t.Fatal("an automation rewrote a published post through a draft-capped action")
			}
			if !strings.Contains(err.Error(), "live write") {
				t.Errorf("the refusal should name what it prevented, got: %v", err)
			}
			if len(repo.updated) != 0 {
				t.Errorf("the refused update still reached storage: %+v", repo.updated)
			}
		})
	}
}

func TestUpdatingAnActualDraftIsAllowed(t *testing.T) {
	repo := &fakeArticleRepo{existing: map[string]dbpkg.Article{
		"wip": {Slug: "wip", Title: "Old", Status: vayuflow.DraftStatus},
	}}
	c := flowContent{repo: repo}
	if err := c.UpdateDraft(context.Background(), vayuflow.Draft{
		Slug: "wip", Title: "New", Content: "new body", Tags: []string{"a"},
	}); err != nil {
		t.Fatalf("updating a real draft must be allowed: %v", err)
	}
	if len(repo.updated) != 1 {
		t.Fatalf("expected one update, got %d", len(repo.updated))
	}
	// The status must survive the edit — an update that dropped it would leave
	// the row empty, which reads as published.
	if repo.updated[0].Status != vayuflow.DraftStatus {
		t.Errorf("the draft lost its status on update: %q", repo.updated[0].Status)
	}
	if repo.updated[0].Title != "New" {
		t.Errorf("the edit did not apply: %+v", repo.updated[0])
	}
}

// Creating over an existing slug must refuse rather than collide.
func TestCreatingOverAnExistingSlugIsRefused(t *testing.T) {
	repo := &fakeArticleRepo{exists: true}
	c := flowContent{repo: repo}
	if err := c.CreateDraft(context.Background(), vayuflow.Draft{Slug: "taken", Title: "T"}); err == nil {
		t.Fatal("an automation created a post over an existing slug")
	}
	if len(repo.created) != 0 {
		t.Error("the refused create still reached storage")
	}
}

// Updating something that does not exist must fail, not silently create.
func TestUpdatingAMissingPostFails(t *testing.T) {
	repo := &fakeArticleRepo{existing: map[string]dbpkg.Article{}}
	c := flowContent{repo: repo}
	if err := c.UpdateDraft(context.Background(), vayuflow.Draft{Slug: "ghost", Title: "T"}); err == nil {
		t.Fatal("updating a missing post reported success")
	}
	if len(repo.created) != 0 || len(repo.updated) != 0 {
		t.Error("a missing post produced a write")
	}
}

// The adapter must satisfy the interface the engine expects; a signature drift
// would otherwise only show up at wiring time.
func TestFlowContentSatisfiesTheEngineInterface(t *testing.T) {
	var _ vayuflow.ContentWriter = flowContent{}
}
