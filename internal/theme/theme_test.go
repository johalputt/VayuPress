package theme_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestCompileCSSEscapesColors verifies that an injected "</style>" in a colour
// value does not pass through the compiler — the value must be rejected outright
// because it fails the hex colour validation.
func TestCompileCSSEscapesColors(t *testing.T) {
	tok := theme.Default()
	tok.BgDark = "</style><script>alert(1)</script>"

	_, err := theme.CompileCSS(tok)
	if err == nil {
		t.Fatal("CompileCSS should have returned an error for an invalid colour value, but got nil")
	}
	if !strings.Contains(err.Error(), "BgDark") {
		t.Fatalf("expected error to mention 'BgDark', got: %v", err)
	}
}

// TestPresetsAllValid verifies that each of the 8 built-in presets compiles
// successfully with CompileCSS.
func TestPresetsAllValid(t *testing.T) {
	presets := theme.AllPresets()
	if len(presets) != 8 {
		t.Fatalf("expected 8 presets, got %d", len(presets))
	}
	for _, p := range presets {
		css, err := theme.CompileCSS(p)
		if err != nil {
			t.Errorf("preset %q failed to compile: %v", p.Name, err)
			continue
		}
		if css == "" {
			t.Errorf("preset %q compiled to empty CSS", p.Name)
		}
		if !strings.Contains(css, ":root{") {
			t.Errorf("preset %q CSS missing :root{ block", p.Name)
		}
	}
}

// TestStoreRoundTrip saves a Tokens value to an in-memory SQLite database,
// then loads it back and verifies the values match.
func TestStoreRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory SQLite: %v", err)
	}
	defer db.Close()

	// Create the migration table.
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS theme_tokens (
			id      INTEGER PRIMARY KEY CHECK (id = 1),
			name    TEXT NOT NULL DEFAULT 'Default',
			tokens  TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)`)
	if err != nil {
		t.Fatalf("failed to create theme_tokens table: %v", err)
	}

	// Use the Aurora preset so field values differ from the zero-value struct.
	original := theme.Aurora()

	ctx := context.Background()
	if err := theme.Save(ctx, db, original); err != nil {
		t.Fatalf("Save() returned error: %v", err)
	}

	loaded, err := theme.Load(ctx, db)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if loaded.Name != original.Name {
		t.Errorf("Name mismatch: got %q, want %q", loaded.Name, original.Name)
	}
	if loaded.BgDark != original.BgDark {
		t.Errorf("BgDark mismatch: got %q, want %q", loaded.BgDark, original.BgDark)
	}
	if loaded.AccentLight != original.AccentLight {
		t.Errorf("AccentLight mismatch: got %q, want %q", loaded.AccentLight, original.AccentLight)
	}
	if loaded.FontSans != original.FontSans {
		t.Errorf("FontSans mismatch: got %q, want %q", loaded.FontSans, original.FontSans)
	}
	if loaded.MaxWidth != original.MaxWidth {
		t.Errorf("MaxWidth mismatch: got %q, want %q", loaded.MaxWidth, original.MaxWidth)
	}
}

// TestLoadReturnsDefaultWhenEmpty verifies that Load() returns the Default preset
// when no row exists in the database.
func TestLoadReturnsDefaultWhenEmpty(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory SQLite: %v", err)
	}
	defer db.Close()

	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS theme_tokens (
			id      INTEGER PRIMARY KEY CHECK (id = 1),
			name    TEXT NOT NULL DEFAULT 'Default',
			tokens  TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)`)
	if err != nil {
		t.Fatalf("failed to create theme_tokens table: %v", err)
	}

	loaded, err := theme.Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load() on empty table returned error: %v", err)
	}
	def := theme.Default()
	if loaded.Name != def.Name {
		t.Errorf("expected Default preset name %q, got %q", def.Name, loaded.Name)
	}
}
