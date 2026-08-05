// SPDX-License-Identifier: Apache-2.0

package veilaudit

// harden_report_test.go — ADR-0150 §5 S6, the report side.
//
// The row under test answers one question: is the difference between a
// directive being WRITTEN and being IN FORCE being reported honestly? Every
// assertion here is about that difference, because every way this feature can
// mislead an operator runs through it.

import (
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

const hardenRow = "Requested unit hardening"

func hardenInputs(h vayuveil.HardenState, sb vayuveil.SandboxState, start time.Time) Inputs {
	return Inputs{
		Enabled: true, Channels: vayuveil.Channels(),
		Observations: map[vayuveil.ChannelID]vayuveil.Observation{},
		Enforced:     map[vayuveil.Needs]bool{},
		Sandbox:      sb, Harden: h, ProcessStart: start,
	}
}

// THE test. A drop-in written after this process started has changed nothing
// about this process, and the row must be a Warn — the exposure is live until
// the restart. Toning it to Info would let a page that has fixed nothing yet
// read as a page that has fixed something.
func TestHardeningWrittenButNotYetInForceIsAWarningNotContext(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	row := rowFor(t, Run(hardenInputs(
		vayuveil.HardenState{Installed: true, HaveResult: true, DropInPresent: true, DropInAt: start.Add(time.Minute)},
		unsandboxed(), start)), hardenRow)

	if row.Status != Warn {
		t.Fatalf("a written-but-not-in-force drop-in must be Warn, got %v", row.Status)
	}
	if row.Status == Pass {
		t.Fatal("this row must never be a pass")
	}
	if !strings.Contains(row.Detail, "at exec") {
		t.Fatalf("the detail must explain WHY it is not in force; got %q", row.Detail)
	}
}

// The serious one, and the only Fail on this row: a drop-in that predates this
// process and still is not in force. Something wrote a configuration somewhere
// this service does not read from, and an operator has been told it was applied.
func TestAStaleDropInThatNeverTookIsAFailure(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	row := rowFor(t, Run(hardenInputs(
		vayuveil.HardenState{Installed: true, HaveResult: true, DropInPresent: true, DropInAt: start.Add(-time.Hour)},
		unsandboxed(), start)), hardenRow)

	if row.Status != Fail {
		t.Fatalf("a drop-in that predates the process and did not take must be a Fail, got %v", row.Status)
	}
	if !strings.Contains(row.Detail, "STILL not in force") {
		t.Fatalf("the detail must say it is still not in force; got %q", row.Detail)
	}
}

// The row is never green, on any input. Its job is the relationship between
// written and enforcing, and the controls themselves already have their own
// rows above — a second green row would inflate the "verified enforcing" tile
// with a control that does not separately exist.
func TestTheHardeningRowIsNeverAPass(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	fullyHardened := sandboxed()
	fullyHardened.SwapMaxZero, fullyHardened.SwapMaxKnown = true, true

	for _, tc := range []struct {
		name string
		h    vayuveil.HardenState
		sb   vayuveil.SandboxState
	}{
		{"everything in force", vayuveil.HardenState{Installed: true}, fullyHardened},
		{"pending", vayuveil.HardenState{Installed: true, Pending: true}, unsandboxed()},
		{"reverted", vayuveil.HardenState{Installed: true, HaveResult: true, Reverted: true}, unsandboxed()},
		{"failed", vayuveil.HardenState{Installed: true, HaveResult: true, Failed: true}, unsandboxed()},
		{"never requested", vayuveil.HardenState{Installed: true}, unsandboxed()},
		{"unreadable", vayuveil.HardenState{}, vayuveil.SandboxState{}},
	} {
		row := rowFor(t, Run(hardenInputs(tc.h, tc.sb, start)), hardenRow)
		if row.Status == Pass {
			t.Errorf("%s: the hardening row went green", tc.name)
		}
	}
	// No second assertion on the pass COUNT. It would be the same claim wearing
	// arithmetic: the row is never Pass, so a count that excludes it and a count
	// that includes it are equal by construction. A test that cannot fail
	// independently of the one above it is a line, not a check.
}

// A revert must reach the report even on an install that is fully hardened
// anyway. Something about this unit stopped the service starting, and that
// survives the current posture being fine.
func TestARevertSurvivesAHealthyPosture(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	full := sandboxed()
	full.SwapMaxZero, full.SwapMaxKnown = true, true

	row := rowFor(t, Run(hardenInputs(
		vayuveil.HardenState{Installed: true, HaveResult: true, Reverted: true,
			DropInPresent: true, DropInAt: start.Add(-time.Hour),
			Detail: "the service did not come back"},
		full, start)), hardenRow)

	if row.Status != Warn {
		t.Fatalf("a revert must be a warning, got %v", row.Status)
	}
	if !strings.Contains(row.Detail, "the service did not come back") {
		t.Fatalf("the worker's own reason must reach the report; got %q", row.Detail)
	}
}

// An unreadable sandbox produces Unverified, never a comfortable answer in
// either direction.
func TestAnUnreadableSandboxLeavesTheHardeningRowUnverified(t *testing.T) {
	row := rowFor(t, Run(hardenInputs(vayuveil.HardenState{Installed: true},
		vayuveil.SandboxState{}, time.Now())), hardenRow)
	if row.Status != Unverified {
		t.Fatalf("want Unverified, got %v", row.Status)
	}
}
