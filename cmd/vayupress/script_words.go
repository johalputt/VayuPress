// SPDX-License-Identifier: Apache-2.0

package main

// script_words.go — the sentences the console's scripts say, in the page's
// language. A script asks for one with vpT('…') (static/js/admin-os.js); the
// sentences are read once from the scripts compiled into the binary, and a
// console page in a language other than English carries that language's
// versions in #vp-words, a JSON block the script reads. The page is the
// carrier rather than a file of its own because it already knows its
// language, and a script asking for its words would answer its first
// message before they arrived.

import (
	"encoding/json"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/johalputt/vayupress/internal/i18n"
)

// vpTCall is a vpT call with its sentence as a single-quoted literal, the
// only form the console's scripts use for it.
var vpTCall = regexp.MustCompile(`vpT\(\s*'((?:[^'\\]|\\.)*)'`)

// scriptSentences is every sentence the console's scripts pass to vpT,
// unquoted, sorted.
var scriptSentences = sync.OnceValue(func() []string {
	seen := map[string]bool{}
	_ = fs.WalkDir(embeddedStaticFS, "js", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return nil
		}
		b, err := fs.ReadFile(embeddedStaticFS, p)
		if err != nil {
			return nil
		}
		for _, m := range vpTCall.FindAllStringSubmatch(string(b), -1) {
			seen[strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(m[1])] = true
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
})

// scriptWords is the #vp-words block for a page in lang: "" in English, and
// a sentence the catalogue lacks is left out, so vpT says it in English.
// json.Marshal writes <, > and & as < and the like, so no sentence can
// close the block.
func scriptWords(lang string) string {
	if lang == "" || lang == "en" {
		return ""
	}
	words := map[string]string{}
	for _, s := range scriptSentences() {
		if tr := i18n.Translation(lang, s); tr != "" {
			words[s] = tr
		}
	}
	if len(words) == 0 {
		return ""
	}
	b, err := json.Marshal(words)
	if err != nil {
		return ""
	}
	return `<script type="application/json" id="vp-words">` + string(b) + `</script>`
}

// withScriptWords puts lang's #vp-words into a whole page, before </body>; a
// fragment (an htmx swap) has none and is returned as it is, so a page never
// carries two.
func withScriptWords(body, lang string) string {
	i := strings.LastIndex(body, "</body>")
	if i < 0 {
		return body
	}
	words := scriptWords(lang)
	if words == "" {
		return body
	}
	return body[:i] + words + body[i:]
}
