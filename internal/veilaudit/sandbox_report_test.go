// SPDX-License-Identifier: Apache-2.0

package veilaudit

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

// sandboxed is a service running under the shipped unit: private /dev, exactly
// CAP_NET_BIND_SERVICE, no_new_privs on, seccomp filtering.
func sandboxed() vayuveil.SandboxState {
	return vayuveil.SandboxState{
		Supported:       true,
		NoNewPrivs:      true,
		NoNewPrivsKnown: true,
		SeccompMode:     2, SeccompModeKnown: true,
		CapEff: 1 << vayuveil.CapNetBindService, CapEffKnown: true,
		PrivateDev: true, PrivateDevKnown: true,
		PrivateTmp: true, PrivateTmpKnown: true,
		ProtectedHome: true, ProtectedHomeKnown: true,
	}
}

// unsandboxed is the same binary started by hand, or from a unit predating the
// hardening block. Everything was READ; the answers are just bad.
func unsandboxed() vayuveil.SandboxState {
	return vayuveil.SandboxState{
		Supported:       true,
		NoNewPrivs:      false,
		NoNewPrivsKnown: true,
		CapEff:          0, CapEffKnown: true,
		PrivateDev: false, PrivateDevKnown: true,
		PrivateTmp: false, PrivateTmpKnown: true,
		ProtectedHome: false, ProtectedHomeKnown: true,
	}
}

func deviceMiss(technique string) vayuveil.AttackResult {
	return vayuveil.AttackResult{
		Technique: technique, Asset: vayuveil.AssetFramebuffer,
		Outcome: vayuveil.AttackNothingPresent, ViaDeviceNode: true,
		Detail: "nothing matches /dev/fb* on this host, so there was no target.",
	}
}

