// SPDX-License-Identifier: Apache-2.0

package main

// vayuveil_audit_test.go — the pre-release adversarial pass over ADR-0150 P0.
//
// Both findings below came from attacking the subsystem rather than reviewing
// it, and neither is a bug in the sense of "the code does not do what it says".
// Each is the code doing exactly what it says, where what it says was wrong.

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

// ATTACK: read the panel over someone's shoulder, or out of a screenshot they
// pasted into a support thread. What does it tell me about their machine?
func TestProbeDetailDoesNotPrintRawEnvironmentValues(t *testing.T) {
	h := vayuveil.Host{
		Exists:   func(string) bool { return false },
		Readable: func(string) bool { return false },
		Glob:     func(string) []string { return nil },
		ReadFile: func(string) string { return "" },
		Env: func(k string) string {
			switch k {
			case "WAYLAND_DISPLAY":
				return "wayland-0"
			case "DBUS_SESSION_BUS_ADDRESS":
				return "unix:path=/run/user/1000/bus,guid=deadbeef"
			case "AT_SPI_BUS_ADDRESS":
				return "unix:path=/run/user/1000/at-spi/bus_0"
			case "DISPLAY":
				return ":0"
			}
			return ""
		},
	}
	for id, obs := range vayuveil.Inventory(h) {
		for _, secret := range []string{"/run/user/1000", "guid=deadbeef", "at-spi/bus_0"} {
			if strings.Contains(obs.Detail, secret) {
				t.Errorf("channel %q prints %q into the panel. A bus address carries the runtime "+
					"directory and the numeric UID; the operator needs to know a session is "+
					"addressable, not its socket path.", id, secret)
			}
		}
	}
}

// ATTACK: send the toggle something it does not recognise. A control that
// silently does the OPPOSITE of what was asked is worse than one that refuses —
// an operator who fires "activate" with a typo, a stale client, or a proxy that
// mangles the query gets deactivation and a success response saying so quietly.
func TestTheToggleRefusesAValueItDoesNotUnderstand(t *testing.T) {
	for _, v := range []string{"true", "yes", "ON", "enable", "", "2", "1 "} {
		state, ok := veilToggleState(v)
		if ok && state == "on" {
			continue // an explicit, understood yes
		}
		if ok && state == "off" {
			t.Errorf("query on=%q was accepted and interpreted as DEACTIVATE. Anything that is not "+
				"a recognised on or off must be refused, not silently inverted.", v)
		}
	}
	if s, ok := veilToggleState("1"); !ok || s != "on" {
		t.Error("the documented activate value is not accepted")
	}
	if s, ok := veilToggleState("0"); !ok || s != "off" {
		t.Error("the documented deactivate value is not accepted")
	}
}
