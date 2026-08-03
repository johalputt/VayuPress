// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Pin the pool to ONE connection. ":memory:" gives every connection its own
	// database, so a second concurrent query silently sees an EMPTY schema — a
	// test can then pass against code that reads the wrong thing, because the
	// wrong thing returned nothing. This was found by a mutation that should have
	// failed and did not.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	applyShippedSchema(t, db)
	return New(db)
}

func TestGetAllReturnsDefaultsWhenEmpty(t *testing.T) {
	s := newTestStore(t)
	all, err := s.GetAll(context.Background(), ForPrimary())
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if all[KeySiteName] != Defaults[KeySiteName] {
		t.Errorf("expected default site name %q, got %q", Defaults[KeySiteName], all[KeySiteName])
	}
	if all[KeyThemePrimaryDark] != "#2dd4bf" {
		t.Errorf("expected default dark primary, got %q", all[KeyThemePrimaryDark])
	}
}

func TestSetManyOverridesDefaultsAndInvalidatesCache(t *testing.T) {
	s := newTestStore(t)
	// Warm the cache with defaults.
	if _, err := s.GetAll(context.Background(), ForPrimary()); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if err := s.SetMany(context.Background(), ForPrimary(), map[string]string{
		KeySiteName:          "Custom Blog",
		KeyThemePrimaryLight: "#123456",
		"unknown.key":        "ignored", // must be dropped by allowlist
	}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	all, err := s.GetAll(context.Background(), ForPrimary())
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if all[KeySiteName] != "Custom Blog" {
		t.Errorf("expected overridden name, got %q", all[KeySiteName])
	}
	if all[KeyThemePrimaryLight] != "#123456" {
		t.Errorf("expected overridden primary, got %q", all[KeyThemePrimaryLight])
	}
	if _, ok := all["unknown.key"]; ok {
		t.Error("unknown key should not be persisted")
	}
	// Untouched key still returns its default.
	if all[KeySiteAuthor] != Defaults[KeySiteAuthor] {
		t.Errorf("expected default author, got %q", all[KeySiteAuthor])
	}
}

// TestTorSpaceKeysPersist is a regression guard for the ADR-0141 world toggle:
// KeyTorSpaceEnabled / KeyTorSpaceAPIKey must be registered in AllKeys, or
// SetMany silently drops the write (returning success) and the one-click switch
// appears to work but never persists — the reloaded page always reads "off".
func TestTorSpaceKeysPersist(t *testing.T) {
	for _, k := range []string{KeyTorSpaceEnabled, KeyTorSpaceAPIKey} {
		if !AllKeys[k] {
			t.Fatalf("%q missing from AllKeys — SetMany will silently drop it", k)
		}
	}
	s := newTestStore(t)
	if err := s.SetMany(context.Background(), ForPrimary(), map[string]string{
		KeyTorSpaceEnabled: "on",
		KeyTorSpaceAPIKey:  "deadbeef",
	}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if got := s.Get(context.Background(), ForPrimary(), KeyTorSpaceEnabled); got != "on" {
		t.Errorf("KeyTorSpaceEnabled did not persist: got %q, want %q", got, "on")
	}
	if got := s.Get(context.Background(), ForPrimary(), KeyTorSpaceAPIKey); got != "deadbeef" {
		t.Errorf("KeyTorSpaceAPIKey did not persist: got %q", got)
	}
}

func TestGetSingleFallsBackToDefault(t *testing.T) {
	s := newTestStore(t)
	if got := s.Get(context.Background(), ForPrimary(), KeyThemeAccentLight); got != "#f59e0b" {
		t.Errorf("expected default accent, got %q", got)
	}
}

func TestFeatureEnabledDefaultsOn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Unset → defaults to "on" → enabled.
	if !s.FeatureEnabled(ctx, ForPrimary(), KeyFeatureComments) {
		t.Error("comments should default to enabled")
	}
	// Explicit "off" disables.
	if err := s.SetMany(ctx, ForPrimary(), map[string]string{KeyFeatureComments: "off"}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if s.FeatureEnabled(ctx, ForPrimary(), KeyFeatureComments) {
		t.Error("comments should be disabled after setting off")
	}
	// Re-enable.
	if err := s.SetMany(ctx, ForPrimary(), map[string]string{KeyFeatureComments: "on"}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if !s.FeatureEnabled(ctx, ForPrimary(), KeyFeatureComments) {
		t.Error("comments should be re-enabled after setting on")
	}
}

// applyShippedSchema builds the settings table from the SHIPPED migrations
// rather than a hand-written CREATE TABLE.
//
// The harness used to restate the schema in Go, and a restated schema drifts:
// it would have kept the pre-082 single-key primary key while the store queried
// by scope, so every per-domain test would have exercised a table the product
// does not have. Reading migration 082 means a test cannot pass against a
// schema nobody ships.
func applyShippedSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	// 010 creates the original table; 082 rebuilds it with the scope in the key.
	for _, f := range []string{
		"../db/migrations/010-site-settings.up.sql",
		"../db/migrations/082-settings-scope.up.sql",
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
			if _, err := db.Exec(line); err != nil {
				t.Fatalf("%s: %v (%s)", f, err, line)
			}
		}
	}
}
