// SPDX-License-Identifier: Apache-2.0

package ui

import "strings"

// OverviewPage is the page kind for a place the operator checks on (members,
// a newsletter, traffic, the shield): the title with one line of state, one
// figure group beside what happened lately, then rows.
type OverviewPage struct {
	Title   string
	State   HTML // beside the title: a count, or a dot and a sentence
	Actions HTML // the page's own buttons, at most one primary
	Tabs    HTML // the page's tabs, under the title, when it has them
}

// Band is an overview's one figure group: its heading and period, the
// figures, the chart under them, and what stands beside it (recent activity
// or facts, as a Section). A band without an aside takes the whole width.
// Sentence says the figures in words instead, where a row of numbers would
// only be read as one ("412 of 424 posts ready for search; 9 need a title").
type Band struct {
	Title, Hint string
	Sentence    string
	Figures     []Figure
	Chart       HTML
	Aside       HTML
}

// Overview renders the page: its header, the band, then the sections.
func Overview(p OverviewPage, band Band, sections ...HTML) HTML {
	var b strings.Builder
	b.WriteString(`<div class="sa-overview" data-page-kind="overview"><div class="page-header"><h1>` + string(Text(p.Title)))
	if p.State != "" {
		b.WriteString(` <span class="sa-overview__state">` + string(p.State) + `</span>`)
	}
	b.WriteString(`</h1>`)
	if p.Actions != "" {
		b.WriteString(`<div class="page-actions">` + string(p.Actions) + `</div>`)
	}
	b.WriteString(`</div>` + string(p.Tabs))

	cls := "sa-overview__band"
	if band.Aside == "" {
		cls += " sa-overview__band--full"
	}
	var figs strings.Builder
	for _, f := range band.Figures {
		figs.WriteString(string(f.Cell()))
	}
	main := ""
	if band.Sentence != "" {
		main = `<p class="sa-overview__sentence">` + string(Text(band.Sentence)) + `</p>`
	}
	if figs.Len() > 0 {
		main += `<div class="stat-grid">` + figs.String() + `</div>`
	}
	b.WriteString(`<div class="` + cls + `">` + string(Section(band.Title, band.Hint, HTML(main)+band.Chart)) + string(band.Aside) + `</div>`)
	for _, s := range sections {
		b.WriteString(string(s))
	}
	b.WriteString(`</div>`)
	return HTML(b.String())
}
