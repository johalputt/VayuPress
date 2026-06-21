package diagram

import (
	"fmt"
	"regexp"
	"strings"
)

// class.go — parser and emitter for `classDiagram`. Supported:
// class declarations, attributes/methods within class bodies, and relationships:
// <|-- (inheritance), *-- (composition), o-- (aggregation), --> (association),
// -- (link), ..> (dependency), ..|> (realization). Labels via `: text`.

var classRelRe = regexp.MustCompile(`^(.+?)\s*((?:<\|--|<\|\.\.|\*--|o--|-->|--\.|<--|\.\.>|<\.\.|--|\.\.))\s*(.+?)(?:\s*:\s*(.*))?$`)

type classNode struct {
	id      string
	label   string
	members []string
	x, y    float64
	w, h    float64
	col     int
	row     int
}

type classEdge struct {
	from, to string
	rel      string
	label    string
}

func renderClass(src string) (string, error) {
	nodes := map[string]*classNode{}
	var order []string
	var edges []classEdge
	var currentClass string

	ensure := func(id string) {
		id = strings.TrimSpace(id)
		if _, ok := nodes[id]; !ok {
			nodes[id] = &classNode{id: id, label: id}
			order = append(order, id)
		}
		currentClass = id
	}

	for _, raw := range strings.Split(src, "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "%%") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(l), "classdiagram") {
			currentClass = ""
			continue
		}
		l = strings.TrimSuffix(l, ";")

		// class Body { ... } or class Body
		if strings.HasPrefix(strings.ToLower(l), "class ") {
			parts := strings.Fields(l)
			if len(parts) >= 2 {
				ensure(parts[1])
			}
			continue
		}
		// closing brace
		if l == "}" {
			currentClass = ""
			continue
		}
		// relationship line
		if m := classRelRe.FindStringSubmatch(l); m != nil {
			fromID := strings.TrimSpace(m[1])
			toID := strings.TrimSpace(m[3])
			ensure(fromID)
			ensure(toID)
			edges = append(edges, classEdge{from: fromID, to: toID, rel: strings.TrimSpace(m[2]), label: strings.TrimSpace(m[4])})
			currentClass = ""
			continue
		}
		// member inside a class (indented or after class open brace)
		if currentClass != "" && l != "{" {
			if n, ok := nodes[currentClass]; ok {
				n.members = append(n.members, l)
			}
		}
	}

	if len(nodes) == 0 {
		return "", fmt.Errorf("class: no classes parsed")
	}
	return emitClass(nodes, order, edges), nil
}

const (
	clsFont     = 13.0
	clsPadX     = 14.0
	clsHeaderH  = 30.0
	clsMemberH  = 20.0
	clsNodeGap  = 28.0
	clsLayerGap = 48.0
	clsMargin   = 20.0
)

