// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/scheduler"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/tagrules"
)

// seedStoredPost stores a post as one written before the tag rules was: its
// tags as sent, and links keyed by the old lower-cased tag_norm.
func seedStoredPost(t *testing.T, id string, tags ...string) {
	t.Helper()
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at) VALUES(?,?,?,?,?, 'published',?,?)`,
		id, "T "+id, id, "<p>x</p>", strings.Join(tags, ","), at, at); err != nil {
		t.Fatal(err)
	}
	for _, tag := range tags {
		if _, err := dbpkg.DB.Exec(`INSERT INTO article_tags(article_id, tag, tag_norm, created_at) VALUES(?,?,?,?)`, id, tag, strings.ToLower(tag), at); err != nil {
			t.Fatal(err)
		}
	}
}

func storedTags(t *testing.T, id string) (tags, links string) {
	t.Helper()
	if err := dbpkg.DB.QueryRow(`SELECT tags, COALESCE((SELECT group_concat(tag_norm) FROM (SELECT tag_norm FROM article_tags WHERE article_id=a.id ORDER BY rowid)),'') FROM articles a WHERE id=?`, id).Scan(&tags, &links); err != nil {
		t.Fatal(err)
	}
	return tags, links
}

func forgetTagShares(t *testing.T) {
	t.Helper()
	tagShares.Store(nil)
	t.Cleanup(func() { tagShares.Store(nil) })
}

// The pass gives each stored post its tags under the rules: the rarest across
// the site past MaxPerPost, no stop-words, links under their keys. It keeps
// what it replaced, leaves posts already under the rules and the post's date
// alone, and once at the end never runs again.
func TestTheStoredTagsAreCleanedOnce(t *testing.T) {
	a := textRepairApp(t)
	forgetTagShares(t)
	for i := 1; i <= 5; i++ {
		seedStoredPost(t, fmt.Sprintf("f%d", i), "tutorial", "developer")
	}
	sent := []string{"Machine Learning", "with", "tutorial", "developer"}
	for i := 0; i < 9; i++ {
		sent = append(sent, fmt.Sprintf("rare%d", i))
	}
	seedStoredPost(t, "p1", sent...)
	seedStoredPost(t, "p2", "Go Lang")
	seedStoredPost(t, "p3", "go")
	var before string
	_ = dbpkg.DB.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM articles WHERE id='p1'`).Scan(&before)

	if err := a.cleanStoredTags(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}

	rare := "rare0,rare1,rare2,rare3,rare4,rare5,rare6,rare7,rare8"
	if tags, links := storedTags(t, "p1"); tags != "Machine Learning,"+rare || links != "machine-learning,"+rare {
		t.Errorf("p1 kept tags=%q links=%q; want the rarest ten in the order sent, keyed", tags, links)
	}
	if tags, links := storedTags(t, "p2"); tags != "Go Lang" || links != "go-lang" {
		t.Errorf("p2 tags=%q links=%q; a link under its old tag_norm is rewritten under its key", tags, links)
	}
	originals := map[string]string{}
	rows, err := dbpkg.DB.Query(`SELECT article_id, tags FROM tag_originals`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, tags string
		_ = rows.Scan(&id, &tags)
		originals[id] = tags
	}
	_ = rows.Close()
	if len(originals) != 2 || originals["p1"] != strings.Join(sent, ",") || originals["p2"] != "Go Lang" {
		t.Errorf("originals kept = %v; want p1's and p2's as they were, and no other", originals)
	}
	var after string
	_ = dbpkg.DB.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM articles WHERE id='p1'`).Scan(&after)
	if after != before {
		t.Errorf("updated_at moved from %s to %s: the post was not edited", before, after)
	}
	var scanned, changed int
	var done sql.NullString
	_ = dbpkg.DB.QueryRow(`SELECT scanned, changed, done_at FROM tag_cleanup WHERE id=1`).Scan(&scanned, &changed, &done)
	if scanned != 8 || changed != 2 || !done.Valid {
		t.Errorf("record: scanned=%d changed=%d done=%v; want 8, 2, done", scanned, changed, done.Valid)
	}

	seedStoredPost(t, "p4", "with", "go")
	if err := a.cleanStoredTags(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	if tags, _ := storedTags(t, "p4"); tags != "with,go" {
		t.Errorf("a finished pass ran again: p4 tags %q", tags)
	}
}

// The pass resumes after the last post it cleaned, and reaches every post
// across its batches.
func TestTheTagPassResumesAndCrossesBatches(t *testing.T) {
	a := textRepairApp(t)
	forgetTagShares(t)
	seedStoredPost(t, "a0", "with", "go")
	for i := 1; i <= tagCleanupBatch+1; i++ {
		seedStoredPost(t, fmt.Sprintf("b%04d", i), "with", "go")
	}
	if _, err := dbpkg.DB.Exec(`INSERT INTO tag_cleanup(id, cursor) VALUES(1, 'a0')`); err != nil {
		t.Fatal(err)
	}
	if err := a.cleanStoredTags(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	if tags, _ := storedTags(t, "a0"); tags != "with,go" {
		t.Errorf("the post before where the pass stopped was cleaned again: %q", tags)
	}
	var left int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE id LIKE 'b%' AND tags<>'go'`).Scan(&left)
	if left != 0 {
		t.Errorf("%d posts after the cursor were not cleaned", left)
	}
}

