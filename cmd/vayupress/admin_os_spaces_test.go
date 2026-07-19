package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// TestOSSpacesCardsCSPSafe verifies the Spaces page fragments are CSP-safe (no
// inline style, no external host) and carry the right content for each world.
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

	// Add-Tor card carries the exact provisioner command and reflects status.
	notProv := osSpacesAddTorCard(false, "")
	assertCSPSafe(t, "addtor/none", notProv)
	if !strings.Contains(notProv, torSpaceSetupCmd) {
		t.Errorf("add-tor card must show the command %q", torSpaceSetupCmd)
	}
	if !strings.Contains(notProv, "Not provisioned yet") {
		t.Error("add-tor card must show not-provisioned status when absent")
	}
	prov := osSpacesAddTorCard(true, "abcxyz.onion")
	assertCSPSafe(t, "addtor/provisioned", prov)
	if !strings.Contains(prov, "provisioned") || !strings.Contains(prov, "abcxyz.onion") {
		t.Error("provisioned add-tor card must show status + the sibling onion")
	}

	// Tor-self card shows the install's own onion.
	self := osSpacesTorSelfCard("abcxyz.onion")
	assertCSPSafe(t, "torself", self)
	if !strings.Contains(self, "abcxyz.onion") {
		t.Error("tor-self card must show this install's onion")
	}
}

// TestTorSpaceStateSafe: the sibling probe never panics and returns empty when
// no Tor Space is present (the sandbox has neither the unit nor the marker).
func TestTorSpaceStateSafe(t *testing.T) {
	prov, onion := torSpaceState()
	if prov {
		t.Skip("a Tor Space unit exists in this environment; skipping absence check")
	}
	if onion != "" {
		t.Errorf("onion should be empty when unprovisioned, got %q", onion)
	}
}

// TestOSSpacesFullRenderCSPSafe drives the whole page shell for both worlds via
// the card composition + layout, asserting CSP-safety and the correct badge.
func TestOSSpacesFullRenderCSPSafe(t *testing.T) {
	prev := config.Cfg.OnionMode
	defer func() { config.Cfg.OnionMode = prev }()

	build := func(onion bool, domain string) string {
		body := osSpacesHeader() + osSpacesCurrentCard(onion, domain) + osSpacesModelCard()
		if onion {
			body += osSpacesTorSelfCard(domain)
		} else {
			body += osSpacesAddTorCard(false, "")
		}
		return adminOSShellHead("N", "Spaces", "spaces", &osSettings{SiteName: "Demo"}) +
			body + adminOSShellFoot("N", osSpacesScript, false)
	}

	config.Cfg.OnionMode = false
	clear := build(false, "johal.in")
	assertCSPSafe(t, "spaces/full/clearnet", clear)
	if !strings.Contains(clear, torSpaceSetupCmd) {
		t.Error("clearnet Spaces page must surface the provisioner command")
	}

	config.Cfg.OnionMode = true
	tor := build(true, "abcxyz.onion")
	assertCSPSafe(t, "spaces/full/tor", tor)
}
