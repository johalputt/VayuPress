// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_charts.go — server-rendered, CSP-safe charts for the VayuOS analytics
// console. Everything here emits static SVG/HTML with CSS classes (no inline
// styles, no external JS, no CDNs), so the strict admin CSP (style-src 'self',
// script-src 'self') is preserved. Percentages snap to the shared w-N width
// classes; everything is drawn in the page's ink, its colour kept for links
// and states. GDPR posture is unchanged — these only visualise the existing
// aggregate, no-PII queries.

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

// osBarList renders a bar list: each row is a label, the count and its share,
// and a bar proportional to the largest value. Every bar is one ink: a row's
// colour said nothing a row's place does not, and a palette across a list is
// ornament. Returns the empty sentence when there is nothing to show.
func osBarList(items []osChartBar, denom osBarDenom, emptyMsg string) string {
	if len(items) == 0 {
		if emptyMsg == "" {
			emptyMsg = "No data yet."
		}
		return `<p class="table-empty">` + emptyMsg + `</p>`
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
	for _, it := range items {
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
		out += `<div class="vp-bar">` +
			`<span class="vp-bar__label" title="` + html.EscapeString(label) + `">` + lab + `</span>` +
			`<span class="vp-bar__val">` + humanCount(it.Value) + share + `</span>` +
			`<span class="vp-bar__track"><span class="vp-bar__fill ` + barWidthClass(pct) + `"></span></span>` +
			`</div>`
	}
	out += `</div>`
	return out
}

// prettyChartDate turns an ISO date (2006-01-02) into a compact "Jan 2" for
// tooltips/axis labels; falls back to the raw string if it does not parse.
func prettyChartDate(iso string) string {
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Format("Jan 2")
	}
	return iso
}

// readoutDate is a day as the chart's readout names it, with its weekday
// ("Thu, Oct 12"), since a week's shape is most of what a day's count says.
func readoutDate(iso string) string {
	if t, err := time.Parse("2006-01-02", iso); err == nil {
		return t.Format("Mon, Jan 2")
	}
	return iso
}

// osTrendChart is a range's page views and visitors a day, as two lines in
// ink: the views solid, the visitors dashed and lighter, no fill. It is drawn
// only from two days with views; one day is a figure, and the band already
// says it.
//
// The lines are an SVG stretched to the chart's box (preserveAspectRatio
// none, strokes that do not scale), and every word on the chart is HTML: SVG
// text scales with its drawing, and the chart is 360 to 700 pixels wide
// depending on the screen, so a 12-unit label read 5 pixels high at the
// narrow end. The axis is the first and last day, and the middle one when it
// falls on a day; three gridlines carry their values. Each day is a band
// whose <title> says its counts, the browser's tooltip without the script;
// admin-os-intel.js reads the same counts from the band and sets a readout
// at reading size beside the day, with a dot on each line.
func osTrendChart(series []analytics.DayPageviews, title string) string {
	days := 0
	for _, d := range series {
		if d.Count > 0 {
			days++
		}
	}
	if days < 2 {
		return ""
	}
	// The drawing's own units; the box it is stretched to is set in CSS.
	const w, h = 1000.0, 200.0
	const padT, padB = 4.0, 4.0
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
	xAt := func(i int) float64 { return float64(i) / float64(n-1) * w }
	yAt := func(v int) float64 { return baseY - (float64(v)/float64(max))*plotH }

	grid, ylabels := "", ""
	for _, g := range []struct {
		f   float64
		cls string
	}{{1, "top"}, {0.5, "mid"}, {0, "base"}} {
		y := baseY - g.f*plotH
		grid += fmt.Sprintf(`<line class="chart-grid" x1="0" y1="%.1f" x2="%.0f" y2="%.1f"></line>`, y, w, y)
		ylabels += `<span class="vp-trend-y vp-trend-y--` + g.cls + `">` + humanCount(int(float64(max)*g.f+0.5)) + `</span>`
	}

	pv, vis := "", ""
	for i, d := range series {
		x := xAt(i)
		pv += fmt.Sprintf("%.1f,%.1f ", x, yAt(d.Count))
		vis += fmt.Sprintf("%.1f,%.1f ", x, yAt(d.Visitors))
	}

	// A day's band spans the midpoints to its neighbours, so the whole slice
	// is hoverable.
	pts := ""
	for i, d := range series {
		x := xAt(i)
		left, right := 0.0, w
		if i > 0 {
			left = (xAt(i-1) + x) / 2
		}
		if i < n-1 {
			right = (x + xAt(i+1)) / 2
		}
		pts += fmt.Sprintf(`<g class="vp-pt" data-day="%s" data-pv="%d" data-vis="%d"><rect class="vp-pt__hit" x="%.1f" y="0" width="%.1f" height="%.0f"></rect>`,
			html.EscapeString(readoutDate(d.Date)), d.Count, d.Visitors, left, right-left, h) +
			fmt.Sprintf(`<line class="vp-pt__guide" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"></line>`, x, padT, x, baseY) +
			`<title>` + html.EscapeString(prettyChartDate(d.Date)) + `: ` + humanCount(d.Count) + ` page views, ` + humanCount(d.Visitors) + ` visitors</title></g>`
	}

	xlabels := `<span>` + html.EscapeString(prettyChartDate(series[0].Date)) + `</span>`
	if (n-1)%2 == 0 {
		xlabels += `<span>` + html.EscapeString(prettyChartDate(series[(n-1)/2].Date)) + `</span>`
	} else {
		xlabels += `<span></span>`
	}
	xlabels += `<span class="vp-trend-x__end">` + html.EscapeString(prettyChartDate(series[n-1].Date)) + `</span>`

	return `<div class="vp-trend" data-chart>` +
		`<div class="vp-trend-legend"><span class="vp-legend__item"><span class="vp-legend__line"></span>Page views</span>` +
		`<span class="vp-legend__item"><span class="vp-legend__line vp-legend__line--vis"></span>Visitors</span></div>` +
		`<div class="vp-trend-plot">` + ylabels +
		`<svg class="vp-trend-svg" viewBox="0 0 1000 200" preserveAspectRatio="none" role="img" aria-label="` + html.EscapeString(title) + `" data-max="` + strconv.Itoa(max) + `">` +
		grid +
		`<polyline class="vp-trend-line vp-trend-line--pv" points="` + pv + `"></polyline>` +
		`<polyline class="vp-trend-line vp-trend-line--vis" points="` + vis + `"></polyline>` +
		pts + `</svg><span class="vp-trend-dot" hidden></span><span class="vp-trend-dot vp-trend-dot--vis" hidden></span>` +
		`<div class="vp-trend-tip" hidden></div></div>` +
		`<div class="vp-trend-x">` + xlabels + `</div></div>`
}

// osBarsFromAudience adapts audience stats to bar-list rows.
func osBarsFromAudience(items []analytics.AudienceStat) []osChartBar {
	out := make([]osChartBar, 0, len(items))
	for _, it := range items {
		out = append(out, osChartBar{Label: it.Label, Value: it.Count})
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