func emitClass(nodes map[string]*classNode, order []string, edges []classEdge) string {
	// Simple grid layout: up to 4 columns.
	cols := 4
	if len(order) < 4 {
		cols = len(order)
	}

	// Size each node.
	for _, id := range order {
		n := nodes[id]
		w := approxTextWidth(n.label, clsFont) + 2*clsPadX
		for _, m := range n.members {
			mw := approxTextWidth(m, clsFont) + 2*clsPadX
			if mw > w {
				w = mw
			}
		}
		if w < 80 {
			w = 80
		}
		n.w = w
		n.h = clsHeaderH + float64(len(n.members))*clsMemberH
		if len(n.members) > 0 {
			n.h += 8 // separator padding
		}
	}

	// Assign grid positions.
	colW := make([]float64, cols)
	rowH := map[int]float64{}
	for i, id := range order {
		n := nodes[id]
		col := i % cols
		row := i / cols
		n.col = col
		n.row = row
		if n.w > colW[col] {
			colW[col] = n.w
		}
		if n.h > rowH[row] {
			rowH[row] = n.h
		}
	}

	colX := make([]float64, cols)
	x := clsMargin
	for c := 0; c < cols; c++ {
		colX[c] = x
		x += colW[c] + clsNodeGap
	}
	rowY := map[int]float64{}
	y := clsMargin
	maxRow := 0
	for _, id := range order {
		if nodes[id].row > maxRow {
			maxRow = nodes[id].row
		}
	}
	for r := 0; r <= maxRow; r++ {
		rowY[r] = y
		y += rowH[r] + clsLayerGap
	}

	for _, id := range order {
		n := nodes[id]
		n.x = colX[n.col]
		n.y = rowY[n.row]
	}

	canvasW := x - clsNodeGap + clsMargin
	canvasH := y - clsLayerGap + clsMargin

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="vp-diagram vp-diagram--class" viewBox="0 0 %s %s" role="img" aria-label="Class diagram" preserveAspectRatio="xMidYMid meet">`,
		num(canvasW), num(canvasH))
	b.WriteString(`<defs><marker id="vp-arrow" class="vp-diagram__arrowmarker" markerWidth="10" markerHeight="10" refX="8" refY="3" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L8,3 L0,6 Z" class="vp-diagram__arrowhead"></path></marker><marker id="vp-arrow-open" class="vp-diagram__arrowmarker" markerWidth="10" markerHeight="10" refX="8" refY="3" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L8,3 L0,6" fill="none" class="vp-diagram__arrowhead"></path></marker><marker id="vp-diamond" class="vp-diagram__arrowmarker" markerWidth="12" markerHeight="8" refX="10" refY="4" orient="auto" markerUnits="strokeWidth"><polygon points="0,4 5,0 10,4 5,8" class="vp-diagram__arrowhead"></polygon></marker><marker id="vp-diamond-open" class="vp-diagram__arrowmarker" markerWidth="12" markerHeight="8" refX="10" refY="4" orient="auto" markerUnits="strokeWidth"><polygon points="0,4 5,0 10,4 5,8" fill="none" class="vp-diagram__arrowhead"></polygon></marker></defs>`)

	// Edges first (under nodes).
	for _, e := range edges {
		from, to := nodes[e.from], nodes[e.to]
		if from == nil || to == nil {
			continue
		}
		fnFrom := &fcNode{x: from.x, y: from.y, w: from.w, h: from.h}
		fnTo := &fcNode{x: to.x, y: to.y, w: to.w, h: to.h}
		x1, y1, x2, y2 := edgeAnchors(fnFrom, fnTo, false, "TD")
		dashed := strings.Contains(e.rel, ".")
		cls := "vp-diagram__edge"
		if dashed {
			cls += " vp-diagram__edge--dashed"
		}
		marker := "url(#vp-arrow)"
		if strings.Contains(e.rel, "o") {
			marker = "url(#vp-diamond-open)" // aggregation: hollow diamond
		} else if strings.Contains(e.rel, "*") {
			marker = "url(#vp-diamond)" // composition: filled diamond
		}
		fmt.Fprintf(&b, `<line class="%s" x1="%s" y1="%s" x2="%s" y2="%s" marker-end="%s"></line>`,
			cls, num(x1), num(y1), num(x2), num(y2), marker)
		if e.label != "" {
			fmt.Fprintf(&b, `<text class="vp-diagram__edgelabel" x="%s" y="%s" text-anchor="middle" dominant-baseline="middle" font-size="12">%s</text>`,
				num((x1+x2)/2), num((y1+y2)/2-4), esc(e.label))
		}
	}

	// Nodes.
	for _, id := range order {
		n := nodes[id]
		// Box outline.
		fmt.Fprintf(&b, `<rect class="vp-diagram__node vp-diagram__node--class" x="%s" y="%s" width="%s" height="%s" rx="4" ry="4"></rect>`,
			num(n.x), num(n.y), num(n.w), num(n.h))
		// Header label.
		fmt.Fprintf(&b, `<text class="vp-diagram__label vp-diagram__label--class" x="%s" y="%s" text-anchor="middle" dominant-baseline="central" font-size="%s">%s</text>`,
			num(n.x+n.w/2), num(n.y+clsHeaderH/2), num(clsFont), esc(n.label))
		// Separator line.
		if len(n.members) > 0 {
			fmt.Fprintf(&b, `<line class="vp-diagram__edge" x1="%s" y1="%s" x2="%s" y2="%s"></line>`,
				num(n.x), num(n.y+clsHeaderH), num(n.x+n.w), num(n.y+clsHeaderH))
		}
		// Members.
		for i, m := range n.members {
			my := n.y + clsHeaderH + 8 + float64(i)*clsMemberH + clsMemberH/2
			fmt.Fprintf(&b, `<text class="vp-diagram__label" x="%s" y="%s" dominant-baseline="central" font-size="12">%s</text>`,
				num(n.x+clsPadX), num(my), esc(m))
		}
	}

	b.WriteString(`</svg>`)
	return b.String()
}
