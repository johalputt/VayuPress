package diagram

import (
	"strings"
	"testing"
)

func TestRenderFlowchartBasic(t *testing.T) {
	src := "flowchart TD\n  A[Start] --> B{Choice}\n  B -->|yes| C(Done)\n  B -->|no| A"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("expected svg root, got: %.40s", svg)
	}
	for _, want := range []string{"vp-diagram--flowchart", "Start", "Choice", "Done", "url(#vp-arrow)"} {
		if !strings.Contains(svg, want) {
			t.Errorf("flowchart svg missing %q", want)
		}
	}
	if strings.Contains(svg, "<script") || strings.Contains(svg, "foreignObject") {
		t.Errorf("svg contained forbidden element: %s", svg)
	}
}

func TestRenderSequenceBasic(t *testing.T) {
	src := "sequenceDiagram\n  participant A as Alice\n  participant B as Bob\n" +
		"  A->>B: Hello\n  B-->>A: Hi back\n  Note over A: thinking"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"vp-diagram--sequence", "Alice", "Bob", "Hello", "Hi back", "vp-diagram__lifeline"} {
		if !strings.Contains(svg, want) {
			t.Errorf("sequence svg missing %q", want)
		}
	}
}

func TestRenderEscapesLabels(t *testing.T) {
	src := "flowchart LR\n  A[\"<script>alert(1)</script>\"] --> B[ok]"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(svg, "<script>") {
		t.Errorf("label injection not escaped: %s", svg)
	}
	if !strings.Contains(svg, "&lt;script&gt;") {
		t.Errorf("expected escaped label, got: %s", svg)
	}
}

func TestRenderUnsupported(t *testing.T) {
	if _, err := Render(""); err != ErrUnsupported {
		t.Errorf("empty: want ErrUnsupported, got %v", err)
	}
	if _, err := Render("pie title Pets\n  \"Dogs\" : 5"); err != ErrUnsupported {
		t.Errorf("pie (unimplemented): want ErrUnsupported, got %v", err)
	}
	if _, err := Render("just some prose"); err != ErrUnsupported {
		t.Errorf("prose: want ErrUnsupported, got %v", err)
	}
}

func TestFlowchartHandlesCycle(t *testing.T) {
	// A cycle must not hang layer assignment.
	src := "graph LR\n A-->B\n B-->C\n C-->A"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("cycle render: %v", err)
	}
	if !strings.Contains(svg, "<svg") {
		t.Errorf("expected svg for cyclic graph")
	}
}
