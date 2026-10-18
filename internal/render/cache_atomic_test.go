// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// A cached page is served straight from its file while a re-render rewrites
// it (stale-while-revalidate). Written in place, the file was truncated and
// then filled, so a reader in between was sent an empty page with a 200: the
// e2e run of 2026-10-18 got <body></body> for the seeded post. A reader sees
// the old page or the new one, whole, never a part of either.
func TestACachedPageIsNeverReadHalfWritten(t *testing.T) {
	dir := t.TempDir()
	was := config.Cfg.CacheDir
	config.Cfg.CacheDir = dir
	t.Cleanup(func() { config.Cfg.CacheDir = was })

	old, next := strings.Repeat("a", 256<<10), strings.Repeat("b", 256<<10)
	if err := CacheWrite("post/index.html", old); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, "post", "index.html")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var bad string
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(full)
			if err == nil && string(b) != old && string(b) != next {
				bad = "a reader got a page of " + strconv.Itoa(len(b)) + " bytes, neither the old one nor the new"
				return
			}
		}
	}()
	for i := 0; i < 300 && bad == ""; i++ {
		content := next
		if i%2 == 1 {
			content = old
		}
		if err := CacheWrite("post/index.html", content); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if bad != "" {
		t.Fatal(bad)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "post", ".*")); len(left) != 0 {
		t.Errorf("temporary files left beside the page: %v", left)
	}
}
