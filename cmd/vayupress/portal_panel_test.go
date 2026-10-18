// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/render"
)

// These guard the reported "the X sometimes closes the panel and sometimes does
// nothing".
//
// The cause was not the click handler. The close button was position: absolute at
// the panel's top-right, and the brand row was a full-width flex row at the top of
// the panel BODY — the two overlapped, and the brand painted on top, so the click
// landed on the brand instead of the button. Measured in a real browser: at the
// button's centre, elementsFromPoint returned .vp-portal-brand once the reveal
// animation had settled, and returned nothing at all while it was still running —
// which is precisely why closing worked only sometimes. The button was also 36px,
// under the 44px minimum, and the panel is its own scroll container, so an
// absolutely positioned button scrolled out of view on a long panel.
//
// The fix is structural: the brand and the close button are siblings in a sticky
// header, so they cannot overlap and the button cannot scroll away. These tests pin
// that structure, because the CSS that reintroduces the overlap would look
// perfectly reasonable in review.

func portalCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "static", "css", "portal.css"))
	if err != nil {
		t.Fatalf("read portal.css: %v", err)
	}
	return string(b)
}

// TestCloseButtonLivesInTheStickyHeader pins the structure that makes the overlap
// impossible: the brand goes in the header, never in the scrolling body.
func TestCloseButtonLivesInTheStickyHeader(t *testing.T) {
	js := render.PortalJS

	// Both must be built into the header element.
	head := strings.Index(js, "el('div', 'vp-portal-head')")
	if head < 0 {
		t.Fatal("the panel must have a header element to pin the close button in")
	}
	for _, want := range []string{"head.appendChild(brand)", "head.appendChild(closeBtn)"} {
		if !strings.Contains(js, want) {
			t.Errorf("missing %q — the brand and the close button must be header siblings, "+
				"or they can overlap again", want)
		}
	}
	// The body must never render the brand: that is what used to cover the button.
	if strings.Contains(js, `body.innerHTML = '<div class="vp-portal-brand"`) {
		t.Error("the brand must not be rendered into the panel body — it overlays the close button there")
	}

	css := portalCSS(t)
	// The header must be sticky, or the button scrolls away with the content.
	headBlock := cssBlock(t, css, ".vp-portal-head {")
	if !strings.Contains(headBlock, "position: sticky") {
		t.Error(".vp-portal-head must be position: sticky — the panel is the scroll container")
	}
	if !strings.Contains(headBlock, "z-index") {
		t.Error(".vp-portal-head needs a z-index so body content cannot paint over the close button")
	}
}

// TestCloseButtonIsAReachableTarget pins the hit area and the absence of the
// rotate-on-hover, which stuck on after a tap because touch devices keep :hover.
func TestCloseButtonIsAReachableTarget(t *testing.T) {
	css := portalCSS(t)
	// Anchor on the top-level rule (column 0). The other two occurrences are
	// indented reduced-motion resets inside media queries, which declare only a
	// transition and would make this assertion look at the wrong block.
	block := cssBlock(t, css, "\n.vp-portal-close {")

	for _, want := range []string{"width: 44px", "height: 44px"} {
		if !strings.Contains(block, want) {
			t.Errorf(".vp-portal-close must declare %s (WCAG 2.5.5 minimum); block was:\n%s", want, block)
		}
	}
	// It must no longer be absolutely positioned inside the scrolling panel.
	if strings.Contains(block, "position: absolute") {
		t.Error(".vp-portal-close must not be absolutely positioned in the panel — it scrolls away and overlaps content")
	}
	// A rotate on hover reads as a stuck control on touch, where hover persists
	// after the tap.
	if strings.Contains(css, ".vp-portal-close:hover { transform: rotate(90deg); }") {
		t.Error("the close button must not rotate on hover — the state sticks after a tap on touch devices")
	}
	// The icon must not be able to become the event target.
	if !strings.Contains(css, ".vp-portal-close svg") || !strings.Contains(css, "pointer-events: none") {
		t.Error("the close icon must set pointer-events: none so the tap always lands on the button")
	}
}

// TestPanelSubtreeUsesBorderBox guards the input that spilled past the panel edge:
// width 100% plus padding measured wider than its container.
func TestPanelSubtreeUsesBorderBox(t *testing.T) {
	css := portalCSS(t)
	if !strings.Contains(css, ".vp-portal-overlay *,") {
		t.Error("the widget subtree must declare box-sizing: border-box, or a full-width input overflows the panel")
	}
}

// The account panel is one view on every theme and every page (3cw: on the
// account page it opened in an older look, because the view chosen hung on a
// page attribute one template forgot). It is a summary, not a dashboard: no
// folded boxes, no chips, one line per fact, each a link to its control.
func TestTheAccountPanelIsOneSummary(t *testing.T) {
	js := render.PortalJS
	i, j := strings.Index(js, "function viewAccount()"), strings.Index(js, "function loadAccount()")
	if i < 0 || j < i {
		t.Fatal("viewAccount and loadAccount not found in the portal script")
	}
	view := js[i:j]
	for _, not := range []string{"<details", "vp-acc", "vp-chip"} {
		if strings.Contains(view, not) {
			t.Errorf("the account view still carries %q", not)
		}
	}
	for _, not := range []string{"data-vp-portal') ===", "viewAccountSections"} {
		if strings.Contains(js, not) {
			t.Errorf("the portal script still picks an account view by page: %q", not)
		}
	}
	for _, want := range []string{
		"class=\"vp-me__row\"", "/members/account#", "/api/v1/members/me?detail=1",
		"'Two-factor'", "'Emails'", "'Your comments'", "'VayuTalk'", "/os/talk", "data-vp-logout",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the account panel is missing %q", want)
		}
	}
	css := portalCSS(t)
	for _, want := range []string{".vp-me__row {", ".vp-me__k", ".vp-me__v", ".vp-me__warn", ".vp-portal-body.vp-refresh > *"} {
		if !strings.Contains(css, want) {
			t.Errorf("portal.css is missing %q", want)
		}
	}
	if !strings.Contains(cssBlock(t, css, ".vp-me__row {"), "min-height: 3.4rem") {
		t.Error("the panel's lines are tap targets and must stay at least 44px tall")
	}
	for _, gone := range []string{".vp-acc", ".vp-chip", ".vp-portal-account-id"} {
		if strings.Contains(css, gone) {
			t.Errorf("portal.css still styles the retired %s", gone)
		}
	}
}

// TestBottomSheetSpansThePhone guards a source-order bug: the compact pass set
// max-width on the panel LATER in the file than the bottom-sheet block, so the
// sheet was pinned to 312px and sat inset from the screen edge.
func TestBottomSheetSpansThePhone(t *testing.T) {
	css := portalCSS(t)
	sheet := strings.LastIndex(css, "max-width: 100%; width: 100%;")
	compact := strings.Index(css, "max-width: 19.5rem;")
	if sheet < 0 {
		t.Fatal("the phone bottom sheet must restate a full-width max-width")
	}
	if compact >= 0 && sheet < compact {
		t.Error("the sheet's full-width rule must come AFTER the compact max-width, or source order pins it narrow")
	}
}

// cssBlock returns the first declaration block starting with the given selector.
func cssBlock(t *testing.T, css, selector string) string {
	t.Helper()
	i := strings.Index(css, selector)
	if i < 0 {
		t.Fatalf("selector %q not found in portal.css", selector)
	}
	end := strings.Index(css[i:], "}")
	if end < 0 {
		t.Fatalf("unterminated block for %q", selector)
	}
	return css[i : i+end]
}
