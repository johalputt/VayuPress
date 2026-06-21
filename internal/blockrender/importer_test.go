package blockrender

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImportHTMLBasicBlocks(t *testing.T) {
	html := `<h2>Title</h2>
<p>First paragraph with <strong>bold</strong> text.</p>
<ul><li>one</li><li>two</li></ul>
<ol><li>step 1</li><li>step 2</li></ol>
<blockquote>Wise words</blockquote>
<pre><code class="language-go">fmt.Println("hi")</code></pre>
<hr>
<img src="/media/x.png" alt="a cat">`

	blocks := ImportHTML(html)

	wantTypes := []string{"heading", "paragraph", "list", "list", "quote", "code", "divider", "image"}
	if len(blocks) != len(wantTypes) {
		t.Fatalf("got %d blocks, want %d: %+v", len(blocks), len(wantTypes), blocks)
	}
	for i, wt := range wantTypes {
		if blocks[i].Type != wt {
			t.Errorf("block %d: type %q, want %q", i, blocks[i].Type, wt)
		}
	}

	if blocks[0].Level != 2 {
		t.Errorf("heading level: got %d want 2", blocks[0].Level)
	}
	if !strings.Contains(blocks[1].Text, "bold") {
		t.Errorf("paragraph lost inline text: %q", blocks[1].Text)
	}
	if len(blocks[2].Items) != 2 || blocks[2].Style != "unordered" {
		t.Errorf("ul mismapped: %+v", blocks[2])
	}
	if blocks[3].Style != "ordered" {
		t.Errorf("ol should be ordered: %+v", blocks[3])
	}
	if blocks[5].Lang != "go" {
		t.Errorf("code lang: got %q want go", blocks[5].Lang)
	}
	if !strings.Contains(blocks[5].Text, "Println") {
		t.Errorf("code text lost: %q", blocks[5].Text)
	}
	if blocks[7].URL != "/media/x.png" || blocks[7].Alt != "a cat" {
		t.Errorf("image mismapped: %+v", blocks[7])
	}
}

// H1 clamps to 2, H6 clamps to 4 (editor supports levels 2..4).
func TestImportHTMLHeadingClamp(t *testing.T) {
	blocks := ImportHTML(`<h1>Big</h1><h6>Small</h6>`)
	if len(blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Level != 2 {
		t.Errorf("h1 should clamp to level 2, got %d", blocks[0].Level)
	}
	if blocks[1].Level != 4 {
		t.Errorf("h6 should clamp to level 4, got %d", blocks[1].Level)
	}
}

// An image wrapped in a <p> or <figure> becomes a lone image block, not a paragraph.
func TestImportHTMLLoneImage(t *testing.T) {
	blocks := ImportHTML(`<p><img src="/m/a.jpg" alt="x"></p><figure><img src="/m/b.jpg" alt="y"><figcaption>cap</figcaption></figure>`)
	if len(blocks) != 2 {
		t.Fatalf("want 2 image blocks, got %d: %+v", len(blocks), blocks)
	}
	for i, b := range blocks {
		if b.Type != "image" {
			t.Errorf("block %d: type %q want image", i, b.Type)
		}
	}
}

// Empty / whitespace-only input yields a single empty paragraph so the editor
// always has something to hydrate.
func TestImportHTMLEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t "} {
		blocks := ImportHTML(in)
		if len(blocks) != 1 || blocks[0].Type != "paragraph" {
			t.Errorf("ImportHTML(%q): want single paragraph, got %+v", in, blocks)
		}
	}
}

// Round-trip: imported blocks must render without error through Render.
func TestImportHTMLRendersCleanly(t *testing.T) {
	blocks := ImportHTML(`<h2>Hi</h2><p>Body</p><ul><li>a</li></ul>`)
	raw, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := Render(string(raw))
	if err != nil {
		t.Fatalf("Render failed on imported blocks: %v", err)
	}
	if !strings.Contains(out, "Hi") || !strings.Contains(out, "Body") {
		t.Errorf("rendered output missing content: %q", out)
	}
}

// Script content must never survive as executable markup after import+render.
func TestImportHTMLDropsScripts(t *testing.T) {
	blocks := ImportHTML(`<p>safe</p><script>alert(1)</script>`)
	raw, _ := json.Marshal(blocks)
	out, _, err := Render(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<script>") {
		t.Errorf("script tag survived conversion: %q", out)
	}
}
