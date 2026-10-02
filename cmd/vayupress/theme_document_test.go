// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Theme is a document: the live preview fills the page, and the Studio is the
// inspector beside it, its groups four tabs (plan §3: "tabs replace the
// chips"). One tab shows at a time, each wired to its panel for assistive
// technology, and none of the accordion, its chips or its jump bar is left.
func TestTheThemeIsADocument(t *testing.T) {
	rec := httptest.NewRecorder()
	(&App{}).handleOSTheme(rec, httptest.NewRequest(http.MethodGet, "/os/theme", nil))
	page := rec.Body.String()
	for _, want := range []string{`<div class="sa-doc" data-page-kind="document" data-theme-studio>`, `<h1 class="sa-doc__title">Theme</h1>`,
		`<aside class="sa-doc__inspector" aria-label="Theme settings">`, `data-theme-frame`, `role="tablist"`} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	for i, key := range []string{"design", "colour", "layout", "advanced"} {
		tab := regexp.MustCompile(`<button [^>]*role="tab" id="tt-` + key + `" aria-controls="tp-` + key + `" aria-selected="(true|false)"`).FindStringSubmatch(page)
		panel := regexp.MustCompile(`<div class="sa-itabs__panel" role="tabpanel" id="tp-` + key + `" aria-labelledby="tt-` + key + `" data-theme-panel="` + key + `"( hidden)?>`).FindStringSubmatch(page)
		if tab == nil || panel == nil {
			t.Errorf("%s: no tab and panel wired to each other", key)
			continue
		}
		if first := i == 0; (tab[1] == "true") != first || (panel[1] == "") != first {
			t.Errorf("%s: selected=%s hidden=%q; only the first tab shows at first", key, tab[1], panel[1])
		}
	}
	for _, gone := range []string{"cz-group", "cz-chip", "cz-sec", "data-studio-mode", "customizer__panel"} {
		if strings.Contains(page, gone) {
			t.Errorf("the old Studio's %q is still drawn", gone)
		}
	}
	// Every element the Studio's script looks up is on the page: a lost hook
	// turns its control off without an error. Read from the script.
	src, err := os.ReadFile("../../static/js/admin-os-theme.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	var missing []string
	n := 0
	for _, m := range regexp.MustCompile(`querySelector(?:All)?\('\[(data-[a-z-]+)\]'\)`).FindAllStringSubmatch(js, -1) {
		n++
		if !strings.Contains(page, " "+m[1]) {
			missing = append(missing, "["+m[1]+"]")
		}
	}
	for _, m := range regexp.MustCompile(`getElementById\('([a-z-]+)'\)`).FindAllStringSubmatch(js, -1) {
		n++
		if !strings.Contains(page, `id="`+m[1]+`"`) {
			missing = append(missing, "#"+m[1])
		}
	}
	if n < 30 {
		t.Fatalf("read %d hooks from the script; the patterns no longer match it", n)
	}
	// The nav rows and the a11y swatches are built by the script itself.
	for _, m := range missing {
		if m != "[data-nav-row]" && m != "[data-nav-label]" && m != "[data-nav-href]" {
			t.Errorf("the script looks up %s and the page has none", m)
		}
	}
}
