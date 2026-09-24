// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A colour a script sets after an action — a status line turning red, a QR
// code's backing — appears only after someone clicks, so the design walk in
// tests/e2e never sees it. Five pages set #ef4444 and #22c55e this way, the
// palette of a different product, behind var() names that did not exist. A
// script names a token, like the stylesheet does.
var scriptColourLiteral = regexp.MustCompile(`\.style\.(?:color|background\w*|border\w*|outline\w*|fill|stroke)\s*=\s*[^;\n]*(?:#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\()`)

// A token read with a colour fallback (var(--color-danger,#ef4444)) shows the
// fallback whenever the name is wrong, and three of them were: the console's
// tokens are always defined, so a fallback only ever hides a misspelling.
var tokenWithColourFallback = regexp.MustCompile(`var\(--[\w-]+\s*,\s*(?:#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\()`)

func TestConsoleScriptsSetOnlyTokenColours(t *testing.T) {
	var files []string
	goFiles, _ := filepath.Glob("*.go")
	jsFiles, _ := filepath.Glob(filepath.Join("..", "..", "static", "js", "*.js"))
	files = append(files, goFiles...)
	files = append(files, jsFiles...)
	files = append(files, filepath.Join("..", "..", "static", "css", "vayuos.css"))
	checked := 0
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasSuffix(base, "_test.go") || vendorJS[base] {
			continue
		}
		b, err := os.ReadFile(f) // #nosec G304 -- this repository's own sources
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, m := range scriptColourLiteral.FindAllString(string(b), -1) {
			t.Errorf("%s: a script sets a colour that is not a token: %s", base, m)
		}
		for _, m := range tokenWithColourFallback.FindAllString(string(b), -1) {
			t.Errorf("%s: a token is read with a colour fallback, which only shows when the name is wrong: %s", base, m)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d files checked — the scan is not looking in the right place", checked)
	}
}
