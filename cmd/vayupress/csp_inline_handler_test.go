// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// inlineHandlerRe finds an HTML event-handler attribute (onclick="…",
// onchange='…'). The console's CSP allows scripts by nonce only, so the browser
// refuses every such handler: the control renders, is clicked, and does nothing
// but file a CSP report. Five shipped that way (mode transitions, fault
// simulation, dead-letter replay).
var inlineHandlerRe = regexp.MustCompile(`\son[a-z]{4,}\s*=\s*["'\\]`)

// No console markup and no console script builds an inline event handler.
// Sources are read rather than pages rendered, so a page nobody remembered to
// render in a test is covered too.
func TestNoInlineEventHandlerReachesTheConsole(t *testing.T) {
	var files []string
	for _, pattern := range []string{"*.go", "../../static/js/*.js", "../../internal/render/*.go"} {
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
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".min.js") {
			continue
		}
		src, err := os.ReadFile(f) // #nosec G304 -- walking this repository
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(src), "\n") {
			if m := inlineHandlerRe.FindString(line); m != "" {
				t.Errorf("%s:%d builds an inline event handler (%s), which the console's CSP refuses; bind it from a nonce'd or same-origin script instead",
					filepath.Base(f), i+1, strings.TrimSpace(m))
			}
		}
	}
}
