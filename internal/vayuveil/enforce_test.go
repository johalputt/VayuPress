// SPDX-License-Identifier: Apache-2.0

package vayuveil

// enforce_test.go — proving the hardening is applied, not just written.
//
// This one has to run against the REAL process. A unit test with a fake kernel
// would prove that the fake returns what the fake was told to return; the
// question here is whether calling ApplyProcessHardening actually leaves this
// process undumpable, and only the kernel can answer that.
//
// Applying it inside the test suite is deliberate and one-way: RLIMIT_CORE
// cannot be raised again by an unprivileged process. A test binary has no use
// for a core file, so the cost is nothing and the evidence is real.

import "testing"

// Written because a mutation survived: ApplyProcessHardening returning early
// after the first control left the second one decorative, and every test in the
// suite still passed because none of them applied it for real.
//
// The first attempt at this test ALSO passed against that mutation, for a
// different and more embarrassing reason: this environment already ships with
// RLIMIT_CORE soft-limited to 0, so asserting "the limit is zero after applying"
// asserted a fact that was true before the call. A test whose precondition
// already satisfies its conclusion measures nothing. The limit is therefore
// raised first, and the test skips honestly where it cannot be.
func TestApplyingTheHardeningActuallyHardensThisProcess(t *testing.T) {
	if !hardeningSupported {
		t.Skip("this platform cannot set or read process dumpability")
	}
	if !raiseCoreLimitForTest(t) {
		t.Skip("the hard core limit is 0, so this process cannot create the precondition this test " +
			"needs; it would otherwise pass without demonstrating anything")
	}
	if before := VerifyProcessHardening(); before.CoreLimitZero {
		t.Fatal("the core limit is still zero after raising it, so the assertion below would hold " +
			"whether or not the code under test ran")
	}

	if err := ApplyProcessHardening(); err != nil {
		t.Fatalf("applying the hardening failed: %v", err)
	}

	got := VerifyProcessHardening()
	if !got.Known {
		t.Fatal("the kernel did not answer PR_GET_DUMPABLE after the call that sets it")
	}
	if !got.Undumpable {
		t.Error("ApplyProcessHardening returned success and this process is still dumpable — a core " +
			"file or a same-user read of /proc/<pid>/mem would still reach session tokens and the " +
			"keystore key")
	}
	if !got.CoreLimitKnown {
		t.Fatal("getrlimit(RLIMIT_CORE) did not answer after the call that sets it")
	}
	if !got.CoreLimitZero {
		t.Error("the SECOND control was not applied. Two independent mechanisms is the entire point: " +
			"dumpability can be turned back on inside this process, the resource limit cannot, and a " +
			"version that only applies one has half the protection it reports")
	}
}

// Verification must read the kernel, not remember the call. A cached answer
// would report the state at boot forever, including after something changed it.
func TestVerificationAsksTheKernelRatherThanRememberingTheCall(t *testing.T) {
	if !hardeningSupported {
		t.Skip("this platform cannot read process dumpability")
	}
	// Two reads with nothing in between must both be answered by the kernel; a
	// function returning a stored boolean would pass this too, so the real
	// assertion is the one above — this pins that Verify is side-effect free and
	// repeatable, which a report recomputed on every page load depends on.
	a, b := VerifyProcessHardening(), VerifyProcessHardening()
	if a != b {
		t.Errorf("two consecutive verifications disagree: %+v vs %+v", a, b)
	}
	if !a.Supported {
		t.Error("the platform supports the control and Verify says it does not")
	}
}
