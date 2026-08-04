// SPDX-License-Identifier: Apache-2.0

package main

// vayuveil_boot_test.go — the boot line is a claim, and it reaches further than
// the page does.
//
// An operator who never opens the console still reads their log. A sentence
// there that overstates what is protected is the same defect as a green tile,
// distributed more widely and harder to correct later.

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

// The line must never suggest the machine is defended, in ANY state.
func TestTheBootLineNeverOverstatesWhatIsEnforced(t *testing.T) {
	for name, self := range map[string]vayuveil.SelfHardening{
		"fully hardened": {Supported: true, Known: true, Undumpable: true,
			CoreLimitKnown: true, CoreLimitZero: true},
		"dumpable":     {Supported: true, Known: true, Undumpable: false, CoreLimitKnown: true},
		"unverifiable": {Supported: false},
	} {
		line := veilBootLine(self)
		low := strings.ToLower(line)
		for _, forbidden := range []string{"screenshot", "protected", "secure", "safe"} {
			if strings.Contains(low, forbidden) {
				t.Errorf("%s: the boot line says %q, which a reader takes as a defence", name, forbidden)
			}
		}
		// And it must state the boundary positively, not merely avoid the words.
		if !strings.Contains(line, "enforces none of it on this host") {
			t.Errorf("%s: the boot line does not say Phase 0 enforces nothing on the host", name)
		}
		if !strings.Contains(line, "this process only") {
			t.Errorf("%s: the boot line does not scope the process controls", name)
		}
	}
}

// A dumpable process is the state worth waking up for, and the line must say
// what it costs rather than reporting it as a neutral fact.
func TestTheBootLineSaysWhatADumpableProcessCosts(t *testing.T) {
	line := veilBootLine(vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: false})
	if !strings.Contains(line, "DUMPABLE") {
		t.Error("a dumpable process is not called out in the boot line")
	}
	for _, must := range []string{"session token", "keystore"} {
		if !strings.Contains(line, must) {
			t.Errorf("the line does not say that %q is what a dump would expose", must)
		}
	}
}

// Verified means verified. The line must distinguish "we checked and it holds"
// from "we could not check", because those read identically if you let them.
func TestTheBootLineDistinguishesVerifiedFromUnverified(t *testing.T) {
	ok := veilBootLine(vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true,
		CoreLimitKnown: true, CoreLimitZero: true})
	if !strings.Contains(ok, "undumpable (verified)") || !strings.Contains(ok, "core limit 0 (verified)") {
		t.Errorf("a verified state does not say so: %s", ok)
	}
	unk := veilBootLine(vayuveil.SelfHardening{Supported: true, Known: false})
	if !strings.Contains(unk, "unverified") {
		t.Errorf("an unverifiable state does not say so: %s", unk)
	}
	if strings.Contains(unk, "(verified)") {
		t.Errorf("an unverifiable state claims verification: %s", unk)
	}
}

// The two controls are reported separately. Folding them into one verdict hides
// either of them failing, which is the reason there are two.
func TestTheBootLineReportsBothControlsSeparately(t *testing.T) {
	halfDone := veilBootLine(vayuveil.SelfHardening{
		Supported: true, Known: true, Undumpable: true,
		CoreLimitKnown: true, CoreLimitZero: false,
	})
	if !strings.Contains(halfDone, "undumpable (verified)") {
		t.Error("the control that IS holding is not reported")
	}
	if !strings.Contains(halfDone, "CORE LIMIT NOT ZERO") {
		t.Error("the control that is NOT holding is hidden behind the one that is — which is the " +
			"exact reason the two are separate mechanisms")
	}
}
