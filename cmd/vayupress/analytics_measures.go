// SPDX-License-Identifier: Apache-2.0

package main

// analytics_measures.go — a list ranked by what its visits did, not only by
// how many there were.
//
// A list of referrers, channels, landing pages or devices carries, on each
// row, every measure of the visits behind it (analytics.VisitMeasuresScoped): how
// many, pages a visit, bounce, average visit, conversion. The list shows its
// count; a switch in its corner (admin-os-intel.js) ranks and draws it by
// any other measure, and hovering a row shows them all beside the page's own
// figures. Without the script the list is the count it always was.

import (
	"html"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
)

// visitMeasure is one way a list's rows can be ranked.
type visitMeasure struct {
	id, label string
	value     func(analytics.VisitMeasure) float64
	text      func(analytics.VisitMeasure) string // the row's figure
	count     bool                                // a count, shown with its share of the list
	everyone  func(*analytics.Overview) string    // the page's own figure, for the card
}

func pctText(v float64) string {
	if v >= 10 || v == 0 {
		return strconv.Itoa(int(math.Round(v))) + "%"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + "%"
}

var visitMeasures = []visitMeasure{
	{"visits", "Visits", func(m analytics.VisitMeasure) float64 { return float64(m.Visits) },
		func(m analytics.VisitMeasure) string { return humanCount(m.Visits) }, true,
		func(o *analytics.Overview) string { return humanCount(o.TotalVisits) }},
	{"visitors", "Visitors", func(m analytics.VisitMeasure) float64 { return float64(m.Visitors) },
		func(m analytics.VisitMeasure) string { return humanCount(m.Visitors) }, true,
		func(o *analytics.Overview) string { return humanCount(o.UniqueVisitors) }},
	{"pages", "Pages a visit", analytics.VisitMeasure.PagesPerVisit,
		func(m analytics.VisitMeasure) string { return strconv.FormatFloat(m.PagesPerVisit(), 'f', 1, 64) }, false,
		func(o *analytics.Overview) string {
			if o.TotalVisits == 0 {
				return "—"
			}
			return strconv.FormatFloat(float64(o.TotalPageviews)/float64(o.TotalVisits), 'f', 1, 64)
		}},
	{"bounce", "Bounce", analytics.VisitMeasure.BounceRate,
		func(m analytics.VisitMeasure) string { return pctText(m.BounceRate()) }, false,
		func(o *analytics.Overview) string { return pctText(o.BounceRate) }},
	{"length", "Average visit", analytics.VisitMeasure.AvgVisit,
		func(m analytics.VisitMeasure) string {
			if m.Long == 0 {
				return "—"
			}
			return osDuration(m.AvgVisit())
		}, false,
		func(o *analytics.Overview) string { return osDuration(o.AvgDuration) }},
	{"converted", "Converted", analytics.VisitMeasure.ConversionRate,
		func(m analytics.VisitMeasure) string { return pctText(m.ConversionRate()) }, false, nil},
}

// measuredRow is one row of a measured list: its words and its visits.
type measuredRow struct {
	Label, Hint, Flag, Href string
	M                       analytics.VisitMeasure
}

// osMeasuredList is rows as a bar list counted by first ("visits" or
// "visitors"), carrying every measure for the switch and the card; ov is the
// page's own figures, which the card sets each row's beside.
func osMeasuredList(rows []measuredRow, first string, ov *analytics.Overview, empty string) string {
	if len(rows) == 0 {
		return `<p class="table-empty">` + empty + `</p>`
	}
	measures := make([]visitMeasure, 0, len(visitMeasures))
	for _, m := range visitMeasures {
		// The list's own count first; the other count is left out, as a
		// visit's and a visitor's count of one row read alike.
		if m.count && m.id != first {
			continue
		}
		if m.id == first {
			measures = append([]visitMeasure{m}, measures...)
			continue
		}
		measures = append(measures, m)
	}
	max := make(map[string]float64, len(measures))
	listed := map[string]int{}
	for _, r := range rows {
		for _, m := range measures {
			max[m.id] = math.Max(max[m.id], m.value(r.M))
			if m.count {
				listed[m.id] += int(m.value(r.M))
			}
		}
	}
	// Largest first by the list's own count, ties kept in the store's order.
	sort.SliceStable(rows, func(i, j int) bool { return measures[0].value(rows[i].M) > measures[0].value(rows[j].M) })
	var b strings.Builder
	b.WriteString(`<div class="sa-measure" role="group" aria-label="Rank by" hidden>`)
	for i, m := range measures {
		b.WriteString(`<button type="button" class="sa-measure__opt" data-measure="` + m.id + `" aria-pressed="` + strconv.FormatBool(i == 0) + `">` + m.label + `</button>`)
	}
	b.WriteString(`</div><div class="vp-bars" data-measured>`)
	for _, r := range rows {
		var data, card strings.Builder
		for _, m := range measures {
			v, text := m.value(r.M), m.text(r.M)
			w := 0
			if max[m.id] > 0 {
				w = int(v * 100 / max[m.id])
			}
			share := ""
			if m.count {
				share = osShareLabel(int(v), listed[m.id])
			}
			data.WriteString(` data-v-` + m.id + `="` + strconv.FormatFloat(v, 'f', -1, 64) + `" data-t-` + m.id + `="` + html.EscapeString(text) +
				`" data-w-` + m.id + `="` + barWidthClass(w) + `" data-s-` + m.id + `="` + share + `"`)
			everyone := ""
			if m.everyone != nil && ov != nil && !m.count {
				everyone = `<span class="vp-bar__card-all">` + html.EscapeString(m.everyone(ov)) + ` all</span>`
			}
			card.WriteString(`<span class="vp-bar__card-k">` + m.label + `</span><span class="vp-bar__card-v">` + html.EscapeString(text) + everyone + `</span>`)
		}
		first := measures[0]
		val := `<span class="vp-bar__val" data-measure-val>` + html.EscapeString(first.text(r.M)) +
			`<span class="vp-bar__pct">` + osShareLabel(int(first.value(r.M)), listed[first.id]) + `</span></span>`
		label := html.EscapeString(r.Label)
		if img := countryFlagImg(r.Flag); img != "" {
			label = img + " " + label
		}
		cls := "vp-bar"
		if r.Href != "" {
			cls += " vp-bar--door"
			label = `<a class="vp-bar__door" href="` + html.EscapeString(r.Href) + `">` + label + `</a>`
		}
		hint := r.Label
		if r.Hint != "" {
			hint = r.Hint
		}
		w := 0
		if max[first.id] > 0 {
			w = int(first.value(r.M) * 100 / max[first.id])
		}
		b.WriteString(`<div class="` + cls + `"` + data.String() + `>` +
			`<span class="vp-bar__label" title="` + html.EscapeString(hint) + `">` + label + `</span>` + val +
			`<span class="vp-bar__track"><span class="vp-bar__fill ` + barWidthClass(w) + `"></span></span>` +
			`<span class="vp-bar__card" aria-hidden="true">` + card.String() + `</span></div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// measuredLabel is how a row of dim's list names key, and its tooltip when
// that is not the name.
func measuredLabel(dim, key string) (label, hint string) {
	switch dim {
	case "visit_channel":
		return analytics.ChannelLabel(key), ""
	case "landing":
		return prettyPathText(key), key
	}
	return key, ""
}
