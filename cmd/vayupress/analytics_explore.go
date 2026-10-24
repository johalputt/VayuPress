// SPDX-License-Identifier: Apache-2.0

package main

// analytics_explore.go — the Explorer page, for the install and for each
// hosted site: one figure split by one dimension, filtered, over a range and
// beside the range before, as a table or as bars, and as a CSV file.
//
// The question is the page's address (a GET form), so a result can be
// bookmarked or sent, and a reload asks it again. The answer is counted off
// the request in a cache of its own: the keys are whatever was asked, so it
// keeps a bounded number of them, where the dashboards' cache keeps its few
// windows for good.

import (
	"context"
	"encoding/csv"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/csvsafe"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// exploreDash holds the Explorer's answers: one compute at a time, so a
// burst of questions cannot take the read pool from the dashboards, and at
// most 64 answers.
var exploreDash = &adminFragmentCache{m: map[string]*adminFragment{}, sem: make(chan struct{}, 1), max: 64}

// exploreTTL is how long an answer is shown before it is counted again.
const exploreTTL = time.Minute

// exploreAsk is the question a request asks, read from its address.
type exploreAsk struct {
	q       analytics.ExploreQuery
	bars    bool
	label   string // the range, as the period selector says it
	seg     string // a saved segment's id, applied with the filters
	segName string
}

func exploreAskOf(r *http.Request, scope string) exploreAsk {
	v := r.URL.Query()
	days, label := analyticsPeriod(r)
	q := analytics.ExploreQuery{Scope: scope, Metric: analytics.ExploreMetric(v.Get("metric")), By: v.Get("by"), Days: days, Compare: v.Get("compare") == "1"}
	if q.Metric == "" {
		q.Metric = analytics.ExploreViews
	}
	if !v.Has("by") {
		// Opened without a question: page views by page, the split a reader
		// of the Analytics page asks first. An empty "by" is the form's
		// "Nothing: the total".
		q.By = "path"
	}
	fd, fo, fv := v["fd"], v["fo"], v["fv"]
	for i := range fd {
		if fd[i] == "" {
			continue // the form's empty row
		}
		f := analytics.ExploreFilter{Dim: fd[i]}
		if i < len(fo) {
			f.Op = fo[i]
		}
		if i < len(fv) {
			f.Value = strings.TrimSpace(fv[i])
		}
		q.Filters = append(q.Filters, f)
	}
	return exploreAsk{q: q, bars: v.Get("view") == "bars", label: label, seg: v.Get("seg")}
}

// values is the question as an address's query, in one order, so one
// question is one cache key.
func (x exploreAsk) values() url.Values {
	v := url.Values{}
	v.Set("metric", string(x.q.Metric))
	v.Set("by", x.q.By)
	v.Set("days", strconv.Itoa(x.q.Days))
	if x.q.Compare {
		v.Set("compare", "1")
	}
	if x.bars {
		v.Set("view", "bars")
	}
	if x.seg != "" {
		v.Set("seg", x.seg)
	}
	for _, f := range x.q.Filters {
		v.Add("fd", f.Dim)
		v.Add("fo", f.Op)
		v.Add("fv", f.Value)
	}
	return v
}

// handleOSExplore renders the Explorer for the install, or for the hosted
// site in the address.
func (a *App) handleOSExplore(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// The site's own visits (the primary's, a hosted site's) or every
	// site's on Platform; its segments are the view's definitions.
	v := viewOf(r)
	scope, base, api, site, title := v.scope, v.base+"/explore", v.segmentsAPI(), "", "Explorer"
	switch {
	case v.hosted:
		site, title = v.d.ID, "Explorer · "+v.d.Host
	case v.all():
		title = "Explorer, every site"
	}
	if a.analytics == nil {
		writeOSHTML(w, r, adminOSLayout(nonce, title, "analytics", cfg, ui.Overview(ui.OverviewPage{Title: "Explorer"},
			ui.Band{Title: "Explorer", Aside: ui.Empty("trend", "Analytics are off", "This install was started without the analytics store.", "")})))
		return
	}
	ask := exploreAskOf(r, scope)
	segments, segErr := a.analytics.ListSegments(r.Context(), v.defs)
	invalid := segErr
	if ask.seg != "" && segErr == nil {
		invalid = analytics.ErrNoSegment
		for _, g := range segments {
			if g.ID == ask.seg {
				ask.q.Segment, ask.segName, invalid = g.Filters, g.Name, nil
			}
		}
	}
	if invalid == nil {
		invalid = ask.q.Validate()
	}
	if r.URL.Query().Get("format") == "csv" {
		if invalid != nil {
			writeAPIError(w, r, http.StatusBadRequest, "invalid-question", invalid.Error(), "")
			return
		}
		a.exploreCSV(w, r, ask)
		return
	}
	form := exploreForm(base, ask, segments)
	page := ui.OverviewPage{Title: "Explorer"}
	segs := ui.HTML(exploreSegments(base, api, site, segments, ask, nonce))
	if invalid != nil {
		writeOSHTML(w, r, adminOSLayout(nonce, title, "analytics", cfg, ui.Overview(page,
			ui.Band{Title: "The question", Chart: ui.Callout("warn", ui.Text(invalid.Error())), Aside: form}, segs)))
		return
	}
	page.Actions = ui.HTML(`<a class="btn btn--sm" href="` + html.EscapeString(base+"?"+ask.values().Encode()+"&format=csv") + `" download>Download CSV</a>`)
	band := ui.Band{Title: exploreMetricLabel(ask.q.Metric), Hint: exploreSentence(ask), Aside: form}
	// The answer is cached as its total and its split, joined by a byte no
	// HTML holds; the page around them, the segments among it, is drawn per
	// request, so a segment saved shows at once. The count runs on the
	// cache's goroutine, so it reads nothing this handler goes on to change:
	// it once built on the band, which the lines below write, and raced.
	key := "explore:" + scope + ":" + ask.values().Encode()
	body, ready := exploreDash.get(key, exploreTTL, func(ctx context.Context) string {
		res, err := a.analytics.Explore(ctx, ask.q)
		if err != nil {
			// Shown for the TTL rather than retried on every reload: a
			// question that cannot be counted now will not be in a second.
			return string(ui.Callout("danger", ui.Text("This could not be counted: "+err.Error()))) + "\x00"
		}
		total, split := exploreAnswer(ask, res)
		return string(total) + "\x00" + string(split)
	})
	total, split, _ := strings.Cut(body, "\x00")
	band.Chart = ui.HTML(total)
	if !ready {
		band.Chart = `<p class="table-empty">This is counted in the background; reload in a few seconds.</p>`
	}
	writeOSHTML(w, r, adminOSLayout(nonce, title, "analytics", cfg, ui.Overview(page, band, ui.HTML(split), segs)))
}

// exploreForm is the question, as the form that asks it.
func exploreForm(base string, x exploreAsk, segments []analytics.Segment) ui.HTML {
	opt := func(value, label string, on bool) string {
		s := ""
		if on {
			s = " selected"
		}
		return `<option value="` + html.EscapeString(value) + `"` + s + `>` + html.EscapeString(label) + `</option>`
	}
	var b strings.Builder
	b.WriteString(`<form method="get" action="` + html.EscapeString(base) + `">`)
	var fields []ui.Row
	field := func(id, label, control string) {
		fields = append(fields, ui.Row{Label: label, ID: id, Control: ui.HTML(control)})
	}
	var m strings.Builder
	m.WriteString(`<select class="input" id="ex-metric" name="metric">`)
	for _, o := range analytics.ExploreMetrics() {
		m.WriteString(opt(o.ID, o.Label, o.ID == string(x.q.Metric)))
	}
	m.WriteString(`</select>`)
	field("ex-metric", "Figure", m.String())
	var by strings.Builder
	by.WriteString(`<select class="input" id="ex-by" name="by">` + opt("", "Nothing: the total", x.q.By == ""))
	for _, o := range analytics.ExploreDims() {
		by.WriteString(opt(o.ID, exploreDimNote(o), o.ID == x.q.By))
	}
	by.WriteString(`</select>`)
	field("ex-by", "Split by", by.String())
	if len(segments) > 0 {
		var sg strings.Builder
		sg.WriteString(`<select class="input" id="ex-seg" name="seg">` + opt("", "Everyone", x.seg == ""))
		for _, g := range segments {
			sg.WriteString(opt(g.ID, g.Name, g.ID == x.seg))
		}
		sg.WriteString(`</select>`)
		field("ex-seg", "Segment", sg.String())
	}
	var days strings.Builder
	days.WriteString(`<select class="input" id="ex-days" name="days">`)
	for _, o := range analyticsPeriodOptions {
		days.WriteString(opt(strconv.Itoa(o.Days), "Last "+o.Label, o.Days == x.q.Days))
	}
	days.WriteString(`</select>`)
	field("ex-days", "Range", days.String())
	checked := ""
	if x.q.Compare {
		checked = " checked"
	}
	field("ex-compare", "Beside the range before", `<input type="checkbox" id="ex-compare" name="compare" value="1"`+checked+`>`)
	field("ex-view", "Show as", `<select class="input" id="ex-view" name="view">`+opt("", "Table", !x.bars)+opt("bars", "Bars", x.bars)+`</select>`)
	b.WriteString(string(ui.Rows(fields...)))
	// The filters set, and one empty row for another, up to the limit.
	rows := append([]analytics.ExploreFilter(nil), x.q.Filters...)
	if len(rows) < analytics.ExploreMaxFilters {
		rows = append(rows, analytics.ExploreFilter{})
	}
	b.WriteString(`<fieldset class="sa-explore__filters"><legend class="settings-row-label">Only where</legend>`)
	for i, f := range rows {
		n := strconv.Itoa(i + 1)
		b.WriteString(`<div class="sa-explore__filter"><select class="input" name="fd" aria-label="Filter ` + n + `: dimension">` + opt("", "Anything", f.Dim == ""))
		for _, o := range analytics.ExploreDims() {
			if o.ID != "day" {
				b.WriteString(opt(o.ID, o.Label, o.ID == f.Dim))
			}
		}
		b.WriteString(`</select><select class="input" name="fo" aria-label="Filter ` + n + `: how">` +
			opt(analytics.FilterIs, "is", f.Op == analytics.FilterIs || f.Op == "") +
			opt(analytics.FilterIsNot, "is not", f.Op == analytics.FilterIsNot) +
			opt(analytics.FilterPrefix, "starts with", f.Op == analytics.FilterPrefix) +
			opt(analytics.FilterAtLeast, "at least", f.Op == analytics.FilterAtLeast) +
			opt(analytics.FilterAtMost, "at most", f.Op == analytics.FilterAtMost) +
			`</select><input class="input" name="fv" maxlength="200" aria-label="Filter ` + n + `: value" value="` + html.EscapeString(f.Value) + `"></div>`)
	}
	b.WriteString(`<p class="settings-row-hint">A channel is direct, organic, ai_assisted, social, email, corporate or referral; returning and a visit with a custom event are yes or no; the counts in a visit are whole numbers, and only they take at least and at most. Visits, bounce rate and average visit are counted per visit, so they split and filter by the visit's own facts alone.</p></fieldset>`)
	b.WriteString(`<div class="mt-3"><button type="submit" class="btn btn--primary btn--sm">Count</button></div></form>`)
	return ui.Section("The question", "", ui.HTML(b.String()))
}

// exploreDimNote is a dimension as the split list offers it, saying which
// figures it splits.
func exploreDimNote(o analytics.ExploreOption) string {
	if !o.Visit {
		return o.Label + " (views, visitors, events)"
	}
	return o.Label
}

func exploreMetricLabel(m analytics.ExploreMetric) string {
	for _, o := range analytics.ExploreMetrics() {
		if o.ID == string(m) {
			return o.Label
		}
	}
	return string(m)
}

func exploreDimLabel(id string) string {
	for _, o := range analytics.ExploreDims() {
		if o.ID == id {
			return o.Label
		}
	}
	return id
}

// exploreSentence is the question in words.
func exploreSentence(x exploreAsk) string {
	s := strings.ToUpper(x.label[:1]) + x.label[1:]
	if x.q.By != "" {
		s += ", by " + strings.ToLower(exploreDimLabel(x.q.By))
	}
	if x.segName != "" {
		s += ", in the segment " + strconv.Quote(x.segName)
	}
	for _, f := range x.q.Filters {
		s += ", where " + filterWords(f)
	}
	return s
}

// exploreValue is a figure as the page says it.
func exploreValue(m analytics.ExploreMetric, v float64) string {
	switch m {
	case analytics.ExploreBounceRate:
		return strconv.FormatFloat(v, 'f', 1, 64) + "%"
	case analytics.ExploreAvgVisit:
		return osDuration(v)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

// exploreChange is how a figure moved on the range before. A rate moves by
// points, not by a percentage of itself.
func exploreChange(m analytics.ExploreMetric, cur, prev float64, had bool) string {
	switch {
	case !had:
		return "None before"
	case cur == prev:
		return "The same"
	case m == analytics.ExploreBounceRate:
		return fmt.Sprintf("%+.1f points", cur-prev)
	case prev == 0:
		return "None before"
	}
	return fmt.Sprintf("%+.1f%%", (cur-prev)/prev*100)
}

// exploreKey is a split's value as the page says it.
func exploreKey(by, k string) string {
	switch {
	case k == "":
		return "(not recorded)"
	case by == "channel":
		return analytics.ChannelLabel(k)
	}
	return k
}

// exploreAnswer is the result: the total, for the band, and the split, as
// the section under it.
func exploreAnswer(x exploreAsk, res *analytics.ExploreResult) (ui.HTML, ui.HTML) {
	m := x.q.Metric
	var b strings.Builder
	// A figure that would read 0 is said in words instead.
	switch {
	case res.HasTotal && res.Total == 0 && m == analytics.ExploreBounceRate:
		b.WriteString(`<p class="sa-overview__sentence">None of the visits counted bounced.</p>`)
	case !res.HasTotal || res.Total == 0:
		b.WriteString(`<p class="sa-overview__sentence">Nothing was counted in the ` + html.EscapeString(x.label) + ` that matches.</p>`)
	default:
		note := ""
		if x.q.Compare {
			note = exploreChange(m, res.Total, res.PrevTotal, res.HasPrevTotal) + " on the range before (" + exploreValue(m, res.PrevTotal) + ")"
		}
		b.WriteString(string(ui.Figure{Value: exploreValue(m, res.Total), Label: "Total", Note: note}.Cell()))
	}
	if x.q.By == "" {
		return ui.HTML(b.String()), ""
	}
	hint := countOf(len(res.Rows), "value")
	if res.Truncated {
		hint = "The " + strconv.Itoa(len(res.Rows)) + " largest values; there are more"
	}
	var body string
	if x.bars && m != analytics.ExploreBounceRate && m != analytics.ExploreAvgVisit {
		bars := make([]osChartBar, 0, len(res.Rows))
		for _, r := range res.Rows {
			bars = append(bars, osChartBar{Label: exploreKey(x.q.By, r.Key), Value: int(r.Value)})
		}
		denom := osShareHidden()
		if additive(m) && x.q.By != "day" {
			denom = osShareOf(int(res.Total))
		}
		body = osBarList(bars, denom, "Nothing matches.")
	} else {
		headers := []string{exploreDimLabel(x.q.By), exploreMetricLabel(m)}
		if additive(m) {
			headers = append(headers, "Share")
		}
		if x.q.Compare {
			headers = append(headers, "Before", "Change")
		}
		rows := make([][]ui.HTML, 0, len(res.Rows))
		for _, r := range res.Rows {
			cells := []ui.HTML{ui.Text(exploreKey(x.q.By, r.Key)), ui.Text(exploreValue(m, r.Value))}
			if additive(m) {
				cells = append(cells, ui.HTML(osShareLabel(int(r.Value), int(res.Total))))
			}
			if x.q.Compare {
				before := "None"
				if r.HasPrev {
					before = exploreValue(m, r.Prev)
				}
				cells = append(cells, ui.Text(before), ui.Text(exploreChange(m, r.Value, r.Prev, r.HasPrev)))
			}
			rows = append(rows, cells)
		}
		body = `<div class="sa-explore__result">` + string(ui.Table(headers, rows, "Nothing matches.")) + `</div>`
	}
	return ui.HTML(b.String()), ui.Section("By "+strings.ToLower(exploreDimLabel(x.q.By)), hint, ui.HTML(body))
}

// additive says whether a figure's split adds up to its total, so a row's
// share of it means something.
func additive(m analytics.ExploreMetric) bool {
	for _, o := range analytics.ExploreMetrics() {
		if o.ID == string(m) {
			return o.Additive
		}
	}
	return false
}

// exploreCSV sends the answer as a file. It is counted for the download
// itself, bounded as the cache's counts are.
func (a *App) exploreCSV(w http.ResponseWriter, r *http.Request, x exploreAsk) {
	if err := x.q.Validate(); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-question", err.Error(), "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), adminFragmentComputeTimeout)
	defer cancel()
	res, err := a.analytics.Explore(ctx, x.q)
	if err != nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "not-counted", "this could not be counted: "+err.Error(), "")
		return
	}
	raw := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	dim := "total"
	if x.q.By != "" {
		dim = x.q.By
	}
	header := []string{dim, string(x.q.Metric)}
	if x.q.Compare {
		header = append(header, "before")
	}
	name := fmt.Sprintf("explore-%s-by-%s-%dd-%s.csv", x.q.Metric, dim, x.q.Days, time.Now().UTC().Format("20060102"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	cw := csv.NewWriter(w)
	_ = cw.Write(header)
	line := func(k string, v, prev float64, had, defined bool) {
		rec := []string{k, ""}
		if defined {
			rec[1] = raw(v)
		}
		if x.q.Compare {
			p := ""
			if had {
				p = raw(prev)
			}
			rec = append(rec, p)
		}
		_ = csvsafe.Write(cw, rec)
	}
	for _, row := range res.Rows {
		k := row.Key
		if x.q.By == "channel" {
			k = analytics.ChannelLabel(k)
		}
		line(k, row.Value, row.Prev, row.HasPrev, true)
	}
	line("Total", res.Total, res.PrevTotal, res.HasPrevTotal, res.HasTotal)
	cw.Flush()
}

// exploreButton opens the Explorer on the period the page shows.
func exploreButton(base string, days int) ui.HTML {
	return ui.HTML(` <a class="btn btn--sm" href="` + html.EscapeString(base+"?days="+strconv.Itoa(days)) + `">Explore</a>`)
}
