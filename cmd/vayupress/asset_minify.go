// SPDX-License-Identifier: Apache-2.0

package main

// asset_minify.go — the console's stylesheets are served without their comments.
//
// The source keeps its comments, because they say why a rule exists, and they
// were a third of what every operator downloaded: the classic sheet was 77 KB
// gzipped on the wire, 27 KB of it prose. Minifying at serve time keeps one
// source of truth and needs no build step.
//
// The minifier is deliberately small and only does what is provably safe:
// comments go, and whitespace collapses — to nothing next to { } ; , and to a
// single space everywhere else. It never touches the inside of a string, and it
// never removes the space before a colon or around + - * /, where CSS gives
// whitespace meaning ("a :hover" is not "a:hover"; calc() needs its spaces).

import (
	"bytes"
	"crypto/sha256"
	"sync"
)

// minifyCSS returns src without comments and with collapsed whitespace.
func minifyCSS(src []byte) []byte {
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

// minifiedCSS memoises minifyCSS by content, so a stylesheet is minified once
// per version and an updated file on disk is picked up by its new hash.
var minifiedCSS sync.Map // [32]byte -> []byte

func minifiedCSSFor(src []byte) []byte {
	key := sha256.Sum256(src)
	if v, ok := minifiedCSS.Load(key); ok {
		return v.([]byte)
	}
	m := minifyCSS(src)
	minifiedCSS.Store(key, m)
	return m
}
