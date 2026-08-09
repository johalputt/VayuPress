// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/json"
	"regexp"
	"strings"
)

// jsonAttrTagRe strips markup before the value reaches a JSON-LD string. It runs
// first so a title carrying inline HTML does not put tags into structured data
// a search engine reads as prose.
var jsonAttrTagRe = regexp.MustCompile(`<[^>]+>`)

// jsonAttrMaxRunes bounds a value inside the JSON-LD block. Runes, not bytes:
// slicing bytes can cut a multi-byte character in half, and half a character is
// not valid UTF-8 for the encoder that follows.
const jsonAttrMaxRunes = 300

// jsonAttr escapes a value for interpolation between the quotes of a JSON-LD
// string, as `"headline":"{{.Title | jsonAttr}}"`.
//
// WHAT WAS WRONG WITH THE HAND-ROLLED VERSION. It replaced `"` with `\"` and
// newlines with spaces, and did nothing about the backslash. So a value ending
// in one — a site name of `Acme\` — rendered as `"name":"Acme\"`, where the
// trailing backslash escapes the closing quote and the JSON-LD block stops being
// parseable. A literal tab passed through raw, which is also invalid inside a
// JSON string. Either way the page's structured data is silently discarded by
// whatever reads it, which is the whole point of emitting it.
//
// Not a security hole, and worth saying why rather than leaving the reader to
// wonder: the tag strip removes `</script>` before it can close the element, and
// the block is `application/ld+json`, so a malformed document is bad data rather
// than executable script. It is a correctness bug in the one function whose
// entire job is to make a value safe to put between quotes.
//
// It surfaced during the pre-release pass on this section because the branding
// write-through newly lets a CLIENT set the site name that lands here. The bug
// was older than that and reachable through the operator's own settings page —
// what changed is who can reach it.
//
// encoding/json does the escaping now: it is the mechanism that already exists,
// it handles backslashes, control characters and invalid UTF-8, and it escapes
// `<`, `>` and `&` to \u-sequences, which is exactly what one wants inside a
// script element. Marshal wraps a string in its own quotes and the template
// supplies its own, so they are trimmed back off.
func jsonAttr(s string) string {
	s = jsonAttrTagRe.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > jsonAttrMaxRunes {
		s = string(r[:jsonAttrMaxRunes])
	}
	b, err := json.Marshal(s)
	if err != nil {
		// Marshal fails on no input this can produce (a string always encodes).
		// Returning empty rather than the raw value keeps a value that somehow
		// could not be escaped out of the document entirely — a missing field is
		// recoverable, an unescaped one is what this function exists to prevent.
		return ""
	}
	return string(b[1 : len(b)-1])
}
