// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

func freshDomCache(t *testing.T) {
	t.Helper()
	old := config.Cfg.CacheDir
	config.Cfg.CacheDir = t.TempDir()
	t.Cleanup(func() { config.Cfg.CacheDir = old })
}

// writeDomArtefact writes the artefact at p, its full path: a rebuild runs
// after the request, and must not read the cache directory a test's cleanup
// puts back.
func writeDomArtefact(t *testing.T, p string, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

// A site's sitemap never written is built while the crawler waits, once,
// however many crawlers ask for it at the same moment.
func TestADomArtefactNeverWrittenIsBuiltOnceWhileWaiting(t *testing.T) {
	freshDomCache(t)
	rel := "sitemap_d_first.xml"
	p := filepath.Join(config.Cfg.CacheDir, rel)
	var builds atomic.Int32
	build := func() {
		builds.Add(1)
		time.Sleep(50 * time.Millisecond)
		writeDomArtefact(t, p, 0)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); freshDomArtefact(rel, build) }()
	}
	wg.Wait()
	if n := builds.Load(); n != 1 {
		t.Errorf("%d builds for eight crawlers at once", n)
	}
	if _, err := os.Stat(filepath.Join(config.Cfg.CacheDir, rel)); err != nil {
		t.Errorf("the crawler was let go before the file was there: %v", err)
	}
}

// A stale sitemap is served as it is, at once, and rebuilt once in the
// background, however many crawlers find it stale while that runs.
func TestAStaleDomArtefactIsServedAndRebuiltOnceBehind(t *testing.T) {
	freshDomCache(t)
	rel := "sitemap_d_stale.xml"
	p := filepath.Join(config.Cfg.CacheDir, rel)
	writeDomArtefact(t, p, 2*domSEOFreshWindow)
	release := make(chan struct{})
	var builds atomic.Int32
	build := func() {
		builds.Add(1)
		<-release
		writeDomArtefact(t, p, 0)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		for i := 0; i < 8; i++ {
			freshDomArtefact(rel, build)
		}
	}()
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Error("crawlers waited for the rebuild instead of being served the file there")
	}
	close(release)
	<-served
	waitForDomRebuild(rel)
	if n := builds.Load(); n != 1 {
		t.Errorf("%d rebuilds started for eight crawlers", n)
	}
	if domArtefactStale(rel) {
		t.Error("the background rebuild never wrote the file")
	}
}

// A sitemap within its freshness window is not rebuilt.
func TestAFreshDomArtefactIsNotRebuilt(t *testing.T) {
	freshDomCache(t)
	rel := "feed_d_fresh.xml"
	writeDomArtefact(t, filepath.Join(config.Cfg.CacheDir, rel), 0)
	freshDomArtefact(rel, func() { t.Error("a fresh file was rebuilt") })
}

// A rebuild that panics leaves the old file served and the process up, and
// the next stale request may rebuild again.
func TestAPanickingRebuildTakesNothingDown(t *testing.T) {
	freshDomCache(t)
	rel := "robots_d_panic.txt"
	writeDomArtefact(t, filepath.Join(config.Cfg.CacheDir, rel), 2*domSEOFreshWindow)
	done := make(chan struct{})
	freshDomArtefact(rel, func() { defer close(done); panic("a broken rebuild") })
	<-done
	waitForDomRebuild(rel)
	rebuilt := make(chan struct{})
	freshDomArtefact(rel, func() { close(rebuilt) })
	select {
	case <-rebuilt:
	case <-time.After(5 * time.Second):
		t.Error("after a panic, no rebuild could start again")
	}
	waitForDomRebuild(rel)
}

// waitForDomRebuild waits for rel's background rebuild to end, up to five
// seconds.
func waitForDomRebuild(rel string) {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, running := domRebuilding.Load(rel); !running {
			return
		}
	}
}
