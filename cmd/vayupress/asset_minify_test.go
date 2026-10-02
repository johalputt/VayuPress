// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/johalputt/vayupress/internal/render"

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
		if got := string(render.MinifyCSS([]byte(c.in))); got != c.want {
			t.Errorf("%s: render.MinifyCSS(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

// The shipped stylesheets lose their comments and keep every rule: the
// number of blocks and declarations is the same before and after.
func TestMinifiedStylesheetsKeepEveryRule(t *testing.T) {
	comment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	src, err := os.ReadFile("../../static/css/vayuos.css") // #nosec G304 -- repository file
	if err != nil {
		t.Fatal(err)
	}
	min := string(render.MinifyCSS(src))
	plain := comment.ReplaceAllString(string(src), "")
	for _, ch := range []string{"{", "}", ";"} {
		if a, b := strings.Count(plain, ch), strings.Count(min, ch); a != b {
			t.Errorf("%d %q before minifying, %d after", a, ch, b)
		}
	}
	if strings.Contains(min, "/*") {
		t.Error("a comment survived minifying")
	}
}

// The console is SERVED the minified stylesheet: the control is the handler,
// not the function.
func TestConsoleStylesheetIsServedMinified(t *testing.T) {
	src, err := os.ReadFile("../../static/css/vayuos.css") // #nosec G304 -- repository file
	if err != nil {
		t.Fatal(err)
	}
	// Both ways the file is found: the synced copy on disk (what production
	// serves) and the copy compiled into the binary (when STATIC_DIR is missing).
	disk := t.TempDir()
	if err := os.MkdirAll(filepath.Join(disk, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disk, "css", "vayuos.css"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"from disk": disk, "from the binary": filepath.Join(disk, "absent")} {
		t.Setenv("STATIC_DIR", dir)
		w := httptest.NewRecorder()
		serveAdminOSAsset("css/vayuos.css", "text/css; charset=utf-8")(w, httptest.NewRequest(http.MethodGet, "/os/static/css/vayuos.css", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, w.Code)
		}
		if got := w.Body.String(); got != string(render.MinifyCSS(src)) {
			t.Errorf("%s: served %d bytes, which is not the minified stylesheet (%d bytes; source %d)", name, len(got), len(render.MinifyCSS(src)), len(src))
		}
	}
}

// Every console page downloads the stylesheet before it can paint, so its size
// is a budget, not an accident. The budget is on the minified bytes, the part
// the source controls. Not on gzip output: the same file compresses to
// different sizes under Go 1.26 and Go 1.27, so a gzip budget measured the
// toolchain as much as the CSS, and went red on CI's newer Go with nothing
// changed. The sheet minified to 311,232 bytes when the budget was set at
// 315,000. The P6 components then spent that headroom: the nine control
// states, the bell's groups, the command bar's preview and the site switcher,
// with 43 dead rules removed on the way. Raised to 320,000 on 2026-09-25 for
// that work. Raised to 330,000 on 2026-09-26 for the Outside services page,
// with room for what the release still adds. Raised to 350,000 on 2026-10-02
// for the Mail redesign (Mail plan, fidelity step 8): three columns, the list's
// rows and groups, the sidebar and the selection colours, 332,200 bytes after
// 61 dead .vm-* rules went, with room for the reader, the switcher, compose and
// Mail's motion still to come. Above 335,629 the budget no longer notices the
// classic sheet's decoration coming back, as it used to; nothing else does
// either, so that one is now a matter of review. A budget is discipline, not a
// limit: raise it on purpose, in this line, when a requirement needs it —
// never cut a feature to stay under it.
const consoleCSSBudget = 350_000

func TestConsoleStylesheetFitsItsBudget(t *testing.T) {
	src, err := os.ReadFile("../../static/css/vayuos.css") // #nosec G304 -- repository file
	if err != nil {
		t.Fatal(err)
	}
	if n := len(render.MinifyCSS(src)); n > consoleCSSBudget {
		t.Errorf("the console's stylesheet is %d bytes minified, over the %d-byte budget", n, consoleCSSBudget)
	}
}
