// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Each case is one rule of the minifier: what it may remove, and the whitespace
// CSS gives meaning to, which it must keep.
func TestMinifyCSSKeepsWhatCSSMeans(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"comments go":                         {"a { color: red; /* why */ }", "a{color: red;}"},
		"whitespace around braces":            {"a  {\n  color: red ;\n}\n\nb { x: y }", "a{color: red;}b{x: y}"},
		"a descendant pseudo keeps its space": {"a :hover { x: y }", "a :hover{x: y}"},
		"calc keeps its operators":            {"a { width: calc(100% - 2px + 1em) }", "a{width: calc(100% - 2px + 1em)}"},
		"strings are untouched":               {`a::before { content: "  /* not a comment */  "; }`, `a::before{content: "  /* not a comment */  ";}`},
		"escaped quote in a string":           {`a { content: "a\"  b" }`, `a{content: "a\"  b"}`},
		"a comment separates tokens":          {"a/**/b { x: y }", "a b{x: y}"},
		"selector lists":                      {"a ,\n b { x: y }", "a,b{x: y}"},
	} {
		if got := string(minifyCSS([]byte(c.in))); got != c.want {
			t.Errorf("%s: minifyCSS(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

// The shipped stylesheets lose their comments and keep every rule: the
// number of blocks and declarations is the same before and after.
func TestMinifiedStylesheetsKeepEveryRule(t *testing.T) {
	comment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	for _, f := range []string{"../../static/css/admin-os.css", "../../static/css/vayuos.css"} {
		src, err := os.ReadFile(f) // #nosec G304 -- repository file
		if err != nil {
			t.Fatal(err)
		}
		min := string(minifyCSS(src))
		plain := comment.ReplaceAllString(string(src), "")
		for _, ch := range []string{"{", "}", ";"} {
			if a, b := strings.Count(plain, ch), strings.Count(min, ch); a != b {
				t.Errorf("%s: %d %q before minifying, %d after", f, a, ch, b)
			}
		}
		if strings.Contains(min, "/*") {
			t.Errorf("%s: a comment survived minifying", f)
		}
	}
}

// The console is SERVED the minified stylesheet: the control is the handler,
// not the function.
func TestConsoleStylesheetIsServedMinified(t *testing.T) {
	src, err := os.ReadFile("../../static/css/admin-os.css") // #nosec G304 -- repository file
	if err != nil {
		t.Fatal(err)
	}
	// Both ways the file is found: the synced copy on disk (what production
	// serves) and the copy compiled into the binary (when STATIC_DIR is missing).
	disk := t.TempDir()
	if err := os.MkdirAll(filepath.Join(disk, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disk, "css", "admin-os.css"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"from disk": disk, "from the binary": filepath.Join(disk, "absent")} {
		t.Setenv("STATIC_DIR", dir)
		w := httptest.NewRecorder()
		serveAdminOSAsset("css/admin-os.css", "text/css; charset=utf-8")(w, httptest.NewRequest(http.MethodGet, "/os/static/css/admin-os.css", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, w.Code)
		}
		if got := w.Body.String(); got != string(minifyCSS(src)) {
			t.Errorf("%s: served %d bytes, which is not the minified stylesheet (%d bytes; source %d)", name, len(got), len(minifyCSS(src)), len(src))
		}
	}
}

// Every console page downloads both stylesheets before it can paint, so their
// size is a budget, not an accident. The figure is what the wire carries: the
// minified file, gzipped the way gzipMiddleware does it. The budget sits about
// 1.5 KB above today's 53 KB: ordinary work fits, and the rules the classic
// console left behind (2 KB of selectors nothing renders, removed with it)
// would not fit if they came back. Raise it on purpose, in this line.
const consoleCSSBudget = 54<<10 + 512

func TestConsoleStylesheetsFitTheirBudget(t *testing.T) {
	total := 0
	for _, f := range []string{"../../static/css/admin-os.css", "../../static/css/vayuos.css"} {
		src, err := os.ReadFile(f) // #nosec G304 -- repository file
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(minifyCSS(src)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d bytes on the wire", filepath.Base(f), buf.Len())
		total += buf.Len()
	}
	if total > consoleCSSBudget {
		t.Errorf("the console's stylesheets are %d bytes gzipped, over the %d-byte budget", total, consoleCSSBudget)
	}
}
