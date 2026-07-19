package main

import "testing"

// TestPendingTorSiteRoundTrip: a freshly-minted placeholder host is recognised as
// a pending Tor site, is unique per call, and a real .onion (or a normal clearnet
// host) is NOT mistaken for one — so the "Minting .onion…" label and the
// auto-refresh-while-pending only ever fire on genuinely-pending rows.
func TestPendingTorSiteRoundTrip(t *testing.T) {
	a, b := newPendingHost(), newPendingHost()
	if a == b {
		t.Fatalf("newPendingHost must be unique per call, got %q twice", a)
	}
	for _, h := range []string{a, b} {
		if !isPendingTorSite(h) {
			t.Errorf("newPendingHost() = %q not recognised as pending", h)
		}
	}
	for _, h := range []string{
		"abcdefghij234567.onion", // an assigned onion
		"shop.example.com",       // a clearnet host
		"pending-nope.onion",     // pending prefix but a real onion, not a placeholder
		"",
	} {
		if isPendingTorSite(h) {
			t.Errorf("isPendingTorSite(%q) = true, want false", h)
		}
	}
}
