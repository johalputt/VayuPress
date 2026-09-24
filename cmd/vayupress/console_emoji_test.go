// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

// isEmoji reports whether r is a pictograph that would stand in for an icon:
// the emoji planes, the Miscellaneous Symbols and Dingbats blocks, the arrow
// and star emoji outside them, and the clock and media glyphs. Typographic
// marks that read as text in any font stay allowed: ✓ ✕ ✦, geometric shapes
// (● ○ ▲), box drawing, arrows (→ ↗) and ⌘.
func isEmoji(r rune) bool {
	switch {
	case r >= 0x1F000:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return !strings.ContainsRune("✓✕✦✗✔✖❯❮", r)
	case r == 0x2B05 || r == 0x2B06 || r == 0x2B07 || r == 0x2B50 || r == 0x2B55:
		return true
	case r == 0x231A || r == 0x231B || (r >= 0x23E9 && r <= 0x23FA):
		return true
	}
	return false
}

// escapedRuneRe finds a character written as an escape rather than typed: a
// JavaScript or Go \uXXXX (two of them for a surrogate pair), a Go \UXXXXXXXX,
// or an HTML numeric entity. The link button in the mail composer was an
// emoji written as &#128279;, which a scan of typed characters never sees.
var escapedRuneRe = regexp.MustCompile(`\\u[dD][89abAB][0-9a-fA-F]{2}\\u[dD][c-fC-F][0-9a-fA-F]{2}|\\u[0-9a-fA-F]{4}|\\U[0-9a-fA-F]{8}|&#[xX][0-9a-fA-F]+;|&#[0-9]+;`)

// decodeEscapes returns line with every escaped character replaced by the
// character itself.
func decodeEscapes(line string) string {
	return escapedRuneRe.ReplaceAllStringFunc(line, func(m string) string {
		switch {
		case strings.HasPrefix(m, "&#x"), strings.HasPrefix(m, "&#X"):
			n, _ := strconv.ParseUint(m[3:len(m)-1], 16, 32)
			return string(rune(n))
		case strings.HasPrefix(m, "&#"):
			n, _ := strconv.ParseUint(m[2:len(m)-1], 10, 32)
			return string(rune(n))
		case len(m) == 12: // surrogate pair
			hi, _ := strconv.ParseUint(m[2:6], 16, 32)
			lo, _ := strconv.ParseUint(m[8:12], 16, 32)
			return string(utf16.DecodeRune(rune(hi), rune(lo)))
		}
		n, _ := strconv.ParseUint(m[2:], 16, 32)
		return string(rune(n))
	})
}

// emojiExempt are reader-facing pages, not the console: they have their own
// look and none of the console's icon set.
var emojiExempt = map[string]string{
	"handlers_member_portal.go": "the member portal, a reader's own account pages",
	"handlers_payments.go":      "the reader checkout and its receipts",
}

// emojiExemptLines are single lines outside the console inside files that are
// otherwise console code.
var emojiExemptLines = []string{
	`id="vayu-theme-toggle" class="vayu-theme-toggle au-theme-toggle"`, // handlers_team.go: the public sign-in page's own theme toggle
}

// No console page and no console script draws an emoji. The console has one
// icon set (vayuos_icons.go), which follows the colour scheme, forced colours
// and the Tor accent; an emoji follows none of them and renders differently on
// every platform. Sources are read rather than pages rendered, so a page no
// test renders is covered too.
func TestConsoleDrawsNoEmoji(t *testing.T) {
	var files []string
	for _, pattern := range []string{"*.go", "../../static/js/*.js"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 100 {
		t.Fatalf("only %d files scanned; the globs are wrong and this gate checks nothing", len(files))
	}
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".min.js") || emojiExempt[base] != "" {
			continue
		}
		src, err := os.ReadFile(f) // #nosec G304 -- walking this repository
		if err != nil {
			t.Fatal(err)
		}
	lines:
		for i, line := range strings.Split(string(src), "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "/*") {
				continue
			}
			for _, ok := range emojiExemptLines {
				if strings.Contains(line, ok) {
					continue lines
				}
			}
			for _, r := range decodeEscapes(line) {
				if isEmoji(r) {
					t.Errorf("%s:%d draws %q; use an icon from the Still Air set (saIcon, or vpIcon in a script) or plain words", base, i+1, string(r))
					break
				}
			}
		}
	}
}

func TestDecodeEscapesFindsWrittenEmoji(t *testing.T) {
	for in, want := range map[string]string{
		`x &#128279; y`:         "x 🔗 y",
		`&#x1F517;`:             "🔗",
		`'\u26a0 '`:             "'⚠ '",
		`"\uD83D\uDD17"`:        `"🔗"`,
		`"\U0001F517"`:          `"🔗"`,
		`plain \n text &amp; b`: `plain \n text &amp; b`,
	} {
		if got := decodeEscapes(in); got != want {
			t.Errorf("decodeEscapes(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsEmoji(t *testing.T) {
	for _, r := range "📌🛡⚠☀✉⬆⏳⌛" {
		if !isEmoji(r) {
			t.Errorf("%q is an emoji the console must not draw", string(r))
		}
	}
	for _, r := range "✓✕✦●○▲─→↗⌘·—" {
		if isEmoji(r) {
			t.Errorf("%q is typography, not an emoji", string(r))
		}
	}
}
