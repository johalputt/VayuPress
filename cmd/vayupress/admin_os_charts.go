package main

// admin_os_charts.go — server-rendered, CSP-safe charts for the VayuOS analytics
// console. Everything here emits static SVG/HTML with CSS classes (no inline
// styles, no external JS, no CDNs), so the strict admin CSP (style-src 'self',
// script-src 'self') is preserved. Percentages snap to the shared w-N width
// classes; colours come from the --chart-N palette in admin-os.css. GDPR posture
// is unchanged — these only visualise the existing aggregate, no-PII queries.

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
)

// prettyPathText returns a human-readable, un-escaped page path for chart labels
// (osBarList escapes it). Query/fragment stripped, percent-decoded, truncated.
func prettyPathText(p string) string {
	disp := p
	if i := strings.IndexAny(disp, "?#"); i >= 0 {
		disp = disp[:i]
	}
	if dec, err := url.QueryUnescape(disp); err == nil && dec != "" {
		disp = dec
	}
	if disp == "" {
		disp = "/"
	}
	if r := []rune(disp); len(r) > 48 {
		disp = string(r[:47]) + "…"
	}
	return disp
}

// osChartBar is one row of a horizontal bar list.
type osChartBar struct {
	Label     string
	LabelHTML string // optional pre-rendered (already-escaped) label, e.g. a flag + name
	Value     int
	Href      string // optional — makes the label a link
}

// barWidthClass snaps a 0..100 percentage to the nearest available w-N class
// (5% buckets) so bars need no inline width style.
func barWidthClass(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	b := ((pct + 2) / 5) * 5
	return "w-" + strconv.Itoa(b)
}

// osBarList renders a colour bar chart: each row is a label, a proportional
// coloured bar (relative to the largest value) and the count. Rows cycle through
// the 8-colour palette. Returns an empty-state when there is nothing to show.
func osBarList(items []osChartBar, emptyMsg string) string {
	if len(items) == 0 {
		if emptyMsg == "" {
			emptyMsg = "No data yet."
		}
		return `<div class="empty-state">` + emptyMsg + `</div>`
	}
	max := 1
	total := 0
	for _, it := range items {
		if it.Value > max {
			max = it.Value
		}
		total += it.Value
	}
	out := `<div class="vp-bars">`
	for i, it := range items {
		label := it.Label
		if label == "" {
			label = "(unknown)"
		}
		lab := html.EscapeString(label)
		if it.LabelHTML != "" {
			lab = it.LabelHTML // caller-escaped rich label (e.g. flag + country name)
		}
		if it.Href != "" {
			lab = `<a href="` + html.EscapeString(it.Href) + `">` + lab + `</a>`
		}
		pct := it.Value * 100 / max
		share := ""
		if total > 0 {
			share = `<span class="vp-bar__pct">` + strconv.Itoa(it.Value*100/total) + `%</span>`
		}
		c := (i % 8) + 1
		out += `<div class="vp-bar vp-bar--c` + strconv.Itoa(c) + `">` +
			`<span class="vp-bar__label" title="` + html.EscapeString(label) + `">` + lab + `</span>` +
			`<span class="vp-bar__val">` + humanCount(it.Value) + share + `</span>` +
			`<span class="vp-bar__track"><span class="vp-bar__fill ` + barWidthClass(pct) + `"></span></span>` +
			`</div>`
	}
	out += `</div>`
	return out
}

// osChartSeg is one slice of a donut chart.
type osChartSeg struct {
	Label string
	Value int
}

// osDonut renders a donut chart (SVG stroke-dasharray on r=15.915 so the
// circumference is 100 → dasharray values are percentages) plus a colour legend.
// stroke-dasharray/offset are presentation attributes, not inline styles, so it
// stays CSP-safe. At most 8 slices are drawn; the rest fold into "Other".
func osDonut(items []osChartSeg, emptyMsg string) string {
	if len(items) == 0 {
		if emptyMsg == "" {
			emptyMsg = "No data yet."
		}
		return `<div class="empty-state">` + emptyMsg + `</div>`
	}
	// Fold beyond 7 slices into an 8th "Other" so colours stay distinct.
	if len(items) > 8 {
		other := 0
		for _, it := range items[7:] {
			other += it.Value
		}
		items = append(items[:7:7], osChartSeg{Label: "Other", Value: other})
	}
	total := 0
	for _, it := range items {
		total += it.Value
	}
	if total <= 0 {
		return `<div class="empty-state">No data yet.</div>`
	}
	segs := ""
	legend := ""
	offset := 25.0 // start at 12 o'clock
	for i, it := range items {
		frac := float64(it.Value) / float64(total) * 100
		c := (i % 8) + 1
		// dashoffset walks backwards so slices sit clockwise from the top.
		segs += fmt.Sprintf(`<circle class="vp-donut__seg donut-c%d" cx="21" cy="21" r="15.915" stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f"></circle>`,
			c, frac, 100-frac, offset)
		offset -= frac
		if offset < 0 {
			offset += 100
		}
		legend += `<div class="vp-legend__item"><span class="vp-legend__dot legend-c` + strconv.Itoa(c) + `"></span>` +
			`<span class="vp-legend__label" title="` + html.EscapeString(it.Label) + `">` + html.EscapeString(it.Label) + `</span>` +
			`<span class="vp-legend__val">` + strconv.Itoa(int(frac+0.5)) + `%</span></div>`
	}
	return `<div class="vp-donut-wrap">` +
		`<svg class="vp-donut" viewBox="0 0 42 42" role="img" aria-hidden="true">` +
		`<circle class="vp-donut__track" cx="21" cy="21" r="15.915"></circle>` + segs + `</svg>` +
		`<div class="vp-legend">` + legend + `</div></div>`
}

