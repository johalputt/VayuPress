// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/users"
	"golang.org/x/sys/unix"
)

// Only a file untouched for the full age is abandoned. One seed per rule: a
// fresh file (an export still being written), a directory and a symlink each
// survive on their own.
func TestStaleTempRemovesOnlyAbandonedFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	write := func(name string, n int, mod time.Time) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write("abandoned.tar", 300, old)
	write("export-in-progress.tar", 500, now.Add(-10*time.Minute))
	if err := os.Mkdir(filepath.Join(dir, "staging"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "staging"), old, old); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.db")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.db")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	// The link itself is old too, so only the symlink rule can keep it: a young
	// link would survive on the age rule alone and prove nothing about this one.
	oldTV := []unix.Timeval{unix.NsecToTimeval(old.UnixNano()), unix.NsecToTimeval(old.UnixNano())}
	if err := unix.Lutimes(filepath.Join(dir, "link.db"), oldTV); err != nil {
		t.Fatal(err)
	}

	if n, b := staleTemp(dir, time.Hour, now, false); n != 1 || b != 300 {
		t.Fatalf("found %d files, %d bytes; want only abandoned.tar (1, 300)", n, b)
	}
	if n, b := staleTemp(dir, time.Hour, now, true); n != 1 || b != 300 {
		t.Errorf("removed %d files, %d bytes; want 1, 300", n, b)
	}
	for _, keep := range []string{"export-in-progress.tar", "staging", "link.db"} {
		if _, err := os.Lstat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s must survive the clear: %v", keep, err)
		}
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("the clear deleted a symlink's target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "abandoned.tar")); !os.IsNotExist(err) {
		t.Errorf("abandoned.tar survived the clear")
	}
}

// A Tor world never calls Cloudflare, even with credentials configured.
func TestCloudflarePurgeNeverLeavesATorWorld(t *testing.T) {
	prev := config.Cfg
	t.Cleanup(func() { config.Cfg = prev })
	config.Cfg.CFZoneID, config.Cfg.CFAPIToken = "zone", "token"
	config.Cfg.OnionMode = false
	if !cloudflareConfigured() {
		t.Fatal("clearnet with credentials must be able to purge")
	}
	config.Cfg.OnionMode = true
	if cloudflareConfigured() {
		t.Error("a Tor world must never send a Cloudflare purge")
	}
	config.Cfg.OnionMode = false
	config.Cfg.CFAPIToken = ""
	if cloudflareConfigured() {
		t.Error("without a token there is nothing to purge with")
	}
}

// Only an administrator clears caches.
func TestClearCacheRefusesANonAdmin(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest(http.MethodPost, "/os/api/storage/clear-cache", strings.NewReader(`{}`))
	r = r.WithContext(context.WithValue(r.Context(), ctxUserKey, &users.User{Role: users.RoleEditor}))
	w := httptest.NewRecorder()
	a.handleOSStorageClearCache(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("an editor got %d; want 403", w.Code)
	}
}
