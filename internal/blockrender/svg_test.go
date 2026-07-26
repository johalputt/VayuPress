// SPDX-License-Identifier: Apache-2.0

package blockrender

import (
	"strings"
	"testing"
)

// TestRawSVGInDiagramBlockRenders confirms a raw SVG pasted into a diagram block
// survives to the output (it is NOT stripped to run-together text) and keeps its
// geometry (viewBox), while a <script> inside it is removed.
func TestRawSVGInDiagramBlockRenders(t *testing.T) {
	svg := `<svg viewBox="0 0 100 40" xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><text x="0" y="20">Step 1</text></svg>`
	out, _, err := Render(`[{"type":"diagram","text":` + jsonString(svg) + `}]`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<svg") || !strings.Contains(out, "viewBox=\"0 0 100 40\"") {
		t.Errorf("SVG (with viewBox) should render inline, got:\n%s", out)
	}
	if !strings.Contains(out, "vp-svg-figure") {
		t.Errorf("SVG should be wrapped in the svg figure, got:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "<script") {
		t.Errorf("<script> must be stripped from a rendered SVG, got:\n%s", out)
	}
}

// TestRawSVGInHTMLBlockRenders confirms an SVG pasted into an HTML card also
// survives (the UGC pass would otherwise strip every SVG element — the exact
// "run-together text" bug).
func TestRawSVGInHTMLBlockRenders(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`
	out, _, err := Render(`[{"type":"html","text":` + jsonString(svg) + `}]`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<svg") || !strings.Contains(out, "<rect") {
		t.Errorf("SVG in an HTML card should render, got:\n%s", out)
	}
}

// TestSanitizeSVGStripsActiveContent covers the sanitiser's removals directly.
func TestSanitizeSVGStripsActiveContent(t *testing.T) {
	in := `<svg onload="steal()" xmlns="http://www.w3.org/2000/svg">` +
		`<script>alert(1)</script>` +
		`<style>@import url(//evil)</style>` +
		`<foreignObject><iframe src="//evil"></iframe></foreignObject>` +
		`<a href="javascript:evil()">x</a>` +
		`<image href="https://evil.example/track.png"/>` +
		`<use href="#local"/>` +
		`<circle cx="5" cy="5" r="4" fill="url(#g)"/></svg>`
	out, ok := SanitizeSVG(in)
	if !ok {
		t.Fatal("expected the SVG to sanitise to a valid result")
	}
	low := strings.ToLower(out)
	for _, bad := range []string{"<script", "onload=", "<style", "<foreignobject", "javascript:", "evil.example"} {
		if strings.Contains(low, bad) {
			t.Errorf("sanitised SVG must not contain %q, got:\n%s", bad, out)
		}
	}
	// Safe internals are preserved.
	if !strings.Contains(out, "<circle") || !strings.Contains(out, "url(#g)") {
		t.Errorf("safe SVG shapes / local refs must be kept, got:\n%s", out)
	}
	if !strings.Contains(out, `href="#local"`) {
		t.Errorf("a local #fragment href must be kept, got:\n%s", out)
	}
}

func TestLooksLikeSVG(t *testing.T) {
	yes := []string{
		`<svg></svg>`,
		"  <svg viewBox='0 0 1 1'></svg>",
		`<?xml version="1.0"?><svg></svg>`,
	}
	no := []string{"", "hello", "<p>x</p>", "flowchart TD\n A-->B", "svg not a tag"}
	for _, s := range yes {
		if !LooksLikeSVG(s) {
			t.Errorf("LooksLikeSVG(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if LooksLikeSVG(s) {
			t.Errorf("LooksLikeSVG(%q) = true, want false", s)
		}
	}
}

// jsonString quotes s as a JSON string literal for embedding in block JSON.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
