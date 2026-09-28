// SPDX-License-Identifier: Apache-2.0

package render

// minify.go — stylesheets are served without their comments.
//
// The sources keep their comments, because they say why a rule exists, and
// that prose was a large share of what every visitor downloaded. Minifying
// when the file is served (the console) or written (the public site's
// stylesheets, WriteCSSAssets) keeps one source of truth and needs no build
// step, and one function does both, so the two cannot drift.
//
// The minifier is deliberately small and only does what is provably safe:
// comments go, and whitespace collapses — to nothing next to { } ; , and to a
// single space everywhere else. It never touches the inside of a string, and it
// never removes the space before a colon or around + - * /, where CSS gives
// whitespace meaning ("a :hover" is not "a:hover"; calc() needs its spaces).

import "bytes"

// MinifyCSS returns src without comments and with collapsed whitespace.
func MinifyCSS(src []byte) []byte {
	out := make([]byte, 0, len(src))
	tight := func(c byte) bool { return c == '{' || c == '}' || c == ';' || c == ',' }
	pendingSpace := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				return out // an unterminated comment runs to the end of the file
			}
			i += 2 + end + 1
			pendingSpace = true // a comment separates tokens like whitespace does
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			pendingSpace = true
		case c == '"' || c == '\'':
			if pendingSpace && len(out) > 0 && !tight(out[len(out)-1]) {
				out = append(out, ' ')
			}
			pendingSpace = false
			j := i + 1
			for j < len(src) && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(src) {
				return append(out, src[i:]...)
			}
			out = append(out, src[i:j+1]...)
			i = j
		default:
			if pendingSpace && len(out) > 0 && !tight(out[len(out)-1]) && !tight(c) {
				out = append(out, ' ')
			}
			pendingSpace = false
			out = append(out, c)
		}
	}
	return out
}
