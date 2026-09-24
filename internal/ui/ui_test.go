// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// hostile is text a person could type. Every primitive must show it as text:
// escaped once, never raw and never twice.
const hostile = `<b>R&D</b>`

const once = `&lt;b&gt;R&amp;D&lt;/b&gt;`

func escapedOnce(t *testing.T, where string, out HTML) {
	t.Helper()
	s := string(out)
	if strings.Contains(s, "<b>R") {
		t.Errorf("%s: text reached the page as markup:\n%s", where, s)
	}
	if strings.Contains(s, "&amp;lt;") || strings.Contains(s, "&amp;amp;") {
		t.Errorf("%s: text was escaped twice:\n%s", where, s)
	}
	if !strings.Contains(s, once) {
		t.Errorf("%s: the text is missing:\n%s", where, s)
	}
}

// One seed per text field of every primitive: a primitive that forgets to
// escape one field fails on that field's name.
func TestEveryPrimitiveEscapesTextOnce(t *testing.T) {
	escapedOnce(t, "Page title", Page(hostile, "", ""))
	escapedOnce(t, "Page sub", Page("T", hostile, ""))
	escapedOnce(t, "Section title", Section(hostile, "", ""))
	escapedOnce(t, "Section hint", Section("T", hostile, ""))
	escapedOnce(t, "Row label", Rows(Row{Label: hostile}))
	escapedOnce(t, "Row hint", Rows(Row{Label: "L", Hint: hostile}))
	escapedOnce(t, "Row id", Rows(Row{Label: "L", ID: hostile}))
	escapedOnce(t, "Fact key", Facts(Fact{Key: hostile}))
	escapedOnce(t, "Figure value", Figures(Figure{Value: hostile}))
	escapedOnce(t, "Figure label", Figures(Figure{Label: hostile}))
	escapedOnce(t, "Figure note", Figures(Figure{Note: hostile}))
	escapedOnce(t, "Disclosure title", Disclosure("", hostile, "", "", false, ""))
	escapedOnce(t, "Disclosure sub", Disclosure("", "T", hostile, "", false, ""))
	escapedOnce(t, "Table header", Table([]string{hostile}, nil, ""))
	escapedOnce(t, "Table empty", Table([]string{"A"}, nil, hostile))
	escapedOnce(t, "Empty title", Empty("info", hostile, "", ""))
	escapedOnce(t, "Empty sub", Empty("info", "T", hostile, ""))
	escapedOnce(t, "Tag text", Tag("ok", hostile))
	escapedOnce(t, "Step mark", Steps(Step{Mark: hostile}))
	escapedOnce(t, "Step title", Steps(Step{Title: hostile}))
	escapedOnce(t, "Step detail", Steps(Step{Title: "T", Detail: hostile}))
}

// Markup the caller vouches for passes through untouched: a primitive that
// escaped its HTML arguments would print the markup as text.
func TestTrustedMarkupPassesThrough(t *testing.T) {
	const m = `<a href="/os">x</a>`
	for where, out := range map[string]HTML{
		"Page actions":     Page("T", "", m),
		"Section body":     Section("T", "", m),
		"Row control":      Rows(Row{Label: "L", Control: m}),
		"Fact value":       Facts(Fact{Key: "K", Value: m}),
		"Disclosure state": Disclosure("", "T", "", m, false, ""),
		"Disclosure body":  Disclosure("", "T", "", "", false, m),
		"Table cell":       Table([]string{"A"}, [][]HTML{{m}}, ""),
		"Callout body":     Callout("info", m),
		"Empty action":     Empty("info", "T", "", m),
	} {
		if !strings.Contains(string(out), m) {
			t.Errorf("%s: trusted markup did not pass through:\n%s", where, out)
		}
	}
}

// Only the tones the stylesheet defines reach a class attribute.
func TestToneClassesAreOnlyTheDefinedOnes(t *testing.T) {
	if s := string(Callout(`x" onclick="y`, "")); strings.Contains(s, "onclick") || !strings.Contains(s, "callout--info") {
		t.Errorf("an unknown callout tone reached the markup: %s", s)
	}
	if s := string(Figures(Figure{Tone: `x" onclick="y`})); strings.Contains(s, "onclick") {
		t.Errorf("an unknown figure tone reached the markup: %s", s)
	}
	if s := string(Tag(`x" onclick="y`, "T")); strings.Contains(s, "onclick") || !strings.Contains(s, "badge--muted") {
		t.Errorf("an unknown tag tone reached the markup: %s", s)
	}
	if s := string(Steps(Step{Title: "T", Now: true})); !strings.Contains(s, "sa-step--now") {
		t.Error("the current step is not marked")
	}
	if s := string(Rows(Row{Label: "L", Changed: true})); !strings.Contains(s, "is-changed") {
		t.Error("a changed row is not marked")
	}
}

func TestIconsArePlainPathsFromOneSet(t *testing.T) {
	for _, name := range IconNames() {
		p := icons[name]
		if strings.ContainsAny(p, "<>") && !strings.HasPrefix(strings.TrimSpace(p), "<") {
			t.Errorf("icon %s is not plain SVG path markup", name)
		}
	}
	if !strings.Contains(string(Icon("no-such-icon")), "sa-ico--missing") {
		t.Error("an unknown icon renders silently")
	}
	if !strings.Contains(string(Sprite), `id="sa-i-warn"`) {
		t.Error("the sprite does not carry the set")
	}
}
