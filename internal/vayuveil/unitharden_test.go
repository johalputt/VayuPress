// SPDX-License-Identifier: Apache-2.0

package vayuveil

// unitharden_test.go — ADR-0150 §5 S6.
//
// The failure this file is written to catch is not a crash. It is a page that
// says hardening was applied while the process it describes is running without
// it, which is exactly what happens if anyone ever collapses "the drop-in
// exists" and "the directive is in force" into one boolean.

import (
	"strings"
	"testing"
	"time"
)

func hardened() SandboxState {
	return SandboxState{
		Supported:  true,
		NoNewPrivs: true, NoNewPrivsKnown: true,
		PrivateDev: true, PrivateDevKnown: true,
		PrivateTmp: true, PrivateTmpKnown: true,
		ProtectedHome: true, ProtectedHomeKnown: true,
		SwapMaxZero: true, SwapMaxKnown: true,
	}
}

// THE test. A drop-in written a second after this process started cannot
// possibly affect it, and a report that says otherwise has told an operator a
// control is holding that is not.
func TestADropInWrittenAfterTheProcessStartedIsNotReportedAsApplied(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	st := HardenState{Installed: true, HaveResult: true, DropInPresent: true, DropInAt: start.Add(time.Second)}

	// Nothing in force: the drop-in has been written and the process predates it.
	got := ReconcileHardening(st, SandboxState{Supported: true}, start)
	if got != HardenAwaitingRestart {
		t.Fatalf("a drop-in written after process start must read as awaiting restart, got %v", got)
	}
	d := DescribeHardenVerdict(got, UnverifiedHardening(SandboxState{Supported: true}))
	if !strings.Contains(d, "does not have it") {
		t.Fatalf("the copy must say this process does NOT have it; got %q", d)
	}
	// The word that would make this row a lie.
	for _, forbidden := range []string{"is now in force", "hardening applied", "is protected"} {
		if strings.Contains(strings.ToLower(d), forbidden) {
			t.Fatalf("awaiting-restart copy claims %q: %s", forbidden, d)
		}
	}
}

// The serious one. Written BEFORE this process started and still not in force
// means the file went somewhere this service does not read from.
func TestADropInThatPredatesTheProcessAndDidNotTakeIsReportedAsSuch(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	st := HardenState{Installed: true, HaveResult: true, DropInPresent: true, DropInAt: start.Add(-time.Hour)}

	got := ReconcileHardening(st, SandboxState{Supported: true}, start)
	if got != HardenDidNotTake {
		t.Fatalf("want HardenDidNotTake, got %v", got)
	}
	if got == HardenAwaitingRestart {
		t.Fatal("a stale drop-in must never be excused as awaiting a restart")
	}
}

// The boundary between the two verdicts is a single comparison, so it gets a
// test of its own: equal timestamps must NOT read as awaiting restart.
//
// AppliedAt equal to the process start means the drop-in was in place at exec.
// If it is still not in force, it did not take — the same finding as a drop-in
// written an hour earlier. Rounding the tie the other way would let a request
// that landed in the same second permanently excuse itself.
func TestATieBetweenApplyTimeAndProcessStartCountsAsDidNotTake(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	st := HardenState{Installed: true, HaveResult: true, DropInPresent: true, DropInAt: start}
	if got := ReconcileHardening(st, SandboxState{Supported: true}, start); got != HardenDidNotTake {
		t.Fatalf("equal timestamps must read as did-not-take, got %v", got)
	}
}

