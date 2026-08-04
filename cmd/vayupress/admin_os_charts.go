// SPDX-License-Identifier: Apache-2.0

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
	"time"

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
// osBarDenom is the population a bar list's percentages are taken against.
//
// It exists because the percentage used to be computed against the sum of the
// LISTED rows, which is correct only when the list IS the whole population. On
// a top-N list it overstates without bound: an operator's homepage read "87%"
// of traffic because it was 242 of the 278 views in the ten rows shown, while
// the window held 31,643 — the real share was 0.8%. Every complete breakdown on
// the page (countries, browsers, channels) was right, and every truncated one
// was wrong, which is why it survived review for so long.
//
// The zero value is deliberately NOT a valid answer. A denominator that
// defaults is a denominator nobody chose, and defaulting is exactly what
// produced the wrong number.
type osBarDenom struct {
	total int
	kind  osDenomKind
}

type osDenomKind uint8

const (
	osDenomUnset  osDenomKind = iota // not an answer — see the type comment
	osDenomTotal                     // an explicit population size
	osDenomListed                    // the listed rows ARE the population
	osDenomHidden                    // truncated, population unknown → show no %
)

// osShareOf declares the true population size for a truncated list.
func osShareOf(total int) osBarDenom { return osBarDenom{total: total, kind: osDenomTotal} }

// osShareOfListed declares that the rows passed are the complete breakdown, so
// their sum is the population. Only correct for an un-truncated query.
func osShareOfListed() osBarDenom { return osBarDenom{kind: osDenomListed} }

// osShareHidden suppresses the percentage entirely: the list is truncated and no
// honest denominator is available. No number beats a wrong one.
func osShareHidden() osBarDenom { return osBarDenom{kind: osDenomHidden} }

// osShareLabel renders value's share of total at a precision that survives the
// division.
//
// Integer percentages were fine while the denominator was the sum of the rows
// shown, because every row was then a large fraction of it. Against the real
// population they collapse: a site taking 32,608 page requests, most of them
// crawlers on distinct one-off URLs, has a homepage at 260 — 0.797%, which
// integer division renders as "0%". Every row in the list then reads 0%, and a
// column of zeros is not a more honest number, it is no number at all. The
// previous overstatement and this are the same failure viewed from either end:
// a figure that does not tell the reader what is true.
//
// Clamped at both ends. A caller-supplied total smaller than the rows it is
// meant to contain would print above 100%, and a negative count would print a
// negative share; each reads as a rendering glitch rather than the data problem
// it actually is.
func osShareLabel(value, total int) string {
	if total <= 0 || value <= 0 {
		return "0%"
	}
	if value >= total {
		return "100%"
	}
	pct := float64(value) * 100 / float64(total)
	switch {
	case pct >= 1:
		return strconv.Itoa(int(pct)) + "%"
	case pct >= 0.1:
		// One decimal keeps the homepage distinguishable from the long tail of
		// scanned URLs beneath it, which is the whole question this column answers.
		return strconv.FormatFloat(pct, 'f', 1, 64) + "%"
	default:
		// Below a tenth of a percent the exact figure stops meaning anything, but
		// "0%" would claim the row contributed nothing, which is not true either.
		return "&lt;0.1%"
	}
}

