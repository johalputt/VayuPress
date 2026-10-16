// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/users"
)

// TestSWRRefresherSingleFlight proves a second refresh for a key already in
// flight is dropped, so concurrent hits on the same stale page trigger exactly
// one background re-render.
func TestSWRRefresherSingleFlight(t *testing.T) {
	r := newSWRRefresher(4)
	var calls int64
	started := make(chan struct{})
	release := make(chan struct{})

	r.refresh("k", func() {
		atomic.AddInt64(&calls, 1)
		close(started)
		<-release // hold the slot so the key stays in-flight
	})
	<-started // first refresh is now running

	// Same key while the first is in-flight → must be dropped, not queued.
	r.refresh("k", func() { atomic.AddInt64(&calls, 1) })
	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("single-flight: want 1 call while in-flight, got %d", got)
	}

	close(release)
	// Wait for the first refresh's cleanup to release the key before scheduling
	// again (otherwise the second is legitimately deduped and dropped).
	deadline := time.Now().Add(time.Second)
	for {
		r.mu.Lock()
		inflight := r.inflight["k"]
		r.mu.Unlock()
		if !inflight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first refresh never cleared the in-flight key")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The key is free again and a new refresh may run.
	done := make(chan struct{})
	r.refresh("k", func() { atomic.AddInt64(&calls, 1); close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh after completion did not run")
	}
	if got := atomic.LoadInt64(&calls); got != 2 {
		t.Fatalf("want 2 total calls, got %d", got)
	}
}

// TestSWRRefresherBounded proves the total number of concurrent refreshes never
// exceeds the configured cap — the guard against a re-render herd. Refreshes
// past the cap are dropped (not queued), so readers never wait and the box is
// never saturated.
func TestSWRRefresherBounded(t *testing.T) {
	r := newSWRRefresher(2)
	var concurrent, maxSeen int64
	release := make(chan struct{})

	fn := func() {
		c := atomic.AddInt64(&concurrent, 1)
		for {
			m := atomic.LoadInt64(&maxSeen)
			if c <= m || atomic.CompareAndSwapInt64(&maxSeen, m, c) {
				break
			}
		}
		<-release
		atomic.AddInt64(&concurrent, -1)
	}

	// Schedule more distinct keys than the cap; at most `cap` may run at once,
	// the rest are dropped.
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		r.refresh(k, fn)
	}
	time.Sleep(40 * time.Millisecond)
	if got := atomic.LoadInt64(&maxSeen); got > 2 {
		t.Fatalf("bounded: concurrency exceeded cap: max=%d", got)
	}
	close(release)
	time.Sleep(40 * time.Millisecond) // let the running refreshes drain
}

// TestSWRRefreshNonBlocking proves the top-level swrRefresh helper returns
// immediately (the caller — a request goroutine — must never block on it).
func TestSWRRefreshNonBlocking(t *testing.T) {
	done := make(chan struct{})
	block := make(chan struct{})
	go func() {
		swrRefresh("x", func() { <-block })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("swrRefresh blocked the caller")
	}
	close(block)
}

// A stale page is rendered for the administrator who made it stale, and served
// as it was to everyone else. A theme or settings save marks every page
// stale; the operator reloading to see it was answered with the page from
// before (3bl). Each page that serves stale copies (a post, Home), each kind
// of caller (a reader, a writer who is not an administrator, an
// administrator), against the same stale copy.
func TestAStalePageIsRenderedForTheOperatorAlone(t *testing.T) {
	openMigratedDB(t)
	render.Init(t.TempDir())
	now := time.Now()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,excerpt,tags,status,created_at,updated_at) VALUES('1','Post','the-post','<p>The page as it is now.</p>','','','published',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	a := &App{userStore: users.New(dbpkg.DB), sessions: auth.NewSessionStore(dbpkg.DB)}
	ctx := context.Background()
	session := func(email, role string) string {
		u, err := a.userStore.Create(ctx, email, "N", "correct horse battery", role)
		if err != nil {
			t.Fatal(err)
		}
		tok, err := a.sessions.Create(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	admin, writer := session("op@example.com", users.RoleAdmin), session("w@example.com", users.RoleAuthor)

	const before = "the page from before the save"
	for _, pg := range []struct {
		name, file, key, url string
		serve                func(http.ResponseWriter, *http.Request)
	}{
		{"a post", filepath.Join("posts", "the-post.html"), "post:the-post", "/the-post", a.handleArticlePage},
		{"Home", filepath.Join("home", "index.html"), "home", "/", a.handleHome},
	} {
		// The reader's request schedules the page's background re-render,
		// which would rewrite the file before it is served; held as already
		// running, it is skipped, and what each caller is answered with is
		// the handler's choice alone.
		swrOnce.Do(func() { swrInst = newSWRRefresher(swrConcurrency()) })
		swrInst.mu.Lock()
		swrInst.inflight[pg.key] = true
		swrInst.mu.Unlock()
		cached := filepath.Join(config.Cfg.CacheDir, pg.file)
		get := func(token string) string {
			if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cached, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			_ = os.Chtimes(cached, old, old)
			render.CachePurgeAll()
			req := httptest.NewRequest("GET", pg.url, nil)
			if token != "" {
				req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: token})
			}
			rc := chi.NewRouteContext()
			rc.URLParams.Add("slug", "the-post")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
			rec := httptest.NewRecorder()
			pg.serve(rec, req)
			return rec.Body.String()
		}
		for _, c := range []struct {
			who, token string
			rendered   bool
		}{
			{"a reader", "", false},
			{"a writer who is not an administrator", writer, false},
			{"the administrator", admin, true},
		} {
			got := get(c.token)
			if rendered := got != before; rendered != c.rendered {
				t.Errorf("%s, %s: rendered %v, want %v (%.60q)", pg.name, c.who, rendered, c.rendered, got)
			}
		}
		swrInst.mu.Lock()
		delete(swrInst.inflight, pg.key)
		swrInst.mu.Unlock()
	}
}