func TestEveryVerdictIsReachableAndDistinct(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	bare := SandboxState{Supported: true}

	cases := []struct {
		name string
		st   HardenState
		sb   SandboxState
		want HardenVerdict
	}{
		{"unreadable sandbox", HardenState{}, SandboxState{}, HardenUnknown},
		{"everything in force", HardenState{}, hardened(), HardenInForce},
		{"request in flight", HardenState{Pending: true}, bare, HardenPending},
		{"worker reverted", HardenState{Reverted: true, HaveResult: true}, bare, HardenReverted},
		{"worker failed", HardenState{Failed: true, HaveResult: true}, bare, HardenFailed},
		{"never requested", HardenState{}, bare, HardenNotRequested},
	}
	seen := map[HardenVerdict]string{}
	for _, tc := range cases {
		got := ReconcileHardening(tc.st, tc.sb, start)
		if got != tc.want {
			t.Errorf("%s: want %v got %v", tc.name, tc.want, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("%s and %s produce the same verdict %v", prev, tc.name, got)
		}
		seen[got] = tc.name
	}
}

// A revert outranks everything, because it is news even on an install that turns
// out to be hardened anyway: something about this unit stopped the service
// starting, and that survives the current posture being fine.
func TestARevertIsReportedEvenWhenEverythingIsAlreadyInForce(t *testing.T) {
	start := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	st := HardenState{Installed: true, HaveResult: true, Reverted: true, DropInPresent: true, DropInAt: start.Add(-time.Minute)}
	if got := ReconcileHardening(st, hardened(), start); got != HardenReverted {
		t.Fatalf("a revert must not be hidden by a healthy posture, got %v", got)
	}
	d := DescribeHardenVerdict(HardenReverted, nil)
	if !strings.Contains(d, "REMOVED") {
		t.Fatalf("the revert copy must say the drop-in was removed; got %q", d)
	}
}

// An unreadable directive is treated as missing, never as fine. This is the
// direction the whole subsystem leans and it is cheap to get backwards.
func TestAnUnreadableDirectiveCountsAsNotInForce(t *testing.T) {
	s := hardened()
	s.NoNewPrivsKnown = false // read failed; the value left over says "true"
	missing := UnverifiedHardening(s)
	if len(missing) != 1 || !strings.HasPrefix(missing[0].Directive, "NoNewPrivileges") {
		t.Fatalf("an unreadable directive must be listed as missing, got %v", missing)
	}
	// And the stale true must not leak out of InForce either.
	if on, known := missing[0].InForce(s); known || on {
		t.Fatalf("InForce must report unknown, got on=%v known=%v", on, known)
	}
}

// A platform that exposes nothing must not answer for any directive. Without
// the Supported guard, the zero SandboxState's `false` values would read as
// "known to be off" for every row on a non-Linux build.
func TestAnUnsupportedPlatformAnswersNothing(t *testing.T) {
	for _, d := range HardenBaseline() {
		if _, known := d.InForce(SandboxState{}); known {
			t.Fatalf("%s claimed a known answer on an unsupported platform", d.Directive)
		}
	}
	if got := ReconcileHardening(HardenState{HaveResult: true}, SandboxState{}, time.Now()); got != HardenUnknown {
		t.Fatalf("want HardenUnknown on an unsupported platform, got %v", got)
	}
}

// The baseline's membership rule, pinned. A directive that cannot be read back
// would be written, reported as applied, and never checked again — so every
// entry must carry a working read-back, and the count is pinned so a future
// addition has to come here and think about it.
func TestEveryBaselineDirectiveCanActuallyBeVerified(t *testing.T) {
	base := HardenBaseline()
	if len(base) != 5 {
		t.Fatalf("the baseline is meant to be the five verifiable directives, got %d", len(base))
	}
	on := hardened()
	// Every Known bit set and every value false — a process that genuinely has
	// none of these. NOT SandboxState{Supported: true}, which is the different
	// and weaker state "nothing could be read": a directive that answered
	// identically to both would be verifying the read rather than the control.
	off := SandboxState{
		Supported: true, NoNewPrivsKnown: true, PrivateDevKnown: true,
		PrivateTmpKnown: true, ProtectedHomeKnown: true, SwapMaxKnown: true,
	}
	for _, d := range base {
		if d.ReadBack == "" || d.Denies == "" {
			t.Errorf("%s: a directive must say what it denies and where it is read back from", d.Directive)
		}
		// It must distinguish the two states. A read-back that answers the same
		// thing for a hardened and an unhardened process verifies nothing.
		gotOn, knownOn := d.InForce(on)
		gotOff, knownOff := d.InForce(off)
		if !knownOn || !gotOn {
			t.Errorf("%s: not reported in force against a fully hardened state", d.Directive)
		}
		if !knownOff || gotOff {
			t.Errorf("%s: not reported absent against an unhardened state", d.Directive)
		}
	}
}

// The refusals are load-bearing copy: each names a directive that would look
// good in a release note and says why it is not written.
func TestTheRefusalsNameTheDangerousDirectivesExplicitly(t *testing.T) {
	all := HardenRefusals()
	if len(all) < 4 {
		t.Fatalf("expected the refusal list to be substantive, got %d entries", len(all))
	}
	var joined strings.Builder
	for _, r := range all {
		if r.Directive == "" || r.Reason == "" {
			t.Fatalf("a refusal with no reason is not a refusal: %+v", r)
		}
		joined.WriteString(r.Directive + " " + r.Reason + "\n")
	}
	// ProtectSystem is the one that takes an install down, so it must be named.
	if !strings.Contains(joined.String(), "ProtectSystem") {
		t.Fatal("ProtectSystem must be named in the refusals — it is the directive that can break an install")
	}
	// And it must never appear in the baseline.
	for _, d := range HardenBaseline() {
		if strings.HasPrefix(d.Directive, "ProtectSystem") {
			t.Fatal("ProtectSystem=strict must never be written from a panel button")
		}
	}
}

// AUDIT FINDING — the primary success path reported the excuse, not the truth.
//
// The worker writes the drop-in, restarts the service, waits to see whether the
// unit stayed up, and only THEN writes its result. So on every successful run the
// new process starts BEFORE the result file exists. A verdict computed by
// comparing the result file's timestamp against this process's start therefore
// says "awaiting restart" about a process that already restarted with the drop-in
// in place — turning the one serious finding this row exists to surface into a
// reassuring "wait a moment", on exactly the path an operator takes.
//
// The drop-in's own timestamp is the honest hinge: it is the file systemd read at
// exec, so a process that started after it either got the directive or the
// directive did not take.
func TestTheVerdictComesFromTheDropInNotFromTheWorkersReport(t *testing.T) {
	dropIn := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	processStart := dropIn.Add(2 * time.Second)   // systemd restarted us into it
	resultWritten := dropIn.Add(30 * time.Second) // the worker finished watching

	st := HardenState{
		Installed: true, HaveResult: true,
		DropInPresent: true, DropInAt: dropIn,
		Wrote: []string{"NoNewPrivileges=yes"},
	}
	_ = resultWritten

	if got := ReconcileHardening(st, SandboxState{Supported: true}, processStart); got != HardenDidNotTake {
		t.Fatalf("a process that started AFTER the drop-in and still lacks the directive "+
			"must read as did-not-take, got %v", got)
	}
}

// A directive the worker deliberately SKIPPED is never coming, so telling the
// operator to wait for a restart is wrong twice over: the restart already
// happened, and another one would change nothing.
func TestADeliberatelySkippedDirectiveIsNotReportedAsAwaitingAnything(t *testing.T) {
	dropIn := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	start := dropIn.Add(time.Second)

	sb := hardened()
	sb.ProtectedHome = false // the one the worker refused to write

	st := HardenState{
		Installed: true, HaveResult: true,
		DropInPresent: true, DropInAt: dropIn,
		Wrote:   []string{"NoNewPrivileges=yes"},
		Skipped: []string{"ProtectHome=yes — the data directory is under /home"},
	}
	got := ReconcileHardening(st, sb, start)
	if got == HardenAwaitingRestart {
		t.Fatal("a skipped directive was reported as awaiting a restart that will not change it")
	}
	if got == HardenDidNotTake {
		t.Fatal("a directive the worker refused to write is not one that failed to take")
	}
	if got != HardenSkipped {
		t.Fatalf("want HardenSkipped, got %v", got)
	}
	d := DescribeHardenVerdict(got, UnverifiedHardening(sb))
	if !strings.Contains(d, "will not change at a restart") {
		t.Fatalf("the copy must say a restart will not help; got %q", d)
	}
}

// And the serious verdict still wins when something skipped sits beside
// something that genuinely did not take.
func TestOneDirectiveThatDidNotTakeOutranksAnotherThatWasSkipped(t *testing.T) {
	dropIn := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	sb := hardened()
	sb.ProtectedHome = false // skipped
	sb.NoNewPrivs = false    // written, and did not take

	st := HardenState{
		Installed: true, HaveResult: true,
		DropInPresent: true, DropInAt: dropIn,
		Wrote:   []string{"NoNewPrivileges=yes"},
		Skipped: []string{"ProtectHome=yes — the data directory is under /home"},
	}
	if got := ReconcileHardening(st, sb, dropIn.Add(time.Second)); got != HardenDidNotTake {
		t.Fatalf("want HardenDidNotTake, got %v", got)
	}
}