func osBarList(items []osChartBar, denom osBarDenom, emptyMsg string) string {
	if len(items) == 0 {
		if emptyMsg == "" {
			emptyMsg = "No data yet."
		}
		return `<div class="empty-state">` + emptyMsg + `</div>`
	}
	max := 1
	listed := 0
	for _, it := range items {
		if it.Value > max {
			max = it.Value
		}
		listed += it.Value
	}
	// An unset denominator is a programming error, not a display choice. Fail
	// closed to no percentage rather than silently reinstating the old bug.
	total := 0
	switch denom.kind {
	case osDenomTotal:
		total = denom.total
	case osDenomListed:
		total = listed
	case osDenomHidden, osDenomUnset:
		total = 0
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
			share = `<span class="vp-bar__pct">` + osShareLabel(it.Value, total) + `</span>`
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

// prettyChartDate turns an ISO date (2006-01-02) into a compact "Jan 2" for
// tooltips/axis labels; falls back to the raw string if it does not parse.
func prettyChartDate(iso string) string {
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Format("Jan 2")
	}
	return iso
}

// maxStyledTooltips caps how many per-day styled tooltip groups are drawn, so a
// multi-year daily window cannot balloon the SVG DOM. Beyond it, every day is
// still hoverable but via a lightweight native <title> (browser tooltip) — the
// chart stays fast at 1000+ points.
const maxStyledTooltips = 190

// osTrendChart renders the premium interactive traffic chart: a gradient-filled
// pageviews area, a pageviews line and a unique-visitors line, quartile
// gridlines with value labels, and — on hover over any day's vertical band — a
// guide line, highlighted dots and a styled tooltip showing that day's date,
// pageviews and unique visitors. Everything is pure SVG + CSS classes with a
// uniform viewBox (undistorted dots/text) and CSS-only hover reveal: no inline
// styles, no JavaScript, no distortion, CSP-safe, and instant. GDPR posture is
// unchanged (aggregate no-PII counts only).
func osTrendChart(series []analytics.DayPageviews, title string) string {
	if len(series) == 0 {
		return ""
	}
	const w, h = 1000.0, 220.0
	const padL, padR, padT, padB = 6.0, 6.0, 12.0, 22.0
	plotW := w - padL - padR
	plotH := h - padT - padB
	baseY := h - padB

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
			return padL + plotW/2
		}
		return padL + float64(i)/float64(n-1)*plotW
	}
	yAt := func(v int) float64 {
		return baseY - (float64(v)/float64(max))*plotH
	}

	// Gridlines + y-axis value labels at 0 / 25 / 50 / 75 / 100 %.
	grid := ""
	for _, f := range []float64{0, 0.25, 0.5, 0.75, 1} {
		y := baseY - f*plotH
		grid += fmt.Sprintf(`<line class="chart-grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"></line>`, padL, y, w-padR, y)
		grid += fmt.Sprintf(`<text class="chart-yaxis" x="%.1f" y="%.1f">%s</text>`, padL, y-2, humanCount(int(float64(max)*f+0.5)))
	}

	pv, vis := "", ""
	area := fmt.Sprintf("%.1f,%.1f ", padL, baseY)
	for i, d := range series {
		x := xAt(i)
		pv += fmt.Sprintf("%.1f,%.1f ", x, yAt(d.Count))
		area += fmt.Sprintf("%.1f,%.1f ", x, yAt(d.Count))
		vis += fmt.Sprintf("%.1f,%.1f ", x, yAt(d.Visitors))
	}
	area += fmt.Sprintf("%.1f,%.1f", padL+plotW, baseY)

	// X-axis date labels: first, last, and a handful evenly spaced between, so a
	// long window is not an unreadable smear of dates.
	xlabels := ""
	step := 1
	if n > 8 {
		step = (n + 6) / 7
	}
	for i := 0; i < n; i += step {
		anchor := "chart-xaxis"
		if i == 0 {
			anchor = "chart-xaxis chart-xaxis--start"
		} else if i >= n-step {
			continue // avoid colliding with the forced last label
		}
		xlabels += fmt.Sprintf(`<text class="%s" x="%.1f" y="%.1f">%s</text>`, anchor, xAt(i), h-4, html.EscapeString(prettyChartDate(series[i].Date)))
	}
	if n > 1 {
		xlabels += fmt.Sprintf(`<text class="chart-xaxis chart-xaxis--end" x="%.1f" y="%.1f">%s</text>`, xAt(n-1), h-4, html.EscapeString(prettyChartDate(series[n-1].Date)))
	}

	// Per-day interactive layer: a full-height transparent hit band, a guide line,
	// dots and a styled tooltip, all revealed on :hover by CSS. The band spans the
	// midpoints to its neighbours so the whole vertical slice is hoverable.
	styled := n <= maxStyledTooltips
	pts := ""
	for i, d := range series {
		x := xAt(i)
		left, right := padL, w-padR
		if i > 0 {
			left = (xAt(i-1) + x) / 2
		}
		if i < n-1 {
			right = (x + xAt(i+1)) / 2
		}
		pvY, visY := yAt(d.Count), yAt(d.Visitors)
		hit := fmt.Sprintf(`<rect class="vp-pt__hit" x="%.1f" y="%.1f" width="%.1f" height="%.1f"></rect>`, left, padT, right-left, plotH)
		if !styled {
			// Lightweight native tooltip for very long windows.
			pts += `<g class="vp-pt">` + hit +
				fmt.Sprintf(`<title>%s — %s pageviews · %s visitors</title>`, html.EscapeString(series[i].Date), humanCount(d.Count), humanCount(d.Visitors)) +
				`</g>`
			continue
		}
		guide := fmt.Sprintf(`<line class="vp-pt__guide" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"></line>`, x, padT, x, baseY)
		dotPV := fmt.Sprintf(`<circle class="vp-pt__dot vp-pt__dot--pv" cx="%.1f" cy="%.1f" r="3.5"></circle>`, x, pvY)
		dotVis := fmt.Sprintf(`<circle class="vp-pt__dot vp-pt__dot--vis" cx="%.1f" cy="%.1f" r="3.5"></circle>`, x, visY)
		// Tooltip card: anchor to the left of the guide for points in the right
		// third so it never clips the chart edge.
		tipW, tipH := 132.0, 62.0
		tx := x + 12
		tipCls := "vp-pt__tip"
		if x > w*0.62 {
			tx = x - 12 - tipW
			tipCls = "vp-pt__tip vp-pt__tip--left"
		}
		ty := pvY - tipH - 8
		if ty < padT {
			ty = padT
		}
		tip := fmt.Sprintf(`<g class="%s" transform="translate(%.1f,%.1f)">`, tipCls, tx, ty) +
			fmt.Sprintf(`<rect class="vp-pt__tip-bg" x="0" y="0" width="%.0f" height="%.0f" rx="8"></rect>`, tipW, tipH) +
			`<text class="vp-pt__tip-date" x="12" y="18">` + html.EscapeString(prettyChartDate(d.Date)) + `</text>` +
			`<circle class="vp-pt__tip-dot--pv" cx="16" cy="34" r="3.5"></circle>` +
			`<text class="vp-pt__tip-lbl" x="26" y="38">Pageviews</text>` +
			`<text class="vp-pt__tip-val" x="120" y="38">` + humanCount(d.Count) + `</text>` +
			`<circle class="vp-pt__tip-dot--vis" cx="16" cy="50" r="3.5"></circle>` +
			`<text class="vp-pt__tip-lbl" x="26" y="54">Visitors</text>` +
			`<text class="vp-pt__tip-val" x="120" y="54">` + humanCount(d.Visitors) + `</text>` +
			`</g>`
		pts += `<g class="vp-pt">` + hit + guide + dotPV + dotVis + tip + `</g>`
	}

	peakDay, peakVal := "", 0
	for _, d := range series {
		if d.Count > peakVal {
			peakVal, peakDay = d.Count, d.Date
		}
	}
	peakNote := ""
	if peakVal > 0 {
		peakNote = `<span class="vp-trend-peak">Peak ` + humanCount(peakVal) + ` on ` + html.EscapeString(prettyChartDate(peakDay)) + `</span>`
	}

	return `<div class="vp-trend" data-chart>` +
		`<div class="vp-trend-legend"><span class="vp-legend__item"><span class="vp-legend__dot legend-c1"></span>Pageviews</span>` +
		`<span class="vp-legend__item"><span class="vp-legend__dot legend-c2"></span>Unique visitors</span>` + peakNote + `</div>` +
		`<svg class="vp-trend-svg" viewBox="0 0 1000 220" preserveAspectRatio="xMidYMid meet" role="img" aria-label="` + html.EscapeString(title) + `">` +
		`<defs><linearGradient id="vpTrendFill" x1="0" y1="0" x2="0" y2="1">` +
		`<stop class="vp-trend-grad-0" offset="0"></stop><stop class="vp-trend-grad-1" offset="1"></stop></linearGradient></defs>` +
		grid +
		`<polygon class="vp-trend-area" points="` + area + `"></polygon>` +
		`<polyline class="vp-trend-line vp-trend-line--pv" points="` + pv + `"></polyline>` +
		`<polyline class="vp-trend-line vp-trend-line--vis" points="` + vis + `"></polyline>` +
		xlabels + pts +
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
