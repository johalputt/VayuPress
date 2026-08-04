// SPDX-License-Identifier: Apache-2.0

package render

import (
	"regexp"
	"strings"
	"testing"
)

// articleConstCSS returns the stylesheet the public article page actually
// loads. It is the const, not static/css/article.css — WriteCSSAssets rewrites
// that file from this const at every boot.
func articleConstCSS() string { return articleCSSMin }

// contactClassRe pulls every className the contact widget assigns to an element
// it creates, so the check is driven by what the widget emits rather than by a
// list someone remembered to update.
var contactClassRe = regexp.MustCompile(`className='(vayu-contact[A-Za-z0-9_\- ]*)'`)

// cssRuleFor extracts the declaration body of the FIRST rule whose selector ends
// in the given class, matched on a whole identifier. Returning the block — not a
// yes/no — is deliberate: an assertion that cannot say WHICH rule it matched is
// not an assertion, and a substring test for ".vayu-contact" is satisfied by
// ".vayu-contact-heading".
func cssRuleFor(css, class string) (string, bool) {
	re := regexp.MustCompile(`([^{}]*\.` + regexp.QuoteMeta(class) + `)\{([^}]*)\}`)
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		sel := m[1]
		// The selector must END with this class, not merely contain it as a prefix.
		if i := strings.LastIndex(sel, "."+class); i >= 0 && i+len(class)+1 == len(sel) {
			return m[2], true
		}
	}
	return "", false
}

// The contact form is product markup, not theme markup: it must render on every
// install regardless of which theme is active. Every class it emits therefore
// has to be styled by the shipped stylesheet.
//
// It was not. All eight classes were defined in no stylesheet anywhere — not the
// shipped defaults, not any of the eleven themes — so the whole form rendered as
// raw browser controls.
func TestContactWidgetClassesAreStyled(t *testing.T) {
	css := articleConstCSS()
	seen := map[string]bool{}
	for _, m := range contactClassRe.FindAllStringSubmatch(ContactJS, -1) {
		for _, cls := range strings.Fields(m[1]) {
			seen[cls] = true
		}
	}
	if len(seen) < 6 {
		t.Fatalf("only found %d contact classes in ContactJS; the extractor is broken and this test proves nothing", len(seen))
	}
	for cls := range seen {
		if _, ok := cssRuleFor(css, cls); !ok {
			t.Errorf("the contact widget emits %q and articleCSSMin defines no rule for it — "+
				"that element renders unstyled on every theme", cls)
		}
	}
}

// TestTheContactHoneypotIsActuallyHidden — the attacker's-eye finding.
//
// The widget's own comment calls the "website" input a honeypot "hidden from
// humans", and handlers_contact.go SILENTLY DISCARDS any submission where it is
// non-empty, answering 200 with {"status":"ok"}. The only thing that hides it is
// the .vayu-contact-hp rule: tabIndex=-1 removes it from the tab order and
// aria-hidden removes it from the accessibility tree, but NEITHER hides it
// visually.
//
// With no rule, a visitor saw an unlabelled empty text box between the message
// field and Send. Anyone who typed in it was told "Thanks! Your message has been
// sent." and their message was thrown away. A missing CSS rule caused silent
// data loss, which is why this is a test and not a style preference.
func TestTheContactHoneypotIsActuallyHidden(t *testing.T) {
	if !strings.Contains(ContactJS, `hp.className='vayu-contact-hp'`) {
		t.Fatal("the honeypot input no longer carries vayu-contact-hp; this test is checking the wrong thing")
	}
	decls, ok := cssRuleFor(articleConstCSS(), "vayu-contact-hp")
	if !ok {
		t.Fatal("no rule for .vayu-contact-hp — the honeypot renders as a visible text box, and anything " +
			"typed into it is silently discarded by handlers_contact.go")
	}
	// Assert on the declarations of THAT rule, not on the sheet as a whole.
	flat := strings.ReplaceAll(decls, " ", "")
	hides := strings.Contains(flat, "position:absolute") ||
		strings.Contains(flat, "display:none") ||
		strings.Contains(flat, "visibility:hidden")
	if !hides {
		t.Errorf(".vayu-contact-hp exists but does not take the field out of view: %q", decls)
	}
	// Off-screen, not merely transparent: an opacity-only rule still occupies
	// layout and can be clicked into.
	if !strings.Contains(flat, "left:-") && !strings.Contains(flat, "display:none") {
		t.Errorf(".vayu-contact-hp is positioned but not moved off-screen, so it still occupies layout: %q", decls)
	}
}
