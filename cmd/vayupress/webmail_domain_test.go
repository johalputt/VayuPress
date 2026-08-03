// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// TestMailAddrOf pins the webmail display helper (VayuDomains Stage 3d): a bare
// local part is qualified with the primary domain (unchanged), while an engine
// key that already carries a domain (a secondary mailbox) is shown as-is.
func TestMailAddrOf(t *testing.T) {
	cases := []struct{ key, primary, want string }{
		{"bob", "example.test", "bob@example.test"},                   // primary local part
		{"bob@shop.example", "example.test", "bob@shop.example"},      // secondary full address
		{"sales.team+x", "example.test", "sales.team+x@example.test"}, // punctuated local part
		{"a@b.example", "example.test", "a@b.example"},                // already-full stays put
	}
	for _, c := range cases {
		if got := mailAddrOf(c.key, c.primary); got != c.want {
			t.Errorf("mailAddrOf(%q,%q)=%q, want %q", c.key, c.primary, got, c.want)
		}
	}
}
