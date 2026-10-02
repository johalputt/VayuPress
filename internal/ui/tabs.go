// SPDX-License-Identifier: Apache-2.0

package ui

import "strings"

// Tab is one page of a small app shown as tabs (rule 8): a few pages that
// share a title read as one place, where a sidebar of three items would be a
// second navigation for nothing.
type Tab struct {
	Label, Href string
	Current     bool
}

// Tabs renders the strip under a page's title. Each tab is a link to its own
// page, so a tab can be bookmarked and opened in a new window; the current one
// is marked for assistive technology, not only by its underline.
func Tabs(label string, tabs ...Tab) HTML {
	var b strings.Builder
	b.WriteString(`<nav class="tabs" aria-label="` + string(Text(label)) + `">`)
	for _, t := range tabs {
		cur := ""
		if t.Current {
			cur = ` aria-current="page"`
		}
		b.WriteString(`<a class="tab" href="` + string(Text(t.Href)) + `"` + cur + `>` + string(Text(t.Label)) + `</a>`)
	}
	b.WriteString(`</nav>`)
	return HTML(b.String())
}
