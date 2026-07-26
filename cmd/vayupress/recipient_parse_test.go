// SPDX-License-Identifier: Apache-2.0

package main

import (
	"reflect"
	"testing"
)

// TestParseRecipientList pins the fix for outbound to a "Name <email>" address:
// the bare address is extracted (so a reply to `VayuPress Hello
// <hello@vayupress.com>` delivers to hello@vayupress.com and is recognised as a
// local mailbox, not the malformed host `vayupress.com>`).
func TestParseRecipientList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"hello@vayupress.com", []string{"hello@vayupress.com"}},
		{"VayuPress Hello <hello@vayupress.com>", []string{"hello@vayupress.com"}},
		{"a@x.com, Bob <b@y.com>", []string{"a@x.com", "b@y.com"}},
		{`"Doe, John" <john@x.com>`, []string{"john@x.com"}}, // quoted comma in display name
		{"  spaced@x.com  ", []string{"spaced@x.com"}},
	}
	for _, c := range cases {
		got := parseRecipientList(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseRecipientList(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}

	// A malformed token is kept verbatim (so the operator sees it) rather than
	// dropped — but a valid sibling still parses to its bare address.
	got := parseRecipientList("not an address, good@x.com")
	found := false
	for _, a := range got {
		if a == "good@x.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("valid sibling recipient lost: %#v", got)
	}
	// Critically: no token should carry a trailing angle bracket.
	for _, a := range parseRecipientList("VayuPress Hello <hello@vayupress.com>") {
		if a == "" || a[len(a)-1] == '>' {
			t.Errorf("recipient still carries angle bracket: %q", a)
		}
	}
}
