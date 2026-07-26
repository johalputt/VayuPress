// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuos/torspace"
)

// TestTorSpaceChildIsNotDegradedForHavingNoEngine fixes a diagnostic that accused
// the system of a fault it did not have.
//
// Inside a Tor Space, the console showed "vayutor — DEGRADED — disabled
// (VAYUOS_TOR=off)". That is the architecture, not a failure: BuildChildEnv sets
// VAYUOS_TOR=off on purpose, because the PARENT publishes the onion the child is
// being reached over, and a child running its own engine would be a second onion
// world nested inside the first. So the one console guaranteed to be served over
// Tor was the one reporting Tor as broken.
func TestTorSpaceChildIsNotDegradedForHavingNoEngine(t *testing.T) {
	// The premise: the child really is spawned with the engine switched off.
	env := torspace.BuildChildEnv(t.TempDir(), "example.onion", 8099, "k")
	var off bool
	for _, e := range env {
		if e == "VAYUOS_TOR=off" {
			off = true
		}
	}
	if !off {
		t.Fatal("BuildChildEnv no longer disables the engine — this test's premise is gone")
	}

	t.Setenv(torspace.EnvSpaceChild, "1")
	// a.vayuTor is nil, exactly as it is in a child: without the guard this is the
	// branch that returned DEGRADED.
	ok, detail := (&App{}).vayuTorHealth()
	if !ok {
		t.Errorf("a Tor Space child reports vayutor as degraded (%q); it is reached over the parent's onion", detail)
	}
	if strings.Contains(detail, "disabled") {
		t.Errorf("detail = %q; it must explain the design, not read as a fault", detail)
	}
}

// TestClearnetInstallStillReportsTorOff guards the other side: outside a Tor Space,
// an engine that is switched off is genuinely worth surfacing, and the fix above
// must not silence it everywhere.
func TestClearnetInstallStillReportsTorOff(t *testing.T) {
	t.Setenv(torspace.EnvSpaceChild, "")
	ok, detail := (&App{}).vayuTorHealth()
	if ok {
		t.Error("a clearnet install with no engine must still report vayutor as not running")
	}
	if !strings.Contains(detail, "VAYUOS_TOR=off") {
		t.Errorf("detail = %q, want the actionable env-var hint", detail)
	}
}
