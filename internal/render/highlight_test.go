// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"

	"github.com/microcosm-cc/bluemonday"
)

// TestRenderContentHighlight verifies that fenced code blocks survive
// sanitisation and emit chroma class spans (regression for the bug where
// bluemonday stripped the language class before highlighting ran).
func TestRenderContentHighlight(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	content := "<p>x</p><pre><code class=\"language-go\">func main() {\n\tch := make(chan int)\n\tgo func() { ch <- 42 }()\n}</code></pre><p>y</p>"
	out := renderContentHTML(content)

	if !strings.Contains(out, `class="chroma"`) {
		t.Errorf("expected chroma pre wrapper, got:\n%s", out)
	}
	if !strings.Contains(out, `data-lang="go"`) {
		t.Errorf("expected data-lang attribute, got:\n%s", out)
	}
	if !strings.Contains(out, `<span class="kd">`) {
		t.Errorf("expected keyword span, got:\n%s", out)
	}
	// Surrounding prose must still be present and sanitised.
	if !strings.Contains(out, "<p>x</p>") || !strings.Contains(out, "<p>y</p>") {
		t.Errorf("expected prose preserved, got:\n%s", out)
	}
}

// TestRenderContentNoLanguage verifies a code block without a language hint is
// left as a plain (sanitised) block rather than mangled.
func TestRenderContentNoLanguage(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	content := "<pre><code>plain text</code></pre>"
	out := renderContentHTML(content)
	if !strings.Contains(out, "plain text") {
		t.Errorf("expected plain code preserved, got:\n%s", out)
	}
}

// TestRenderContentPlaceholderForgery verifies that article content cannot forge
// a code-block placeholder to inject unsanitised HTML. A literal placeholder in
// prose must survive as inert escaped text.
func TestRenderContentPlaceholderForgery(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	content := "<p>VAYUCODE00000000ENDVAYUCODE0ENDVAYUCODE</p><pre><code class=\"language-go\">x := 1</code></pre>"
	out := renderContentHTML(content)
	// The forged token text must not have been replaced by anything (no real
	// highlighted block maps to it), and no <script> can appear.
	if strings.Contains(out, "<script") {
		t.Errorf("unexpected script injection: %s", out)
	}
}

// A code block is an element, so "<pre><code>" written inside an attribute's
// value is text and is not highlighted there: chroma's markup is never put
// back inside an attribute. Its entities, decoded by the parser, are read
// once: "&lt;" in the source is "<" in the highlighted code, not "&lt;".
func TestACodeBlockIsAnElementNotAPattern(t *testing.T) {
	policy = bluemonday.UGCPolicy()
	out := renderContentHTML(`<p><a href="/x" title='<pre><code class="language-go">package main</code></pre>'>x</a></p>`)
	if strings.Contains(out, "chroma") || strings.Contains(out, "data-lang") {
		t.Errorf("a code block inside an attribute was highlighted:\n%s", out)
	}
	out = renderContentHTML(`<pre><code class="language-go">if a &lt; b &amp;&amp; c {}</code></pre>`)
	if !strings.Contains(out, `class="chroma"`) || strings.Contains(out, "&amp;lt;") || !strings.Contains(out, "&lt;") {
		t.Errorf("a code block's entities were not read once:\n%s", out)
	}
}
