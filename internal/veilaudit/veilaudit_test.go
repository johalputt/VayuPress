// SPDX-License-Identifier: Apache-2.0

package veilaudit

// veilaudit_test.go — written from "what would make this report a lie?".
//
// A privacy posture report has exactly one way to fail badly: telling somebody
// they are protected when they are not. Every test here is that question asked
// from a different angle.

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

func allAbsent() map[vayuveil.ChannelID]vayuveil.Observation {
	m := map[vayuveil.ChannelID]vayuveil.Observation{}
	for _, c := range vayuveil.Channels() {
		m[c.ID] = vayuveil.Observation{Presence: vayuveil.PresenceAbsent, Detail: "not on this host"}
	}
	return m
}

func on(obs map[vayuveil.ChannelID]vayuveil.Observation) Inputs {
	return Inputs{Enabled: true, Channels: vayuveil.Channels(), Observations: obs,
		EnforcingPhases: map[vayuveil.Phase]bool{}}
}

// THE test. P0 registers channels and enforces none of them, so there is no
// verified-enforcing control anywhere in this install — and Pass is reserved for
// verified enforcing. A single green row at P0 means the report has started
// crediting itself for work it has not done.
func TestNothingGoesGreenBeforeAnythingIsEnforced(t *testing.T) {
	for name, in := range map[string]Inputs{
		"a bare server with none of it present": on(allAbsent()),
		"a desktop with everything wide open": on(func() map[vayuveil.ChannelID]vayuveil.Observation {
			m := map[vayuveil.ChannelID]vayuveil.Observation{}
			for _, c := range vayuveil.Channels() {
				m[c.ID] = vayuveil.Observation{Presence: vayuveil.PresentReachable, Detail: "open"}
			}
			return m
		}()),
	} {
		pass, _, _, _ := Summary(Run(in))
		if pass != 0 {
			t.Errorf("%s: %d row(s) report a PASS while nothing is enforcing. Green has to mean "+
				"verified enforcing, or it means nothing.", name, pass)
		}
	}
}

// An interface that is missing from this host is not a defence. A headless
// server has no framebuffer, and reporting that as protection would tell an
// operator their screen is safe on a machine that has no screen.
func TestAnAbsentInterfaceIsNotReportedAsProtection(t *testing.T) {
	for _, c := range Run(on(allAbsent())) {
		if c.Status == Pass {
			t.Errorf("row %q passes because the interface is missing", c.Title)
		}
	}
	var found bool
	for _, c := range Run(on(allAbsent())) {
		if strings.Contains(c.Detail, "not the same as defended") {
			found = true
		}
	}
	if !found {
		t.Error("no row explains that absence is not a defence, so a reader will take it as one")
	}
}

// A probe that could not determine an answer must never round up to fine.
func TestAnUnknownProbeIsNeverTreatedAsClean(t *testing.T) {
	m := allAbsent()
	m["dev-input"] = vayuveil.Observation{Presence: vayuveil.PresenceUnknown, Detail: "could not read"}
	for _, c := range Run(on(m)) {
		if c.Title == "/dev/input event devices" && c.Status != Unverified {
			t.Errorf("an unreadable probe reports %s, not unverified", statusName(c.Status))
		}
	}
}

// A channel with NO probe result at all is the same problem, one step earlier:
// the sweep did not cover it and the report must not imply it did.
func TestAChannelWithNoObservationIsUnverifiedNotClean(t *testing.T) {
	in := on(map[vayuveil.ChannelID]vayuveil.Observation{}) // nothing observed at all
	_, _, _, unver := Summary(Run(in))
	if unver < len(vayuveil.Channels()) {
		t.Errorf("%d channels had no observation but only %d rows say unverified; the rest are "+
			"reporting on a sweep that never ran", len(vayuveil.Channels()), unver)
	}
}

// A denied channel that is reachable right now is the finding this whole
// subsystem exists to surface. It must be a Fail and it must say so plainly.
func TestADeniedChannelThatIsOpenRightNowFails(t *testing.T) {
	m := allAbsent()
	m["dev-input"] = vayuveil.Observation{Presence: vayuveil.PresentReachable,
		Detail: "/dev/input/event0 can be opened for reading by this process"}
	var row *Check
	for _, c := range Run(on(m)) {
		if c.Title == "/dev/input event devices" {
			cc := c
			row = &cc
		}
	}
	if row == nil {
		t.Fatal("the keyboard channel is not in the report at all")
	}
	if row.Status != Fail {
		t.Errorf("a keylogger interface this process can open reports %s", statusName(row.Status))
	}
	if !strings.Contains(row.Detail, "event0") {
		t.Error("the row does not say what was found, so the operator cannot check it")
	}
}

