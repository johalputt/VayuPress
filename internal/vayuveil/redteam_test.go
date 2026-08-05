// SPDX-License-Identifier: Apache-2.0

package vayuveil

// redteam_test.go — the suite's own credibility.
//
// A capture suite that has only ever run on a machine where nothing is present
// has never demonstrated that it can detect a capture. Every test here feeds it
// a host where the attack SUCCEEDS and checks that it says so.

import (
	"errors"
	"strings"
	"testing"
)

// emptyHost is a machine with none of it.
func emptyHost() Host {
	return Host{
		Exists: func(string) bool { return false }, Readable: func(string) bool { return false },
		Glob: func(string) []string { return nil }, Env: func(string) string { return "" },
		ReadFile: func(string) string { return "" },
	}
}

func noRead(string, int) ([]byte, error) { return nil, errors.New("refused") }

// THE test. Give the suite a host where the framebuffer hands over bytes and it
// must report a capture. Without this, every "nothing captured" result the suite
// has ever produced is unfalsified.
func TestTheSuiteDetectsAnActualCapture(t *testing.T) {
	h := emptyHost()
	h.Glob = func(pat string) []string {
		if pat == "/dev/fb*" {
			return []string{"/dev/fb0"}
		}
		return nil
	}
	stolen := []byte("PIXELS-FROM-SOMEBODY-ELSES-SCREEN")
	rs := RunRedTeam(h, func(p string, _ int) ([]byte, error) {
		if p == "/dev/fb0" {
			return stolen, nil
		}
		return nil, errors.New("refused")
	})

	captured, _, _, _ := RedTeamSummary(rs)
	if captured != 1 {
		t.Fatalf("the framebuffer handed over %d bytes and the suite reported %d captures",
			len(stolen), captured)
	}
	for _, r := range rs {
		if r.Outcome != AttackCapturedContent {
			continue
		}
		if r.Bytes != len(stolen) {
			t.Errorf("the suite reports %d bytes captured, not %d", r.Bytes, len(stolen))
		}
		if !strings.Contains(r.Detail, "/dev/fb0") {
			t.Error("the finding does not say where the content came from")
		}
	}
}

// The other half: on a host with nothing present, nothing may be reported as
// captured — a suite that cries wolf is discarded and then the real finding is
// missed too.
func TestTheSuiteReportsNoCaptureOnAHostWithNothingPresent(t *testing.T) {
	captured, _, _, _ := RedTeamSummary(RunRedTeam(emptyHost(), noRead))
	if captured != 0 {
		t.Errorf("%d captures reported on a host with no devices at all", captured)
	}
}

// ARTIFACT-LEVEL, not transport-level. A device that exists, opens cleanly, and
// returns ZERO bytes is a refusal — the attacker is holding nothing. A suite
// judging on the error value would call that a success for the attacker.
func TestAnOpenThatYieldsNoBytesIsARefusalNotACapture(t *testing.T) {
	h := emptyHost()
	h.Glob = func(pat string) []string {
		if pat == "/dev/fb*" {
			return []string{"/dev/fb0"}
		}
		return nil
	}
	// Opens fine. Returns nothing. The attacker came away empty-handed.
	rs := RunRedTeam(h, func(string, int) ([]byte, error) { return []byte{}, nil })
	captured, refused, _, _ := RedTeamSummary(rs)
	if captured != 0 {
		t.Error("a successful open that produced no content was scored as a capture; the question " +
			"is what the attacker HOLDS, not what the syscall returned")
	}
	if refused == 0 {
		t.Error("the technique ran and produced nothing, and that was not recorded as a refusal")
	}
}

// "Not attempted" must never be counted as a defence, and must be visible.
// §6: a technique that is not in the suite is not defended, and the report must
// not imply otherwise.
func TestNotAttemptedIsNeverCountedAsADefence(t *testing.T) {
	rs := RunRedTeam(emptyHost(), noRead)
	_, refused, notPresent, notAttempted := RedTeamSummary(rs)
	if notAttempted == 0 {
		t.Fatal("the suite claims to attempt every technique in ADR-0150 §6, which this binary " +
			"cannot do — a Wayland handshake needs a Wayland client")
	}
	// Not folded into either honest-looking bucket.
	for _, r := range rs {
		if r.Outcome == AttackNotAttempted {
			if !strings.Contains(r.Detail, "NOT ATTEMPTED") {
				t.Errorf("%q does not say it was not attempted", r.Technique)
			}
			if !strings.Contains(r.Detail, "not defended") {
				t.Errorf("%q does not say it is therefore undefended", r.Technique)
			}
		}
	}
	if names := TechniquesNotAttempted(rs); len(names) != notAttempted {
		t.Errorf("%d techniques were skipped but only %d are named for the page", notAttempted, len(names))
	}
	_ = refused
	_ = notPresent
}

// "Nothing present" is not a defence either. A headless server captures nothing
// because there is no screen, and that is the absence of a target.
func TestNothingPresentIsDistinguishedFromRefused(t *testing.T) {
	for _, r := range RunRedTeam(emptyHost(), noRead) {
		if r.Outcome == AttackNothingPresent && !strings.Contains(r.Detail, "not the presence of a control") {
			t.Errorf("%q reports an absent device without saying that absence is not a defence", r.Technique)
		}
	}
}

// Every result must actually record an outcome, or it vanishes from every count.
func TestEveryResultRecordsAnOutcome(t *testing.T) {
	for _, r := range RunRedTeam(emptyHost(), noRead) {
		if !validResult(r) {
			t.Errorf("result %q has no outcome, no technique or no detail, and is counted by nothing", r.Technique)
		}
	}
}

