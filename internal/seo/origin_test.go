package seo

import "testing"

// TestOriginScheme is the ADR-0140 backbone: onion hosts get http://, every
// other (clearnet) host stays https:// — byte-identical to the old hardcoded
// prefix, so clearnet canonical/OG/sitemap URLs are unchanged.
func TestOriginScheme(t *testing.T) {
	cases := []struct{ host, want string }{
		{"example.com", "https://example.com"},
		{"blog.vayupress.com", "https://blog.vayupress.com"},
		{"abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwxy234.onion", "http://abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwxy234.onion"},
		{"XYZ.ONION", "http://XYZ.ONION"},
	}
	for _, c := range cases {
		if got := Origin(c.host); got != c.want {
			t.Errorf("Origin(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

func TestIsOnion(t *testing.T) {
	if !IsOnion("foo.onion") || !IsOnion("  BAR.Onion ") {
		t.Error("onion hosts must be detected (case/space-insensitive)")
	}
	for _, h := range []string{"", "example.com", "onion.example.com", "not-onion"} {
		if IsOnion(h) {
			t.Errorf("IsOnion(%q) should be false", h)
		}
	}
}
