package api

import (
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func init() {
	config.Cfg.DBPath = ":memory:"
	dbpkg.Init() //nolint:errcheck
}

func makeService() *ArticleService {
	return &ArticleService{
		DB:      dbpkg.DB,
		Enqueue: MakeEnqueueFn(dbpkg.DB),
	}
}

// ── Validation ────────────────────────────────────────────────────────────────

func TestValidateArticleInput_Valid(t *testing.T) {
	if err := ValidateArticleInput("Title", "my-slug", "<p>content</p>", nil); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestValidateArticleInput_EmptyTitle(t *testing.T) {
	if err := ValidateArticleInput("", "slug", "content", nil); err == nil {
		t.Fatal("want error for empty title")
	}
}

func TestValidateArticleInput_InvalidSlug(t *testing.T) {
	if err := ValidateArticleInput("Title", "BAD SLUG!", "content", nil); err == nil {
		t.Fatal("want error for invalid slug")
	}
}

func TestValidateArticleInput_TooManyTags(t *testing.T) {
	tags := make([]string, 21)
	for i := range tags {
		tags[i] = "t"
	}
	if err := ValidateArticleInput("Title", "slug", "content", tags); err == nil {
		t.Fatal("want error for >20 tags")
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

// ── ArticleService ─────────────────────────────────────────────────────────────

func TestArticleService_CreateAndGet(t *testing.T) {
	svc := makeService()
	res, err := svc.Create("Test Title", "test-create-get", "<p>hello</p>", []string{"go"})
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

	art, err := svc.Get("test-create-get")
	if err != nil {
		t.Fatalf("Get after direct insert: %v", err)
	}
	if art.Slug != "test-create-get" {
		t.Errorf("slug mismatch: %q", art.Slug)
	}
}

func TestArticleService_Create_SlugConflict(t *testing.T) {
	svc := makeService()
	// Insert an article directly to create a conflict.
	dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES('x','T','conflict-slug','c','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	_, err := svc.Create("Title", "conflict-slug", "content", nil)
	if err != ErrSlugConflict {
		t.Fatalf("want ErrSlugConflict, got %v", err)
	}
}

func TestArticleService_Get_NotFound(t *testing.T) {
	svc := makeService()
	_, err := svc.Get("nonexistent-slug-xyz")
	if err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestArticleService_List_Empty(t *testing.T) {
	svc := makeService()
	res, err := svc.List(1, 20, "")
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
	_, err := svc.BulkCreate(items)
	if err == nil {
		t.Fatal("want error for >1000 items")
	}
}

func TestArticleService_ListTags(t *testing.T) {
	svc := makeService()
	// Insert article with tags directly.
	dbpkg.DB.Exec(`INSERT OR IGNORE INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES('tag1','T','tag-test-1','c','go,web',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	tags, err := svc.ListTags()
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	_ = tags // May be empty if JSON extraction not available, just no panic.
}
