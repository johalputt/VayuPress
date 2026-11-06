// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/users"
)

// laterQueue writes a post a moment after it is queued, off the request, as
// the install's post queue does.
type laterQueue struct {
	repo interface {
		Create(context.Context, dbpkg.Article) error
	}
	done chan struct{}
}

func (q laterQueue) Enqueue(_ context.Context, art dbpkg.Article, _ string) error {
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = q.repo.Create(context.Background(), art)
		close(q.done)
	}()
	return nil
}

// Write for this site makes a draft that belongs to the site, though the
// queue writes it after the request has moved on: given to the site at once,
// it was not there yet, and every press failed.
func TestWriteForThisSiteMakesTheSitesDraft(t *testing.T) {
	openMigratedDB(t)
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	q := laterQueue{repo: repo, done: make(chan struct{})}
	a := &App{articles: &api.ArticleService{Repo: repo, Queue: q}}
	t.Cleanup(func() { <-q.done; a.bgWG.Wait() })

	site := domain.Domain{ID: "site-test2", Host: "test2.example.com"}
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/d/site-test2/api/content/new", strings.NewReader("{}")), admin)
	req = req.WithContext(context.WithValue(req.Context(), ctxScopedDomainKey, site))
	rec := httptest.NewRecorder()
	a.handleOSScopedContentNew(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Write for this site: %d %s", rec.Code, rec.Body.String())
	}
	var got struct{ Slug string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Slug == "" {
		t.Fatalf("no slug in %s", rec.Body.String())
	}
	var owner string
	if err := dbpkg.DB.QueryRow(`SELECT COALESCE(domain_id,'') FROM articles WHERE slug=?`, got.Slug).Scan(&owner); err != nil || owner != site.ID {
		t.Errorf("the draft belongs to %q (%v), want the site %q", owner, err, site.ID)
	}
}

// The page's script reads a refusal where the server puts it, so the reason
// is shown rather than "Could not create a draft".
func TestScopedContentScriptShowsTheServersReason(t *testing.T) {
	src := scopedContentScript("n")
	if !strings.Contains(src, "j.error&&j.error.message") || strings.Contains(src, "(res.j&&res.j.message)") {
		t.Error("the Content page's script does not read the server's error message")
	}
}
