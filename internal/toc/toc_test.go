package toc

import (
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	html := `<h2>First Section</h2><p>body</p><h3>Subsection</h3><h2>Second Section</h2>`
	entries := Extract(html)
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Anchor != "first-section" {
		t.Errorf("bad anchor: %q", entries[0].Anchor)
	}
	if entries[1].Level != 3 {
		t.Errorf("want level 3, got %d", entries[1].Level)
	}
}

func TestExtract_DuplicateAnchors(t *testing.T) {
	html := `<h2>Section</h2><h2>Section</h2>`
	entries := Extract(html)
	if entries[0].Anchor == entries[1].Anchor {
		t.Error("duplicate anchors should be disambiguated")
	}
}

func TestInjectAnchors(t *testing.T) {
	html := `<h2>My Heading</h2><p>text</p>`
	out := InjectAnchors(html)
	if !strings.Contains(out, `id="my-heading"`) {
		t.Errorf("anchor not injected: %s", out)
	}
}

func TestInjectAnchors_PreservesExisting(t *testing.T) {
	html := `<h2 id="custom">Heading</h2>`
	out := InjectAnchors(html)
	if !strings.Contains(out, `id="custom"`) {
		t.Errorf("existing id overwritten: %s", out)
	}
}

func TestRenderHTML(t *testing.T) {
	entries := []Entry{{Level: 2, Text: "First", Anchor: "first"}, {Level: 3, Text: "Sub", Anchor: "sub"}}
	html := RenderHTML(entries)
	if !strings.Contains(html, `href="#first"`) {
		t.Errorf("link missing: %s", html)
	}
	if !strings.Contains(html, `class="toc-h3"`) {
		t.Errorf("level class missing: %s", html)
	}
}
