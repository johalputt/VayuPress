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
	if _, err := Render("just some prose"); err != ErrUnsupported {
		t.Errorf("prose: want ErrUnsupported, got %v", err)
	}
}

func TestRenderPieBasic(t *testing.T) {
	src := "pie title Pets\n  \"Dogs\" : 5\n  \"Cats\" : 3\n  \"Fish\" : 2"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render pie: %v", err)
	}
	for _, want := range []string{"vp-diagram--pie", "Pets", "Dogs", "Cats", "Fish", "vp-diagram__slice--0"} {
		if !strings.Contains(svg, want) {
			t.Errorf("pie svg missing %q", want)
		}
	}
	if strings.Contains(svg, "<script") {
		t.Errorf("pie svg contained <script>")
	}
}

func TestRenderPieSingleSlice(t *testing.T) {
	src := "pie\n  \"Only\" : 100"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render single-slice pie: %v", err)
	}
	if !strings.Contains(svg, "<circle") {
		t.Errorf("single-slice pie should use <circle>, got: %.80s", svg)
	}
}

func TestRenderStateBasic(t *testing.T) {
	src := "stateDiagram-v2\n  [*] --> Idle\n  Idle --> Running : start\n  Running --> [*]"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render state: %v", err)
	}
	for _, want := range []string{"vp-diagram--state", "Idle", "Running", "vp-diagram__statepoint"} {
		if !strings.Contains(svg, want) {
			t.Errorf("state svg missing %q", want)
		}
	}
}

func TestRenderClassBasic(t *testing.T) {
	src := "classDiagram\n  class Animal {\n    +name string\n    +speak()\n  }\n  class Dog\n  Animal <|-- Dog"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render class: %v", err)
	}
	for _, want := range []string{"vp-diagram--class", "Animal", "Dog", "speak"} {
		if !strings.Contains(svg, want) {
			t.Errorf("class svg missing %q", want)
		}
	}
}

func TestRenderGanttBasic(t *testing.T) {
	src := "gantt\n  title Project\n  section Phase1\n  Task A : a1, 2024-01-01, 7d\n  Task B : after a1, 5d"
	svg, err := Render(src)
	if err != nil {
		t.Fatalf("render gantt: %v", err)
	}
	for _, want := range []string{"vp-diagram--gantt", "Project", "Task A", "Task B"} {
		if !strings.Contains(svg, want) {
			t.Errorf("gantt svg missing %q", want)
		}
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
