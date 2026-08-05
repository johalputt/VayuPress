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
		line := veilBootLine(self, vayuveil.SandboxState{})
		low := strings.ToLower(line)
		for _, forbidden := range []string{"screenshot", "protected", "secure", "safe"} {
			if strings.Contains(low, forbidden) {
				t.Errorf("%s: the boot line says %q, which a reader takes as a defence", name, forbidden)
			}
		}
		// And it must state the boundary positively, not merely avoid the words.
		if !strings.Contains(line, "enforced host-wide=0") {
			t.Errorf("%s: the boot line does not say nothing is enforced host-wide", name)
		}
		if !strings.Contains(line, "this process only") {
			t.Errorf("%s: the boot line does not scope the process controls", name)
		}
	}
}

// A dumpable process is the state worth waking up for, and the line must say
// what it costs rather than reporting it as a neutral fact.
func TestTheBootLineSaysWhatADumpableProcessCosts(t *testing.T) {
	line := veilBootLine(vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: false}, vayuveil.SandboxState{})
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
		CoreLimitKnown: true, CoreLimitZero: true}, vayuveil.SandboxState{})
	if !strings.Contains(ok, "undumpable (verified)") || !strings.Contains(ok, "core limit 0 (verified)") {
		t.Errorf("a verified state does not say so: %s", ok)
	}
	unk := veilBootLine(vayuveil.SelfHardening{Supported: true, Known: false}, vayuveil.SandboxState{})
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
	}, vayuveil.SandboxState{})
	if !strings.Contains(halfDone, "undumpable (verified)") {
		t.Error("the control that IS holding is not reported")
	}
	if !strings.Contains(halfDone, "CORE LIMIT NOT ZERO") {
		t.Error("the control that is NOT holding is hidden behind the one that is — which is the " +
			"exact reason the two are separate mechanisms")
	}
}

// The boot line has to carry the sandbox, because the operator this line exists
// for is the one who never opens the page — and a unit that predates the
// hardening block produces no other warning anywhere.
func TestTheBootLineReportsTheServiceSandbox(t *testing.T) {
	self := vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true,
		CoreLimitKnown: true, CoreLimitZero: true}

	hardened := veilBootLine(self, vayuveil.SandboxState{
		Supported: true, PrivateDev: true, PrivateDevKnown: true,
		NoNewPrivs: true, NoNewPrivsKnown: true})
	if !strings.Contains(hardened, "private /dev (verified)") {
		t.Errorf("a verified private /dev is not reported: %s", hardened)
	}

	shared := veilBootLine(self, vayuveil.SandboxState{
		Supported: true, PrivateDev: false, PrivateDevKnown: true,
		NoNewPrivs: false, NoNewPrivsKnown: true})
	if !strings.Contains(shared, "SHARED /dev") {
		t.Errorf("a shared /dev is not called out: %s", shared)
	}
	// A warning an operator cannot act on is a warning wasted.
	if !strings.Contains(shared, "PrivateDevices=yes") {
		t.Errorf("the line does not name the missing directive: %s", shared)
	}
	if !strings.Contains(shared, "NoNewPrivileges NOT set") {
		t.Errorf("a unit without NoNewPrivileges is not called out: %s", shared)
	}

	// Unknown is unknown, in the log as much as on the page.
	unknown := veilBootLine(self, vayuveil.SandboxState{Supported: true})
	if !strings.Contains(unknown, "service sandbox: unverified") {
		t.Errorf("an unread sandbox does not say so: %s", unknown)
	}
	if strings.Contains(unknown, "(verified)") && !strings.Contains(unknown, "undumpable (verified)") {
		t.Errorf("an unread sandbox claims verification: %s", unknown)
	}
}

// A unit missing its sandbox must WARN, not scroll past as info — and an
// unanswerable platform must NOT warn, or the level stops meaning anything on
// the machines where a real warning still matters.
func TestOnlyActionableStatesWarnAtBoot(t *testing.T) {
	hardenedSelf := vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true,
		CoreLimitKnown: true, CoreLimitZero: true}
	goodSandbox := vayuveil.SandboxState{Supported: true, PrivateDevKnown: true, PrivateDev: true}

	for name, tc := range map[string]struct {
		self vayuveil.SelfHardening
		sb   vayuveil.SandboxState
		warn bool
	}{
		"all good":         {hardenedSelf, goodSandbox, false},
		"dumpable process": {vayuveil.SelfHardening{Supported: true, Known: true}, goodSandbox, true},
		"unit without PrivateDevices": {hardenedSelf,
			vayuveil.SandboxState{Supported: true, PrivateDevKnown: true, PrivateDev: false}, true},
		"sandbox unreadable":   {hardenedSelf, vayuveil.SandboxState{Supported: true}, false},
		"platform unsupported": {vayuveil.SelfHardening{Supported: false}, vayuveil.SandboxState{}, false},
	} {
		if got := veilPostureIsWarning(tc.self, tc.sb); got != tc.warn {
			t.Errorf("%s: warning=%v, want %v", name, got, tc.warn)
		}
	}
}
