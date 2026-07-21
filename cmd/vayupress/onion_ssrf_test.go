package main

import (
	"strings"
	"testing"
)

// TestIsDeliverableOnionHost locks the strict SSRF gate on the onion-to-onion
// network sinks: only a bare v3 onion (56 base32 chars + ".onion") is accepted,
// so a recipient/sender address can never smuggle a port, path, userinfo or an
// extra label to steer the request at another host.
func TestIsDeliverableOnionHost(t *testing.T) {
	good := strings.Repeat("a", 56) + ".onion"
	if !isDeliverableOnionHost(good) {
		t.Fatalf("valid v3 onion rejected: %q", good)
	}
	if !isDeliverableOnionHost("  " + strings.ToUpper(good) + "  ") {
		t.Errorf("case/space-normalised v3 onion should pass")
	}
	bad := []string{
		"", ".onion", "example.com", "example.onion",
		strings.Repeat("a", 55) + ".onion", // too short
		strings.Repeat("a", 57) + ".onion", // too long
		good + ":9999",                     // port
		good + "/evil",                     // path
		"evil.com@" + good,                 // userinfo smuggle
		"sub." + good,                      // extra label
		strings.Repeat("1", 56) + ".onion", // '1' is outside the base32 alphabet
	}
	for _, h := range bad {
		if isDeliverableOnionHost(h) {
			t.Errorf("expected reject for %q", h)
		}
	}
}
