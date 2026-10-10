// SPDX-License-Identifier: Apache-2.0

// Package tagrules says what a tag is (PIPELINE 3ay). A tag is a specific
// topic, a few to a post, as on Medium, Stack Overflow or Ghost: on johal.in
// the tool that writes the posts made tags of title words ("with", "which")
// and put one set of twenty on every post, so a tag page listed the whole site
// and related posts were the newest ones whatever they were about.
//
//   - One key per tag: lowercase words joined by single hyphens, so "Machine
//     Learning", "machine_learning" and "machine-learning" are one tag, with
//     one page and one URL. "+", "#" and "." are kept: C++, C# and Node.js
//     are not C, C or "node-js".
//   - At most MaxLen characters: a sentence is not a topic.
//   - Never a stop-word.
//   - At most MaxPerPost to a post. When more are sent the rarest across the
//     site are kept, rarity being what makes a tag specific; a post is never
//     refused for its tags.
package tagrules

import (
	"sort"
	"strings"
	"unicode"
)

const (
	MaxLen     = 40
	MaxPerPost = 10
)

// Key is the tag's identity: its page's address and what tag_norm stores.
// "" means it is no tag at all.
func Key(tag string) string {
	var b strings.Builder
	sep := false
	for _, r := range strings.ToLower(strings.TrimSpace(tag)) {
		switch {
		case kept(r):
			if sep && b.Len() > 0 {
				b.WriteByte('-')
			}
			sep = false
			b.WriteRune(r)
		default:
			sep = true
		}
	}
	k := b.String()
	if k == "" || len([]rune(k)) > MaxLen || stopWords[k] {
		return ""
	}
	return k
}

// kept is a character a key keeps; any run of others is one hyphen.
func kept(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '#' || r == '.'
}

// Display is the tag as shown: as written, its spaces collapsed and what
// surrounds it trimmed, so a tag stored as JSON (`["news"`, from the importers
// before the rules) shows as "news".
func Display(tag string) string {
	return strings.Join(strings.Fields(strings.TrimFunc(tag, func(r rune) bool { return !kept(r) })), " ")
}

// Clean returns the tags a post keeps, as shown, in the order they were sent:
// those that are tags at all, each key once, at most MaxPerPost. frequency
// gives how many posts carry a key; with it the rarest are kept, without it
// (nil) the first.
func Clean(tags []string, frequency func(key string) int) []string {
	type tag struct {
		show, key string
		at, freq  int
	}
	seen := make(map[string]bool, len(tags))
	kept := make([]tag, 0, len(tags))
	for _, t := range tags {
		show := Display(t)
		k := Key(show)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, tag{show: show, key: k, at: len(kept)})
	}
	if len(kept) > MaxPerPost {
		if frequency != nil {
			for i := range kept {
				kept[i].freq = frequency(kept[i].key)
			}
			sort.SliceStable(kept, func(i, j int) bool { return kept[i].freq < kept[j].freq })
		}
		kept = kept[:MaxPerPost]
		sort.Slice(kept, func(i, j int) bool { return kept[i].at < kept[j].at })
	}
	if len(kept) == 0 {
		return nil
	}
	out := make([]string, len(kept))
	for i, t := range kept {
		out[i] = t.show
	}
	return out
}

// stopWords are the standard English stop-words (NLTK's list): words that say
// nothing about what a post is about.
var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`i me my myself we our ours ourselves you your yours yourself yourselves
		he him his himself she her hers herself it its itself they them their theirs themselves what which who
		whom this that these those am is are was were be been being have has had having do does did doing a an
		the and but if or because as until while of at by for with about against between into through during
		before after above below to from up down in out on off over under again further then once here there
		when where why how all any both each few more most other some such no nor not only own same so than too
		very s t can will just don should now d ll m o re ve y ain aren couldn didn doesn hadn hasn haven isn ma
		mightn mustn needn shan shouldn wasn weren won wouldn`) {
		m[w] = true
	}
	return m
}()