// §8 is the reason the rest of the report is worth believing. Every limit is
// present in every report and no input clears any of them.
func TestThePermanentLimitsAppearInEveryReportAndNothingClearsThem(t *testing.T) {
	for name, in := range map[string]Inputs{
		"nothing present": on(allAbsent()),
		"everything enforcing": func() Inputs {
			i := on(allAbsent())
			i.EnforcingPhases = map[vayuveil.Phase]bool{
				vayuveil.PhaseP1Screen: true, vayuveil.PhaseP2Input: true,
				vayuveil.PhaseP3Accessibility: true, vayuveil.PhaseP4Sandbox: true,
				vayuveil.PhaseP5Hygiene: true,
			}
			return i
		}(),
	} {
		var permanent int
		for _, c := range Run(in) {
			if c.Permanent {
				permanent++
				if c.Status == Pass {
					t.Errorf("%s: permanent limit %q went green; these never clear", name, c.Title)
				}
			}
		}
		if permanent < 7 {
			t.Errorf("%s: only %d permanent limits in the report; ADR-0150 §8 lists more, and a "+
				"report where everything eventually goes green teaches the reader to stop reading it",
				name, permanent)
		}
	}
}

// The four things the ADR says will never be claimed have to be named, not
// gestured at. A reader who cannot see "camera" or "root" in the report has not
// been told the boundary.
func TestTheReportNamesEachThingItWillNeverClaim(t *testing.T) {
	var all strings.Builder
	for _, c := range Run(on(allAbsent())) {
		all.WriteString(c.Title + " " + c.Detail + "\n")
	}
	body := strings.ToLower(all.String())
	for _, must := range []string{"root", "kernel", "firmware", "camera", "compositor", "recall"} {
		if !strings.Contains(body, must) {
			t.Errorf("the report never mentions %q, so its boundary is not stated where anyone reads it", must)
		}
	}
}

// The switch is a switch on the REPORTING. Saying otherwise would be the exact
// claim defect this package exists to avoid — an operator who reads "off" as
// "unprotected" has been told something false in the other direction.
func TestTheInactiveReportDoesNotClaimTurningItOnProtectsAnything(t *testing.T) {
	checks := Run(Inputs{Enabled: false, Channels: vayuveil.Channels()})
	if len(checks) != 1 {
		t.Fatalf("an inactive install produced %d rows; it should say one thing", len(checks))
	}
	d := strings.ToLower(checks[0].Detail)
	if !strings.Contains(d, "not a switch on any protection") && !strings.Contains(d, "switch on the reporting") {
		t.Error("the inactive row does not say that this control governs reporting rather than protection")
	}
	if checks[0].Status == Fail {
		t.Error("an install with the reporting switched off is reported as a failure, which invents " +
			"a risk that turning a report on would not remove")
	}
}

// statusName is here rather than on Status itself. Its only caller is a test
// failure message, and a String() method in the package would be unreachable
// production code carried for the benefit of the test suite — which the deadcode
// gate correctly refuses.
func statusName(s Status) string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	case Unverified:
		return "unverified"
	}
	return "info"
}

// ── The verified-enforcing row, attacked ────────────────────────────────────

// The ONE row allowed to be green must not go green on a platform that could not
// be asked. "We could not check" rendering as "protected" is the single most
// dangerous rounding error this report can make.
func TestTheOneGreenRowRefusesToGoGreenUnverified(t *testing.T) {
	for name, h := range map[string]vayuveil.SelfHardening{
		"platform does not support it": {Supported: false},
		"kernel did not answer":        {Supported: true, Known: false},
	} {
		in := on(allAbsent())
		in.SelfHardening = h
		pass, _, _, unver := Summary(Run(in))
		if pass != 0 {
			t.Errorf("%s: reported as enforcing anyway", name)
		}
		if unver == 0 {
			t.Errorf("%s: not reported as unverified either, so it vanished from the report", name)
		}
	}
}

// A process that CAN be dumped is a finding, not a shrug. A core file holds the
// session tokens and the keystore key.
func TestADumpableProcessIsAFailNotAnInfo(t *testing.T) {
	in := on(allAbsent())
	in.SelfHardening = vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: false}
	var row *Check
	for _, c := range Run(in) {
		if strings.Contains(c.Title, "refuses to be dumped") {
			cc := c
			row = &cc
		}
	}
	if row == nil {
		t.Fatal("the process-hardening row is missing from the report")
	}
	if row.Status != Fail {
		t.Errorf("a dumpable process reports %s", statusName(row.Status))
	}
	for _, must := range []string{"session token", "keystore", "/proc/<pid>/mem"} {
		if !strings.Contains(row.Detail, must) {
			t.Errorf("the row does not say that %q is what a dump would expose", must)
		}
	}
}

