package main

import "testing"

// TestMailAddrOf pins the webmail display helper (VayuDomains Stage 3d): a bare
// local part is qualified with the primary domain (unchanged), while an engine
// key that already carries a domain (a secondary mailbox) is shown as-is.
func TestMailAddrOf(t *testing.T) {
	cases := []struct{ key, primary, want string }{
		{"bob", "johal.in", "bob@johal.in"},                   // primary local part
		{"bob@shop.example", "johal.in", "bob@shop.example"},  // secondary full address
		{"sales.team+x", "johal.in", "sales.team+x@johal.in"}, // punctuated local part
		{"a@b.example", "johal.in", "a@b.example"},            // already-full stays put
	}
	for _, c := range cases {
		if got := mailAddrOf(c.key, c.primary); got != c.want {
			t.Errorf("mailAddrOf(%q,%q)=%q, want %q", c.key, c.primary, got, c.want)
		}
	}
}
