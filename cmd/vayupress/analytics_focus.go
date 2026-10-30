// SPDX-License-Identifier: Apache-2.0

package main

// analytics_focus.go — an Analytics page narrowed to some readers.
//
// Every row of every list is a way in: clicking "www.google.com" under
// Referrers opens the overview of the readers who came from Google, and
// every view, Live, the Explorer and an export keep to them until the
// filter is taken off. The filters are the Explorer's (package focus), read
// off the address (?f=dim:op:value, once each), carried in the request's
// context to every reader, and written into every link the page makes, so a
// narrowed page can be kept or sent as its address.

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/johalputt/vayupress/internal/analytics"
	"github.com/johalputt/vayupress/internal/focus"
	"github.com/johalputt/vayupress/internal/ui"
)

// focusParam is the address's name for one filter.
const focusParam = "f"

func encodeFocus(f analytics.ExploreFilter) string { return f.Dim + ":" + f.Op + ":" + f.Value }

// focusOf reads the filters q names, each "dimension:operator:value". A
// filter the Explorer would refuse is left out; a later one on the same
// dimension and operator replaces an earlier (two pages read "is" is no
// reader at all, and a live row's link adds its filter after the page's);
// at most ExploreMaxFilters are kept.
func focusOf(q url.Values) []analytics.ExploreFilter {
	var out []analytics.ExploreFilter
	at := map[string]int{}
	for _, raw := range q[focusParam] {
		parts := strings.SplitN(raw, ":", 3)
		if len(parts) != 3 {
			continue
		}
		f := analytics.ExploreFilter{Dim: parts[0], Op: parts[1], Value: parts[2]}
		if analytics.ValidFilter(f) != nil {
			continue
		}
		if i, ok := at[f.Dim+":"+f.Op]; ok {
			out[i] = f
			continue
		}
		if len(out) == analytics.ExploreMaxFilters {
			continue
		}
		at[f.Dim+":"+f.Op] = len(out)
		out = append(out, f)
	}
	return out
}

// with is p narrowed by f too: a filter on the same dimension and operator
// is replaced. ok is false when p already holds as many filters as a page
// takes, and p is returned as it was.
func (p analyticsSpan) with(f analytics.ExploreFilter) (analyticsSpan, bool) {
	fs := make([]analytics.ExploreFilter, 0, len(p.focus)+1)
	for _, g := range p.focus {
		if g.Dim != f.Dim || g.Op != f.Op {
			fs = append(fs, g)
		}
	}
	if len(fs) == analytics.ExploreMaxFilters {
		return p, false
	}
	p.focus = append(fs, f)
	return p, true
}

// without is p with its i-th filter taken off.
func (p analyticsSpan) without(i int) analyticsSpan {
	fs := make([]analytics.ExploreFilter, 0, len(p.focus))
	fs = append(fs, p.focus[:i]...)
	p.focus = append(fs, p.focus[i+1:]...)
	return p
}

// focusQuery is p's filters as an address's query ("?f=…"), for an
// endpoint that takes no period (the live panel); "" when there are none.
func (p analyticsSpan) focusQuery() string {
	if len(p.focus) == 0 {
		return ""
	}
	q := url.Values{}
	for _, f := range p.focus {
		q.Add(focusParam, encodeFocus(f))
	}
	return "?" + q.Encode()
}

// narrowed is ctx narrowed to fs for every reader: the event log's, and the
// engagement store's through the visitors the event log names.
func (a *App) narrowed(ctx context.Context, fs []analytics.ExploreFilter) context.Context {
	if len(fs) == 0 || a.analytics == nil {
		return ctx
	}
	st, in := a.analytics, focus.With(ctx, fs)
	return focus.WithVisitors(in, func(col, from, until string) (string, []any) {
		return st.FocusVisitors(in, col, from, until)
	})
}

// focusIs is how "dimension is value" reads on the bar, where it reads
// better than the Explorer's "Visit's referrer is …".
var focusIs = map[string]string{
	"visit_referrer": "Came from", "visit_channel": "Arrived by", "path": "Read", "landing": "Landed on",
	"exit": "Left from", "country": "In", "region": "In", "city": "In", "device": "On", "browser": "Using",
	"os": "On", "language": "Language", "event": "Did", "utm_source": "Campaign source",
	"utm_medium": "Campaign medium", "utm_campaign": "Campaign",
}

// focusWords is one filter as the bar names it.
func focusWords(f analytics.ExploreFilter) string {
	value := f.Value
	switch f.Dim {
	case "visit_channel":
		value = analytics.ChannelLabel(f.Value)
	case "country":
		value = countryName(f.Value)
	case "returning":
		if f.Op == analytics.FilterIs {
			return map[string]string{"yes": "Returning the same day", "no": "First visit of the day"}[f.Value]
		}
	case "converted":
		if f.Op == analytics.FilterIs {
			return map[string]string{"yes": "Converted", "no": "Did not convert"}[f.Value]
		}
	}
	if w, ok := focusIs[f.Dim]; ok && f.Op == analytics.FilterIs {
		return w + " " + value
	}
	op := map[string]string{analytics.FilterIs: "is", analytics.FilterIsNot: "is not", analytics.FilterPrefix: "starts with",
		analytics.FilterAtLeast: "at least", analytics.FilterAtMost: "at most"}[f.Op]
	return exploreDimLabel(f.Dim) + " " + op + " " + value
}

