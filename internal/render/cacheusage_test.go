// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"testing"
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

// CacheUsage counts rendered pages and nothing else. Each file that is not a
// page is its own seed, so widening the allow-list to any one of them fails
// here.
func TestCacheUsageCountsOnlyRenderedPages(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")

	pages := map[string]int{
		"home/index.html":                 100,
		"home/d_shop.example/index.html":  200,
		"posts/hello.html":                300,
		"posts/hello.csp":                 10,
		"tags/news.html":                  400,
		"d_shop.example/tags/offers.html": 500,
	}
	notPages := []string{
		"update-backups/vp.db.pre-update", // the pre-update database backups
		"search-index.gob",
		"intel/feeds.json",
		"verifiedbot/ranges.json",
		"sitemap.xml",
		"feed.xml",
		"robots.txt",
		".render-stamp",
		"d_/x", // a bare "d_" is not a per-domain page directory
	}
	want := int64(0)
	for p, n := range pages {
		writeCacheFile(t, filepath.Join(cache, p), n)
		want += int64(n)
	}
	for _, p := range notPages {
		writeCacheFile(t, filepath.Join(cache, p), 7)
	}
	if files, bytes := CacheUsage(cache); files != len(pages) || bytes != want {
		t.Fatalf("CacheUsage = %d files, %d bytes; want %d, %d: something that is not a page was counted", files, bytes, len(pages), want)
	}
}
