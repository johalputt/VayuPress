// SPDX-License-Identifier: Apache-2.0

package aiassist

// quality.go — reject model output that is not a draft.
//
// A generated draft is inserted straight into an author's editor, so the one thing
// this must never do is hand over garbage as though it succeeded. Small, heavily
// quantised free models — exactly the ones people try first — fail in three
// recognisable ways:
//
//  1. They leak their own special tokens: "<SPECIAL_205>", "<|im_start|>".
//  2. They emit multilingual token salad, several unrelated scripts per sentence.
//  3. They return their internal monologue ("We need to write a blog post
//     about…") instead of the post.
//
// All three are obvious to a human in one glance and all three used to be
// inserted as a 2,000-word "draft". Detecting them is not about second-guessing a
// competent model — the checks are deliberately blunt, so ordinary prose in any
// language passes and only genuinely broken output is refused.

import (
	"regexp"
	"strings"
	"unicode"
)

// brokenTokenPatterns match control vocabulary that should never survive into
// user-visible text. A model emitting these is serving raw tokeniser output.
var brokenTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`<SPECIAL_\d+>`),
	regexp.MustCompile(`<\|[a-zA-Z0-9_]+\|>`),
	regexp.MustCompile(`\[UNUSED_\d+\]`),
	regexp.MustCompile(`<0x[0-9A-Fa-f]{2}>`),
	regexp.MustCompile(`(?i)<\/?s>|<pad>|<unk>|<eos>|<bos>`),
}

// monologueOpeners are how a reasoning model starts talking to itself. They are
// only treated as a failure when they open the text, because a post could
// legitimately contain such a phrase further in.
var monologueOpeners = []string{
	"we need to write", "we need to create", "we need to produce", "the user wants",
	"the user is asking", "the user asked", "okay, so", "okay, the user", "alright, so",
	"first, i need to", "first, let me", "let me think", "let me analyze", "let me analyse",
	"let me start", "let me re-check", "let me first", "i should write", "i need to write",
	"the task is to write", "the instruction says", "let's see", "hmm,", "wait,",
	"looking at the instruction", "based on the instruction", "the blog engine",
}

// scriptRanges are the script families counted for the salad check. Latin is
// excluded because it is the baseline for markup and most prose.
var scriptRanges = map[string]*unicode.RangeTable{
	"han":        unicode.Han,
	"devanagari": unicode.Devanagari,
	"arabic":     unicode.Arabic,
	"hangul":     unicode.Hangul,
	"cyrillic":   unicode.Cyrillic,
	"thai":       unicode.Thai,
	"hebrew":     unicode.Hebrew,
	"hiragana":   unicode.Hiragana,
	"katakana":   unicode.Katakana,
}

// Quality thresholds. These are loose on purpose: the goal is to catch output no
// human would accept, not to police style.
const (
	// scriptMinRunes is how many characters of a script must appear before it
	// counts as present at all, so a single borrowed word is ignored.
	scriptMinRunes = 5
	// scriptSaladMin is how many distinct non-Latin scripts must be present
	// together to call it salad. Three is safe: a bilingual post uses one
	// non-Latin script, a trilingual one is vanishingly rare, and broken decoders
	// routinely emit five or six.
	scriptSaladMin = 3
)

// Unusable reports whether text is broken model output rather than a draft, with
// a reason phrased for the author.
//
// It is deliberately conservative. Every rule here describes output that is
// unambiguously not prose, because a false positive throws away a real draft.
func Unusable(text string) (bool, string) {
	if bad, why := unusableGarbage(text); bad {
		return bad, why
	}
	t := strings.TrimSpace(text)
	// Monologue check against the opening of the text only.
	head := strings.ToLower(t)
	if len(head) > 300 {
		head = head[:300]
	}
	head = strings.TrimLeft(head, "#*_> \t\n-")
	for _, opener := range monologueOpeners {
		if strings.HasPrefix(head, opener) {
			return true, "the model returned its own thinking (\"" + firstWords(t, 8) +
				"…\") instead of the post — try a different model, or run it again"
		}
	}
	return false, ""
}

// unusableGarbage covers the failures that are garbage NO MATTER where in the reply
// they appear: leaked control tokens and script salad. It is separated from the
// monologue check because the two must run at different times — garbage disqualifies
// the whole reply, while a monologue is only fatal once salvage has failed to find an
// article behind it.
func unusableGarbage(text string) (bool, string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return true, "the model returned an empty draft"
	}
	for _, re := range brokenTokenPatterns {
		if m := re.FindString(t); m != "" {
			return true, "the model leaked internal tokens such as " + m +
				" instead of writing prose — this usually means that model is serving broken output, so try a different one"
		}
	}
	if n, names := distinctScripts(t); n >= scriptSaladMin {
		return true, "the draft mixes unrelated writing systems (" + strings.Join(names, ", ") +
			"), which means the model produced garbled text rather than a post — try a different model"
	}
	return false, ""
}

