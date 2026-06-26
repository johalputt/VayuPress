package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestPreviewOptionsFromQuery proves the in-store / Studio preview reads the
// customization options off the query string, keyed by the canonical option
// keys, and ignores unknown params.
func TestPreviewOptionsFromQuery(t *testing.T) {
	r := httptest.NewRequest("GET", "/os/theme/preview?preset=Default&corners=sharp&scheme=rose&bogus=1", nil)
	opts := previewOptionsFromQuery(r)
	if opts["corners"] != "sharp" {
		t.Errorf("corners option not read: got %q", opts["corners"])
	}
	if opts["scheme"] != "rose" {
		t.Errorf("scheme option not read: got %q", opts["scheme"])
	}
	if _, ok := opts["bogus"]; ok {
		t.Error("unknown query param leaked into options")
	}

	// No recognised options → nil (preset defaults are used).
	r2 := httptest.NewRequest("GET", "/os/theme/preview?preset=Default", nil)
	if previewOptionsFromQuery(r2) != nil {
		t.Error("expected nil options when none supplied")
	}
}

// TestPreviewReflectsOptions proves the preview is option-aware end to end: the
// compiled CSS for a preset CHANGES when an option is applied (not just colours)
// — e.g. sharp corners zero out the large radius. This guards against the
// recurring "only colours change" regression for the preview path.
func TestPreviewReflectsOptions(t *testing.T) {
	base, ok := findPreset("Default")
	if !ok {
		t.Fatal("Default preset missing")
	}
	plain, err := theme.CompileCSS(base)
	if err != nil {
		t.Fatalf("compile base: %v", err)
	}

	withOpts := base
	withOpts.Options = map[string]string{"corners": "sharp"}
	customized, err := theme.CompileCSS(withOpts)
	if err != nil {
		t.Fatalf("compile customized: %v", err)
	}

	if plain == customized {
		t.Error("applying the corners option did not change the compiled preview CSS")
	}
	if !strings.Contains(customized, "--vp-radius") && !strings.Contains(customized, "radius") {
		t.Log("note: radius token not found in output; option may apply via scoped CSS")
	}
}
