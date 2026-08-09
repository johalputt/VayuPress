// SPDX-License-Identifier: Apache-2.0

package render

import (
	"encoding/json"
	"strings"
	"testing"
)

// The contract: whatever comes out, dropped between two quotes, is a parseable
// JSON string. Asserting that by actually PARSING is what makes this test worth
// having — a list of expected escape sequences would agree with any escaper that
// happened to produce them and miss the input nobody thought of.
func parsesAsJSONString(t *testing.T, in string) string {
	t.Helper()
	doc := `{"name":"` + jsonAttr(in) + `"}`
	var out struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("jsonAttr(%q) produced a JSON-LD block that does not parse: %v\n  %s", in, err, doc)
	}
	return out.Name
}

// FINDING (S8 pre-release pass) — a value ending in a backslash escaped the
// closing quote and broke the whole JSON-LD block.
//
// The hand-rolled escaper replaced `"` with `\"` and did nothing about `\`, so a
// site name of `Acme\` rendered as "name":"Acme\" — the trailing backslash
// escapes the quote the template supplies and the document stops parsing. A
// literal tab passed through raw, which is also invalid inside a JSON string.
// Either way the structured data is silently discarded by whatever reads it,
// which is the entire purpose of emitting it.
func TestJSONAttrSurvivesTheCharactersThatUsedToBreakIt(t *testing.T) {
	for _, in := range []string{
		`Acme\`,                    // the finding: escapes the closing quote
		`Acme\\`,                   // and an even number of them
		"Acme\ttab",                // raw control character
		"Acme\x00nul",              // the other end of the control range
		`Acme " quoted`,            // the case the old version did handle
		"multi\nline",              // newline
		"multi\r\nline",            // CRLF, which the old version half-handled
		`Ünïcödé — 日本語`,            // multi-byte, must survive intact
		string([]byte{0xff, 0xfe}), // invalid UTF-8
	} {
		parsesAsJSONString(t, in)
	}
}

// The value has to survive, not merely be safe. An escaper that returned "" for
// everything would pass the parse test and empty every site's structured data.
func TestJSONAttrKeepsTheValueItEscapes(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`Acme\`, `Acme\`},
		{`Acme " quoted`, `Acme " quoted`},
		{"Ünïcödé — 日本語", "Ünïcödé — 日本語"},
		{"  padded  ", "padded"},
	} {
		if got := parsesAsJSONString(t, c.in); got != c.want {
			t.Errorf("jsonAttr(%q) round-trips to %q, want %q", c.in, got, c.want)
		}
	}
}

// Markup is stripped before escaping, so a title carrying inline HTML does not
// put tags into structured data a search engine reads as prose — and, more to
// the point, cannot carry a closing script tag into the element.
func TestJSONAttrStripsMarkupAndCannotCloseTheScriptElement(t *testing.T) {
	got := parsesAsJSONString(t, `Acme</script><img src=x onerror=alert(1)>`)
	if strings.Contains(strings.ToLower(got), "<") || strings.Contains(strings.ToLower(got), "script") {
		t.Fatalf("markup survived into the JSON-LD block: %q", got)
	}
	// encoding/json escapes the angle brackets too, so even a lone "<" that the
	// tag regex cannot match leaves as < rather than as itself.
	if raw := jsonAttr("a < b"); strings.Contains(raw, "<") {
		t.Errorf("a bare angle bracket reached the script element unescaped: %q", raw)
	}
}

// The cap is on RUNES. Slicing bytes can cut a multi-byte character in half, and
// half a character is not valid UTF-8 — which the encoder then replaces with a
// substitution character, corrupting the last visible glyph of every long
// non-ASCII title.
func TestJSONAttrTruncatesOnCharacterBoundaries(t *testing.T) {
	long := strings.Repeat("日", 400)
	got := parsesAsJSONString(t, long)
	if r := []rune(got); len(r) != jsonAttrMaxRunes {
		t.Fatalf("truncated to %d runes, want %d", len(r), jsonAttrMaxRunes)
	}
	if strings.ContainsRune(got, '�') {
		t.Error("truncation split a character, so the value ends in a replacement glyph")
	}
}
