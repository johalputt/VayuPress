// SPDX-License-Identifier: Apache-2.0

package main

// script_words.go — the sentences the console's scripts say, in the page's
// language. A script asks for one with vpT('…') (vpTScript, below); the
// sentences are read once from the scripts compiled into the binary, and a
// console page in a language other than English carries that language's
// versions in #vp-words, a JSON block in its <head> that vpT reads. The page
// is the carrier rather than a file of its own because it already knows its
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

// withScriptWords puts lang's #vp-words into a whole page, in its <head>, so
// it is there before any script runs; a fragment (an htmx swap) has none and
// is returned as it is, so a page never carries two.
func withScriptWords(body, lang string) string {
	i := strings.Index(body, "</head>")
	if i < 0 {
		return body
	}
	words := scriptWords(lang)
	if words == "" {
		return body
	}
	return body[:i] + words + body[i:]
}

// vpTScript defines window.vpT(msg, vals): msg in the language the page is
// written in, from #vp-words, with its placeholders filled as i18n.T fills
// them (internal/i18n/console.go, render and placeholder): {name}, and
// {n, plural, =1 {…} one {…} other {…}} with # for the number, the branch
// chosen by the language's own rules. The shell runs it at the top of the
// body, beside vpIcon, because a page's own script runs before admin-os.js
// and may say a sentence as it loads (Mail's word count does).
const vpTScript = `(function(){
var vpWords = null;
function vpT(msg, vals) {
  if (vpWords === null) {
    var el = document.getElementById('vp-words');
    try { vpWords = el ? JSON.parse(el.textContent) : {}; } catch (e) { vpWords = {}; }
  }
  return vpFill(vpWords[msg] || msg, vals || {}, '');
}
function vpClosing(text, open) {
  var depth = 0;
  for (var j = open; j < text.length; j++) {
    var c = text.charAt(j);
    if (c === '{') depth++;
    else if (c === '}' && --depth === 0) return j;
  }
  return -1;
}
function vpFill(text, vals, hash) {
  var out = '';
  for (var i = 0; i < text.length;) {
    var c = text.charAt(i);
    if (c === '#' && hash !== '') { out += hash; i++; continue; }
    if (c !== '{') { out += c; i++; continue; }
    var end = vpClosing(text, i);
    if (end < 0) { out += text.slice(i); break; }
    out += vpPlace(text.slice(i + 1, end), vals);
    i = end + 1;
  }
  return out;
}
function vpPlace(inner, vals) {
  var comma = inner.indexOf(',');
  if (comma < 0) {
    var v = vals[inner.trim()];
    return v === undefined || v === null ? '' : String(v);
  }
  var name = inner.slice(0, comma).trim();
  var rest = inner.slice(comma + 1);
  var comma2 = rest.indexOf(',');
  if (comma2 < 0 || rest.slice(0, comma2).trim() !== 'plural') return '{' + inner + '}';
  var n = Math.trunc(Number(vals[name]) || 0);
  var forms = vpBranches(rest.slice(comma2 + 1));
  if (forms['=' + n] !== undefined) return vpFill(forms['=' + n], vals, String(n));
  var cat = 'other';
  try { cat = new Intl.PluralRules(document.documentElement.lang || 'en').select(Math.abs(n)); } catch (e) {}
  return vpFill(forms[cat] !== undefined ? forms[cat] : (forms.other || ''), vals, String(n));
}
function vpBranches(s) {
  var out = {};
  for (var i = 0; i < s.length;) {
    var open = s.indexOf('{', i);
    if (open < 0) break;
    var end = vpClosing(s, open);
    if (end < 0) break;
    out[s.slice(i, open).trim()] = s.slice(open + 1, end);
    i = end + 1;
  }
  return out;
}
window.vpT = vpT;
})();`
