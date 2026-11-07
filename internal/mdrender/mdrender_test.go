// SPDX-License-Identifier: Apache-2.0

package mdrender

import (
	"strings"
	"testing"
	"time"
)

// TestABlockPostIsWrittenFromItsBlocks — the editor's text is already inline
// Markdown, so it is written as it is; the blocks give the structure.
func TestABlockPostIsWrittenFromItsBlocks(t *testing.T) {
	blocks := `[
		{"type":"heading","level":2,"text":"Why it matters"},
		{"type":"paragraph","text":"It is **bold** and [linked](https://example.com)."},
		{"type":"list","style":"ordered","items":["one","two"]},
		{"type":"code","lang":"go","text":"fmt.Println(1)"},
		{"type":"quote","text":"said once"},
		{"type":"image","url":"/media/a.webp","alt":"a cat","caption":"The cat"},
		{"type":"table","header":["k","v"],"rows":[["a","1|2"]]},
		{"type":"tasklist","items":["done","todo"],"checked":[true,false]},
		{"type":"divider"}
	]`
	got := RenderPost(Post{Title: "A post", URL: "https://example.com/a-post", Published: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC), Tags: []string{"go", "cats"}, BlocksJSON: blocks, HTML: "<p>ignored</p>"})
	for _, want := range []string{
		"# A post\n",
		"- URL: https://example.com/a-post\n",
		"- Published: 2026-11-07\n",
		"- Topics: go, cats\n",
		"## Why it matters",
		"It is **bold** and [linked](https://example.com).",
		"1. one\n2. two",
		"```go\nfmt.Println(1)\n```",
		"> said once",
		"![a cat](/media/a.webp)\n\n*The cat*",
		"| k | v |\n| --- | --- |\n| a | 1\\|2 |",
		"- [x] done\n- [ ] todo",
		"---",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ignored") {
		t.Errorf("a post with blocks was written from its HTML:\n%s", got)
	}
}

// TestAnHTMLPostIsWrittenFromItsOwnHTML — johal.in's posts are HTML with a
// style sheet of their own; the Markdown keeps what a reader reads and none
// of the style, script or controls.
func TestAnHTMLPostIsWrittenFromItsOwnHTML(t *testing.T) {
	src := `<style>.magazine-article { color: red; }</style>
<div class="magazine-article"><article>
<p class="lead">Master <strong>Telemetry</strong> with <em>Python</em>.</p>
<h2>Core Concepts</h2>
<ul><li><strong>Metrics:</strong> numbers over time</li><li>Traces<ul><li>spans</li></ul></li></ul>
<pre><code class="language-python">print("hi")
</code></pre>
<div class="code-header"><div class="code-lang">Python</div><button class="code-copy">Copy</button></div>
<blockquote><p>The best way to learn is by doing.</p></blockquote>
<table><thead><tr><th>Issue</th><th>Fix</th></tr></thead><tbody><tr><td>ImportError</td><td>Run <code>pip install</code></td></tr></tbody></table>
<p>See <a href="https://opentelemetry.io/">the docs</a> and <a href="javascript:alert(1)">this</a>.</p>
<img src="https://cdn.pixabay.com/photo/a.jpg" alt="graph">
<script>alert(1)</script>
</article></div>`
	got := FromHTML(src)
	for _, want := range []string{
		"Master **Telemetry** with *Python*.",
		"## Core Concepts",
		"- **Metrics:** numbers over time\n- Traces\n  - spans",
		"```python\nprint(\"hi\")\n```",
		"> The best way to learn is by doing.",
		"| Issue | Fix |\n| --- | --- |\n| ImportError | Run `pip install` |",
		"See [the docs](https://opentelemetry.io/) and this.",
		"![graph](https://cdn.pixabay.com/photo/a.jpg)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, never := range []string{"magazine-article", "color: red", "alert(1)", "Copy", "javascript:"} {
		if strings.Contains(got, never) {
			t.Errorf("%q reached the Markdown:\n%s", never, got)
		}
	}
}

// TestAListingNamesEachPostWithItsLink — the home listing's Markdown.
func TestAListingNamesEachPostWithItsLink(t *testing.T) {
	got := RenderListing("johal.in", []Item{
		{Title: "First [post]", URL: "https://johal.in/first", Excerpt: "  An   excerpt. ", Published: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)},
		{Title: "Second", URL: "https://johal.in/second"},
	})
	for _, want := range []string{
		"# johal.in\n",
		"## [First \\[post\\]](https://johal.in/first)\n\n2026-11-06\n\nAn excerpt.\n",
		"## [Second](https://johal.in/second)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestACodeBlockHoldingAFenceStaysOneBlock — a fence inside code would end
// the block early and spill the rest as Markdown.
func TestACodeBlockHoldingAFenceStaysOneBlock(t *testing.T) {
	got := FromHTML("<pre><code>```\nnot markdown\n```</code></pre>")
	if !strings.HasPrefix(got, "````\n```\nnot markdown\n```\n````") {
		t.Errorf("a fence inside code was not fenced longer:\n%s", got)
	}
}
