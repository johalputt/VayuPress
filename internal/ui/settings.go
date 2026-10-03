// SPDX-License-Identifier: Apache-2.0

package ui

// State is a state as the page grammar shows it: a dot and a word. A filled
// badge is for a label a person chose; a state the system reports is this.
// The tone is ok, warn, danger, accent or neutral; anything else is
// neutral, so a mistyped tone can never read as a good one.
func State(tone, text string) HTML {
	switch tone {
	case "ok", "warn", "danger", "accent":
	default:
		tone = "neutral"
	}
	return HTML(`<span class="sa-indicator"><span class="sa-dot sa-dot--` + tone + `" aria-hidden="true"></span>` + string(Text(text)) + `</span>`)
}

// SettingsPage is the page kind whose rows edit in place: the title with the
// page's state beside it, a sentence on what it is for, and its sections. A
// page whose rows save together ends with SaveBar; one that saves each change
// as it is made (Outside services) has no bar to show.
func SettingsPage(title string, state HTML, sub string, sections ...HTML) HTML {
	head := `<div class="page-header"><h1>` + string(Text(title)) + `</h1>`
	if state != "" {
		head += `<span class="page-state">` + string(state) + `</span>`
	}
	head += `</div>`
	if sub != "" {
		head += `<p class="page-sub">` + string(Text(sub)) + `</p>`
	}
	parts := []HTML{HTML(`<div class="settings-page" data-page-kind="settings">` + head)}
	parts = append(parts, sections...)
	parts = append(parts, `</div>`)
	return Join(parts...)
}

// SaveBar is the one bar that saves or discards every changed row. It rises
// when the first row changes (settingsPageScript in cmd/vayupress, which a
// page gets from settingsLayout); any control with data-setting-key takes
// part.
func SaveBar() HTML {
	return `<div class="settings-bar" data-settings-bar hidden><span class="settings-bar__count" data-settings-count aria-live="polite"></span>` +
		`<button type="button" class="btn btn--ghost btn--sm" data-settings-discard>Discard</button>` +
		`<button type="button" class="btn btn--primary btn--sm" data-settings-save>Save changes</button></div>`
}
