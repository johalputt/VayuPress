// Package diagram is a dependency-free Mermaid→SVG compiler (ADR-0070, Phase 3).
//
// It renders a useful subset of Mermaid — flowcharts and sequence diagrams — to
// a static, themeable SVG entirely on the server: no headless browser, no Node,
// no client JavaScript, no eval. The output uses currentColor and CSS classes so
// it inherits the page theme, paints instantly, and prints perfectly, leaving
// the strict reader CSP untouched.
//
// Pipeline:  source → lexer/parser → AST → layout → SVG emitter → bluemonday SVG
// allowlist.  Anything unsupported returns ErrUnsupported so the caller can fall
// back to an annotated code block. The emitted SVG is sanitised through a closed
// allowlist policy (no <script>, no <foreignObject>, no event handlers) — the
// same no-raw-HTML-escape-hatch posture as the rest of the renderer.
package diagram

import (
	"errors"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// ErrUnsupported is returned when the source is empty or its diagram type is not
// implemented. Callers should degrade gracefully (e.g. render a code block).
var ErrUnsupported = errors.New("diagram: unsupported or empty source")

// svgPolicy is the closed allowlist applied to every emitted SVG. Only the
// presentational element/attribute set the emitters actually use is permitted;
// scripts, event handlers, foreignObject, and external references cannot survive.
var svgPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("svg", "g", "path", "rect", "circle", "line", "polyline",
		"polygon", "text", "tspan", "marker", "defs", "title")
	p.AllowAttrs("xmlns", "viewBox", "width", "height", "role",
		"aria-label", "class", "preserveAspectRatio").OnElements("svg")
	p.AllowAttrs("class", "transform").OnElements("g")
	p.AllowAttrs("d", "class", "marker-end", "marker-start", "fill", "stroke",
		"stroke-width", "stroke-dasharray").OnElements("path")
	p.AllowAttrs("x", "y", "width", "height", "rx", "ry", "class", "fill",
		"stroke", "stroke-width").OnElements("rect")
	p.AllowAttrs("cx", "cy", "r", "class", "fill", "stroke", "stroke-width").OnElements("circle")
	p.AllowAttrs("x1", "y1", "x2", "y2", "class", "stroke", "stroke-width",
		"stroke-dasharray", "marker-end").OnElements("line")
	p.AllowAttrs("points", "class", "fill", "stroke", "stroke-width").OnElements("polyline", "polygon")
	p.AllowAttrs("x", "y", "dx", "dy", "class", "text-anchor", "dominant-baseline",
		"fill", "font-size", "font-weight").OnElements("text", "tspan")
	p.AllowAttrs("id", "class", "markerWidth", "markerHeight", "refX", "refY",
		"orient", "markerUnits", "viewBox").OnElements("marker")
	p.AllowAttrs("class").OnElements("title")
	return p
}()

// Render compiles Mermaid source to a sanitised, themeable SVG fragment. It
// returns ErrUnsupported for empty input or an unimplemented diagram type, and a
// descriptive error for a malformed but recognised diagram.
func Render(source string) (string, error) {
	src := strings.TrimSpace(source)
	if src == "" {
		return "", ErrUnsupported
	}
	kind := detectKind(src)
	var raw string
	var err error
	switch kind {
	case kindFlowchart:
		raw, err = renderFlowchart(src)
	case kindSequence:
		raw, err = renderSequence(src)
	default:
		return "", ErrUnsupported
	}
	if err != nil {
		return "", err
	}
	return svgPolicy.Sanitize(raw), nil
}

type diagramKind int

const (
	kindUnknown diagramKind = iota
	kindFlowchart
	kindSequence
)

// detectKind inspects the first non-empty, non-directive line to choose a parser.
func detectKind(src string) diagramKind {
	for _, line := range strings.Split(src, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "%%") {
			continue
		}
		head := strings.ToLower(l)
		switch {
		case strings.HasPrefix(head, "sequencediagram"):
			return kindSequence
		case strings.HasPrefix(head, "flowchart"), strings.HasPrefix(head, "graph"):
			return kindFlowchart
		}
		return kindUnknown
	}
	return kindUnknown
}

// esc escapes text for safe inclusion in SVG text nodes and attribute values.
func esc(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

// approxTextWidth estimates a label's pixel width at the given font size using an
// average glyph advance — good enough for box sizing without font metrics.
func approxTextWidth(s string, fontSize float64) float64 {
	return float64(len([]rune(s))) * fontSize * 0.6
}
