// SPDX-License-Identifier: Apache-2.0

package markdown

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
)

// update rewrites the goldens from the renderers as they are now. Run it only
// to accept a difference someone has read: `go test ./internal/markdown -update`.
var update = flag.Bool("update", false, "rewrite testdata/golden from the current renderers")

// Every renderer, over a frozen corpus (five of the project's own documents,
// copied so editing a doc cannot move this, and a file of edge cases for each
// extension in use), must produce the HTML in testdata/golden byte for byte.
// The goldens were written by goldmark v1.8.6. A goldmark upgrade, or a
// change to a renderer's options, shows up here as a readable diff, file by
// file, before it shows up on anyone's site.
func TestEveryRendererProducesItsGoldenHTML(t *testing.T) {
	renderers := map[string]goldmark.Markdown{"document": Document, "mail": Mail, "inline": Inline, "block": Block}
	inputs, err := filepath.Glob("testdata/corpus/*.markdown")
	if err != nil || len(inputs) < 6 {
		t.Fatalf("corpus: %v %v", inputs, err)
	}
	for name, md := range renderers {
		for _, in := range inputs {
			src, err := os.ReadFile(in) // #nosec G304 -- test corpus
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			if err := md.Convert(src, &got); err != nil {
				t.Fatalf("%s %s: %v", name, in, err)
			}
			golden := filepath.Join("testdata", "golden", name, strings.TrimSuffix(filepath.Base(in), ".markdown")+".html")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden) // #nosec G304 -- test golden
			if err != nil {
				t.Fatalf("%s: no golden (%v); write one with -update", golden, err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("%s renders %s differently from %s, first at: %s", name, filepath.Base(in), golden, firstDifference(got.Bytes(), want))
			}
		}
	}
}

// firstDifference quotes both sides around the first differing byte.
func firstDifference(got, want []byte) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(0, i-40)
	return "got …" + string(got[from:min(len(got), i+40)]) + "… want …" + string(want[from:min(len(want), i+40)]) + "…"
}
