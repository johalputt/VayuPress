// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// leadingHolds states PlainTextLeading's contract against PlainText itself:
// the result is where PlainText(s) begins, and it is either longer than n or
// the whole of it.
func leadingHolds(t *testing.T, s string, n int) {
	t.Helper()
	full := PlainText(s)
	got := PlainTextLeading(s, n)
	if !strings.HasPrefix(full, got) {
		t.Fatalf("PlainTextLeading(%d) = %q\nis not where PlainText begins: %q", n, got, full)
	}
	if len(got) <= n && got != full {
		t.Fatalf("PlainTextLeading(%d) = %q: shorter than asked, and not all of %q", n, got, full)
	}
}

// Each case but the last opens something before the first place a cut is
// tried and closes it after, so a cut that ignored it would put its inside into
// the text.
func TestPlainTextLeadingNeverCutsInsideWhatPlainTextRemoves(t *testing.T) {
	words := strings.Repeat("<p>Readers see this sentence first.</p>", 40)
	filler := strings.Repeat("<i>x</i>", 200) // tags, so there is a '>' to cut after
	for name, s := range map[string]string{
		"a comment":   "<!-- HIDDEN " + filler + " HIDDEN -->" + words,
		"an svg":      `<svg viewBox="0 0 9 9"><text>HIDDEN</text>` + filler + "<text>HIDDEN</text></svg>" + words,
		"a script":    "<script>var HIDDEN = 1;" + filler + "</script>" + words,
		"a long tag":  `<p title="` + strings.Repeat("HIDDEN ", 200) + `">` + words,
		"plain words": words,
	} {
		t.Run(name, func(t *testing.T) {
			got := PlainTextLeading(s, 160)
			if strings.Contains(got, "HIDDEN") || strings.Contains(got, "<") {
				t.Fatalf("the excerpt text carries what PlainText removes: %q", got[:min(len(got), 200)])
			}
			leadingHolds(t, s, 160)
		})
	}
}

// TestPlainTextLeadingNeverSplitsAnEntity — a cut anywhere but just after a
// '>' would, for some lengths, land inside "&amp;" and leave "&am" where the
// text has "&". The lengths vary so the cut does not always fall between two.
func TestPlainTextLeadingNeverSplitsAnEntity(t *testing.T) {
	s := strings.Repeat("<i>"+strings.Repeat("&amp;", 60)+"</i>", 40)
	for n := 100; n < 400; n += 7 {
		leadingHolds(t, s, n)
	}
}

// TestPlainTextLeadingConvertsOnlyWhatItNeeds — the point of it: a long post
// is not converted whole for 160 characters.
func TestPlainTextLeadingConvertsOnlyWhatItNeeds(t *testing.T) {
	s := strings.Repeat("<p>Readers see this sentence first.</p>", 500) // ~20 KB
	if got, full := PlainTextLeading(s, 160), PlainText(s); len(got) >= len(full)/4 {
		t.Fatalf("converted %d of %d bytes of text for a 160-byte excerpt", len(got), len(full))
	}
}

func FuzzPlainTextLeading(f *testing.F) {
	f.Add("<p>a <b>b</b> c</p><!-- x --><svg><text>y</text></svg> d &amp; e > f", 3)
	f.Add("<script>1</script><p>text, more .</p>", 4)
	f.Add(strings.Repeat("<p>x &lt; y</p> ", 50), 20)
	f.Fuzz(func(t *testing.T, s string, n int) {
		if n < 0 || n > 400 {
			return
		}
		leadingHolds(t, s, n)
	})
}