// The green row must state its scope. "VayuPress cannot be dumped" is true and
// useful; a reader taking it for "this machine is protected from memory capture"
// has been misled by a row that is individually correct.
func TestTheGreenRowStatesItsScope(t *testing.T) {
	in := on(allAbsent())
	in.SelfHardening = vayuveil.SelfHardening{Supported: true, Known: true, Undumpable: true}
	var row *Check
	for _, c := range Run(in) {
		if c.Status == Pass {
			cc := c
			row = &cc
		}
	}
	if row == nil {
		t.Fatal("a verified control produced no passing row at all")
	}
	if !strings.Contains(row.Detail, "nothing else on the machine") {
		t.Error("the only green row on the page does not say how small it is, so it reads as " +
			"machine-wide memory protection")
	}
}

// ── The capture suite, attacked ─────────────────────────────────────────────

// A technique that came away empty is NOT a VayuVeil pass. Nothing in P0
// refused it — the host's permissions did — and crediting the subsystem with
// somebody else's work is how a report starts flattering itself.
func TestAnEmptyHandedAttackIsNotCreditedToVayuVeil(t *testing.T) {
	in := on(allAbsent())
	in.RedTeam = []vayuveil.AttackResult{{
		Technique: "read the framebuffer directly", Outcome: vayuveil.AttackRefused,
		Detail: "no bytes.",
	}}
	pass, _, _, _ := Summary(Run(in))
	if pass != 0 {
		t.Error("an attack that came away empty was scored as a control passing")
	}
	for _, c := range Run(in) {
		if strings.HasPrefix(c.Title, "Attack:") && !strings.Contains(c.Detail, "Nothing in Phase 0") {
			t.Error("the row does not say that Phase 0 is not what refused it")
		}
	}
}

// A capture is the finding the suite exists for and must be a Fail.
func TestACaptureIsAFail(t *testing.T) {
	in := on(allAbsent())
	in.RedTeam = []vayuveil.AttackResult{{
		Technique: "read the framebuffer directly", Outcome: vayuveil.AttackCapturedContent,
		Bytes: 4096, Detail: "CAPTURED 4096 bytes from /dev/fb0.",
	}}
	var found bool
	for _, c := range Run(in) {
		if strings.Contains(c.Title, "read the framebuffer") {
			found = true
			if c.Status != Fail {
				t.Errorf("content in an attacker's hands reports %s", statusName(c.Status))
			}
		}
	}
	if !found {
		t.Fatal("a successful capture does not appear in the report at all")
	}
}

// Not-attempted must be Unverified, never Info — Info reads as "fine, nothing to
// see", and §6 says a technique not in the suite is not defended.
func TestNotAttemptedTechniquesAreUnverifiedNotContext(t *testing.T) {
	in := on(allAbsent())
	in.RedTeam = []vayuveil.AttackResult{{
		Technique: "wlr-screencopy a frame of the whole screen",
		Outcome:   vayuveil.AttackNotAttempted,
		Detail:    "NOT ATTEMPTED — needs a Wayland client. This technique is therefore not defended and not tested.",
	}}
	// Matched on the "Attack: " prefix, not on the technique name. The report also
	// carries a CHANNEL row called "wlr-screencopy protocol", which is legitimately
	// Info when the interface is absent — a name-only match finds that row instead
	// and fails on correct code. Same defect as every other assertion in this
	// project that could not say which element it had found.
	var checked bool
	for _, c := range Run(in) {
		if !strings.HasPrefix(c.Title, "Attack: ") {
			continue
		}
		checked = true
		if c.Status != Unverified {
			t.Errorf("a technique nobody ran reports %s, which reads as a clean result",
				statusName(c.Status))
		}
	}
	if !checked {
		t.Fatal("no attack row in the report at all")
	}
}

// A suite that never ran must say so rather than producing an empty clean sweep.
func TestASuiteThatNeverRanSaysSo(t *testing.T) {
	in := on(allAbsent())
	in.RedTeam = nil
	var said bool
	for _, c := range Run(in) {
		if strings.Contains(c.Detail, "has not run") {
			said = true
			if c.Status != Unverified {
				t.Errorf("a suite that never ran reports %s", statusName(c.Status))
			}
		}
	}
	if !said {
		t.Error("the report is silent about the capture suite not having run, which reads as though " +
			"it ran and found nothing")
	}
}
