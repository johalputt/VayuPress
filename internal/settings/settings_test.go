package settings

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE site_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

func TestGetAllReturnsDefaultsWhenEmpty(t *testing.T) {
	s := newTestStore(t)
	all, err := s.GetAll(context.Background())
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
	if _, err := s.GetAll(context.Background()); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if err := s.SetMany(context.Background(), map[string]string{
		KeySiteName:          "Custom Blog",
		KeyThemePrimaryLight: "#123456",
		"unknown.key":        "ignored", // must be dropped by allowlist
	}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	all, err := s.GetAll(context.Background())
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

func TestGetSingleFallsBackToDefault(t *testing.T) {
	s := newTestStore(t)
	if got := s.Get(context.Background(), KeyThemeAccentLight); got != "#f59e0b" {
		t.Errorf("expected default accent, got %q", got)
	}
}

func TestFeatureEnabledDefaultsOn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Unset → defaults to "on" → enabled.
	if !s.FeatureEnabled(ctx, KeyFeatureComments) {
		t.Error("comments should default to enabled")
	}
	// Explicit "off" disables.
	if err := s.SetMany(ctx, map[string]string{KeyFeatureComments: "off"}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if s.FeatureEnabled(ctx, KeyFeatureComments) {
		t.Error("comments should be disabled after setting off")
	}
	// Re-enable.
	if err := s.SetMany(ctx, map[string]string{KeyFeatureComments: "on"}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if !s.FeatureEnabled(ctx, KeyFeatureComments) {
		t.Error("comments should be re-enabled after setting on")
	}
}
