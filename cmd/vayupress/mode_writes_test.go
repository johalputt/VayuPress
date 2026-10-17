// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/scheduler"
)

// A scheduled post that falls due while the install is read-only is held, not
// failed: MarkFailed ends a schedule the operator still wants, for a refusal
// that lifts with the mode.
func TestAScheduledPostWaitsOutReadOnly(t *testing.T) {
	openMigratedDB(t)
	a := &App{
		scheduler: scheduler.New(dbpkg.DB),
		articles:  &api.ArticleService{Repo: dbpkg.NewArticleRepo(dbpkg.DB), Queue: queue.NewSQLiteWriter(dbpkg.DB, 0)},
	}
	p, err := a.scheduler.Schedule(context.Background(), "held-schedule", "Held", "<p>x</p>", nil, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mode.Global.ForceTransition(mode.ModeReadOnly, "test")
	t.Cleanup(mode.Global.Reset)

	a.publishDuePosts()

	posts, err := a.scheduler.List(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range posts {
		if q.ID == p.ID && q.Status != scheduler.StatusScheduled {
			t.Fatalf("a due post read-only mode refused is %q (%s), want it still scheduled", q.Status, q.Error)
		}
	}
}

// The API's three writes answer 503 writes_refused in read-only mode, through
// the real write queue: the integration harness writes directly and never meets
// the refusal.
func TestTheAPIRefusesPostWritesInReadOnly(t *testing.T) {
	openMigratedDB(t)
	a := &App{articles: &api.ArticleService{Repo: dbpkg.NewArticleRepo(dbpkg.DB), Queue: queue.NewSQLiteWriter(dbpkg.DB, 0)}}
	if _, err := dbpkg.WDB.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES('ro-1','Kept','kept-post','<p>kept</p>','',datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Post("/api/v1/articles", a.handleCreateArticle)
	r.Put("/api/v1/articles/{slug}", a.handleUpdateArticle)
	r.Delete("/api/v1/articles/{slug}", a.handleDeleteArticle)

	mode.Global.ForceTransition(mode.ModeReadOnly, "test")
	t.Cleanup(mode.Global.Reset)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/articles", `{"title":"New","slug":"new-post","content":"<p>x</p>"}`},
		{"PUT", "/api/v1/articles/kept-post", `{"title":"Changed"}`},
		{"DELETE", "/api/v1/articles/kept-post", ``},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"writes_refused"`) {
			t.Errorf("%s %s in read-only = %d %s, want 503 writes_refused", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
	var jobs int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Errorf("%d write jobs were queued in read-only mode", jobs)
	}
}
