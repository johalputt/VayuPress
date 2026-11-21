// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
	"time"

	vtor "github.com/johalputt/vayupress/internal/vayuos/vayutor"
)

// VayuTor is drawn in its reader's language. What is code stays code in
// every language (a command, a header's name, an address), and what the
// engine recorded (a health reason) is shown as it was recorded.
func TestVayuTorIsDrawnInThePagesLanguage(t *testing.T) {
	setup := torSetup("de", "n", true)
	for _, want := range []string{"Onion-Dienste sind aus", "Onion-Dienste einschalten"} {
		if !strings.Contains(setup, want) {
			t.Errorf("the German setup page lacks %q", want)
		}
	}
	if strings.Contains(setup, "Turn on onion services") {
		t.Error("the German setup page still says its button in English")
	}

	st := vtor.Status{Health: vtor.HealthDown, HealthSince: time.Now().Add(-90 * time.Minute), HealthReason: "no circuit built",
		HealthLog: []vtor.HealthEvent{{State: vtor.HealthHealthy, At: time.Now().Add(-3 * time.Hour)}}}
	health := osTorHealthCard("de", st)
	for _, want := range []string{"Zustand und Warnungen", "seit 1 Std. 30 Min.", "<code>tor.onion_down</code>", `translate="no">no circuit built`, "Gesund"} {
		if !strings.Contains(health, want) {
			t.Errorf("the German health card lacks %q", want)
		}
	}
	remedy := osTorLogRemedy("de", "[warn] No route to host")
	if !strings.Contains(remedy, "Dieses Netzwerk blockiert Tor auf IP-Ebene.") || !strings.Contains(remedy, "<code>VAYUOS_TOR_BRIDGES</code>") {
		t.Errorf("the German log remedy reads %q", remedy)
	}
	if privacy := osTorPrivacyNote("de", vtor.Status{}); !strings.Contains(privacy, "Nichts wird verfolgt.") || strings.Contains(privacy, "Nothing is tracked") {
		t.Errorf("the German privacy note reads %q", privacy)
	}
}
