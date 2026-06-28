package render

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

// writeCachedFile creates a fake cached page and returns its path and mod time.
func writeCachedFile(t *testing.T, rel string) (string, time.Time) {
	t.Helper()
	full := filepath.Join(config.Cfg.CacheDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("<html>cached</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	return full, fi.ModTime()
}

func TestCacheEntryFresh(t *testing.T) {
	config.Cfg.CacheDir = t.TempDir()
	full, mtime := writeCachedFile(t, "posts/p1.html")
	fi, _ := os.Stat(full)

	// Cutoff before the file's mtime → fresh.
	setStaleCutoff(mtime.Add(-time.Second))
	if !CacheEntryFresh(fi) {
		t.Error("file written after cutoff should be fresh")
	}
	// Cutoff after the file's mtime → stale.
	setStaleCutoff(mtime.Add(time.Second))
	if CacheEntryFresh(fi) {
		t.Error("file written before cutoff should be stale")
	}
	// Zero cutoff (the legacy/no-change case) → everything fresh.
	setStaleCutoff(time.Time{})
	if !CacheEntryFresh(fi) {
		t.Error("zero cutoff should treat every cached file as fresh")
	}
	if CacheEntryFresh(nil) {
		t.Error("nil FileInfo must not be fresh")
	}
}

func TestReconcileCacheVersionKeepsCacheWhenUnchanged(t *testing.T) {
	config.Cfg.CacheDir = t.TempDir()
	atomic.StoreInt64(&staleBeforeNano, 0)

	// Stamp written by this exact renderer, with an old cutoff.
	old := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Second)
	writeRenderStamp(renderStampPath(), cacheFingerprint(), old)
	full, _ := writeCachedFile(t, "posts/keep.html")

	ReconcileCacheVersion()

	// The cached file must NOT be deleted (no site-wide wipe).
	if _, err := os.Stat(full); err != nil {
		t.Errorf("unchanged renderer must keep cached pages: %v", err)
	}
	// The cutoff must be restored to the stamp's value, not advanced to now.
	if got := atomic.LoadInt64(&staleBeforeNano); got != old.UnixNano() {
		t.Errorf("cutoff = %d, want restored %d", got, old.UnixNano())
	}
}

func TestReconcileCacheVersionLegacyStampKeepsCache(t *testing.T) {
	config.Cfg.CacheDir = t.TempDir()
	atomic.StoreInt64(&staleBeforeNano, 0)

	// Legacy single-line stamp (fingerprint only) produced by the old scheme.
	_ = os.WriteFile(renderStampPath(), []byte(cacheFingerprint()), 0o644)
	full, _ := writeCachedFile(t, "posts/legacy.html")

	ReconcileCacheVersion()

	if _, err := os.Stat(full); err != nil {
		t.Errorf("legacy matching stamp must keep cached pages: %v", err)
	}
	// Legacy stamp parses with a zero cutoff → everything fresh.
	if got := atomic.LoadInt64(&staleBeforeNano); got != (time.Time{}).UnixNano() {
		t.Errorf("legacy cutoff = %d, want zero-time nanos %d", got, (time.Time{}).UnixNano())
	}
}

func TestReconcileCacheVersionAdvancesOnChange(t *testing.T) {
	config.Cfg.CacheDir = t.TempDir()
	atomic.StoreInt64(&staleBeforeNano, 0)

	// A stamp from a DIFFERENT renderer fingerprint.
	writeRenderStamp(renderStampPath(), "different-fingerprint", time.Now().Add(-time.Hour))
	full, mtime := writeCachedFile(t, "posts/stale.html")

	before := time.Now()
	ReconcileCacheVersion()

	// Files are kept (lazy), not wiped.
	if _, err := os.Stat(full); err != nil {
		t.Errorf("changed renderer must NOT delete cached pages (lazy refresh): %v", err)
	}
	// Cutoff advanced to ~now, so the pre-existing file is now stale.
	got := atomic.LoadInt64(&staleBeforeNano)
	if got < before.UnixNano() {
		t.Errorf("cutoff should have advanced to ~now; got %d, before=%d", got, before.UnixNano())
	}
	fi, _ := os.Stat(full)
	_ = mtime
	if CacheEntryFresh(fi) {
		t.Error("a page written before the renderer change must read as stale")
	}
	// Stamp must now carry the current fingerprint so a restart is a no-op.
	fp, _, ok := readRenderStamp(renderStampPath())
	if !ok || fp != cacheFingerprint() {
		t.Errorf("stamp not rewritten with current fingerprint: ok=%v fp=%q", ok, fp)
	}
}

func TestCachePurgeAllIsLazyAndPersists(t *testing.T) {
	config.Cfg.CacheDir = t.TempDir()
	atomic.StoreInt64(&staleBeforeNano, 0)
	full, _ := writeCachedFile(t, "posts/global.html")

	before := time.Now()
	CachePurgeAll()

	// Not deleted — refreshed lazily on next request.
	if _, err := os.Stat(full); err != nil {
		t.Errorf("CachePurgeAll must not delete cached pages: %v", err)
	}
	// Pre-existing page is now stale.
	fi, _ := os.Stat(full)
	if CacheEntryFresh(fi) {
		t.Error("CachePurgeAll should mark existing pages stale")
	}
	// Cutoff persisted so a restart keeps them stale.
	_, since, ok := readRenderStamp(renderStampPath())
	if !ok || since.Before(before.Add(-time.Second)) {
		t.Errorf("CachePurgeAll must persist the advanced cutoff; ok=%v since=%v", ok, since)
	}
}
