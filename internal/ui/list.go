// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strconv"
	"strings"
)

// ListPage is the page kind for a collection worked through one item at a
// time (posts, comments, domains, files): the title with how many there are,
// the collection's views as a segmented control, a search, the page's own
// action, then the list with the selected item's inspector beside it.
type ListPage struct {
	Title   string
	Count   string // beside the title: "412 · 3 drafts"
	Views   HTML   // Segments
	Search  HTML   // Search
	Actions HTML   // the page's own buttons, at most one primary
	Sub     HTML   // a line under the header, only when the list needs one
}

// List renders the page. The inspector sits beside the list where there is
// room and below it where there is not; it is never an overlay, so reading an
// item's details never hides the item. An empty inspector leaves the list the
// whole width.
func List(p ListPage, list, inspector HTML) HTML {
	var b strings.Builder
	b.WriteString(`<div class="sa-list" data-page-kind="list"><div class="page-header"><h1>` + string(Text(p.Title)))
	if p.Count != "" {
		b.WriteString(` <span class="sa-list__count">` + string(Text(p.Count)) + `</span>`)
	}
	b.WriteString(`</h1>`)
	if p.Views != "" || p.Search != "" || p.Actions != "" {
		b.WriteString(`<div class="page-actions">` + string(p.Views) + string(p.Search) + string(p.Actions) + `</div>`)
	}
	b.WriteString(`</div>`)
	if p.Sub != "" {
		b.WriteString(`<p class="page-sub">` + string(p.Sub) + `</p>`)
	}
	b.WriteString(`<div class="sa-list__shell`)
	if inspector == "" {
		b.WriteString(` sa-list__shell--full`)
	}
	b.WriteString(`"><div class="sa-list__main">` + string(list) + `</div>`)
	if inspector != "" {
		b.WriteString(`<aside class="sa-list__inspector" data-list-inspector aria-label="Details">` + string(inspector) + `</aside>`)
	}
	b.WriteString(`</div></div>`)
	return HTML(b.String())
}

// Segment is one view of a list: its label, where it goes, and how many items
// it holds (a negative count is not shown). CountID names the count, for a page
// that updates it in place when an item moves between views.
type Segment struct {
	Label   string
	Href    string
	Count   int
	On      bool
	CountID string
}

// Segments draws a list's views as a segmented control of links, so each
// view has its own address and survives a reload.
func Segments(label string, segs ...Segment) HTML {
	var b strings.Builder
	b.WriteString(`<nav class="seg-filter" aria-label="` + string(Text(label)) + `">`)
	for _, s := range segs {
		b.WriteString(`<a class="seg-btn`)
		if s.On {
			b.WriteString(` is-active" aria-current="page`)
		}
		b.WriteString(`" href="` + string(Text(s.Href)) + `">` + string(Text(s.Label)))
		if s.Count >= 0 {
			b.WriteString(` <span class="muted"`)
			if s.CountID != "" {
				b.WriteString(` id="` + string(Text(s.CountID)) + `"`)
			}
			b.WriteString(`>` + strconv.Itoa(s.Count) + `</span>`)
		}
		b.WriteString(`</a>`)
	}
	b.WriteString(`</nav>`)
	return HTML(b.String())
}

// SearchBox is a list's search. With an Action it is a GET form, carrying the
// Keep values (the view it narrows) as hidden fields; without one it is a
// field a script filters the list with, found by the data attribute in Hook.
type SearchBox struct {
	Action      string
	Name        string
	Value       string
	Placeholder string
	Keep        [][2]string
	Hook        string
}

// Search renders the field. It narrows the list and submits nothing of the
// page's, which is why the grammar lets it stand in the page when every other
// field rises in a sheet.
func Search(s SearchBox) HTML {
	field := `<input type="search" class="input" placeholder="` + string(Text(s.Placeholder)) + `" aria-label="` + string(Text(s.Placeholder)) + `" autocomplete="off"`
	if s.Name != "" {
		field += ` name="` + string(Text(s.Name)) + `" value="` + string(Text(s.Value)) + `"`
	}
	if s.Hook != "" {
		field += ` ` + string(Text(s.Hook))
	}
	field += `>`
	inner := string(Icon("search")) + field
	if s.Action == "" {
		return HTML(`<label class="sa-find">` + inner + `</label>`)
	}
	var b strings.Builder
	b.WriteString(`<form class="sa-find" method="GET" action="` + string(Text(s.Action)) + `" role="search">`)
	for _, kv := range s.Keep {
		b.WriteString(`<input type="hidden" name="` + string(Text(kv[0])) + `" value="` + string(Text(kv[1])) + `">`)
	}
	b.WriteString(inner + `</form>`)
	return HTML(b.String())
}
