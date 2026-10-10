// SPDX-License-Identifier: Apache-2.0

// Package textfix undoes text that was UTF-8, read as Latin-1 or Windows-1252
// and stored again, once or several times over: "—" becomes "â€”", then
// "Ã¢â‚¬â€", and so on. Posts on johal.in arrived this way from the tool
// that wrote them; VayuPress itself converts no charset on any write path.
//
// A run is restored only when undoing the layers ends in clean text. A run
// that still reads as garbled after every layer that can be undone has lost
// bytes on the way, and what it said cannot be known: it is left as it is and
// counted, never guessed at.
package textfix

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// cp1252 maps the Windows-1252 characters of bytes 0x80–0x9F back to their
// byte. Latin-1 maps the same bytes to the C1 controls U+0080–U+009F, which
// fold back to themselves.
var cp1252 = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87,
	'ˆ': 0x88, '‰': 0x89, 'Š': 0x8A, '‹': 0x8B, 'Œ': 0x8C, 'Ž': 0x8E,
	'‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97,
	'˜': 0x98, '™': 0x99, 'š': 0x9A, '›': 0x9B, 'œ': 0x9C, 'ž': 0x9E, 'Ÿ': 0x9F,
}

// byteOf is the byte r was read from, if r is one a single byte reads as.
func byteOf(r rune) (byte, bool) {
	if r >= 0x80 && r <= 0xFF {
		return byte(r), true
	}
	b, ok := cp1252[r]
	return b, ok
}

// layered is a run worth trying: a UTF-8 lead byte read as a character (Â to
// ô) followed by a continuation byte read as one. Accented text matches it too
// ("é»"), and is left alone because undoing it does not give valid UTF-8.
var layered = regexp.MustCompile(`[\x{C2}-\x{F4}][\x{80}-\x{BF}€‚ƒ„…†‡ˆ‰Š‹ŒŽ‘’“”•–—˜™šœžŸ]`)

// garbled is the mark of layers still in place after undoing all that can be:
// the lead bytes of a second layer (Ã, Â) before a continuation, "â€" (the
// start of every punctuation mark from U+2000), or two lead bytes together,
// where the continuation between them was lost. Only this is counted lost, so
// accented text that merely looks layered is never reported.
var garbled = regexp.MustCompile(`[ÃÂ][\x{80}-\x{BF}€‚ƒ„…†‡ˆ‰Š‹ŒŽ‘’“”•–—˜™šœžŸ]|â€|[ÃÂ]{2}`)

// protected is markup whose text is shown as written: a post about encodings
// quotes garbled text on purpose.
var protected = regexp.MustCompile(`(?is)<(pre|code|kbd|samp)\b.*?</(pre|code|kbd|samp)>`)

// Result says what Repair did.
type Result struct {
	Restored int // runs restored to clean text
	Lost     int // garbled runs left as they are: bytes were lost
}

// Repair restores every run in s that can be restored and leaves the rest.
func Repair(s string) (string, Result) {
	var res Result
	if !hasLead(s) {
		return s, res
	}
	var b strings.Builder
	last := 0
	for _, loc := range protected.FindAllStringIndex(s, -1) {
		b.WriteString(repairText(s[last:loc[0]], &res))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(repairText(s[last:], &res))
	return b.String(), res
}

// repairText repairs each run of characters a single byte can read as.
func repairText(s string, res *Result) string {
	if !hasLead(s) {
		return s
	}
	var b strings.Builder
	start := -1
	flush := func(end int) {
		if start >= 0 {
			b.WriteString(repairRun(s[start:end], res))
			start = -1
		}
	}
	for i, r := range s {
		if _, ok := byteOf(r); ok {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
		b.WriteRune(r)
	}
	flush(len(s))
	return b.String()
}

// repairRun undoes layers while each one gives valid UTF-8, and keeps the
// result only if it is clean.
func repairRun(run string, res *Result) string {
	if !layered.MatchString(run) {
		return run
	}
	cur := run
	for {
		raw := make([]byte, 0, len(cur))
		for _, r := range cur {
			c, ok := byteOf(r)
			if !ok {
				break
			}
			raw = append(raw, c)
		}
		if utf8.RuneCountInString(cur) != len(raw) || !utf8.Valid(raw) || string(raw) == cur {
			break
		}
		cur = string(raw)
	}
	if cur == run || garbled.MatchString(cur) {
		if garbled.MatchString(run) {
			res.Lost++
		}
		return run
	}
	res.Restored++
	return cur
}

// hasLead reports whether s holds a UTF-8 lead byte read as a character, the
// first sign of any layer; most text has none and is returned untouched.
func hasLead(s string) bool {
	for _, r := range s {
		if r >= 0xC2 && r <= 0xF4 {
			return true
		}
	}
	return false
}
