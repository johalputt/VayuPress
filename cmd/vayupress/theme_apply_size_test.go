package main

import (
	"encoding/json"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// themeApplyBodyCap mirrors the MaxBytesReader limit on POST /os/api/theme/apply
// (handleThemeApply). Keep these in sync.
const themeApplyBodyCap = 512 * 1024

// TestThemeApplyPayloadFitsCap guards against the "request body too large" bug:
// applying a customized theme sends the full token set INCLUDING the per-theme
// component CSS (custom_css), and the richest design themes are ~33 KB. This
// asserts every preset's apply payload stays well under the endpoint cap (and
// documents why the old 32 KB cap broke Apply for Apex/Agora).
func TestThemeApplyPayloadFitsCap(t *testing.T) {
	for _, p := range theme.AllPresets() {
		body, err := json.Marshal(map[string]any{"tokens": p})
		if err != nil {
			t.Fatalf("%s: marshal failed: %v", p.Name, err)
		}
		if len(body) >= themeApplyBodyCap {
			t.Errorf("%s apply payload is %d bytes, at/over the %d cap — raise the "+
				"MaxBytesReader limit in handleThemeApply/handleThemePreview", p.Name, len(body), themeApplyBodyCap)
		}
	}
}
