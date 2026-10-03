// SPDX-License-Identifier: Apache-2.0

package ui

// StatusPage is the page kind for something the system does on its own
// (backups, updates, storage): whether it is well, said once in a sentence with
// its mark, then what is running now, then its history. The sentence comes
// before any figure or detail, because it is the answer the page is opened for.
type StatusPage struct {
	Title   string
	Actions HTML   // the page's own buttons, at most one primary
	Tone    string // ok, warn or danger; anything else is neutral
	State   string // "Backed up 2 h ago, and it restores"
	Detail  HTML   // one line under it, which may carry a path in <code>
	Tabs    HTML   // the page's tabs, under the title, when it has them
}

// statusMarks draws each tone's mark. The sentence already says the state in
// words, so the mark is decoration to a screen reader.
var statusMarks = map[string]string{"ok": "check", "warn": "warn", "danger": "error", "neutral": "info"}

// Status renders the page: its header, the state, then the sections. The
// wrapper is sa-statuspage, not sa-status: that name is the system bar's
// status area, a no-shrink flex row, and a page that borrowed it was laid out
// as one line several screens wide.
func Status(p StatusPage, sections ...HTML) HTML {
	tone := p.Tone
	if _, ok := statusMarks[tone]; !ok {
		tone = "neutral"
	}
	head := `<div class="sa-statuspage" data-page-kind="status">` + string(Page(p.Title, "", p.Actions)) + string(p.Tabs) +
		`<div class="sa-status__head sa-status__head--` + tone + `"><span class="sa-status__mark" aria-hidden="true">` +
		string(Icon(statusMarks[tone])) + `</span><div><p class="sa-status__state">` + string(Text(p.State)) + `</p>`
	if p.Detail != "" {
		head += `<p class="sa-status__detail">` + string(p.Detail) + `</p>`
	}
	parts := append([]HTML{HTML(head + `</div></div>`)}, sections...)
	return Join(append(parts, `</div>`)...)
}
