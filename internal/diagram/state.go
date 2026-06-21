package diagram

import (
	"fmt"
	"regexp"
	"strings"
)

// state.go — parser and emitter for `stateDiagram` / `stateDiagram-v2`. Supported:
// transitions `A --> B`, optional `: label`, and the `[*]` pseudo-state (initial
// when on the left, final when on the right), rendered as a filled circle. Layout
// reuses the flowchart layered engine; states are rounded boxes.

var stateEdgeRe = regexp.MustCompile(`^(.+?)\s*-->\s*([^:]+?)(?:\s*:\s*(.*))?$`)

func renderState(src string) (string, error) {
	nodes := map[string]*fcNode{}
	var order []string
	var edges []fcEdge
	seCount := 0

	ensure := func(token string) string {
		token = strings.TrimSpace(token)
		if token == "[*]" {
			// Each [*] occurrence is its own pseudo-state marker.
			id := fmt.Sprintf("__se_%d__", seCount)
			seCount++
			nodes[id] = &fcNode{id: id, label: "", shape: "se", layer: -1}
			order = append(order, id)
			return id
		}
		// Strip a quoted description if present: A : desc handled by caller.
		id := token
		if n, ok := nodes[id]; ok {
			_ = n
			return id
		}
		nodes[id] = &fcNode{id: id, label: id, shape: "round", layer: -1}
		order = append(order, id)
		return id
	}

	for _, raw := range strings.Split(src, "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "%%") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(l), "statediagram") {
			continue
		}
		l = strings.TrimSuffix(l, ";")
		if m := stateEdgeRe.FindStringSubmatch(l); m != nil {
			from := ensure(m[1])
			to := ensure(m[2])
			edges = append(edges, fcEdge{from: from, to: to, label: strings.TrimSpace(m[3])})
			continue
		}
		// Standalone `state Name` / bare identifier.
		f := strings.Fields(l)
		if len(f) >= 2 && strings.EqualFold(f[0], "state") {
			ensure(f[1])
		}
	}

	if len(nodes) == 0 {
		return "", fmt.Errorf("state: no states parsed")
	}
	assignLayers(nodes, order, edges)
	return emitState(nodes, order, edges), nil
}

func emitState(nodes map[string]*fcNode, order []string, edges []fcEdge) string {
	// Size nodes (top-down layout, like flowchart TD).
	for _, id := range order {
		n := nodes[id]
		if n.shape == "se" {
			n.w, n.h = 22, 22
			continue
		}
		n.w = approxTextWidth(n.label, fcFont) + 2*fcPadX
		if n.w < 54 {
			n.w = 54
		}
		n.h = fcNodeH
	}

	layers := map[int][]*fcNode{}
	maxLayer := 0
	for _, id := range order {
		n := nodes[id]
		layers[n.layer] = append(layers[n.layer], n)
		if n.layer > maxLayer {
			maxLayer = n.layer
		}
	}
	var crossExtent float64
	layerCross := map[int]float64{}
	for ly := 0; ly <= maxLayer; ly++ {
		var c float64
		for _, n := range layers[ly] {
			c += n.w + fcNodeGap
		}
		if c > 0 {
			c -= fcNodeGap
		}
		layerCross[ly] = c
		if c > crossExtent {
			crossExtent = c
		}
	}
	mainPos := fcMargin
	for ly := 0; ly <= maxLayer; ly++ {
		cross := fcMargin + (crossExtent-layerCross[ly])/2
		var ms float64
		for _, n := range layers[ly] {
			n.x = cross
			n.y = mainPos
			cross += n.w + fcNodeGap
			if n.h > ms {
				ms = n.h
			}
		}
		mainPos += ms + fcLayerGap
	}
	mainPos -= fcLayerGap
	canvasW := crossExtent + 2*fcMargin
	canvasH := mainPos + fcMargin

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="vp-diagram vp-diagram--state" viewBox="0 0 %s %s" role="img" aria-label="State diagram" preserveAspectRatio="xMidYMid meet">`,
		num(canvasW), num(canvasH))
	b.WriteString(`<defs><marker id="vp-arrow" class="vp-diagram__arrowmarker" markerWidth="10" markerHeight="10" refX="8" refY="3" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L8,3 L0,6 Z" class="vp-diagram__arrowhead"></path></marker></defs>`)

	for _, e := range edges {
		from, to := nodes[e.from], nodes[e.to]
		x1, y1, x2, y2 := edgeAnchors(from, to, false, "TD")
		fmt.Fprintf(&b, `<line class="vp-diagram__edge" x1="%s" y1="%s" x2="%s" y2="%s" marker-end="url(#vp-arrow)"></line>`,
			num(x1), num(y1), num(x2), num(y2))
		if e.label != "" {
			fmt.Fprintf(&b, `<text class="vp-diagram__edgelabel" x="%s" y="%s" text-anchor="middle" dominant-baseline="middle" font-size="12">%s</text>`,
				num((x1+x2)/2), num((y1+y2)/2-4), esc(e.label))
		}
	}

	for _, id := range order {
		n := nodes[id]
		if n.shape == "se" {
			fmt.Fprintf(&b, `<circle class="vp-diagram__statepoint" cx="%s" cy="%s" r="9"></circle>`,
				num(n.x+n.w/2), num(n.y+n.h/2))
			continue
		}
		fmt.Fprintf(&b, `<rect class="vp-diagram__node vp-diagram__node--round" x="%s" y="%s" width="%s" height="%s" rx="12" ry="12"></rect>`,
			num(n.x), num(n.y), num(n.w), num(n.h))
		fmt.Fprintf(&b, `<text class="vp-diagram__label" x="%s" y="%s" text-anchor="middle" dominant-baseline="central" font-size="%s">%s</text>`,
			num(n.x+n.w/2), num(n.y+n.h/2), num(fcFont), esc(n.label))
	}
	b.WriteString(`</svg>`)
	return b.String()
}
