// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/db"
)

// A tag page with fewer than thinTagPosts posts is kept out of search, its
// links still followed; one with that many is offered as before.
func TestAThinTagPageIsNotIndexed(t *testing.T) {
	const noindex = `<meta name="robots" content="noindex,follow">`
	for n, want := range map[int]bool{1: true, thinTagPosts - 1: true, thinTagPosts: false, 40: false} {
		page, err := RenderTagPage("example.com", "1.0.0", "go", nil, n, false)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(page, noindex); got != want {
			t.Errorf("%d posts: noindex %v, want %v", n, got, want)
		}
	}
}

// A broad tag's page is kept out of search however many posts it lists: it
// says nothing about any one of them.
func TestABroadTagPageIsNotIndexed(t *testing.T) {
	page, err := RenderTagPage("example.com", "1.0.0", "tutorial", nil, 40, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, `<meta name="robots" content="noindex,follow">`) {
		t.Error("a broad tag page with 40 posts is offered to search")
	}
}

// A post links each tag to the tag's one page, and never to a word that has
// none: a post stored before the tag rules shows its tags under them.
func TestAPostLinksOnlyToTagPages(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	art := db.Article{Title: "T", Slug: "t", Content: "<p>x</p>", Tags: []string{"Machine Learning", "with", "C#"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	page, err := RenderArticle(art)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="/tags/machine-learning"`, `href="/tags/c%23"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the post page has no %s", want)
		}
	}
	if strings.Contains(page, `/tags/with"`) || strings.Contains(page, "#with<") {
		t.Error("the post page shows the stop-word with as a tag")
	}
}

// Purging a post's tags reaches each tag page's cache file, which is under the
// tag's key, however the post spells the tag.
func TestAPurgeReachesTheTagPageByKey(t *testing.T) {
	dir := t.TempDir()
	old := config.Cfg.CacheDir
	config.Cfg.CacheDir = dir
	t.Cleanup(func() { config.Cfg.CacheDir = old })
	page := filepath.Join(dir, "tags", "machine-learning.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	CachePurge("t", []string{"Machine Learning"}, nil, nil, nil)
	if _, err := os.Stat(page); !os.IsNotExist(err) {
		t.Error("the cached page of machine-learning outlived a purge of the tag Machine Learning")
	}
}