// Reading our OWN memory proves nothing — self can always read self. The suite
// must target another process, or it is testing that a process can read itself.
func TestTheMemoryTechniqueTargetsAnotherProcessNotOurselves(t *testing.T) {
	var opened []string
	h := emptyHost()
	h.Exists = func(p string) bool { return p == "/proc/1/mem" }
	RunRedTeam(h, func(p string, _ int) ([]byte, error) {
		opened = append(opened, p)
		return nil, errors.New("refused")
	})
	for _, p := range opened {
		if strings.Contains(p, "/proc/self/") {
			t.Errorf("the suite reads %s — a process can always read its own memory, so this "+
				"technique would pass on a completely unprotected machine", p)
		}
	}
	var sawOther bool
	for _, p := range opened {
		if p == "/proc/1/mem" {
			sawOther = true
		}
	}
	if !sawOther {
		t.Error("the memory technique never targets another process at all")
	}
}

// A capture on the SECOND node must still be found.
//
// Written because a mutation survived: scoring on the error rather than on the
// byte count let the loop break on the first node that opened cleanly and
// returned nothing, and a machine with /dev/fb0 empty and /dev/fb1 handing over
// pixels would have been reported clean. The suite must keep looking until it
// either holds content or runs out of nodes.
func TestACaptureOnALaterNodeIsStillFound(t *testing.T) {
	h := emptyHost()
	h.Glob = func(pat string) []string {
		if pat == "/dev/fb*" {
			return []string{"/dev/fb0", "/dev/fb1"}
		}
		return nil
	}
	stolen := []byte("PIXELS-FROM-THE-SECOND-NODE")
	rs := RunRedTeam(h, func(p string, _ int) ([]byte, error) {
		if p == "/dev/fb1" {
			return stolen, nil
		}
		return []byte{}, nil // fb0 opens fine and yields nothing
	})
	captured, _, _, _ := RedTeamSummary(rs)
	if captured != 1 {
		t.Fatalf("content was handed over by /dev/fb1 and the suite reported %d captures — an "+
			"empty first node must not stop the search", captured)
	}
	for _, r := range rs {
		if r.Outcome == AttackCapturedContent && !strings.Contains(r.Detail, "/dev/fb1") {
			t.Errorf("the finding names the wrong node: %s", r.Detail)
		}
	}
}

// validResult guards the invariant that a result actually recorded an outcome.
//
// It lives here rather than on AttackResult because its only caller is the test
// above: a method in the package would be unreachable production code carried
// for the suite's benefit, which the deadcode gate refuses and is right to. The
// invariant is real either way — a result whose outcome was never set is counted
// by no branch of RedTeamSummary and disappears from the report in silence,
// which is the one way a capture suite can lie without anybody editing a claim.
func validResult(r AttackResult) bool {
	return r.Outcome != outcomeUnset && strings.TrimSpace(r.Technique) != "" &&
		strings.TrimSpace(r.Detail) != ""
}

// ── The channel that matters on the machines this binary actually runs on ────
//
// Every other screen-capture technique in the suite targets something a
// headless server does not have: no framebuffer, no DRM card node, no Wayland
// socket, no X display. So on a real VayuPress host the entire screen half of
// the suite reported "nothing present" and proved nothing.
//
// /dev/vcs*, /dev/vcsa* and /dev/vcsu* are the virtual console's screen memory,
// readable as plain text. They exist on a server, and whatever was last typed
// at a console login is sitting in them. A capture suite whose only screen
// technique cannot fire on the deployment target was testing somebody else's
// threat model.
func TestConsoleScreenMemoryIsBothRegisteredAndAttacked(t *testing.T) {
	var chans []Channel
	for _, c := range Channels() {
		if c.ID == "dev-vcs" {
			chans = append(chans, c)
		}
	}
	if len(chans) != 1 {
		t.Fatalf("expected exactly one console-memory channel, found %d", len(chans))
	}
	c := chans[0]
	if c.Default != DispositionDeny {
		t.Errorf("console screen memory is not default-deny (%v)", c.Default)
	}
	if !c.Complete() {
		t.Error("the channel leaves one of its obligations unanswered")
	}

	// And the suite ACTUALLY TRIES it — a registry entry with no technique is
	// a channel declared and never tested, which §6 says must not be implied
	// to be defended.
	h := Host{
		Glob:   func(pat string) []string { return map[string][]string{"/dev/vcs*": {"/dev/vcs1"}}[pat] },
		Exists: func(string) bool { return false },
	}
	var tried []string
	read := func(path string, _ int) ([]byte, error) {
		tried = append(tried, path)
		return []byte("root@host:~# "), nil
	}
	results := RunRedTeam(h, read)

	var found *AttackResult
	for i := range results {
		if strings.Contains(results[i].Technique, "console") {
			found = &results[i]
		}
	}
	if found == nil {
		t.Fatal("no technique in the suite reaches console screen memory")
	}
	if found.Outcome != AttackCapturedContent {
		t.Errorf("the console node yielded bytes and the suite reported %v", found.Outcome)
	}
	if !found.ViaDeviceNode {
		t.Error("the console technique is not marked as reaching a device node, so a verified " +
			"private /dev will not be credited for denying it")
	}
	if len(tried) == 0 || tried[0] != "/dev/vcs1" {
		t.Errorf("the suite did not read the console node; it read %v", tried)
	}
}
