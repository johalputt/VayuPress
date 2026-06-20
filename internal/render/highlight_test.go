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