// Each tag has one address: its other spellings are sent there for good, the
// address itself is served without another redirect whatever it holds, and a
// word that is no tag has no page.
func TestATagHasOneAddress(t *testing.T) {
	setupRelatedTestDB(t)
	render.Init(t.TempDir())
	forgetTagShares(t)
	a := &App{siteSettings: settings.New(dbpkg.DB), articles: &api.ArticleService{Repo: dbpkg.NewArticleRepo(dbpkg.DB)}}
	for _, c := range []struct{ path, param, location string }{
		{"/tags/Machine_Learning", "Machine_Learning", "/tags/machine-learning"},
		{"/tags/Machine%20Learning", "Machine Learning", "/tags/machine-learning"},
		{"/tags/C++", "C++", "/tags/c++"},
		{"/tags/C%2B%2B", "C%2B%2B", "/tags/c++"},
	} {
		rec := httptest.NewRecorder()
		a.handleTagPage(rec, routeRequest(c.path, "tag", c.param))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != c.location {
			t.Errorf("%s: %d to %q, want 301 to %s", c.path, rec.Code, rec.Header().Get("Location"), c.location)
		}
	}
	rec := httptest.NewRecorder()
	a.handleTagPage(rec, routeRequest("/tags/c%2B%2B", "tag", "c%2B%2B"))
	if rec.Code == http.StatusMovedPermanently {
		t.Errorf("/tags/c%%2B%%2B is c++ itself, escaped, and was redirected to %q", rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	a.handleTagPage(rec, routeRequest("/tags/with", "tag", "with"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("/tags/with: %d, want 404", rec.Code)
	}
	// Where a redirect sends a key is that key again: no loop, for the symbols
	// a key keeps and for any script.
	for _, key := range []string{"machine-learning", "c++", "c#", ".net", "node.js", "東京", "zürich"} {
		u, err := url.Parse("/tags/" + url.PathEscape(key))
		if err != nil {
			t.Fatal(err)
		}
		if got := tagrules.Key(strings.TrimPrefix(u.Path, "/tags/")); got != key {
			t.Errorf("%q links to %s, which is the tag %q", key, u.String(), got)
		}
	}
}

// A tag is broad when it is on more than half the published posts: the one
// rule (render.Ubiquitous) Halcyon's desks and related reading use too.
func TestWhatMakesATagBroad(t *testing.T) {
	forgetTagShares(t)
	if broadTag("t") {
		t.Error("a tag is broad before anything was counted")
	}
	for _, c := range []struct {
		posts, total int
		broad        bool
	}{
		{50, 100, false}, {51, 100, true}, {0, 0, false},
	} {
		tagShares.Store(&tagShareCounts{posts: map[string]int{"t": c.posts}, total: c.total})
		if got := broadTag("t"); got != c.broad {
			t.Errorf("%d of %d posts: broad %v, want %v", c.posts, c.total, got, c.broad)
		}
	}
}

// Related posts are not found through a broad tag: through it, they were the
// newest posts whatever they were about.
func TestRelatedPostsPassOverABroadTag(t *testing.T) {
	setupRelatedTestDB(t)
	forgetTagShares(t)
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	ctx := context.Background()
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mk := func(slug string, hoursAgo int, tags ...string) {
		at := base.Add(-time.Duration(hoursAgo) * time.Hour)
		if err := repo.Create(ctx, dbpkg.Article{ID: slug, Title: slug, Slug: slug, Content: "<p>x</p>", Tags: tags, Status: "published", CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		if err := dbpkg.RunInTx(ctx, dbpkg.DB, func(tx *sql.Tx) error { return dbpkg.SyncArticleTagsByIDTx(tx, slug, at, tags) }); err != nil {
			t.Fatal(err)
		}
	}
	mk("current", 0, "tutorial", "sqlite")
	mk("tutorial-newest", 1, "tutorial")
	mk("sqlite-older", 5, "sqlite")
	tagShares.Store(&tagShareCounts{posts: map[string]int{"tutorial": 90, "sqlite": 2}, total: 100})
	got := (&App{}).relatedArticles(ctx, "current", []string{"tutorial", "sqlite"}, 1)
	if len(got) != 1 || got[0].Slug != "sqlite-older" {
		t.Errorf("related = %+v, want sqlite-older (through sqlite, not the broad tutorial)", got)
	}
}

// A post scheduled with more tags than the rules keep is staged, under them,
// as it will be published; it is not refused.
func TestAScheduledPostIsNeverRefusedForItsTags(t *testing.T) {
	openMigratedDB(t)
	forgetTagShares(t)
	a := &App{scheduler: scheduler.New(dbpkg.DB)}
	tags := []string{"with"}
	for i := 0; i < 25; i++ {
		tags = append(tags, fmt.Sprintf("topic%02d", i))
	}
	body, _ := json.Marshal(map[string]any{"slug": "later", "title": "Later", "content": "<p>x</p>", "tags": tags, "publish_at": "2030-01-01T00:00:00Z"})
	rec := httptest.NewRecorder()
	a.handleScheduleCreate(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/schedule", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("scheduling a post with %d tags: %d %s", len(tags), rec.Code, rec.Body.String())
	}
	var staged string
	_ = dbpkg.DB.QueryRow(`SELECT tags FROM scheduled_posts WHERE slug='later'`).Scan(&staged)
	if n := len(strings.Split(staged, ",")); n != tagrules.MaxPerPost || strings.Contains(staged, "with") {
		t.Errorf("staged %d tags %q, want %d without the stop-word", n, staged, tagrules.MaxPerPost)
	}
}

// A tag's count joins its links written before the tag rules (old tag_norm)
// with those written after (its key), so its rarity is the whole of it.
func TestATagIsCountedUnderItsKey(t *testing.T) {
	openMigratedDB(t)
	forgetTagShares(t)
	seedStoredPost(t, "old", "Machine Learning")
	at := time.Now()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at) VALUES('new','T','new','<p>x</p>','machine-learning','published',?,?)`, at, at); err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.RunInTx(context.Background(), dbpkg.DB, func(tx *sql.Tx) error {
		return dbpkg.SyncArticleTagsByIDTx(tx, "new", at, []string{"machine-learning"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := countTagShares(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := tagFrequency("machine-learning"); n != 2 {
		t.Errorf("machine-learning counted on %d posts, want 2", n)
	}
}
