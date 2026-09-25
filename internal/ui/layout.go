// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strconv"
	"strings"
)

// Page is the app header of a page: its title, the actions that belong to the
// whole page (at most one primary), and one sentence on what the page is for.
func Page(title, sub string, actions HTML) HTML {
	var b strings.Builder
	b.WriteString(`<div class="page-header"><h1>` + string(Text(title)) + `</h1>`)
	if actions != "" {
		b.WriteString(`<div class="page-actions">` + string(actions) + `</div>`)
	}
	b.WriteString(`</div>`)
	if sub != "" {
		b.WriteString(`<p class="page-sub">` + string(Text(sub)) + `</p>`)
	}
	return HTML(b.String())
}

// Section opens a band of the page with its heading and an optional hint on
// the right. The body below it never repeats the heading.
func Section(title, hint string, body HTML) HTML {
	h := ""
	if hint != "" {
		h = `<span class="section-head__hint">` + string(Text(hint)) + `</span>`
	}
	return HTML(`<section><div class="section-head"><h2 class="section-head__title">` +
		string(Text(title)) + `</h2>` + h + `</div>` + string(body) + `</section>`)
}

// Row is one setting: what it is, what it does, and its control on the right.
// Changed marks a row whose value differs from what is saved.
type Row struct {
	Label, Hint string
	Control     HTML
	ID          string // the control's id, so the label can point at it
	Changed     bool
}

// Rows renders settings as rows with the control on the right.
func Rows(rows ...Row) HTML {
	var b strings.Builder
	b.WriteString(`<div class="settings-rows">`)
	for _, r := range rows {
		cls := "settings-row"
		if r.Changed {
			cls += " is-changed"
		}
		label := string(Text(r.Label))
		if r.ID != "" {
			label = `<label for="` + string(Text(r.ID)) + `">` + label + `</label>`
		}
		b.WriteString(`<div class="` + cls + `"><div class="settings-row-info"><div class="settings-row-label">` + label + `</div>`)
		if r.Hint != "" {
			b.WriteString(`<div class="settings-row-hint">` + string(Text(r.Hint)) + `</div>`)
		}
		b.WriteString(`</div><div class="settings-row-control">` + string(r.Control) + `</div></div>`)
	}
	b.WriteString(`</div>`)
	return HTML(b.String())
}

// Fact is one line of a fact list: a name and its value, which may carry
// markup (a status dot, a link).
type Fact struct {
	Key   string
	Value HTML
}

// Facts renders plain rows of name and value — the install facts on Home.
func Facts(facts ...Fact) HTML {
	var b strings.Builder
	b.WriteString(`<dl class="sa-facts">`)
	for _, f := range facts {
		b.WriteString(`<div class="sa-fact"><dt>` + string(Text(f.Key)) + `</dt><dd>` + string(f.Value) + `</dd></div>`)
	}
	b.WriteString(`</dl>`)
	return HTML(b.String())
}

// Figure is one measured number and what it counts. Tone "warn" or "danger"
// marks a figure that wants attention; the number itself is never coloured
// alone, the note says why.
type Figure struct {
	Value, Label, Note, Tone string
}

// Figures renders the few numbers that answer "what is the state of this".
func Figures(figs ...Figure) HTML {
	cells := make([]HTML, len(figs))
	for i, f := range figs {
		cells[i] = f.Cell()
	}
	return FigureRow(cells...)
}

// FigureRow lays out figure cells built one at a time, for a page that
// decides each figure's tone between them.
func FigureRow(cells ...HTML) HTML {
	return HTML(`<div class="stat-grid">` + string(Join(cells...)) + `</div>`)
}

// Cell renders one figure.
func (f Figure) Cell() HTML {
	cls := "stat-card"
	switch f.Tone {
	case "warn", "danger":
		cls += " stat-card--" + f.Tone
	}
	s := `<div class="` + cls + `"><div class="stat-card__label">` + string(Text(f.Label)) +
		`</div><div class="stat-card__value">` + string(Text(f.Value)) + `</div>`
	if f.Note != "" {
		s += `<div class="stat-card__note">` + string(Text(f.Note)) + `</div>`
	}
	return HTML(s + `</div>`)
}

// Disclosure is a row that opens to show more: an icon (already drawn, so any
// icon helper can supply it), a title, one line under it, a state label on the
// right, and the body. No script: <details>. The chevron turns when it opens.
func Disclosure(icon HTML, title, sub string, state HTML, open bool, body HTML) HTML {
	o := ""
	if open {
		o = " open"
	}
	ic := ""
	if icon != "" {
		ic = `<span class="mon-acc__ic" aria-hidden="true">` + string(icon) + `</span>`
	}
	return HTML(`<details class="mon-acc"` + o + `><summary class="mon-acc__sum">` + ic +
		`<span class="mon-acc__head"><span class="mon-acc__title">` + string(Text(title)) + `</span>` +
		`<span class="mon-acc__sub">` + string(Text(sub)) + `</span></span>` + string(state) +
		`<span class="mon-acc__chev" aria-hidden="true">` + string(Icon("chev-d")) + `</span></summary><div class="mon-acc__body">` + string(body) + `</div></details>`)
}

