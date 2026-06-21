package diagram

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// flowchart.go — parser, layered (Sugiyama-style) layout, and SVG emitter for
// `flowchart`/`graph` diagrams. Supported: directions TD/TB/LR/RL/BT; node
// shapes rect [..], rounded (..), and diamond {..}; edges --> , --- , -.-> with
// optional |labels|. Cycles are handled defensively (layer assignment is capped).

type fcNode struct {
	id    string
	label string
	shape string // "rect" | "round" | "diamond"
	layer int
	order int
	x, y  float64
	w, h  float64
}

type fcEdge struct {
	from, to string
	label    string
	dashed   bool
}

var (
	// A[label] / A(label) / A{label}  (shape captured by bracket type)
	fcNodeRe = regexp.MustCompile(`^([A-Za-z0-9_]+)\s*(\[[^\]]*\]|\([^)]*\)|\{[^}]*\})?$`)
	// edge operator: -->  ---  -.->  ==>  with optional |label|
	fcEdgeRe = regexp.MustCompile(`^(.*?)\s*(-\.->|-->|---|==>)\s*(?:\|([^|]*)\|\s*)?(.*)$`)
)

func renderFlowchart(src string) (string, error) {
	lines := strings.Split(src, "\n")
	dir := "TD"
	nodes := map[string]*fcNode{}
	var order []string
	var edges []fcEdge

	ensure := func(token string) (string, bool) {
		token = strings.TrimSpace(token)
		if token == "" {
			return "", false
		}
		m := fcNodeRe.FindStringSubmatch(token)
		if m == nil {
			return "", false
		}
		id := m[1]
		n, ok := nodes[id]
		if !ok {
			n = &fcNode{id: id, label: id, shape: "rect", layer: -1, order: len(order)}
			nodes[id] = n
			order = append(order, id)
		}
		if m[2] != "" {
			body := m[2]
			switch body[0] {
			case '[':
				n.shape = "rect"
			case '(':
				n.shape = "round"
			case '{':
				n.shape = "diamond"
			}
			n.label = strings.TrimSpace(body[1 : len(body)-1])
			if n.label == "" {
				n.label = id
			}
		}
		return id, true
	}

	for i, raw := range lines {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "%%") {
			continue
		}
		if i == 0 || strings.HasPrefix(strings.ToLower(l), "flowchart") || strings.HasPrefix(strings.ToLower(l), "graph") {
			// header line: "flowchart LR" / "graph TD"
			fields := strings.Fields(l)
			if len(fields) >= 2 {
				d := strings.ToUpper(fields[1])
				switch d {
				case "TD", "TB", "LR", "RL", "BT":
					dir = d
				}
			}
			continue
		}
		l = strings.TrimSuffix(l, ";")
		if m := fcEdgeRe.FindStringSubmatch(l); m != nil {
			from, okF := ensure(m[1])
			to, okT := ensure(m[4])
			if okF && okT {
				edges = append(edges, fcEdge{from: from, to: to, label: strings.TrimSpace(m[3]), dashed: m[2] == "-.->"})
				continue
			}
		}
		// Standalone node declaration.
		ensure(l)
	}

	if len(nodes) == 0 {
		return "", fmt.Errorf("flowchart: no nodes parsed")
	}

	assignLayers(nodes, order, edges)
	return emitFlowchart(dir, nodes, order, edges), nil
}

