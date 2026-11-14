// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// i18nMovedScripts is every console script whose words a person reads have
// moved into vpT. From then on an English sentence written beside vpT fails
// TestAMovedScriptHasNoEnglishOutsideVpT, as a moved page's does beside T.
var i18nMovedScripts = []string{"admin-os-drive.js"}

var (
	// jsLiteral is a quoted string on one line.
	jsLiteral = regexp.MustCompile(`'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"`)
	// jsComment is a comment: a block, or // after a space or at a line's
	// start (not the // of an address inside a string).
	jsComment = regexp.MustCompile(`/\*[\s\S]*?\*/|(?m)(?:^|\s)//[^\n]*$`)
	// vpTGiven is a vpT call with the values it fills its sentence with.
	vpTGiven = regexp.MustCompile(`vpT\(\s*'((?:[^'\\]|\\.)*)'\s*(?:,\s*\{([^}]*)\})?`)
	jsKey    = regexp.MustCompile(`(\w+)\s*:`)
)

// TestAMovedScriptHasNoEnglishOutsideVpT — a script moved into vpT stays
// moved: an English sentence written beside it would be said in English in
// every language.
func TestAMovedScriptHasNoEnglishOutsideVpT(t *testing.T) {
	for _, name := range i18nMovedScripts {
		b, err := fs.ReadFile(embeddedStaticFS, "js/"+name)
		if err != nil {
			t.Fatal(err)
		}
		src := jsComment.ReplaceAllString(string(b), "")
		for _, m := range jsLiteral.FindAllStringSubmatchIndex(src, -1) {
			if strings.HasSuffix(strings.TrimRight(src[:m[0]], " \t\n"), "vpT(") {
				continue
			}
			lit := ""
			if m[2] >= 0 {
				lit = src[m[2]:m[3]]
			} else {
				lit = src[m[4]:m[5]]
			}
			if eng := i18nEnglishIn(lit); eng != "" {
				t.Errorf("%s: English outside vpT: %q", name, eng)
			}
		}
	}
}

// TestEveryVpTSentenceIsGivenItsValues — a placeholder a script's sentence
// names and its call gives no value for is written on the page empty.
func TestEveryVpTSentenceIsGivenItsValues(t *testing.T) {
	_ = fs.WalkDir(embeddedStaticFS, "js", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return nil
		}
		b, _ := fs.ReadFile(embeddedStaticFS, p)
		for _, m := range vpTGiven.FindAllStringSubmatch(string(b), -1) {
			given := map[string]bool{}
			for _, k := range jsKey.FindAllStringSubmatch(m[2], -1) {
				given[k[1]] = true
			}
			for _, name := range sentencePlaceholders(strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(m[1])) {
				if !given[name] {
					t.Errorf("%s: vpT(%q) names {%s} and is given no value for it", p, m[1], name)
				}
			}
		}
		return nil
	})
}

// A console page in German carries the scripts' sentences in German, once,
// at the end of the page; an English page and a fragment carry none.
func TestAConsolePageCarriesItsScriptsWords(t *testing.T) {
	found := false
	for _, s := range scriptSentences() {
		if s == "Uploading {file}…" {
			found = true
		}
	}
	if !found {
		t.Fatal("Drive's script's sentences are not among the scripts' sentences")
	}
	words := scriptWords("de")
	if !strings.Contains(words, `"Uploading {file}…":"{file} wird hochgeladen…"`) {
		t.Errorf("the German block has no German upload sentence: %.200s", words)
	}
	if strings.Count(words, "</") != 1 {
		t.Error("something in the block closes it early")
	}
	if scriptWords("en") != "" {
		t.Error("an English page carries a block of words it does not need")
	}
	page := "<html><body><main>…</main></body></html>"
	if got := withScriptWords(page, "de"); !strings.Contains(got, `<script type="application/json" id="vp-words">`) || !strings.HasSuffix(got, "</script></body></html>") {
		t.Errorf("the block is not at the end of the page: %.120s", got)
	}
	if frag := "<p>a swapped part</p>"; withScriptWords(frag, "de") != frag {
		t.Error("a fragment was given a block, so a page could carry two")
	}
}
