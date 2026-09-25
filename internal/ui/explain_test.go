// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// A fold starts closed, and a tip carries its sentence as the button's name so
// it is heard without being opened. Both escape what they are given once.
func TestExplainIsClosedAndTipIsNamed(t *testing.T) {
	e := string(Explain(`<p>Why.</p>`))
	if !strings.HasPrefix(e, `<details class="sa-explain">`) || strings.Contains(e, " open") {
		t.Errorf("Explain must start closed: %s", e)
	}
	tip := string(Tip(`Keys <b>& "tokens"`))
	if !strings.Contains(tip, `aria-label="Keys &lt;b&gt;&amp; &#34;tokens&#34;"`) {
		t.Errorf("a tip is not named by its escaped sentence: %s", tip)
	}
	if strings.Contains(tip, "<b>") || strings.Contains(tip, "&amp;amp;") {
		t.Errorf("a tip must escape its text exactly once: %s", tip)
	}
}

// Brief keeps the finding and tips the rest; one sentence has nothing to tip,
// and a full stop inside a sentence is not an end.
func TestBriefKeepsTheFirstSentence(t *testing.T) {
	for _, c := range []struct{ in, shown, tipped string }{
		{"Auto-block is off. Nothing is added.", "Auto-block is off.", "Nothing is added."},
		{"Only one sentence.", "Only one sentence.", ""},
		{"Version 3.17 is out. Update.", "Version 3.17 is out.", "Update."},
		{"Read /proc/1/mem. no capital follows", "Read /proc/1/mem. no capital follows", ""},
	} {
		out := string(Brief(c.in))
		shown, _, _ := strings.Cut(out, `<button`)
		if shown != string(Text(c.shown)) {
			t.Errorf("Brief(%q) shows %q, want %q", c.in, shown, c.shown)
		}
		hasTip := strings.Contains(out, `class="sa-tip"`)
		if (c.tipped != "") != hasTip || (hasTip && !strings.Contains(out, `aria-label="`+c.tipped+`"`)) {
			t.Errorf("Brief(%q) = %s, want the tip %q", c.in, out, c.tipped)
		}
	}
}
