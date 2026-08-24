// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFootprintCacheIsServed pins that the Storage page reads directory sizes
// from the background-refreshed cache — not by walking the trees on the request
// path (which blocked for seconds on a multi-GB render cache and inflated p95).
func TestFootprintCacheIsServed(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	media := filepath.Join(dir, "media")
	backups := filepath.Join(dir, "backups")
	for _, d := range []string{cache, media, backups} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeN(t, filepath.Join(cache, "page1"), 4096)
	writeN(t, filepath.Join(cache, "page2"), 2048)
	writeN(t, filepath.Join(media, "img"), 500)
	db := filepath.Join(dir, "vayupress.db")
	writeN(t, db, 10)

	// Populate the cache the way the background refresher does, then read.
	refreshFootprint(cache, media, backups)
	s := collectSysStats(db, cache, media, backups)

	if s.CacheSize != 6144 {
		t.Errorf("CacheSize = %d, want 6144 (from cache, not a live walk)", s.CacheSize)
	}
	if s.MediaSize != 500 {
		t.Errorf("MediaSize = %d, want 500", s.MediaSize)
	}
	if s.DBSize != 10 {
		t.Errorf("DBSize = %d, want 10 (live stat)", s.DBSize)
	}

	// A new file added AFTER the last refresh is NOT reflected until the next
	// background refresh — proving the read path does not re-walk.
	writeN(t, filepath.Join(cache, "page3"), 9999)
	s2 := collectSysStats(db, cache, media, backups)
	if s2.CacheSize != 6144 {
		t.Errorf("CacheSize changed to %d without a refresh — the read path is walking the tree", s2.CacheSize)
	}
	// After a refresh it picks up the new file.
	refreshFootprint(cache, media, backups)
	if s3 := collectSysStats(db, cache, media, backups); s3.CacheSize != 16143 {
		t.Errorf("CacheSize after refresh = %d, want 16143", s3.CacheSize)
	}
}

func writeN(t *testing.T, p string, n int) {
	t.Helper()
	if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}
