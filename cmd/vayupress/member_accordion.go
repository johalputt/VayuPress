// SPDX-License-Identifier: Apache-2.0

package main

// member_accordion.go — the Monetization design language on the member pages.
//
// The member account page was a flat stack of eight equal-weight cards, so
// everything shouted at the same volume and the page had to be read top to bottom
// to find anything. The console's Monetization page solved the same problem: each
// option collapses to one summary row — icon tile · title · one-line subtitle ·
// status chip · rotating chevron — and expanding reveals the existing card,
// chromeless, with a short reveal.
//
// This is that pattern for the public member surfaces. It mirrors monAcc in
// admin_os_monetization.go deliberately, down to the class-name shape, so the two
// stay recognisably one design. Native <details>: no JavaScript, so nothing to
// break under a strict CSP and nothing to lose if a script fails to load.

import "html"

// maAcc renders one collapsible member-page section. Pass open=true for the row
// that should already be expanded when the page loads — normally exactly one, the
// thing the visitor most likely came for.
func maAcc(icon, title, sub, chipHTML, body string, open bool) string {
	openAttr := ""
	if open {
		openAttr = " open"
	}
	subHTML := ""
	if sub != "" {
		subHTML = `<span class="ma-acc__sub">` + html.EscapeString(sub) + `</span>`
	}
	return `<details class="ma-acc"` + openAttr + `>
  <summary class="ma-acc__sum">
    <span class="ma-acc__ic" aria-hidden="true">` + icon + `</span>
    <span class="ma-acc__head"><span class="ma-acc__title">` + html.EscapeString(title) + `</span>` + subHTML + `</span>
    ` + chipHTML + `
    <svg class="ma-acc__chev" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6"></path></svg>
  </summary>
  <div class="ma-acc__body">` + body + `</div>
</details>`
}

// maChip renders a status chip for an accordion summary. on=true gives it the
// affirmative colour, so state is legible without expanding the row.
func maChip(label string, on bool) string {
	cls := "ma-chip"
	if on {
		cls += " ma-chip--on"
	}
	return `<span class="` + cls + `">` + html.EscapeString(label) + `</span>`
}

// maSectionHead names a group of rows, matching the console's .section-head.
func maSectionHead(title, hint string) string {
	hintHTML := ""
	if hint != "" {
		hintHTML = `<span class="ma-sec__hint">` + html.EscapeString(hint) + `</span>`
	}
	return `<div class="ma-sec"><h2 class="ma-sec__title">` + html.EscapeString(title) + `</h2>` + hintHTML + `</div>`
}
