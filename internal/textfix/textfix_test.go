// SPDX-License-Identifier: Apache-2.0

package textfix

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// layers garbles s n times the way it happened: UTF-8 bytes read as
// Windows-1252 (or Latin-1) and stored again. Built from x/text's table, not
// from Repair's, so a wrong entry in Repair's cannot agree with it.
func layers(t *testing.T, s string, n int, latin1 bool) string {
	t.Helper()
	for i := 0; i < n; i++ {
		var b strings.Builder
		for _, c := range []byte(s) {
			r := rune(c)
			// Windows keeps the five bytes Windows-1252 leaves undefined as
			// the C1 controls of the same number; the table gives U+FFFD.
			if !latin1 {
				if w := charmap.Windows1252.DecodeByte(c); w != utf8.RuneError {
					r = w
				}
			}
			b.WriteRune(r)
		}
		s = b.String()
	}
	return s
}

// Every layer count, through either table, comes back to what was written.
func TestLayersAreUndone(t *testing.T) {
	for _, want := range []string{"allocation → triggers", "a — b", "it’s “quoted”", "naïve café", "€5 • 3×4 ≠ 11", "ship it 🚀", "東京"} {
		for n := 1; n <= 4; n++ {
			for _, latin1 := range []bool{false, true} {
				in := "<p>" + layers(t, want, n, latin1) + "</p>"
				got, res := Repair(in)
				if got != "<p>"+want+"</p>" || res.Lost != 0 || res.Restored == 0 {
					t.Errorf("%q in %d layers (latin1 %v): %q %+v", want, n, latin1, got, res)
				}
			}
		}
	}
}

// What cannot be restored is left exactly as it is and counted: the run on
// johal.in lost the bytes after the first of its character.
func TestALostCharacterIsLeftAndCounted(t *testing.T) {
	for _, in := range []string{
		"metabolism ÃÃÃÃÃÂ¢ triggers",                                         // as the page shows it
		"metabolism Ã\u0083Ã\u0083Ã\u0083Ã\u0083Ã\u0083\u00c2\u00a2 triggers", // with the C1 controls kept
	} {
		got, res := Repair(in)
		if got != in || res.Lost != 1 || res.Restored != 0 {
			t.Errorf("%q: %q %+v", in, got, res)
		}
	}
}

// Text that is not garbled is never changed: accented words, symbols written
// as such, and garbled text quoted inside code, which a post about encodings
// shows on purpose.
func TestCleanTextAndCodeAreLeft(t *testing.T) {
	for _, in := range []string{
		"São Paulo, Zürich, déjà vu — £5 · ©2026 Ã", // a lone Ã, as in a heading's initial
		"<pre>mojibake: \u00c3\u00a9</pre> and <code>\u00e2\u20ac\u201d</code>",
		"plain ascii only",
		"il a dit «un café»", // é before » looks like a layer and is not one
	} {
		if got, res := Repair(in); got != in || res != (Result{}) {
			t.Errorf("%q changed: %q %+v", in, got, res)
		}
	}
	got, _ := Repair("<code>\u00c3\u00a9</code> then \u00c3\u00a9")
	if got != "<code>\u00c3\u00a9</code> then é" {
		t.Errorf("only the text outside code is repaired: %q", got)
	}
}
