// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
)

// forgetTagIndex empties the topic index memo now and when the test ends, so
// no test is answered from counts another test left behind.
func forgetTagIndex(t *testing.T) {
	t.Helper()
	reset := func() {
		tagIndexMemo.Lock()
		tagIndexMemo.m = map[string]tagIndexEntry{}
		tagIndexMemo.refreshing = map[string]bool{}
		tagIndexMemo.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// The topic index is counted once per tagIndexTTL, not per request. Its count
// reads every tag link, 4.6 million rows on johal.in, and ran on every view.
// Once the counts are older than the TTL, the old counts keep serving while
// they are counted again.
func TestTheTopicIndexIsNotCountedPerRequest(t *testing.T) {
	setupRelatedTestDB(t)
	render.Init(t.TempDir())
	forgetTagIndex(t)
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	a := &App{siteSettings: settings.New(dbpkg.DB), articles: &api.ArticleService{Repo: repo}}
	ctx := context.Background()
	now := time.Now()
	if err := repo.Create(ctx, dbpkg.Article{ID: "p", Title: "p", Slug: "p", Content: "<p>x</p>", Tags: []string{"sqlite"},
		Status: "published", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.RunInTx(ctx, dbpkg.DB, func(tx *sql.Tx) error {
		return dbpkg.SyncArticleTagsByIDTx(tx, "p", now, []string{"sqlite"})
	}); err != nil {
		t.Fatal(err)
	}
	get := func() string {
		rec := httptest.NewRecorder()
		a.handleTagIndex(rec, httptest.NewRequest(http.MethodGet, "/tags", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("/tags answered %d", rec.Code)
		}
		return rec.Body.String()
	}
	if !strings.Contains(get(), "sqlite") {
		t.Fatal("the topic index does not list the one tag; the fixture does not exercise the count")
	}

	// The tag links go. Within the TTL the index is not counted again.
	if _, err := dbpkg.DB.Exec(`DELETE FROM article_tags`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get(), "sqlite") {
		t.Fatal("the second view counted the tags again: the topic index reads every tag link on every request")
	}

	// Past the TTL: the old counts serve, and a count runs off the request path.
	tagIndexMemo.Lock()
	e := tagIndexMemo.m[""]
	e.at = e.at.Add(-tagIndexTTL - time.Second)
	tagIndexMemo.m[""] = e
	tagIndexMemo.Unlock()
	if !strings.Contains(get(), "sqlite") {
		t.Error("a view after the TTL waited for the new count instead of serving the old one")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		tagIndexMemo.Lock()
		fresh, busy := time.Since(tagIndexMemo.m[""].at) < tagIndexTTL, tagIndexMemo.refreshing[""]
		tagIndexMemo.Unlock()
		if fresh && !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("counts older than the TTL were never counted again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(get(), "sqlite") {
		t.Error("after the new count the topic index still lists a tag no post carries")
	}
}
