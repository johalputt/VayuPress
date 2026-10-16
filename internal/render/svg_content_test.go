// SPDX-License-Identifier: Apache-2.0

package render

import (
	"regexp"
	"strings"
	"testing"
)

// The technical-debt guide as johal.in stores it: its capacity diagram is
// drawn (it was dropped whole, labels and all), on its own dark ground, its
// marker ids its own, and nothing of its style="" left for the CSP to refuse.
func TestAPostsDiagramIsDrawn(t *testing.T) {
	Init(t.TempDir())
	out := renderContentHTML(realPost(t, "technical-debt-visible-paydown-guide.html"))
	svg := regexp.MustCompile(`(?s)<svg[^>]*>.*?</svg>`).FindString(out)
	if svg == "" {
		t.Fatal("the post's diagram is not on the page")
	}
	for _, want := range []string{
		`class="vp-svg"`,
		`<text x="40" y="64" font-family="ui-monospace,monospace" font-size="9" fill="#dce6ff">STANDING ALLOCATION: 15-20% per team, per quarter</text>`,
		`<rect x="0" y="0" width="720" height="150" fill="#0b1220" rx="8">`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the diagram lacks %s\n%s", want, svg)
		}
	}
	if strings.Contains(svg, "style=") {
		t.Error("the diagram keeps a style attribute, which the CSP refuses")
	}
}

// One seed per rule: each carries one thing the policy must take out, and the
// check names that thing alone.
func TestADiagramKeepsNothingThatRuns(t *testing.T) {
	Init(t.TempDir())
	for _, c := range []struct {
		name, in, never string
	}{
		{"a script", `<svg viewBox="0 0 10 10"><script>alert(1)</script><rect width="1" height="1"/></svg>`, "alert"},
		{"an event handler", `<svg viewBox="0 0 10 10" onload="alert(1)"><rect width="1" height="1"/></svg>`, "onload"},
		{"foreignObject's HTML", `<svg viewBox="0 0 10 10"><foreignObject><p>loose words</p></foreignObject></svg>`, "loose words"},
		{"a link", `<svg viewBox="0 0 10 10"><a href="javascript:alert(1)"><text>x</text></a></svg>`, "javascript"},
		{"a paint from elsewhere", `<svg viewBox="0 0 10 10"><rect width="1" height="1" fill="url(https://evil.example/p.svg#a)"/></svg>`, "evil.example"},
		{"a background from elsewhere", `<svg viewBox="0 0 10 10" style="background:url(https://evil.example/b.png)"><rect width="1" height="1"/></svg>`, "evil.example"},
		{"an image", `<svg viewBox="0 0 10 10"><image href="https://evil.example/i.png" width="1" height="1"/></svg>`, "evil.example"},
		{"use", `<svg viewBox="0 0 10 10"><use href="https://evil.example/s.svg#x"/></svg>`, "evil.example"},
		{"a style sheet", `<svg viewBox="0 0 10 10"><style>body{display:none}</style><rect width="1" height="1"/></svg>`, "display:none"},
		{"an animation element", `<svg viewBox="0 0 10 10"><rect width="1" height="1"><set fill="red"/></rect></svg>`, "<set"},
		{"an animation attribute", `<svg viewBox="0 0 10 10"><rect width="1" height="1" attributeName="fill"/></svg>`, "attributename"},
	} {
		out := renderContentHTML(c.in)
		if strings.Contains(out, c.never) {
			t.Errorf("%s survived: %s", c.name, out)
		}
		if !strings.Contains(out, `<svg`) {
			t.Errorf("%s: the drawing around it was lost too: %s", c.name, out)
		}
	}
}

// A background the drawing cannot take leaves no backing at all: a rectangle
// without its fill would paint the drawing black.
func TestARefusedBackgroundLeavesNoBacking(t *testing.T) {
	Init(t.TempDir())
	out := renderContentHTML(`<svg viewBox="0 0 10 10" style="background:url(https://evil.example/b.png)"><circle r="1"/></svg>`)
	if strings.Contains(out, "<rect") {
		t.Errorf("a refused background left a backing: %s", out)
	}
}

// "<svg" written inside an attribute is text, not a drawing: it stays in the
// attribute, escaped, and no markup is put back into the tag.
func TestSVGInAnAttributeIsNotADrawing(t *testing.T) {
	Init(t.TempDir())
	out := renderContentHTML(`<p><a href="/x" title='<svg aria-label="a onmouseover=alert(1) "></svg>'>x</a></p>`)
	if strings.Contains(out, "<svg") {
		t.Errorf("an attribute's text was drawn: %s", out)
	}
	if regexp.MustCompile(`<a [^>]*\sonmouseover`).MatchString(out) {
		t.Errorf("the link took an attribute from the text: %s", out)
	}
}

// Two diagrams in one post each keep their own marker, and their references
// follow: "arrk" in both would otherwise draw the second's arrows with the
// first's.
func TestEachDiagramKeepsItsOwnIDs(t *testing.T) {
	Init(t.TempDir())
	one := `<svg viewBox="0 0 10 10"><defs><marker id="arrk" viewBox="0 0 10 10"><path d="M0,0L10,5L0,10z"/></marker></defs><path d="M1,1 L9,9" marker-end="url(#arrk)"/></svg>`
	out := renderContentHTML(one + one)
	for _, want := range []string{`id="vp-d0-arrk"`, `marker-end="url(#vp-d0-arrk)"`, `id="vp-d1-arrk"`, `marker-end="url(#vp-d1-arrk)"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s: %s", want, out)
		}
	}
}

// A text's style becomes its attributes, where the CSP lets a page draw them.
func TestADiagramsStyleBecomesAttributes(t *testing.T) {
	Init(t.TempDir())
	out := renderContentHTML(`<svg viewBox="0 0 10 10"><text x="1" y="5" style="fill:#ffe08a; font-size:9px">label</text></svg>`)
	if !strings.Contains(out, `fill="#ffe08a"`) || !strings.Contains(out, `font-size="9px"`) {
		t.Errorf("the text's style did not become attributes: %s", out)
	}
}

// A post with no drawing is not reparsed: its markup reaches the article
// policy exactly as written.
func TestAPostWithoutADiagramIsUntouched(t *testing.T) {
	in := `<p>One<br>two <b>three</p><ul><li>a<li>b</ul>`
	if got, drawings := liftDiagrams(in, "n"); got != in || drawings != nil {
		t.Errorf("a post without <svg> was rewritten: %q", got)
	}
}

// Halcyon's small capitals stop at a diagram: a span inside <text> would not
// be drawn.
func TestHalcyonLeavesADiagramsCapitals(t *testing.T) {
	withHalcyon(t, map[string]string{"smallcaps": "on"})
	out := halcyonArticle(t, `<p>The VISIBLE case.</p><svg viewBox="0 0 10 10"><text x="1" y="5">STANDING ALLOCATION</text></svg>`)
	if !strings.Contains(out, `<text x="1" y="5">STANDING ALLOCATION</text>`) {
		t.Errorf("the diagram's capitals were dressed: %s", regexp.MustCompile(`(?s)<svg[^>]*vp-svg.*?</svg>`).FindString(out))
	}
}
