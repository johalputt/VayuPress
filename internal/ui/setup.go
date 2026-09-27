// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strconv"
	"strings"
)

// SetupStep is one thing an app needs before it runs.
type SetupStep struct {
	Title, Detail string
	Done          bool
	// Href is where the step is done, when the console can do it.
	Href string
	// Command is what does it when only a terminal can (the installer, say),
	// shown so it can be read before it is run. The page's Action copies it:
	// the most a page can do for a step outside it.
	Command string
}

// SetupPage is what an app shows until it can run: what it is, what it needs
// with the steps already done ticked, and the one thing to do next. It is one
// of the page grammar's six kinds, and says so in data-page-kind.
type SetupPage struct {
	Icon, Title, What string
	Steps             []SetupStep
	Action            HTML // the one primary button: the next thing to do
	More              HTML // a quiet secondary link
}

// Setup renders the page.
func Setup(p SetupPage) HTML {
	var b strings.Builder
	b.WriteString(`<section class="sa-setup" data-page-kind="setup"><div class="sa-setup__icon" aria-hidden="true">` +
		string(Icon(p.Icon)) + `</div><h1 class="sa-setup__title">` + string(Text(p.Title)) + `</h1>`)
	if p.What != "" {
		b.WriteString(`<p class="sa-setup__what">` + string(Text(p.What)) + `</p>`)
	}
	if len(p.Steps) > 0 {
		b.WriteString(`<ol class="sa-setup__steps">`)
		for i, s := range p.Steps {
			cls, mark, done := "sa-setup__step", strconv.Itoa(i+1), ""
			if s.Done {
				cls += " is-done"
				mark = string(Icon("check"))
				done = `<span class="vp-sr-only"> (done)</span>`
			}
			title := string(Text(s.Title)) + done
			if s.Href != "" {
				title = `<a class="sa-setup__link" href="` + string(Text(s.Href)) + `">` + title + `</a>`
			}
			b.WriteString(`<li class="` + cls + `"><span class="sa-setup__mark" aria-hidden="true">` + mark +
				`</span><div class="sa-setup__body"><span class="sa-setup__label">` + title + `</span>`)
			if s.Detail != "" {
				b.WriteString(`<span class="sa-setup__detail">` + string(Text(s.Detail)) + `</span>`)
			}
			if s.Command != "" {
				b.WriteString(`<code class="sa-setup__cmd">` + string(Text(s.Command)) + `</code>`)
			}
			b.WriteString(`</div>`)
			if s.Href != "" {
				b.WriteString(`<span class="sa-setup__go" aria-hidden="true">` + string(Icon("chev-r")) + `</span>`)
			}
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ol>`)
	}
	if p.Action != "" || p.More != "" {
		b.WriteString(`<div class="sa-setup__actions">` + string(p.Action) + string(p.More) + `</div>`)
	}
	b.WriteString(`</section>`)
	return HTML(b.String())
}
