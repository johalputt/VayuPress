package blockrender

import (
	"strings"
	"testing"
)

func TestRenderEmpty(t *testing.T) {
	h, txt, err := Render("")
	if err != nil || h != "" || txt != "" {
		t.Fatalf("empty input should yield empty output, got h=%q txt=%q err=%v", h, txt, err)
	}
}

func TestRenderInvalidJSON(t *testing.T) {
	if _, _, err := Render("not json"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRenderBasicBlocks(t *testing.T) {
	doc := `[
		{"type":"heading","level":2,"text":"Hello"},
		{"type":"paragraph","text":"World of blocks"},
		{"type":"list","style":"ordered","items":["one","two"]},
		{"type":"quote","text":"a quote"},
		{"type":"divider"}
	]`
	h, txt, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h2>Hello</h2>", "<p>World of blocks</p>", "<ol>", "<li>one</li>", "<blockquote>", "<hr"} {
		if !strings.Contains(h, want) {
			t.Errorf("output missing %q\nGOT: %s", want, h)
		}
	}
	if !strings.Contains(txt, "Hello") || !strings.Contains(txt, "World") {
		t.Errorf("excerpt missing text: %q", txt)
	}
}

// TestRenderSanitizesXSS is the security-critical case: script/event-handler
// injection in any text field must not survive into the rendered HTML.
func TestRenderSanitizesXSS(t *testing.T) {
	doc := `[
		{"type":"paragraph","text":"<script>alert(1)</script>"},
		{"type":"heading","level":2,"text":"<img src=x onerror=alert(2)>"},
		{"type":"image","url":"javascript:alert(3)","alt":"\"><script>x</script>"}
	]`
	h, _, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	// A live tag would appear with a literal "<" prefix; escaped text shows as
	// "&lt;". Assert no live <script>/<img> tag and no live javascript: URL.
	for _, bad := range []string{"<script", "<img", "javascript:alert"} {
		if strings.Contains(h, bad) {
			t.Errorf("XSS payload survived sanitisation: %q\nGOT: %s", bad, h)
		}
	}
}

func TestRenderCodeLanguageClassSafe(t *testing.T) {
	doc := `[{"type":"code","lang":"go\"><script>","text":"package main"}]`
	h, _, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "<script") {
		t.Errorf("code lang allowed attribute breakout: %s", h)
	}
	if !strings.Contains(h, "package main") {
		t.Errorf("code body missing: %s", h)
	}
}

func TestRenderUnknownBlockSkipped(t *testing.T) {
	doc := `[{"type":"future-widget","text":"x"},{"type":"paragraph","text":"ok"}]`
	h, _, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "<p>ok</p>") {
		t.Errorf("known block not rendered: %s", h)
	}
}

func TestExcerptTruncates(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := excerpt(long)
	if len([]rune(got)) > 210 {
		t.Errorf("excerpt too long: %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix: %q", got)
	}
}

func TestRenderVideoFacade(t *testing.T) {
	doc := `[{"type":"embed","kind":"video","url":"https://youtu.be/dQw4w9WgXcQ",` +
		`"title":"Demo","thumbURL":"/media/abc.jpg",` +
		`"embedSrc":"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"}]`
	h, _, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, `class="video-facade"`) {
		t.Errorf("facade wrapper missing: %s", h)
	}
	if !strings.Contains(h, `data-embed-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"`) {
		t.Errorf("validated embed src did not survive sanitization: %s", h)
	}
	if strings.Contains(h, "<iframe") {
		t.Errorf("facade must NOT contain an iframe (click-to-load only): %s", h)
	}
}

func TestRenderVideoFacadeRejectsForeignOrigin(t *testing.T) {
	// A crafted embedSrc pointing at a non-allowlisted origin must NOT appear and
	// the block must degrade to a safe link card.
	doc := `[{"type":"embed","kind":"video","url":"https://evil.example/x",` +
		`"title":"Bad","embedSrc":"https://evil.example/embed/x"}]`
	h, _, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "evil.example/embed") {
		t.Errorf("foreign embed origin leaked into output: %s", h)
	}
	if strings.Contains(h, "data-embed-src") {
		t.Errorf("invalid embed src must be dropped: %s", h)
	}
	if !strings.Contains(h, "embed-card") {
		t.Errorf("expected fallback link card: %s", h)
	}
}

func TestRenderDiagramBlock(t *testing.T) {
	doc := `[{"type":"diagram","text":"flowchart LR\n A[Start] --> B[End]"}]`
	h, _, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "<svg") || !strings.Contains(h, "vp-diagram--flowchart") {
		t.Errorf("diagram svg missing: %s", h)
	}
	if !strings.Contains(h, "vp-diagram-figure") {
		t.Errorf("figure wrapper missing: %s", h)
	}
}

func TestRenderDiagramBlockFallback(t *testing.T) {
	// Unsupported diagram type degrades to an escaped code block, not an error.
	doc := `[{"type":"diagram","text":"unknowndiagram\n  foo bar"}]`
	h, _, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, "vp-diagram-fallback") {
		t.Errorf("expected fallback code block: %s", h)
	}
	if strings.Contains(h, "<svg") {
		t.Errorf("unsupported diagram must not emit svg: %s", h)
	}
}

func TestRenderInlineFormatting(t *testing.T) {
	cases := []struct {
		name  string
		doc   string
		wants []string
	}{
		{
			name:  "bold and italic in paragraph",
			doc:   `[{"type":"paragraph","text":"a **bold** and *italic* word"}]`,
			wants: []string{"<strong>bold</strong>", "<em>italic</em>"},
		},
		{
			name:  "inline code",
			doc:   "[{\"type\":\"paragraph\",\"text\":\"run `go test` now\"}]",
			wants: []string{"<code>go test</code>"},
		},
		{
			name:  "link",
			doc:   `[{"type":"paragraph","text":"see [docs](https://example.com/x)"}]`,
			wants: []string{`href="https://example.com/x"`, "docs</a>"},
		},
		{
			name:  "strikethrough",
			doc:   `[{"type":"paragraph","text":"~~gone~~ kept"}]`,
			wants: []string{"<del>gone</del>"},
		},
		{
			name:  "heading keeps tag and adds emphasis",
			doc:   `[{"type":"heading","level":2,"text":"a **b**"}]`,
			wants: []string{"<h2>", "<strong>b</strong>", "</h2>"},
		},
		{
			name:  "list item formatting",
			doc:   `[{"type":"list","style":"unordered","items":["**one**","two"]}]`,
			wants: []string{"<li><strong>one</strong></li>", "<li>two</li>"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := Render(tc.doc)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, w := range tc.wants {
				if !strings.Contains(out, w) {
					t.Errorf("output missing %q\ngot: %s", w, out)
				}
			}
		})
	}
}

func TestRenderInlineStillStripsXSS(t *testing.T) {
	// A script tag and a javascript: link inside formatted text must not survive.
	doc := `[{"type":"paragraph","text":"hi <script>alert(1)</script> [x](javascript:alert(2)) **b**"}]`
	out, _, err := Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(strings.ToLower(out), "<script") {
		t.Fatalf("script tag survived: %s", out)
	}
	if strings.Contains(strings.ToLower(out), "javascript:") {
		t.Fatalf("javascript: URL survived: %s", out)
	}
	if !strings.Contains(out, "<strong>b</strong>") {
		t.Fatalf("legit formatting lost: %s", out)
	}
}