// osTrendChart renders a two-series area+line chart: pageviews (filled area +
// line) and unique visitors (line), with light gridlines. Pure SVG + classes.
func osTrendChart(series []analytics.DayPageviews, title string) string {
	if len(series) == 0 {
		return ""
	}
	const w, h = 720.0, 180.0
	const padB = 18.0 // bottom padding for date labels
	max := 1
	for _, d := range series {
		if d.Count > max {
			max = d.Count
		}
		if d.Visitors > max {
			max = d.Visitors
		}
	}
	n := len(series)
	xAt := func(i int) float64 {
		if n == 1 {
			return w / 2
		}
		return float64(i) / float64(n-1) * w
	}
	yAt := func(v int) float64 {
		return (h - padB) - (float64(v)/float64(max))*(h-padB-6)
	}
	// Gridlines (quartiles).
	grid := ""
	for _, f := range []float64{0.25, 0.5, 0.75} {
		y := (h - padB) * (1 - f)
		grid += fmt.Sprintf(`<line class="chart-grid" x1="0" y1="%.1f" x2="%.1f" y2="%.1f"></line>`, y, w, y)
	}
	pv, vis := "", ""
	area := fmt.Sprintf("0,%.1f ", h-padB)
	for i, d := range series {
		x, y := xAt(i), yAt(d.Count)
		pv += fmt.Sprintf("%.1f,%.1f ", x, y)
		area += fmt.Sprintf("%.1f,%.1f ", x, y)
		vis += fmt.Sprintf("%.1f,%.1f ", x, yAt(d.Visitors))
	}
	area += fmt.Sprintf("%.1f,%.1f", w, h-padB)
	// First/last date labels.
	labels := ""
	if n > 0 {
		labels = fmt.Sprintf(`<text class="chart-axis" x="0" y="%.1f">%s</text>`+
			`<text class="chart-axis chart-axis--end" x="%.1f" y="%.1f">%s</text>`,
			h-4, html.EscapeString(series[0].Date), w, h-4, html.EscapeString(series[n-1].Date))
	}
	return `<div class="vp-trend">` +
		`<div class="vp-trend-legend"><span class="vp-legend__item"><span class="vp-legend__dot legend-c1"></span>Pageviews</span>` +
		`<span class="vp-legend__item"><span class="vp-legend__dot legend-c2"></span>Unique visitors</span></div>` +
		`<svg class="vp-trend-svg" viewBox="0 0 720 180" preserveAspectRatio="none" role="img" aria-label="` + html.EscapeString(title) + `">` +
		grid +
		`<polygon class="vp-trend-area" points="` + area + `"></polygon>` +
		`<polyline class="vp-trend-line vp-trend-line--pv" points="` + pv + `"></polyline>` +
		`<polyline class="vp-trend-line vp-trend-line--vis" points="` + vis + `"></polyline>` +
		labels +
		`</svg></div>`
}

// osBarsFromAudience adapts audience stats to bar-list rows.
func osBarsFromAudience(items []analytics.AudienceStat) []osChartBar {
	out := make([]osChartBar, 0, len(items))
	for _, it := range items {
		out = append(out, osChartBar{Label: it.Label, Value: it.Count})
	}
	return out
}

// osSegsFromAudience adapts audience stats to donut segments.
func osSegsFromAudience(items []analytics.AudienceStat) []osChartSeg {
	out := make([]osChartSeg, 0, len(items))
	for _, it := range items {
		out = append(out, osChartSeg{Label: it.Label, Value: it.Count})
	}
	return out
}

// humanCount formats a count with thousands separators for readability.
func humanCount(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	// Insert commas.
	var out []byte
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
