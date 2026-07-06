package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
)

func TestIsCacheWarm(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/hello", nil)
	if isCacheWarm(r) {
		t.Fatal("plain request should not be a warm probe")
	}
	r.Header.Set(warmHeader, "1")
	if !isCacheWarm(r) {
		t.Fatal("request with warm header should be a warm probe")
	}
}

func TestWarmTunables(t *testing.T) {
	t.Setenv("VAYUPRESS_CACHE_WARM", "")
	if !cacheWarmEnabled() {
		t.Fatal("warmer should be enabled by default")
	}
	t.Setenv("VAYUPRESS_CACHE_WARM", "0")
	if cacheWarmEnabled() {
		t.Fatal("VAYUPRESS_CACHE_WARM=0 must disable the warmer")
	}

	t.Setenv("VAYUPRESS_CACHE_WARM_DELAY_MS", "40")
	if got := warmDelay(); got != 40*time.Millisecond {
		t.Fatalf("warmDelay = %v, want 40ms", got)
	}
	t.Setenv("VAYUPRESS_CACHE_WARM_DELAY_MS", "bogus")
	if got := warmDelay(); got != defaultWarmDelay {
		t.Fatalf("bad delay should fall back to default, got %v", got)
	}

	t.Setenv("VAYUPRESS_CACHE_WARM_INTERVAL", "10m")
	if got := warmInterval(); got != 10*time.Minute {
		t.Fatalf("warmInterval = %v, want 10m", got)
	}
	// Below the 1-minute floor is rejected in favour of the default.
	t.Setenv("VAYUPRESS_CACHE_WARM_INTERVAL", "5s")
	if got := warmInterval(); got != defaultWarmInterval {
		t.Fatalf("sub-minute interval should fall back to default, got %v", got)
	}
}

func TestCacheFresh(t *testing.T) {
	dir := t.TempDir()
	old := config.Cfg.CacheDir
	config.Cfg.CacheDir = dir
	t.Cleanup(func() { config.Cfg.CacheDir = old })

	if cacheFresh(filepath.Join("posts", "missing.html")) {
		t.Fatal("a missing cache entry must not be fresh")
	}

	if err := os.MkdirAll(filepath.Join(dir, "posts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "posts", "hello.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cacheFresh(filepath.Join("posts", "hello.html")) {
		t.Fatal("a just-written cache entry should be fresh")
	}
}

func TestDiscardWriter(t *testing.T) {
	var w http.ResponseWriter = &discardWriter{h: make(http.Header)}
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(200)
	n, err := w.Write([]byte("a lot of bytes we do not keep"))
	if err != nil || n != 29 {
		t.Fatalf("discardWriter.Write = (%d, %v), want (29, nil)", n, err)
	}
	if w.Header().Get("Content-Type") != "text/html" {
		t.Fatal("discardWriter must retain headers")
	}
}
