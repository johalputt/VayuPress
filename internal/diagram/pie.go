package diagram

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// pie.go — parser and SVG emitter for `pie` charts. Supported:
//
//	pie title Pets
//	  "Dogs" : 5
//	  "Cats" : 3
//
// `showData` is accepted (values are always shown in the legend). Slice colours
// are CSS classes (vp-diagram__slice--0..7) so the chart stays themeable.

type pieSlice struct {
	label string
	value float64
}

var pieRowRe = regexp.MustCompile(`^\s*"([^"]*)"\s*:\s*([0-9]+(?:\.[0-9]+)?)\s*$`)

func renderPie(src string) (string, error) {
	var title string
	var slices []pieSlice
	var total float64

	for _, raw := range strings.Split(src, "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "%%") {
			continue
		}
		low := strings.ToLower(l)
		if strings.HasPrefix(low, "pie") {
			rest := strings.TrimSpace(l[3:])
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "showData"))
			if strings.HasPrefix(strings.ToLower(rest), "title") {
				title = strings.TrimSpace(rest[5:])
			}
			continue
		}
		if strings.HasPrefix(low, "title") {
			title = strings.TrimSpace(l[5:])
			continue
		}
		if m := pieRowRe.FindStringSubmatch(l); m != nil {
			v, _ := strconv.ParseFloat(m[2], 64)
			slices = append(slices, pieSlice{label: m[1], value: v})
			total += v
		}
	}

	if len(slices) == 0 || total <= 0 {
		return "", fmt.Errorf("pie: no data rows parsed")
	}
	return emitPie(title, slices, total), nil
}

const (
	pieR        = 120.0
	pieCx       = 140.0
	pieTop      = 16.0
	pieLegendW  = 220.0
	pieRowH     = 22.0
	pieTitlePad = 30.0
)

func emitPie(title string, slices []pieSlice, total float64) string {
	titleH := 0.0
	if title != "" {
		titleH = pieTitlePad
	}
	cy := pieTop + titleH + pieR
	legendH := float64(len(slices))*pieRowH + 2*pieTop
	diagramH := titleH + 2*pieR + 2*pieTop
	canvasH := math.Max(diagramH, legendH+titleH)
	canvasW := pieCx + pieR + 24 + pieLegendW

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="vp-diagram vp-diagram--pie" viewBox="0 0 %s %s" role="img" aria-label="Pie chart" preserveAspectRatio="xMidYMid meet">`,
		num(canvasW), num(canvasH))

	if title != "" {
		fmt.Fprintf(&b, `<text class="vp-diagram__title" x="%s" y="%s" text-anchor="middle" font-size="16" font-weight="600">%s</text>`,
			num(pieCx), num(pieTop+16), esc(title))
	}

	angle := -math.Pi / 2 // start at 12 o'clock
	for i, s := range slices {
		frac := s.value / total
		sweep := frac * 2 * math.Pi
		x1 := pieCx + pieR*math.Cos(angle)
		y1 := cy + pieR*math.Sin(angle)
		angle += sweep
		x2 := pieCx + pieR*math.Cos(angle)
		y2 := cy + pieR*math.Sin(angle)
		large := 0
		if sweep > math.Pi {
			large = 1
		}
		cls := fmt.Sprintf("vp-diagram__slice vp-diagram__slice--%d", i%8)
		if len(slices) == 1 {
			// Single slice: a full circle (an arc of 2π is degenerate as a path).
			fmt.Fprintf(&b, `<circle class="%s" cx="%s" cy="%s" r="%s"></circle>`,
				cls, num(pieCx), num(cy), num(pieR))
		} else {
			fmt.Fprintf(&b, `<path class="%s" d="M%s,%s L%s,%s A%s,%s 0 %d 1 %s,%s Z"></path>`,
				cls, num(pieCx), num(cy), num(x1), num(y1), num(pieR), num(pieR), large, num(x2), num(y2))
		}
	}

	// Legend.
	lx := pieCx + pieR + 24
	ly := pieTop + titleH
	for i, s := range slices {
		pct := s.value / total * 100
		ry := ly + float64(i)*pieRowH
		fmt.Fprintf(&b, `<rect class="vp-diagram__slice--%d" x="%s" y="%s" width="14" height="14" rx="2"></rect>`,
			i%8, num(lx), num(ry))
		fmt.Fprintf(&b, `<text class="vp-diagram__legend" x="%s" y="%s" dominant-baseline="central" font-size="13">%s — %s (%s%%)</text>`,
			num(lx+22), num(ry+7), esc(s.label), num(s.value), num(math.Round(pct*10)/10))
	}

	b.WriteString(`</svg>`)
	return b.String()
}
