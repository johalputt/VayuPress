// SPDX-License-Identifier: Apache-2.0

package main

import (
	"regexp"
	"strings"
	"testing"
)

// AUDIT FINDING (pre-release adversarial pass).
//
// Several controls in the mailbox card build a CSS selector by pasting an email
// address after "#":
//
//	hx-include="#vm-q-dana@example.com"
//
// That is not a valid selector. An id SELECTOR must be a CSS identifier, and
// "@" is not a valid identifier character — so the browser parses "#vm-q-dana",
// then chokes on "@example", and querySelectorAll throws SyntaxError. htmx
// therefore includes NOTHING, and the field the button was supposed to send is
// silently dropped.
//
// The element itself is fine: getElementById finds it, because an id ATTRIBUTE
// may contain almost anything. Only the selector is invalid. That asymmetry is
// why this survives review — the markup looks correct, and it is, right up to
// the moment something tries to select it.
//
// Confirmed against a real DOM implementation, not by reading the spec:
//
//	querySelectorAll('#vm-q-dana@example.com')
//	  -> SyntaxError: Invalid selector
//	getElementById('vm-q-dana@example.com')
//	  -> found
//
// Consequences, in the order they cost something:
//
//   - The QUOTA save button has shipped in this state. An operator sets a
//     mailbox quota, presses Save, gets a success-looking refresh, and the value
//     never left the page.
//   - The HANDOVER confirmation could never be sent, so the one-way control
//     added in this same batch would refuse every attempt — a feature that
//     cannot succeed, guarded by a gate that can never be satisfied.

// selectorish matches the id-selector form these attributes used.
var selectorish = regexp.MustCompile(`hx-include="#[^"]*"`)

// cssIdent is the character set an id selector may contain without escaping.
var cssIdent = regexp.MustCompile(`^[A-Za-z_-][A-Za-z0-9_-]*$`)

func TestNoControlBuildsAnIDSelectorFromAnEmailAddress(t *testing.T) {
	src := readSourceFile(t, "vayuos_mail_accounts.go")
	for _, m := range selectorish.FindAllString(src, -1) {
		// A literal, constant selector is fine. What is not fine is one built by
		// concatenating a value — that value is an address on every current call
		// site, and an address always contains "@".
		if strings.Contains(m, `" +`) || strings.Contains(m, `+ "`) {
			t.Errorf("a CSS id selector is built by concatenation: %s\n"+
				"If the concatenated value is an email, the selector is invalid and "+
				"querySelectorAll throws, so the field is never included and the control "+
				"silently does nothing.", m)
		}
	}
}

// The rendered markup is the artefact that matters, so assert on it: every
// hx-include selector a mailbox card emits must actually be selectable.
func TestEveryRenderedIncludeSelectorIsValid(t *testing.T) {
	a := appWithMailAccounts(t)
	accs, err := a.vayuMail.Accounts().List(t.Context())
	if err != nil || len(accs) == 0 {
		t.Fatalf("no account to render: %v", err)
	}
	card := a.vayuAccountCard(t.Context(), accs[0])

	for _, m := range regexp.MustCompile(`hx-include="([^"]*)"`).FindAllStringSubmatch(card, -1) {
		sel := strings.TrimSpace(m[1])
		if !strings.HasPrefix(sel, "#") {
			continue // attribute and relative selectors are handled elsewhere
		}
		if !cssIdent.MatchString(strings.TrimPrefix(sel, "#")) {
			t.Errorf("rendered selector %q is not a valid CSS id selector, so querySelectorAll "+
				"throws and the control includes no fields at all", sel)
		}
	}
}

// The ids themselves must stay unique per mailbox, or one mailbox's Save button
// sends another mailbox's value — which would be a far worse bug than the one
// being fixed.
func TestPerMailboxFieldIDsAreDistinct(t *testing.T) {
	// DISTINCT mailboxes must not share an id, or one mailbox's Save button sends
	// another mailbox's field. The near-misses are deliberate: a naive
	// "replace every non-identifier character with -" would map all four of these
	// to the same string, which is why a digest is used instead.
	seen := map[string]string{}
	for _, email := range []string{
		"dana@example.com", "dana@example.org", "dan.a@example.com",
		"dan-a@example.com", "dana@example-com", "a@b.c", "",
	} {
		for _, prefix := range []string{"vm-q-", "vm-ho-"} {
			id := vmFieldID(prefix, email)
			if !cssIdent.MatchString(id) {
				t.Errorf("vmFieldID(%q, %q) = %q, which is not selector-safe", prefix, email, id)
			}
			if prev, dup := seen[id]; dup {
				t.Errorf("vmFieldID collision: %q and %q both produce %q — one mailbox's control "+
					"would send another mailbox's field", prev, prefix+email, id)
			}
			seen[id] = prefix + email
		}
	}

	// The SAME mailbox spelled differently must give the SAME id. Addresses are
	// case-insensitive throughout this store (normEmail lowercases on every
	// path), so two spellings are one mailbox — and an id that changed with the
	// casing would point the control at an element that is not on the page.
	for _, pair := range [][2]string{
		{"dana@example.com", "DANA@EXAMPLE.COM"},
		{"dana@example.com", "  dana@example.com  "},
	} {
		if a, b := vmFieldID("vm-q-", pair[0]), vmFieldID("vm-q-", pair[1]); a != b {
			t.Errorf("%q and %q are the same mailbox but produce %q and %q", pair[0], pair[1], a, b)
		}
	}
}
