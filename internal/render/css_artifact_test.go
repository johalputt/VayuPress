// SPDX-License-Identifier: Apache-2.0

package render

import (
	"os"
	"path/filepath"
	"testing"
)

// Four stylesheets under static/css/ are NOT source files. WriteCSSAssets
// rewrites each one from a Go const at every boot, so a rule typed into the
// static file is reverted the next time the process starts — a fix that reaches
// nobody, which is exactly the failure mode the repo's standing rule about
// putting fixes in the binary is there to prevent.
//
// Editing one of these means editing the const in render.go. This test exists
// because the two copies can drift silently and the drift only shows up as a
// style that works locally and vanishes in production.
func TestGeneratedCSSArtifactsMatchTheirConsts(t *testing.T) {
	dir := t.TempDir()
	WriteCSSAssets(dir)

	for _, name := range []string{"article.css", "admin.css", "high-contrast.css", "custom.css"} {
		got, err := os.ReadFile(filepath.Join(dir, "css", name))
		if err != nil {
			t.Errorf("%s: WriteCSSAssets emitted nothing: %v", name, err)
			continue
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "static", "css", name))
		if err != nil {
			t.Errorf("%s: missing from static/css: %v", name, err)
			continue
		}
		if string(got) == string(want) {
			continue
		}
		t.Errorf("static/css/%s (%d bytes) does not match what the binary writes (%d bytes). "+
			"This file is generated — edit the corresponding const in internal/render/render.go, "+
			"then regenerate. A rule added to the static file alone is erased at the next boot.",
			name, len(want), len(got))
	}
}