func rowFor(t *testing.T, checks []Check, titlePart string) Check {
	t.Helper()
	var found []Check
	for _, c := range checks {
		if strings.Contains(c.Title, titlePart) {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one row containing %q, found %d", titlePart, len(found))
	}
	return found[0]
}

// THE row this change exists for. A device-node technique that found nothing on
// a service with a VERIFIED private /dev has not found an empty machine — it has
// run into a control that is in force. The old text said "the absence of a
// device, not the presence of a control", which understated real protection on
// every correctly deployed install.
func TestADeniedDeviceNodeIsCreditedAsAControlNotAsAnEmptyMachine(t *testing.T) {
	in := on(allAbsent())
	in.Sandbox = sandboxed()
	in.RedTeam = []vayuveil.AttackResult{deviceMiss("read the framebuffer directly")}

	row := rowFor(t, Run(in), "read the framebuffer directly")
	if row.Status != Pass {
		t.Errorf("a verified private /dev denying the framebuffer is reported as %v, not a pass; "+
			"real protection is going unreported", row.Status)
	}
	if strings.Contains(row.Detail, "not the presence of a control") {
		t.Error("the row still says absence-not-control while a control is what refused it")
	}
	if !strings.Contains(row.Detail, "private /dev") {
		t.Errorf("the row does not name what refused it: %q", row.Detail)
	}
	// And it must still say whose control it is. Crediting VayuVeil for the init
	// system's work is the §8 misattribution pointed inward.
	if !strings.Contains(row.Detail, "rest of the machine is unaffected") {
		t.Errorf("the row does not carry its scope: %q", row.Detail)
	}
}

// The other direction, which is the one that must not regress. With no verified
// private /dev, the same result is absence and nothing more — and absence is
// never a pass.
func TestTheSameMissOnAnUnsandboxedHostIsStillJustAbsence(t *testing.T) {
	for name, sb := range map[string]vayuveil.SandboxState{
		"read the mount table, no private /dev": unsandboxed(),
		"could not read the mount table":        {Supported: true},
		"platform does not expose it":           {},
	} {
		in := on(allAbsent())
		in.Sandbox = sb
		in.RedTeam = []vayuveil.AttackResult{deviceMiss("read the framebuffer directly")}

		row := rowFor(t, Run(in), "read the framebuffer directly")
		if row.Status == Pass {
			t.Errorf("%s: an empty machine is being reported as a control", name)
		}
	}
}

// THE adversarial one. /proc/1/mem is not reached through a device node, so a
// private /dev has nothing to do with it. If the credit is applied by outcome
// alone rather than by ViaDeviceNode, this row goes green on the strength of a
// control that is irrelevant to it — a pass bought with somebody else's
// evidence, which is the exact shape of the mistake §8 forbids.
func TestAPrivateDevDoesNotCreditTechniquesThatNeverTouchedADeviceNode(t *testing.T) {
	in := on(allAbsent())
	in.Sandbox = sandboxed()
	in.RedTeam = []vayuveil.AttackResult{{
		Technique: "read another process's memory through /proc/<pid>/mem",
		Asset:     vayuveil.AssetMemoryImage,
		Outcome:   vayuveil.AttackNothingPresent,
		// deliberately NOT ViaDeviceNode
		Detail: "/proc/1/mem does not exist on this host, so there was no target.",
	}}

	row := rowFor(t, Run(in), "Attack: read another process's memory")
	if row.Status == Pass {
		t.Error("a private /dev was credited for a technique that reaches procfs, not a device " +
			"node; the attribution is keyed on the outcome instead of on how the asset is reached")
	}
}

// A technique that CAPTURED must stay a Fail no matter how good the sandbox
// rows look. A control that is in force elsewhere never launders a live finding.
func TestASandboxDoesNotLaunderAnActualCapture(t *testing.T) {
	in := on(allAbsent())
	in.Sandbox = sandboxed()
	in.RedTeam = []vayuveil.AttackResult{{
		Technique: "read the framebuffer directly", Asset: vayuveil.AssetFramebuffer,
		Outcome: vayuveil.AttackCapturedContent, ViaDeviceNode: true, Bytes: 4096,
		Detail: "CAPTURED 4096 bytes from /dev/fb0.",
	}}
	if row := rowFor(t, Run(in), "read the framebuffer directly"); row.Status != Fail {
		t.Errorf("content was captured and the row reads %v", row.Status)
	}
}

// The sandbox rows themselves: read means read, unread means unverified. Never
// the comfortable rounding.
func TestSandboxRowsReportUnverifiedRatherThanFineWhenUnread(t *testing.T) {
	in := on(allAbsent())
	in.Sandbox = vayuveil.SandboxState{Supported: true} // nothing readable
	checks := Run(in)

	for _, part := range []string{
		"Capture device nodes are unreachable",
		"Privileges this process actually holds",
		"cannot gain privileges by running another program",
	} {
		if row := rowFor(t, checks, part); row.Status != Unverified {
			t.Errorf("%q reports %v on a host where nothing could be read", part, row.Status)
		}
	}
}

// And the good case is genuinely green, or the whole exercise reported nothing.
func TestAProperlyDeployedServiceGetsCreditForItsUnit(t *testing.T) {
	in := on(allAbsent())
	in.Sandbox = sandboxed()
	checks := Run(in)

	for _, part := range []string{
		"Capture device nodes are unreachable",
		"Privileges this process actually holds",
		"cannot gain privileges by running another program",
	} {
		if row := rowFor(t, checks, part); row.Status != Pass {
			t.Errorf("%q reports %v on a correctly hardened unit", part, row.Status)
		}
	}

	// An install running unhardened is warned, and told which directive is
	// missing — a warning an operator cannot act on is a warning wasted.
	in.Sandbox = unsandboxed()
	warned := Run(in)
	dev := rowFor(t, warned, "Capture device nodes are unreachable")
	if dev.Status != Warn {
		t.Errorf("a shared /dev reports %v, not a warning", dev.Status)
	}
	if !strings.Contains(dev.Detail, "PrivateDevices=yes") {
		t.Errorf("the warning does not name the missing directive: %q", dev.Detail)
	}
	nnp := rowFor(t, warned, "cannot gain privileges by running another program")
	if !strings.Contains(nnp.Detail, "NoNewPrivileges=yes") {
		t.Errorf("the no-new-privs warning does not name the missing directive: %q", nnp.Detail)
	}
}

// Holding more privilege than the unit grants is a finding, not a shrug.
func TestExtraCapabilitiesAreFlagged(t *testing.T) {
	in := on(allAbsent())
	sb := sandboxed()
	sb.CapEff = 1<<vayuveil.CapNetBindService | 1<<21 // + CAP_SYS_ADMIN
	in.Sandbox = sb
	if row := rowFor(t, Run(in), "Privileges this process actually holds"); row.Status != Warn {
		t.Errorf("a process holding CAP_SYS_ADMIN reports %v", row.Status)
	}
}