// assignLayers gives each node a layer via a longest-path relaxation from roots
// (indegree 0). Iterations are capped at len(nodes) so cycles cannot loop.
func assignLayers(nodes map[string]*fcNode, order []string, edges []fcEdge) {
	indeg := map[string]int{}
	for _, id := range order {
		indeg[id] = 0
	}
	for _, e := range edges {
		indeg[e.to]++
	}
	for _, id := range order {
		if indeg[id] == 0 {
			nodes[id].layer = 0
		}
	}
	// Any node still unlayered (part of a cycle) seeds at layer 0.
	for _, id := range order {
		if nodes[id].layer < 0 {
			nodes[id].layer = 0
		}
	}
	// Longest-path relaxation, with layers clamped to len-1 so a cycle (which has
	// no valid topological layering) terminates with a compact result instead of
	// growing a new layer on every pass.
	maxL := len(order) - 1
	if maxL < 0 {
		maxL = 0
	}
	for iter := 0; iter < len(order)+1; iter++ {
		changed := false
		for _, e := range edges {
			want := nodes[e.from].layer + 1
			if want > maxL {
				want = maxL
			}
			if nodes[e.to].layer < want {
				nodes[e.to].layer = want
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	// Assign intra-layer order by first-seen index.
	counts := map[int]int{}
	for _, id := range order {
		n := nodes[id]
		n.order = counts[n.layer]
		counts[n.layer]++
	}
}

const (
	fcNodeH    = 42.0
	fcPadX     = 18.0
	fcLayerGap = 70.0
	fcNodeGap  = 28.0
	fcMargin   = 16.0
	fcFont     = 14.0
)

func emitFlowchart(dir string, nodes map[string]*fcNode, order []string, edges []fcEdge) string {
	// Size every node from its label.
	for _, id := range order {
		n := nodes[id]
		n.w = approxTextWidth(n.label, fcFont) + 2*fcPadX
		if n.shape == "diamond" {
			n.w += 16
		}
		if n.w < 54 {
			n.w = 54
		}
		n.h = fcNodeH
	}

	// Group by layer.
	layers := map[int][]*fcNode{}
	maxLayer := 0
	for _, id := range order {
		n := nodes[id]
		layers[n.layer] = append(layers[n.layer], n)
		if n.layer > maxLayer {
			maxLayer = n.layer
		}
	}

	horizontal := dir == "LR" || dir == "RL"
	// Cross-axis extent: widest layer determines canvas cross size.
	var crossExtent float64
	layerCross := map[int]float64{}
	for ly := 0; ly <= maxLayer; ly++ {
		var c float64
		for _, n := range layers[ly] {
			if horizontal {
				c += n.h + fcNodeGap
			} else {
				c += n.w + fcNodeGap
			}
		}
		if c > 0 {
			c -= fcNodeGap
		}
		layerCross[ly] = c
		if c > crossExtent {
			crossExtent = c
		}
	}

	// Position nodes. Main axis = layer index; cross axis = order within layer,
	// centred against the widest layer.
	mainPos := fcMargin
	layerMainSize := map[int]float64{}
	for ly := 0; ly <= maxLayer; ly++ {
		var ms float64
		for _, n := range layers[ly] {
			if horizontal {
				if n.w > ms {
					ms = n.w
				}
			} else {
				if n.h > ms {
					ms = n.h
				}
			}
		}
		layerMainSize[ly] = ms
	}
	for ly := 0; ly <= maxLayer; ly++ {
		cross := fcMargin + (crossExtent-layerCross[ly])/2
		for _, n := range layers[ly] {
			if horizontal {
				n.x = mainPos
				n.y = cross
				cross += n.h + fcNodeGap
			} else {
				n.x = cross
				n.y = mainPos
				cross += n.w + fcNodeGap
			}
		}
		mainPos += layerMainSize[ly] + fcLayerGap
	}
	mainPos -= fcLayerGap

	// Handle reversed directions by flipping the main axis.
	var canvasW, canvasH float64
	if horizontal {
		canvasW = mainPos + fcMargin
		canvasH = crossExtent + 2*fcMargin
	} else {
		canvasW = crossExtent + 2*fcMargin
		canvasH = mainPos + fcMargin
	}
	if dir == "RL" {
		for _, id := range order {
			nodes[id].x = canvasW - nodes[id].x - nodes[id].w
		}
	}
	if dir == "BT" {
		for _, id := range order {
			nodes[id].y = canvasH - nodes[id].y - nodes[id].h
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="vp-diagram vp-diagram--flowchart" viewBox="0 0 %s %s" role="img" aria-label="Flowchart" preserveAspectRatio="xMidYMid meet">`,
		num(canvasW), num(canvasH))
	b.WriteString(`<defs><marker id="vp-arrow" class="vp-diagram__arrowmarker" markerWidth="10" markerHeight="10" refX="8" refY="3" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L8,3 L0,6 Z" class="vp-diagram__arrowhead"></path></marker></defs>`)

	// Edges first (behind nodes).
	for _, e := range edges {
		from, to := nodes[e.from], nodes[e.to]
		x1, y1, x2, y2 := edgeAnchors(from, to, horizontal, dir)
		dash := ""
		if e.dashed {
			dash = ` stroke-dasharray="5 4"`
		}
		fmt.Fprintf(&b, `<line class="vp-diagram__edge" x1="%s" y1="%s" x2="%s" y2="%s"%s marker-end="url(#vp-arrow)"></line>`,
			num(x1), num(y1), num(x2), num(y2), dash)
		if e.label != "" {
			mx, my := (x1+x2)/2, (y1+y2)/2
			fmt.Fprintf(&b, `<text class="vp-diagram__edgelabel" x="%s" y="%s" text-anchor="middle" dominant-baseline="middle" font-size="12">%s</text>`,
				num(mx), num(my-4), esc(e.label))
		}
	}

	// Nodes.
	for _, id := range order {
		n := nodes[id]
		emitNode(&b, n)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func emitNode(b *strings.Builder, n *fcNode) {
	cx, cy := n.x+n.w/2, n.y+n.h/2
	switch n.shape {
	case "diamond":
		fmt.Fprintf(b, `<polygon class="vp-diagram__node vp-diagram__node--diamond" points="%s,%s %s,%s %s,%s %s,%s"></polygon>`,
			num(cx), num(n.y), num(n.x+n.w), num(cy), num(cx), num(n.y+n.h), num(n.x), num(cy))
	case "round":
		fmt.Fprintf(b, `<rect class="vp-diagram__node vp-diagram__node--round" x="%s" y="%s" width="%s" height="%s" rx="20" ry="20"></rect>`,
			num(n.x), num(n.y), num(n.w), num(n.h))
	default:
		fmt.Fprintf(b, `<rect class="vp-diagram__node" x="%s" y="%s" width="%s" height="%s" rx="6" ry="6"></rect>`,
			num(n.x), num(n.y), num(n.w), num(n.h))
	}
	fmt.Fprintf(b, `<text class="vp-diagram__label" x="%s" y="%s" text-anchor="middle" dominant-baseline="central" font-size="%s">%s</text>`,
		num(cx), num(cy), num(fcFont), esc(n.label))
}

// edgeAnchors returns start/end points on the node borders along the flow axis.
func edgeAnchors(from, to *fcNode, horizontal bool, dir string) (x1, y1, x2, y2 float64) {
	fcx, fcy := from.x+from.w/2, from.y+from.h/2
	tcx, tcy := to.x+to.w/2, to.y+to.h/2
	if horizontal {
		if from.x < to.x {
			return from.x + from.w, fcy, to.x, tcy
		}
		return from.x, fcy, to.x + to.w, tcy
	}
	if from.y < to.y {
		return fcx, from.y + from.h, tcx, to.y
	}
	return fcx, from.y, tcx, to.y + to.h
}

// num formats a float with up to one decimal and no trailing zeros.
func num(f float64) string {
	s := strconv.FormatFloat(f, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	return s
}