// distinctScripts counts non-Latin script families with a meaningful presence.
func distinctScripts(s string) (int, []string) {
	counts := map[string]int{}
	for _, r := range s {
		if r < unicode.MaxASCII {
			continue
		}
		for name, tbl := range scriptRanges {
			if unicode.Is(tbl, r) {
				counts[name]++
				break
			}
		}
	}
	// Hiragana and katakana are the same language; collapse them so ordinary
	// Japanese is never mistaken for salad.
	if counts["hiragana"] > 0 || counts["katakana"] > 0 {
		counts["kana"] = counts["hiragana"] + counts["katakana"]
		delete(counts, "hiragana")
		delete(counts, "katakana")
	}
	var names []string
	for name, n := range counts {
		if n >= scriptMinRunes {
			names = append(names, name)
		}
	}
	sortStrings(names)
	return len(names), names
}

// hasHeading reports whether text contains an HTML or Markdown heading — the test
// for "is this an article".
//
// An earlier version of this check also accepted "several blank-line-separated
// paragraphs", which was worthless: a model reasoning to itself writes in
// paragraphs too, so a monologue passed and was inserted as a 1,464-word draft. A
// heading is the one thing the prompt demands and a stream of thought never has.
func hasHeading(text string) bool {
	lower := strings.ToLower(text)
	for _, tag := range []string{"<h1", "<h2", "<h3"} {
		if strings.Contains(lower, tag) {
			return true
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// "#hashtag" is not a heading; ATX headings need a space after the hashes.
		if strings.HasPrefix(line, "#") {
			rest := strings.TrimLeft(line, "#")
			if rest != line && strings.HasPrefix(rest, " ") && strings.TrimSpace(rest) != "" {
				return true
			}
		}
	}
	return false
}

// StartsLikeArticle reports whether the text opens the way an article does — with
// its title or a block element — rather than with a sentence about the task.
//
// This is the load-bearing check for reasoning-field output, and it is framed as a
// requirement rather than a blacklist on purpose. Listing the ways a model can
// start talking to itself ("we need to write", "let me analyse", "okay, so",
// "wait, let me re-check") is endless; requiring the shape we asked for is one
// rule that covers all of them.
func StartsLikeArticle(text string) bool {
	t := strings.TrimSpace(text)
	// Skip a leading Markdown code fence, which some models wrap output in.
	t = strings.TrimPrefix(t, "```html")
	t = strings.TrimPrefix(t, "```")
	t = strings.TrimSpace(t)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	for _, opener := range []string{"<h1", "<h2", "<article", "<section", "<p>", "<p ", "<div", "<ul", "<ol", "<table", "<!doctype", "<html"} {
		if strings.HasPrefix(lower, opener) {
			return true
		}
	}
	// A Markdown heading on the first line.
	first := t
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		first = t[:i]
	}
	first = strings.TrimSpace(first)
	if strings.HasPrefix(first, "#") {
		rest := strings.TrimLeft(first, "#")
		return rest != first && strings.HasPrefix(rest, " ") && strings.TrimSpace(rest) != ""
	}
	return false
}

// LooksLikeHTML reports whether text is HTML rather than Markdown, so the caller
// can route it to the right importer. Models ignore format instructions often
// enough that guessing beats trusting.
func LooksLikeHTML(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	// A block-level tag anywhere is enough; inline emphasis alone is not, since
	// Markdown output legitimately contains the odd <em>.
	for _, tag := range []string{"<h1", "<h2", "<h3", "<p>", "<p ", "<ul", "<ol", "<table", "<section", "<article", "<div"} {
		if strings.Contains(lower, tag) {
			return true
		}
	}
	return false
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// sortStrings is a tiny insertion sort, kept local so this file pulls in no extra
// dependency for a list that is never longer than a handful of entries.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TrimToArticle drops any preamble before the article actually starts.
//
// This is the salvage path, and it matters more than the refusals: the most common
// reasoning-model shape is thinking FOLLOWED BY the real post, and the second most
// common is a chat opener ("Sure! Here is your article:") before it. Both contain a
// perfectly good draft that only needs its lead-in removed, so cutting to the first
// heading turns two failures into successes.
//
// It only ever cuts — it never rewrites — and it does nothing when the text already
// starts correctly or has no heading to cut to.
func TrimToArticle(text string) string {
	t := strings.TrimSpace(text)
	if t == "" || StartsLikeArticle(t) {
		return t
	}
	if !hasHeading(t) {
		return t // nothing to cut to; the caller refuses it
	}
	// Prefer an HTML h1/h2, then a Markdown ATX heading.
	best := -1
	lower := strings.ToLower(t)
	for _, tag := range []string{"<h1", "<h2"} {
		if i := strings.Index(lower, tag); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best < 0 {
		offset := 0
		for _, line := range strings.Split(t, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				rest := strings.TrimLeft(trimmed, "#")
				if rest != trimmed && strings.HasPrefix(rest, " ") && strings.TrimSpace(rest) != "" {
					best = offset + strings.Index(line, trimmed)
					break
				}
			}
			offset += len(line) + 1
		}
	}
	if best <= 0 {
		return t
	}
	return strings.TrimSpace(t[best:])
}
