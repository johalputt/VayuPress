// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// Sign-in security opens on whether a second factor guards the account, as a
// status page, and offers the one control that changes it: set up in a sheet
// when off, turn off when on. Every control sits inside data-totp-card, the
// wrapper the page's script binds through, or it renders and does nothing.
func TestSignInSecuritySaysItsStateAndOffersItsControl(t *testing.T) {
	for _, c := range []struct {
		name              string
		enabled           bool
		tone, state, hook string
		sheet             bool
	}{
		{"off", false, "sa-status__head--warn", "Two-factor sign-in is off", "data-totp-begin", true},
		{"on", true, "sa-status__head--ok", "Two-factor sign-in is on", "data-totp-disable", false},
	} {
		page := string(securityPage(c.enabled, ""))
		if !strings.HasPrefix(page, `<div data-totp-card>`) || !strings.HasSuffix(page, `</div>`) {
			t.Errorf("%s: the page and its sheet are not inside data-totp-card, so its script binds nothing", c.name)
		}
		if !strings.Contains(page, `data-page-kind="status"`) {
			t.Errorf("%s: Sign-in security is not a status page", c.name)
		}
		head := page[strings.Index(page, "sa-status__head"):]
		if !strings.HasPrefix(head, "sa-status__head "+c.tone) || !strings.Contains(head, c.state) {
			t.Errorf("%s: the page does not open on %q (%s)", c.name, c.state, c.tone)
		}
		if !strings.Contains(page, c.hook) {
			t.Errorf("%s: the %s control is missing", c.name, c.hook)
		}
		if got := strings.Contains(page, `id="totp-sheet"`); got != c.sheet {
			t.Errorf("%s: the set-up sheet is present: %v, want %v", c.name, got, c.sheet)
		}
		if c.sheet && !strings.Contains(page, `data-sheet="totp-sheet" data-totp-begin`) {
			t.Errorf("%s: Set up does not open the sheet it fills", c.name)
		}
		for _, hook := range []string{"data-totp-enroll", "data-totp-qr", "data-totp-key", "data-totp-code", "data-totp-verify"} {
			if c.sheet && !strings.Contains(page, hook) {
				t.Errorf("%s: the sheet lacks %s, which the script fills", c.name, hook)
			}
		}
	}
}
