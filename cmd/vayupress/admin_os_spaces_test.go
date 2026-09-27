// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/ui"
)

// TestSpaceSwitch covers the top-of-sidebar one-click world switch (ADR-0141):
// admin-only, CSP-safe, correct active segment, and static (non-switchable) on a
// whole-install Tor world.
func TestSpaceSwitch(t *testing.T) {
	// Non-admins never see the switch.
	if got := spaceSwitch(accessAuthor, &osSettings{}); got != "" {
		t.Errorf("spaceSwitch must be empty for non-admins, got %q", got)
	}
	if got := spaceSwitch(accessEditor, &osSettings{TorSpaceOn: true}); got != "" {
		t.Errorf("spaceSwitch must be empty for editors, got %q", got)
	}

	// Clearnet install: the switch is a clean two-segment control — Clearnet active,
	// Tor the click-to-enter segment. The live .onion status now lives on the
	// dashboard (osWorldCard), not under the switch, so no status panel is rendered.
	off := spaceSwitch(accessAdmin, &osSettings{})
	assertCSPSafe(t, "spaceSwitch/off", off)
	if !strings.Contains(off, `data-space-switch="on"`) || !strings.Contains(off, `data-space-switch="off"`) {
		t.Error("clearnet switch must offer both segments")
	}
	if !strings.Contains(off, `class="space-switch__seg is-active" data-space-switch="off"`) {
		t.Error("with Tor off, the Clearnet segment must be active")
	}
	if strings.Contains(off, "space-switch__status") {
		t.Error("the .onion status moved to the dashboard — no status panel under the switch")
	}

	// Enabling the Space must NOT change the switch itself (status is on the
	// dashboard now): Clearnet stays active, Tor stays the click-to-enter control,
	// and no .onion is crammed under the switch.
	on := spaceSwitch(accessAdmin, &osSettings{TorSpaceOn: true, TorSpaceRunning: true, TorSpaceOnion: "abcxyz.onion"})
	assertCSPSafe(t, "spaceSwitch/on", on)
	if !strings.Contains(on, `class="space-switch__seg is-active" data-space-switch="off"`) {
		t.Error("Clearnet must stay the active segment on the clearnet console")
	}
	if strings.Contains(on, "data-copy") || strings.Contains(on, "abcxyz.onion") {
		t.Error("the switch must not carry the .onion — it now lives on the dashboard")
	}

	// Whole-install Tor world: static indicator with a working Clearnet back-link,
	// no interactive switch buttons.
	prev := config.Cfg.OnionMode
	config.Cfg.OnionMode = true
	defer func() { config.Cfg.OnionMode = prev }()
	self := spaceSwitch(accessAdmin, &osSettings{})
	assertCSPSafe(t, "spaceSwitch/self", self)
	if strings.Contains(self, "data-space-switch") {
		t.Error("a dedicated Tor install must not offer a switch control")
	}
	if !strings.Contains(self, "is-active") || !strings.Contains(self, "/os/world?target=clearnet") {
		t.Error("dedicated Tor install must show Tor active + a Clearnet back-link")
	}
}

// TestWorldsPartsCSPSafe verifies the Worlds page's parts are CSP-safe and
// carry the right content, including the one-click Anonymous Tor Space toggle.
func TestWorldsPartsCSPSafe(t *testing.T) {
	// Off state: the toggle offers to turn ON, with the honest limit beside it.
	off := string(osSpacesTorSpace(torSpaceStatus{Enabled: false}))
	assertCSPSafe(t, "torspace/off", off)
	if !strings.Contains(off, `data-space-toggle="on"`) || !strings.Contains(off, "Turn on") {
		t.Error("an Off Tor Space must offer to turn it on")
	}
	if !strings.Contains(off, "both worlds run on this server") {
		t.Error("the page must say the two worlds share this server")
	}

	// Running state: offers OFF, shows the onion, reads Running.
	on := string(osSpacesTorSpace(torSpaceStatus{Enabled: true, Running: true, Onion: "abcxyz.onion", Port: 8347}))
	assertCSPSafe(t, "torspace/on", on)
	if !strings.Contains(on, `data-space-toggle="off"`) || !strings.Contains(on, ">Running<") {
		t.Error("a running Tor Space must offer Turn off and read Running")
	}
	if !strings.Contains(on, "http://abcxyz.onion") {
		t.Error("a running Tor Space must show its onion address")
	}

	// Error surfaces, escaped once.
	errCard := string(osSpacesTorSpace(torSpaceStatus{Enabled: true, LastErr: "boom <x>"}))
	assertCSPSafe(t, "torspace/err", errCard)
	if !strings.Contains(errCard, "boom &lt;x&gt;") {
		t.Error("an error must be shown, escaped")
	}

	// A whole-install Tor world's own address.
	self := string(ui.Rows(onionAddressRow("abcxyz.onion")))
	assertCSPSafe(t, "torself", self)
	if !strings.Contains(self, `data-copy="http://abcxyz.onion"`) {
		t.Error("the address row must copy this install's onion")
	}
}
