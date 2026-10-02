// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The editor is the document kind (render 02): a bar with the way back to the
// posts, the post, and the inspector laid open beside it, not a drawer behind
// a button. The bar carries what every visit uses; the rest is one menu away.
func TestTheEditorIsADocument(t *testing.T) {
	out := osEditorBody("hello", "Hello", "[]", `<option value="u1" selected>Ann</option>`, false)
	for _, want := range []string{`data-page-kind="document"`, `class="sa-doc__back" href="/os/posts">`, `</svg>Posts</a>`,
		`class="sa-doc__inspector" data-editor-settings aria-label="Post settings">`,
		`>Publish</h2>`, `>Tags</h2>`, `>In search results</h2>`, `>Cover image</h2>`, `data-editor-byline`, `data-editor-scroller`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The inspector is part of the page, so it is neither hidden at first
	// nor a modal dialog over the writing.
	insp := out[strings.Index(out, `<aside class="sa-doc__inspector`):]
	insp = insp[:strings.Index(insp, ">")]
	for _, not := range []string{"hidden", `role="dialog"`, "aria-modal"} {
		if strings.Contains(insp, not) {
			t.Errorf("the inspector carries %q: %s", not, insp)
		}
	}
	for _, gone := range []string{`class="editor-topbar`, `class="editor-sidebar`, "data-editor-stats", "data-editor-settings-close"} {
		if strings.Contains(out, gone) {
			t.Errorf("the old chrome %q is still drawn", gone)
		}
	}
	// The bar: at most Preview, the inspector's toggle, Save and Publish as
	// buttons of its own, with everything else in More.
	bar := out[strings.Index(out, `<div class="sa-doc__bar">`):strings.Index(out, `<div class="sa-doc__body">`)]
	menu := bar[strings.Index(bar, `<details class="sa-pop editor-more">`):strings.Index(bar, `</details>`)]
	if n := strings.Count(strings.Replace(bar, menu, "", 1), `<button`); n != 4 {
		t.Errorf("the bar has %d buttons of its own, want 4 (Preview, inspector, Save, Publish)", n)
	}
	for _, hook := range []string{"data-editor-focus-btn", "data-editor-split-btn", "data-editor-md-btn", "data-editor-html-btn",
		"data-editor-image-btn", "data-editor-ai-btn", "data-editor-share-btn", "data-editor-undo", "data-editor-redo",
		"data-editor-history-btn", "data-editor-newpage"} {
		if !strings.Contains(menu, hook) {
			t.Errorf("More does not hold %s", hook)
		}
	}

	page := osEditorBody("about", "About", "[]", "", true)
	if !strings.Contains(page, `class="sa-doc__back" href="/os/pages">`) || !strings.Contains(page, `</svg>Pages</a>`) {
		t.Error("a page's editor does not lead back to the pages")
	}
}

// Every element the editor script looks up by a data hook is on the page. The
// script guards each lookup, so a hook the markup lost turns its control off
// without an error anywhere: that is how a redesign drops a feature unseen.
// The hooks are read from the script itself, not listed here, so a new one is
// held to this too.
func TestEveryHookTheEditorScriptUsesIsOnThePage(t *testing.T) {
	src, err := os.ReadFile("../../static/js/admin-os-editor.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	hooks := map[string]bool{}
	for _, m := range regexp.MustCompile(`querySelector\('\[(data-(?:editor|seo|ai)[a-z-]*)\]'\)`).FindAllStringSubmatch(js, -1) {
		hooks[m[1]] = true
	}
	// The settings fields are looked up from a list of names: data-pm-<name>.
	list := regexp.MustCompile(`(?s)var pm = \{\};\s*\[(.*?)\]\.forEach`).FindStringSubmatch(js)
	if list == nil {
		t.Fatal("the script's list of settings fields was not found")
	}
	for _, m := range regexp.MustCompile(`'([a-z-]+)'`).FindAllStringSubmatch(list[1], -1) {
		hooks["data-pm-"+m[1]] = true
	}
	if len(hooks) < 40 {
		t.Fatalf("read only %d hooks from the script; the pattern no longer matches it", len(hooks))
	}
	out := osEditorBody("hello", "Hello", "[]", "", false)
	for h := range hooks {
		if !regexp.MustCompile(`[\s"]` + regexp.QuoteMeta(h) + `[\s>="]`).MatchString(out) {
			t.Errorf("the script looks up [%s] and the page has none", h)
		}
	}
}

// The editor has no app sidebar: its bar leads back, and the room is the
// writing's. Every other Content page keeps it.
func TestTheEditorHasNoAppSidebar(t *testing.T) {
	if out := stillAirShellHead("n", "Edit Post", "editor", saSession(accessAdmin)); strings.Contains(out, `class="sa-appside"`) {
		t.Error("the editor draws the Content sidebar")
	}
	if out := stillAirShellHead("n", "Posts", "posts", saSession(accessAdmin)); !strings.Contains(out, `class="sa-appside"`) {
		t.Error("Posts lost the Content sidebar")
	}
}
