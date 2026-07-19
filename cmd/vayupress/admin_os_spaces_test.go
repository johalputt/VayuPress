package main

import (
	"strings"
	"testing"
)

// TestOSSpacesCardsCSPSafe verifies the Spaces fragments are CSP-safe and carry
// the right content, including the one-click Anonymous Tor Space toggle.
func TestOSSpacesCardsCSPSafe(t *testing.T) {
	header := osSpacesHeader()
	assertCSPSafe(t, "osSpacesHeader", header)

	model := osSpacesModelCard()
	assertCSPSafe(t, "osSpacesModelCard", model)
	for _, want := range []string{"Clearnet Space", "Tor Space", "no mesh"} {
		if !strings.Contains(model, want) {
			t.Errorf("model card missing %q", want)
		}
	}

	// Current-space card, both worlds.
	clear := osSpacesCurrentCard(false, "johal.in")
	assertCSPSafe(t, "current/clearnet", clear)
	if !strings.Contains(clear, "space-badge--clearnet") || strings.Contains(clear, "space-badge--tor") {
		t.Error("clearnet current card must show only the clearnet badge")
	}
	tor := osSpacesCurrentCard(true, "abcxyz.onion")
	assertCSPSafe(t, "current/tor", tor)
	if !strings.Contains(tor, "space-badge--tor") || !strings.Contains(tor, "abcxyz.onion") {
		t.Error("tor current card must show the tor badge and the onion")
	}

	// Off state: the toggle offers to turn ON.
	off := osSpacesTorSpaceCard(torSpaceStatus{Enabled: false})
	assertCSPSafe(t, "torspace/off", off)
	if !strings.Contains(off, `data-space-toggle="on"`) || !strings.Contains(off, "Turn on") {
		t.Error("off card must offer to turn the Tor Space on")
	}
	if !strings.Contains(off, "same server") {
		t.Error("card must carry the honest same-server caveat")
	}

	// Running state: offers OFF, shows the onion, marks Running.
	on := osSpacesTorSpaceCard(torSpaceStatus{Enabled: true, Running: true, Onion: "abcxyz.onion", Port: 8347})
	assertCSPSafe(t, "torspace/on", on)
	if !strings.Contains(on, `data-space-toggle="off"`) || !strings.Contains(on, "Running") {
		t.Error("running card must offer OFF and show Running")
	}
	if !strings.Contains(on, "abcxyz.onion") {
		t.Error("running card must show the anonymous onion address")
	}

	// Error surfaces (html-escaped).
	errCard := osSpacesTorSpaceCard(torSpaceStatus{Enabled: true, LastErr: "boom <x>"})
	assertCSPSafe(t, "torspace/err", errCard)
	if !strings.Contains(errCard, "boom &lt;x&gt;") {
		t.Error("error must be html-escaped and shown")
	}

	// Tor-self card (whole-install Tor).
	self := osSpacesTorSelfCard("abcxyz.onion")
	assertCSPSafe(t, "torself", self)
	if !strings.Contains(self, "abcxyz.onion") {
		t.Error("tor-self card must show this install's onion")
	}
}
