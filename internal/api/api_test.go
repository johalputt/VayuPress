// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/queue"
)

func init() {
	config.Cfg.DBPath = ":memory:"
	dbpkg.Init() //nolint:errcheck
}

var bg = context.Background()

func makeService() *ArticleService {
	return &ArticleService{
		Repo:  dbpkg.NewArticleRepo(dbpkg.DB),
		Queue: queue.NewSQLiteWriter(dbpkg.DB, 0), // 0 = no hard limit in tests
	}
}

// ── Validation ────────────────────────────────────────────────────────────────

func TestValidateArticleInput_Valid(t *testing.T) {
	if err := ValidateArticleInput("Title", "my-slug", "<p>content</p>"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidateArticleInput_EmptyTitle(t *testing.T) {
	if err := ValidateArticleInput("", "slug", "content"); err == nil {
		t.Fatal("want error for empty title")
	}
}

func TestValidateArticleInput_InvalidSlug(t *testing.T) {
	if err := ValidateArticleInput("Title", "BAD SLUG!", "content"); err == nil {
		t.Fatal("want error for invalid slug")
	}
}

func TestSplitTags(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"go,web,test", []string{"go", "web", "test"}},
		{" go , web ", []string{"go", "web"}},
		{"dup,dup", []string{"dup"}},
	}
	for _, c := range cases {
		got := SplitTags(c.in)
		if len(got) != len(c.want) {
			t.Errorf("SplitTags(%q): got %v want %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("SplitTags(%q)[%d]: got %q want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestIsValidSlug(t *testing.T) {
	if !IsValidSlug("hello-world") {
		t.Error("hello-world should be valid")
	}
	if IsValidSlug("UPPER") {
		t.Error("uppercase should be invalid")
	}
	if IsValidSlug("has space") {
		t.Error("space should be invalid")
	}
	if !IsValidSlug("a") {
		t.Error("single char should be valid")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Great Post!":         "my-great-post",
		"Hello, World":           "hello-world",
		"  spaced  out  ":        "spaced-out",
		"already-a-slug":         "already-a-slug",
		"Multiple   ---  Dashes": "multiple-dashes",
		"UPPER CASE":             "upper-case",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"My Great Post!", "Hello, World", "UPPER CASE"} {
		if got := Slugify(in); !IsValidSlug(got) {
			t.Errorf("Slugify(%q) = %q is not a valid slug", in, got)
		}
	}
}

func TestValidateArticleInput_ErrorClassification(t *testing.T) {
	// Invalid slug must surface as ErrInvalidSlug (→ HTTP 400), not a bare error.
	err := ValidateArticleInput("Title", "BAD SLUG", "<p>x</p>")
	if !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("invalid slug: want ErrInvalidSlug, got %v", err)
	}
	if HTTPStatus(err) != 400 {
		t.Errorf("invalid slug: want HTTP 400, got %d", HTTPStatus(err))
	}
	// Other field failures must surface as ErrValidation (→ HTTP 400).
	for _, tc := range []struct {
		name                 string
		title, slug, content string
	}{
		{"empty title", "", "ok", "<p>x</p>"},
		{"empty content", "Title", "ok", ""},
	} {
		err := ValidateArticleInput(tc.title, tc.slug, tc.content)
		if !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", tc.name, err)
		}
		if HTTPStatus(err) != 400 {
			t.Errorf("%s: want HTTP 400, got %d", tc.name, HTTPStatus(err))
		}
	}
}

// ── ArticleService ─────────────────────────────────────────────────────────────

func TestArticleService_CreateAndGet(t *testing.T) {
	svc := makeService()
	res, err := svc.Create(bg, "Test Title", "test-create-get", "<p>hello</p>", []string{"go"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.ID == "" || res.Slug != "test-create-get" {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Process the write job so the article row is visible.
	dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at)
		SELECT article_json->>'$.id', article_json->>'$.title', article_json->>'$.slug',
		       article_json->>'$.content', 'go', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		FROM write_jobs WHERE status='pending' ORDER BY id DESC LIMIT 1`)

	art, err := svc.Get(bg, "test-create-get")
	if err != nil {
		t.Fatalf("Get after direct insert: %v", err)
	}
	if art.Slug != "test-create-get" {
		t.Errorf("slug mismatch: %q", art.Slug)
	}
}

func TestArticleService_Create_SlugConflict(t *testing.T) {
	svc := makeService()
	dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES('x','T','conflict-slug','c','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	_, err := svc.Create(bg, "Title", "conflict-slug", "content", nil)
	if err != ErrSlugConflict {
		t.Fatalf("want ErrSlugConflict, got %v", err)
	}
}

func TestArticleService_Get_NotFound(t *testing.T) {
	svc := makeService()
	_, err := svc.Get(bg, "nonexistent-slug-xyz")
	if err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestArticleService_List_Empty(t *testing.T) {
	svc := makeService()
	res, err := svc.List(bg, 1, 20, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Articles == nil {
		t.Error("Articles should not be nil (empty slice expected)")
	}
}

func TestArticleService_BulkCreate_ExceedsLimit(t *testing.T) {
	svc := makeService()
	items := make([]BulkCreateItem, 1001)
	_, err := svc.BulkCreate(bg, items)
	if err == nil {
		t.Fatal("want error for >1000 items")
	}
}

func TestArticleService_ListTags(t *testing.T) {
	svc := makeService()
	dbpkg.DB.Exec(`INSERT OR IGNORE INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES('tag1','T','tag-test-1','c','go,web',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	tags, err := svc.ListTags(bg)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	_ = tags
}

// Text that reads as garbled is stored as it was sent, through create, bulk
// create and update alike, and the post is offered to its author for review;
// a write that leaves nothing to restore takes it off the list.
func TestArticleService_GarbledTextIsStoredAsSentAndOffered(t *testing.T) {
	svc := makeService()
	queued := func() (title, content string) {
		t.Helper()
		if err := dbpkg.DB.QueryRow(`SELECT article_json->>'$.title', article_json->>'$.content' FROM write_jobs ORDER BY id DESC LIMIT 1`).Scan(&title, &content); err != nil {
			t.Fatal(err)
		}
		return title, content
	}
	offer := func(slug string) string {
		var state string
		_ = dbpkg.DB.QueryRow(`SELECT state FROM text_repair_offer WHERE slug=?`, slug).Scan(&state)
		return state
	}
	sentTitle, sentBody := "It\u00e2\u20ac\u2122s fine", "<p>a \u00e2\u20ac\u201d b</p>"
	if _, err := svc.Create(bg, sentTitle, "garbled-create", sentBody, []string{"go"}); err != nil {
		t.Fatal(err)
	}
	if title, content := queued(); title != sentTitle || content != sentBody {
		t.Errorf("create queued %q, %q: not as sent", title, content)
	}
	if got := offer("garbled-create"); got != "offered" {
		t.Errorf("create: offer %q", got)
	}
	if _, err := svc.BulkCreate(bg, []BulkCreateItem{{Title: "Bulk", Slug: "garbled-bulk", Content: sentBody, Tags: []string{"go"}}}); err != nil {
		t.Fatal(err)
	}
	if _, content := queued(); content != sentBody {
		t.Errorf("bulk create queued %q: not as sent", content)
	}
	if got := offer("garbled-bulk"); got != "offered" {
		t.Errorf("bulk create: offer %q", got)
	}
	dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES('g','T','garbled-update','c','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	title, body := "na\u00c3\u00afve", "<p>caf\u00c3\u00a9</p>"
	if _, err := svc.Update(bg, "garbled-update", &title, &body, nil); err != nil {
		t.Fatal(err)
	}
	if gotTitle, content := queued(); gotTitle != title || content != body {
		t.Errorf("update queued %q, %q: not as sent", gotTitle, content)
	}
	if got := offer("garbled-update"); got != "offered" {
		t.Errorf("update: offer %q", got)
	}
	title, body = "naïve", "<p>café</p>"
	if _, err := svc.Update(bg, "garbled-update", &title, &body, nil); err != nil {
		t.Fatal(err)
	}
	if got := offer("garbled-update"); got != "" {
		t.Errorf("a clean write left the post offered: %q", got)
	}
}

// A post is never refused for its tags. Sent more than the rules keep, it keeps
// the rarest across the site, in the order sent; on create and on an update of
// a post stored before the rules alike.
func TestArticleService_TagsAreCleanedNeverRefused(t *testing.T) {
	svc := makeService()
	svc.TagFrequency = func(key string) int {
		if key == "tutorial" || key == "developer" {
			return 1000
		}
		return 1
	}
	sent := []string{"Machine Learning", "with", "tutorial", "developer"}
	for i := 0; i < 22; i++ {
		sent = append(sent, fmt.Sprintf("topic%02d", i))
	}
	want := "Machine Learning,topic00,topic01,topic02,topic03,topic04,topic05,topic06,topic07,topic08"
	queuedTags := func() string {
		t.Helper()
		var tags string
		if err := dbpkg.DB.QueryRow(`SELECT (SELECT group_concat(value) FROM json_each(article_json->'$.tags')) FROM write_jobs ORDER BY id DESC LIMIT 1`).Scan(&tags); err != nil {
			t.Fatal(err)
		}
		return tags
	}
	if _, err := svc.Create(bg, "Tagged", "tags-create", "<p>x</p>", sent); err != nil {
		t.Fatalf("a post with %d tags was refused: %v", len(sent), err)
	}
	if got := queuedTags(); got != want {
		t.Errorf("create queued tags %q, want %q", got, want)
	}
	dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES('old','T','tags-update','c',?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, strings.Join(sent, ","))
	title := "Edited"
	if _, err := svc.Update(bg, "tags-update", &title, nil, nil); err != nil {
		t.Fatalf("editing a post stored with %d tags was refused: %v", len(sent), err)
	}
	if got := queuedTags(); got != want {
		t.Errorf("update queued tags %q, want %q", got, want)
	}
}

// The posts list filtered by a tag finds it however the tag is spelled.
func TestArticleService_ListByTagFindsItUnderItsKey(t *testing.T) {
	svc := makeService()
	at := time.Now()
	dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at) VALUES('ml','T','list-by-key','c','Machine Learning','published',?,?)`, at, at)
	if err := dbpkg.RunInTx(bg, dbpkg.DB, func(tx *sql.Tx) error {
		return dbpkg.SyncArticleTagsByIDTx(tx, "ml", at, []string{"Machine Learning"})
	}); err != nil {
		t.Fatal(err)
	}
	for _, spelled := range []string{"Machine Learning", "machine_learning", "machine-learning"} {
		res, err := svc.List(bg, 1, 10, spelled)
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 {
			t.Errorf("List by %q found %d posts, want 1", spelled, res.Total)
		}
	}
}
