// SPDX-License-Identifier: Apache-2.0

package ui

import "strings"

// DocumentPage is the page kind for one thing being made (a site, a theme):
// a quiet bar with the title, what is saved or live, and the actions; the
// thing itself, filling the page; and an inspector beside it holding its
// settings. The post editor is the same kind, drawn by its own template
// because its script owns the bar.
type DocumentPage struct {
	Title     string
	State     HTML   // beside the title: what is live, or the last save
	Actions   HTML   // the bar's buttons, at most one primary
	Inspector HTML   // the settings beside the document, as InspectorSections
	Label     string // what the inspector holds, for assistive technology
	Hook      string // a data attribute naming the page for its script, e.g. data-theme-studio
}

// Document renders the page around body, the document itself.
func Document(p DocumentPage, body HTML) HTML {
	var b strings.Builder
	b.WriteString(`<div class="sa-doc" data-page-kind="document"`)
	if p.Hook != "" {
		b.WriteString(` ` + string(Text(p.Hook)))
	}
	b.WriteString(`><div class="sa-doc__bar"><h1 class="sa-doc__title">` + string(Text(p.Title)) + `</h1>`)
	if p.State != "" {
		b.WriteString(`<span class="sa-doc__state">` + string(p.State) + `</span>`)
	}
	b.WriteString(`<span class="sa-doc__fill"></span>` + string(p.Actions) + `</div><div class="sa-doc__body"><div class="sa-doc__main">` + string(body) + `</div>`)
	if p.Inspector != "" {
		b.WriteString(`<aside class="sa-doc__inspector" aria-label="` + string(Text(p.Label)) + `">` + string(p.Inspector) + `</aside>`)
	}
	b.WriteString(`</div></div>`)
	return HTML(b.String())
}

// InspectorSection is one group of an inspector: a quiet head over its rows,
// divided from the next by a hairline.
func InspectorSection(title string, body HTML) HTML {
	return HTML(`<section class="sa-insp"><h2 class="sa-insp__head">` + string(Text(title)) + `</h2>` + string(body) + `</section>`)
}

// InspectorRow is a label and its value on one line, the value read as text
// and, where it is a control, edited where it stands.
func InspectorRow(label string, value HTML) HTML {
	return HTML(`<div class="sa-insp__row"><span class="sa-insp__label">` + string(Text(label)) + `</span><span class="sa-insp__value">` + string(value) + `</span></div>`)
}
