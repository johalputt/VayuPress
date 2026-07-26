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
	"we need to write", "we need to create", "the user wants", "the user is asking",
	"okay, so the user", "okay, the user", "first, i need to", "let me think",
	"i should write", "the task is to write", "let's see, the user",
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

// LooksStructured reports whether text carries the shape of an article — a
// heading, or several paragraphs. Used to decide whether a reasoning model's
// output is a post or a stream of thought.
func LooksStructured(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	lower := strings.ToLower(t)
	if strings.Contains(lower, "<h1") || strings.Contains(lower, "<h2") || strings.Contains(lower, "<h3") {
		return true
	}
	for _, line := range strings.Split(t, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return true
		}
	}
	// Several blank-line-separated paragraphs also count as structure.
	return strings.Count(t, "\n\n") >= 2
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
