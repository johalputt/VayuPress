// SPDX-License-Identifier: Apache-2.0

package ui

// Sheet is a form that rises over the page instead of sitting in it: the page
// grammar keeps fields out of the page flow everywhere but Settings and
// Document. It is a native <dialog>, so the browser gives it the top layer,
// focus kept inside and Escape to close; a button with data-sheet="<id>" opens
// it (admin-os.js). The body is the form as the page already renders it, with
// the same ids, so the page's own script drives it unchanged.
func Sheet(id, title string, body HTML) HTML {
	t := string(Text(title))
	return HTML(`<dialog class="sa-sheet" id="` + string(Text(id)) + `" aria-labelledby="` + string(Text(id)) + `-title">` +
		`<div class="sa-sheet__panel"><div class="sa-sheet__head"><h2 class="sa-sheet__title" id="` + string(Text(id)) + `-title">` + t + `</h2>` +
		`<button type="button" class="btn btn--ghost btn--sm sa-sheet__close" data-sheet-close aria-label="Close">` + string(Icon("x")) + `</button></div>` +
		`<div class="sa-sheet__body">` + string(body) + `</div></div></dialog>`)
}