// osFocusBar is the filters a page is narrowed to, under its section bar:
// each named, each with the way to take it off, and the ways to save them
// as a segment or take them all off; "" when the page is not narrowed.
func osFocusBar(v analyticsView, view string, p analyticsSpan) ui.HTML {
	if len(p.focus) == 0 {
		return ""
	}
	var b strings.Builder
	// A line of words, not chips: a filled pill is the console's sign of a
	// state, and these are what the page counts, each with its way off.
	b.WriteString(`<div class="sa-focus" role="group" aria-label="Narrowed to"><span class="sa-focus__lead">Narrowed to</span>`)
	for i, f := range p.focus {
		words := focusWords(f)
		b.WriteString(`<span class="sa-focus__chip">` + esc(words) +
			`<a class="sa-focus__off" href="` + esc(analyticsHref(v.base, view, p.without(i))) + `" aria-label="` + esc("Take off: "+words) + `">` +
			string(ui.Icon("x")) + `</a></span>`)
	}
	all := p
	all.focus = nil
	b.WriteString(`<span class="sa-focus__end"><button type="button" class="btn btn--ghost btn--sm" data-sheet="segment-new">Save as segment</button>` +
		`<a class="btn btn--ghost btn--sm" href="` + esc(analyticsHref(v.base, view, all)) + `">Clear</a></span></div>`)
	site := ""
	if v.hosted {
		site = v.d.ID
	}
	b.WriteString(`<div id="segments" hidden data-segments-api="` + esc(v.segmentsAPI()) + `" data-segments-site="` + esc(site) + `"></div>`)
	b.WriteString(string(segmentSaveSheet(p.focus, `<span class="text-xs muted" id="segment-status" role="status" aria-live="polite"></span>`)))
	return ui.HTML(b.String())
}

// segmentSaveSheet is the sheet that names fs and saves them as a segment
// (segmentsScript posts it), with after beside its button.
func segmentSaveSheet(fs []analytics.ExploreFilter, after ui.HTML) ui.HTML {
	// The filters ride in a double-quoted attribute. json.Marshal already
	// writes <, > and & as \u escapes, so a double quote is the one
	// character left that could end the attribute; it is replaced by its
	// entity here, where the value is quoted, rather than trusted to a
	// general escaper the reader has to check.
	js, _ := json.Marshal(fs)
	attr := strings.ReplaceAll(string(js), `"`, "&#34;")
	return ui.Sheet("segment-new", "Save as a segment", ui.Rows(
		ui.Row{Label: "Name", ID: "segment-name", Hint: "It stands for " + segmentWords(fs) + ".",
			Control: ui.HTML(`<input class="input" id="segment-name" maxlength="80" placeholder="Returning readers">`)},
	)+ui.HTML(`<div class="mt-3"><button type="button" class="btn btn--primary btn--sm" data-segment-save data-filters="`+attr+`">Save</button> `)+after+`</div>`)
}

// analyticsDoor is the way into the readers of one row of v's page under
// p: door(dim, value) is the overview narrowed by "dim is value" too, or ""
// when the row names nothing to narrow by (an unknown value) or the page
// already holds as many filters as it takes.
func analyticsDoor(v analyticsView, p analyticsSpan) func(dim, value string) string {
	return func(dim, value string) string {
		if value == "" {
			return ""
		}
		f := analytics.ExploreFilter{Dim: dim, Op: analytics.FilterIs, Value: value}
		if analytics.ValidFilter(f) != nil {
			return ""
		}
		for _, g := range p.focus {
			if g == f {
				return "" // the page is these readers already
			}
		}
		np, ok := p.with(f)
		if !ok {
			return ""
		}
		return analyticsHref(v.base, "overview", np)
	}
}

// doorBars is items as bars, each the way into its readers by dim: the
// row's key when it has one (a channel's category), else its label.
func doorBars(items []analytics.AudienceStat, dim string, door func(dim, value string) string) []osChartBar {
	out := osBarsFromAudience(items)
	for i, it := range items {
		value := it.Key
		if value == "" {
			value = it.Label
		}
		out[i].Href = door(dim, value)
	}
	return out
}

// liveDoor is the live panel's way into a row's readers: the address the
// script completes with a row's dimension and value (data-live-door), the
// page's period and filters kept. A live row narrows the Live view, where
// the operator is watching, rather than the overview.
func liveDoor(v analyticsView, p analyticsSpan) string {
	q := url.Values{}
	q.Set("view", "live")
	p.set(q)
	return v.base + "?" + q.Encode()
}
