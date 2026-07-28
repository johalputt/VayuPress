// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The console CSS shipped @font-face rules for three files that existed nowhere —
// not in the repository, not in any binary, not on any install. Every console page
// load fired three 404s and fell through to the system font stack, so it LOOKED
// right and nothing in the build or the test suite noticed.
//
// It survived because a missing font is invisible: the browser silently uses the
// next family in the stack. That is exactly the class of defect a build gate is
// for, since no amount of looking at the panel reveals it.

var cssFontURL = regexp.MustCompile(`url\("([^"]+\.woff2?)"\)`)

func TestConsoleCSSFontsAreActuallyServed(t *testing.T) {
	css, err := os.ReadFile("../../static/css/admin-os.css")
	if err != nil {
		t.Fatalf("read admin-os.css: %v", err)
	}
	matches := cssFontURL.FindAllStringSubmatch(string(css), -1)
	if len(matches) == 0 {
		t.Skip("no @font-face rules in the console CSS")
	}
	for _, m := range matches {
		ref := m[1]
		switch {
		// Served by handleStaticFont from the embedded FS. The allowlist there is
		// the authority on what actually exists, so check against it rather than
		// against the filesystem — a file present in the repo but absent from the
		// allowlist still 404s.
		case strings.HasPrefix(ref, "/static/fonts/"):
			name := strings.TrimPrefix(ref, "/static/fonts/")
			if !fontAllowlist[name] {
				t.Errorf("admin-os.css requests %s, which handleStaticFont does not allowlist — it will 404", ref)
			}
		// This prefix is what broke: it was served from STATIC_DIR on disk with no
		// embedded fallback, so a binary-only update left it 404ing forever.
		case strings.HasPrefix(ref, "/os/static/fonts/"):
			t.Errorf("admin-os.css requests %s, but /os serves no fonts — use /static/fonts/ "+
				"(embedded in the binary) so a binary-only update cannot break it", ref)
		default:
			t.Errorf("admin-os.css requests %s from an unrecognised path; verify a route serves it", ref)
		}
	}
}

// TestConsoleFontStacksHaveSystemFallbacks — every family the console names must
// degrade to something real. Inter and JetBrains Mono are deliberately NOT shipped
// (adding ~200 KB of typefaces to a single-binary product to restyle an admin
// panel is the wrong trade), so the stacks behind them have to carry the design.
func TestConsoleFontStacksHaveSystemFallbacks(t *testing.T) {
	css, err := os.ReadFile("../../static/css/admin-os.css")
	if err != nil {
		t.Fatalf("read admin-os.css: %v", err)
	}
	for _, v := range []struct{ name, fallback string }{
		{"--font-head", "system-ui"},
		{"--font-body", "system-ui"},
		{"--font-mono", "ui-monospace"},
	} {
		i := strings.Index(string(css), v.name+":")
		if i < 0 {
			t.Errorf("%s is no longer defined", v.name)
			continue
		}
		line := string(css)[i:]
		if j := strings.Index(line, ";"); j > 0 {
			line = line[:j]
		}
		if !strings.Contains(line, v.fallback) {
			t.Errorf("%s has no %s fallback (%q) — an unshipped family would leave it to the browser default",
				v.name, v.fallback, line)
		}
	}
}
