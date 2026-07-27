// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_theme_a11y.go — the Theme Studio's accessibility readout.
//
// Colour pickers make it easy to choose an accent that looks striking and is
// unreadable. Contrast is the one property an operator cannot eyeball reliably —
// it depends on relative luminance, not on how bold a colour feels — so the
// Studio measures it and says plainly whether the chosen accent clears WCAG on
// each background. Theme sovereignty is preserved: this reports, it never
// refuses a colour.

import (
	"fmt"
	"html"
	"strings"
)

// wcagAALarge is the WCAG 2.x AA ratio for large text and UI components. Normal
// body text uses wcagAANormal (4.5), already defined alongside contrastRatio.
const wcagAALarge = 3.0

// a11yCheck is one measured colour pairing.
type a11yCheck struct {
	Label      string  // what the pairing is, in the operator's terms
	Foreground string  // hex
	Background string  // hex
	Ratio      float64 // measured contrast, 1.0–21.0
}

// grade returns the strongest WCAG level the ratio clears, plus a badge class.
// AAA is 7.0 for normal text; AA is 4.5; below 3.0 fails everything.
func (c a11yCheck) grade() (label, cls string) {
	switch {
	case c.Ratio >= 7.0:
		return "AAA", "badge badge--ok"
	case c.Ratio >= wcagAANormal:
		return "AA", "badge badge--ok"
	case c.Ratio >= wcagAALarge:
		return "AA large only", "badge badge--warn"
	default:
		return "Fails", "badge badge--danger"
	}
}

// Reading-text tokens the public article theme ships (see articleCSSMin in
// internal/render). Body text is the highest-contrast pairing on the page and
// muted text is the lowest — and it is muted text that carries bylines, dates,
// excerpts and captions, so it is read constantly while being the pairing most
// likely to fail.
const (
	bodyTextDark   = "#e2e8f0"
	bodyTextLight  = "#0f172a"
	mutedTextDark  = "#7c8ba1"
	mutedTextLight = "#64748b"
	// Cards and code blocks sit on a lifted surface rather than the page
	// background, which is a DARKER contrast against the same text in light mode
	// and a LIGHTER one in dark mode. Measuring only against the page background
	// therefore misses the worst case on a page full of cards.
	darkModeSurface = "#0f1420"
)

// themeA11yChecks measures every pairing a reader actually reads, against the
// backgrounds it is read on. Accents are used for links and interactive text —
// normal-size text — so 4.5:1 is the bar that matters, not 3:1.
//
// IT USED TO MEASURE ONLY THE ACCENTS. That made the panel quietly misleading:
// the accent is the colour an operator picked and is therefore the one they are
// already thinking about, while body and muted text are shipped defaults nobody
// re-examines. Muted text failing AA on a dark background is the single most
// common contrast fault in any theme, and this panel reported "Readable" while
// an external audit reported a contrast failure on the same page — because the
// failing pairing was never one of the four it looked at.
//
// A check that covers a subset and presents as covering the whole is worse than
// no check: it converts an unknown into a false assurance.
func themeA11yChecks(accentDark, accent2Dark, accentLight, accent2Light string) []a11yCheck {
	out := []a11yCheck{}
	add := func(label, fg, bg string) {
		if strings.TrimSpace(fg) == "" {
			return
		}
		out = append(out, a11yCheck{Label: label, Foreground: fg, Background: bg, Ratio: contrastRatio(fg, bg)})
	}
	add("Accent on dark background", accentDark, darkModeBG)
	add("Accent 2 on dark background", accent2Dark, darkModeBG)
	add("Accent on light background", accentLight, lightModeBG)
	add("Accent 2 on light background", accent2Light, lightModeBG)
	// The shipped reading text — not operator-chosen, and therefore never
	// questioned unless something measures it.
	add("Body text on dark background", bodyTextDark, darkModeBG)
	add("Body text on light background", bodyTextLight, lightModeBG)
	add("Muted text on dark background", mutedTextDark, darkModeBG)
	add("Muted text on dark card", mutedTextDark, darkModeSurface)
	add("Muted text on light background", mutedTextLight, lightModeBG)
	return out
}

// a11ySummaryChip grades the WEAKEST pairing, so the collapsed section tells the
// truth rather than the most flattering number.
func a11ySummaryChip(checks []a11yCheck) string {
	if len(checks) == 0 {
		return ""
	}
	worst := checks[0]
	for _, c := range checks[1:] {
		if c.Ratio < worst.Ratio {
			worst = c
		}
	}
	switch {
	case worst.Ratio >= wcagAANormal:
		return `<span class="cz-chip cz-chip--live">● Readable</span>`
	case worst.Ratio >= wcagAALarge:
		return `<span class="cz-chip cz-chip--warn">Low contrast</span>`
	default:
		return `<span class="cz-chip cz-chip--bad">Hard to read</span>`
	}
}

// themeA11yPanel renders the readout. It is server-measured from the SAVED
// palette, so it reflects what readers currently get; the Studio re-renders it
// after Apply. Rows name the pairing, show the measured ratio and the WCAG grade.
func themeA11yPanel(checks []a11yCheck) string {
	if len(checks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="cz-a11y" data-theme-a11y>`)
	b.WriteString(`<div class="cz-a11y__head"><span class="cz-a11y__title">Readability</span>` +
		`<span class="text-xs muted">WCAG AA wants 4.5:1 for link text · AAA is 7:1</span></div>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><tbody>`)
	for _, c := range checks {
		gl, gc := c.grade()
		b.WriteString(`<tr>` +
			`<td class="row-title"><span class="cz-a11y__dot" data-color="` + html.EscapeString(c.Foreground) + `" aria-hidden="true"></span>` +
			html.EscapeString(c.Label) + `</td>` +
			`<td class="muted text-sm mono">` + fmt.Sprintf("%.1f:1", c.Ratio) + `</td>` +
			`<td><span class="` + gc + `">` + gl + `</span></td>` +
			`</tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	b.WriteString(`<p class="muted text-xs mt-2">Measured against the theme's own page backgrounds. ` +
		`A colour that fails is still yours to keep — this only tells you what readers will experience.</p>`)
	b.WriteString(`</div>`)
	return b.String()
}