// Explain folds a page's explanation away: one quiet "How this works" line that
// opens to the text. A console page says what is and what to do; why it works
// that way is there for whoever asks. Status, and anything the operator must
// act on, never goes in here. No script: <details>.
func Explain(body HTML) HTML {
	return HTML(`<details class="sa-explain"><summary class="sa-explain__sum">` + string(Icon("info")) +
		`<span>How this works</span></summary><div class="sa-explain__body">` + string(body) + `</div></details>`)
}

// Tip is one sentence of help behind an info mark, shown on hover or focus (a
// tap focuses it on a phone). The sentence is also the button's name, so a
// screen reader hears it without opening anything.
func Tip(text string) HTML {
	t := string(Text(text))
	return HTML(`<button type="button" class="sa-tip" aria-label="` + t + `">` + string(Icon("info")) +
		`<span class="sa-tip__pop" aria-hidden="true">` + t + `</span></button>`)
}

// Brief says a status in its first sentence and puts the rest behind a tip.
// Status text written as a paragraph leads with the finding and follows with
// how it was established; the finding is what a page shows.
func Brief(text string) HTML {
	first, rest := text, ""
	for i := 0; i+2 < len(text); i++ {
		if text[i] == '.' && text[i+1] == ' ' && text[i+2] >= 'A' && text[i+2] <= 'Z' {
			first, rest = text[:i+1], strings.TrimSpace(text[i+2:])
			break
		}
	}
	if rest == "" {
		return Text(first)
	}
	return Text(first) + Tip(rest)
}

// Table renders a table with its header row. Cells are trusted markup; a
// caller puts Text around anything a person typed.
func Table(headers []string, rows [][]HTML, empty string) HTML {
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr>`)
	for _, h := range headers {
		b.WriteString(`<th>` + string(Text(h)) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	if len(rows) == 0 && empty != "" {
		b.WriteString(`<tr><td class="table-empty" colspan="` + strconv.Itoa(len(headers)) + `">` + string(Text(empty)) + `</td></tr>`)
	}
	for _, r := range rows {
		b.WriteString(`<tr>`)
		for _, c := range r {
			b.WriteString(`<td>` + string(c) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return HTML(b.String())
}

// Callout tells the reader something about the page: a failure (danger), a
// risk (warn), a result (ok) or a fact (info). A danger callout is announced
// (role="alert"); the others are polite status.
func Callout(tone string, body HTML) HTML {
	icon, role := "info", "status"
	switch tone {
	case "danger":
		icon, role = "error", "alert"
	case "warn":
		icon = "warn"
	case "ok":
		icon = "check-c"
	default:
		tone = "info"
	}
	return HTML(`<div class="callout callout--` + tone + `" role="` + role + `">` + string(Icon(icon)) +
		`<div class="callout__body">` + string(body) + `</div></div>`)
}

// Empty says a list has nothing in it yet, and what would put something there.
func Empty(icon, title, sub string, action HTML) HTML {
	s := ""
	if sub != "" {
		s = `<div class="empty-sub">` + string(Text(sub)) + `</div>`
	}
	return HTML(`<div class="empty-state"><div class="empty-icon">` + string(Icon(icon)) + `</div><div class="empty-title">` +
		string(Text(title)) + `</div>` + s + string(action) + `</div>`)
}

// Tag is a short status label on its tone's tint: ok, warn, danger, info or
// muted. Anything else is muted, so a caller cannot put a class of its own
// choosing into the markup.
func Tag(tone, text string) HTML {
	switch tone {
	case "ok", "warn", "danger", "info":
	default:
		tone = "muted"
	}
	return HTML(`<span class="badge badge--` + tone + `">` + string(Text(text)) + `</span>`)
}

// Step is one stage of a process: a short mark on the left (a time, a count),
// what happens, and what it means. Now marks the stage the system is in.
type Step struct {
	Mark, Title, Detail string
	Now                 bool
}

// Steps renders a process in order, one stage per line.
func Steps(steps ...Step) HTML {
	var b strings.Builder
	b.WriteString(`<ol class="sa-steps">`)
	for _, s := range steps {
		cls := "sa-step"
		if s.Now {
			cls += " sa-step--now"
		}
		b.WriteString(`<li class="` + cls + `"><span class="sa-step__t">` + string(Text(s.Mark)) +
			`</span><span class="sa-step__n" aria-hidden="true"></span><span class="sa-step__d"><b>` + string(Text(s.Title)) + `</b>`)
		if s.Detail != "" {
			b.WriteString(`<br><span class="muted">` + string(Text(s.Detail)) + `</span>`)
		}
		b.WriteString(`</span></li>`)
	}
	b.WriteString(`</ol>`)
	return HTML(b.String())
}
