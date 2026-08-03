// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every inline <script> this codebase emits must PARSE.
//
// This gate exists because a broken one shipped and stayed broken for ten
// releases. Three handlers were removed by text surgery when their markup was
// retired, and each deletion stopped at the first `});` after its marker — an
// INNER closer, inside `.then(function(res){…});`. Every deletion left its tail,
// and the script ended with orphan `});` lines.
//
// A JavaScript parse error binds NOTHING. Provision now, Issue login, Save
// allowance, sync, disable and remove were all inert on every site console:
// buttons that looked live, reported nothing, and did nothing. An operator
// pressing them saw exactly what they would see if the server were ignoring
// them — which sent a certificate investigation through five releases looking at
// systemd, at DNS, at a stale timestamp, at everything except the button.
//
// Nothing else here could catch it. Go compiles a broken script perfectly. The
// CSP nonce was correct. assertCSPSafe passed. Every test asserting on this
// page's markup passed, because the markup was fine — it was the script that
// could not run. "It compiled and the tests passed" has never been evidence that
// a page works, and this is the sharpest example the repo has.

var (
	// Every opening script tag; which of them to PARSE is decided below, because
	// Go's RE2 has no lookahead and expressing it in the pattern is not possible.
	inlineScriptOpen = regexp.MustCompile(`<script[^>]*>`)
	goConcatInString = regexp.MustCompile("(?s)`\\s*\\+\\s*.*?\\s*\\+\\s*`")
)

// stripGoCommentsAndKeepStrings blanks Go comments so a `<script>` mentioned in
// prose is never mistaken for one that ships. Several files describe their own
// CSP posture in a comment that contains the literal tag.
func stripGoCommentsAndKeepStrings(src string) string {
	var out strings.Builder
	for i, n := 0, len(src); i < n; {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				j = n - i
			}
			out.WriteString(strings.Repeat(" ", j))
			i += j
		case strings.HasPrefix(src[i:], "/*"):
			end := n
			if j := strings.Index(src[i+2:], "*/"); j >= 0 {
				end = i + j + 4
			}
			out.WriteString(strings.Repeat(" ", end-i))
			i = end
		case src[i] == '`':
			end := n
			if j := strings.IndexByte(src[i+1:], '`'); j >= 0 {
				end = i + j + 2
			}
			out.WriteString(src[i:end])
			i = end
		default:
			out.WriteByte(src[i])
			i++
		}
	}
	return out.String()
}

func TestEveryInlineScriptParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; this gate needs a JS parser")
	}
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	dir := t.TempDir()
	checked := 0
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			continue
		}
		src := stripGoCommentsAndKeepStrings(string(raw))
		for _, m := range inlineScriptOpen.FindAllStringIndex(src, -1) {
			tag := src[m[0]:m[1]]
			// An external file is not ours to parse, and a JSON payload is data:
			// `type="application/json"` blocks are hydration state, not
			// JavaScript, and feeding them to a JS parser reports failures that
			// are not failures.
			if strings.Contains(tag, "src=") || strings.Contains(tag, `application/json`) {
				continue
			}
			close := strings.Index(src[m[1]:], "</script>")
			if close < 0 {
				continue
			}
			js := src[m[1] : m[1]+close]
			// Go string concatenation inside the literal: ` + expr + ` becomes a
			// harmless placeholder so the surrounding JavaScript still parses.
			js = goConcatInString.ReplaceAllString(js, `"X"`)
			js = strings.ReplaceAll(js, "`", "")
			if strings.TrimSpace(js) == "" {
				continue
			}
			checked++
			f := filepath.Join(dir, "s.js")
			if err := os.WriteFile(f, []byte(js), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			out, err := exec.Command(node, "--check", f).CombinedOutput() // #nosec G204 -- fixed binary, temp file
			if err != nil {
				line := strings.Count(src[:m[0]], "\n") + 1
				t.Errorf("%s:%d — the inline script does not PARSE, so every handler in it is "+
					"inert: buttons render, look live, and do nothing.\n%s",
					path, line, strings.TrimSpace(string(out)))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no inline scripts were checked, so this gate is asserting nothing")
	}
	t.Logf("inline scripts parsed: %d", checked)
}
