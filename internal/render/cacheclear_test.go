// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

func writeCacheFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// CacheClear removes rendered pages and nothing else. Each file that must
// survive is its own seed, so widening the allow-list to any one of them fails
// here by name.
func TestCacheClearRemovesOnlyRenderedPages(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	prev := config.Cfg.CacheDir
	config.Cfg.CacheDir = cache
	t.Cleanup(func() { config.Cfg.CacheDir = prev })

	pages := map[string]int{
		"home/index.html":                 100,
		"home/d_shop.example/index.html":  200,
		"posts/hello.html":                300,
		"posts/hello.csp":                 10,
		"tags/news.html":                  400,
		"d_shop.example/tags/offers.html": 500,
	}
	keep := []string{
		"update-backups/vp.db.pre-update", // the pre-update database backups
		"search-index.gob",
		"intel/feeds.json",
		"verifiedbot/ranges.json",
		"sitemap.xml",
		"feed.xml",
		"robots.txt",
		".render-stamp",
		"d_", // a bare "d_" is not a per-domain page directory
	}
	want := int64(0)
	for p, n := range pages {
		writeCacheFile(t, filepath.Join(cache, p), n)
		want += int64(n)
	}
	for _, p := range keep {
		if p == "d_" {
			writeCacheFile(t, filepath.Join(cache, "d_", "x"), 1)
			continue
		}
		writeCacheFile(t, filepath.Join(cache, p), 7)
	}

	if files, bytes := CacheUsage(cache); files != len(pages) || bytes != want {
		t.Fatalf("CacheUsage = %d files, %d bytes; want %d, %d", files, bytes, len(pages), want)
	}
	files, bytes := CacheClear()
	if files != len(pages) || bytes != want {
		t.Errorf("CacheClear freed %d files, %d bytes; want %d, %d", files, bytes, len(pages), want)
	}
	for p := range pages {
		if _, err := os.Stat(filepath.Join(cache, p)); !os.IsNotExist(err) {
			t.Errorf("rendered page %s survived the clear", p)
		}
	}
	for _, p := range keep {
		check := p
		if p == "d_" {
			check = "d_/x"
		}
		if _, err := os.Stat(filepath.Join(cache, check)); err != nil {
			t.Errorf("%s is not a rendered page and must survive the clear: %v", check, err)
		}
	}
}

// A page directory that is a symlink (posts/ moved to another disk) keeps its
// link and its target through a clear.
func TestCacheClearKeepsASymlinkedPageDirectory(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	outside := filepath.Join(root, "elsewhere")
	writeCacheFile(t, filepath.Join(outside, "precious.html"), 50)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cache, "posts")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	prev := config.Cfg.CacheDir
	config.Cfg.CacheDir = cache
	t.Cleanup(func() { config.Cfg.CacheDir = prev })

	CacheClear()
	if fi, err := os.Lstat(filepath.Join(cache, "posts")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the clear removed the posts/ symlink, so pages would now be written to the cache's own disk")
	}
	if _, err := os.Stat(filepath.Join(outside, "precious.html")); err != nil {
		t.Errorf("the clear deleted %s, outside CACHE_DIR", filepath.Join(outside, "precious.html"))
	}
}
