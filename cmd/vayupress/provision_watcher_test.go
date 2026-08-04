// SPDX-License-Identifier: Apache-2.0

package main

// provision_watcher_test.go — the root-side watcher that consumes a provisioning
// request, and the repair that re-arms it.
//
// WHY THIS EXISTS, and it is the most expensive lesson of the certificate
// incident. A day of fixes was written, tested, released and signed, and not one
// of them reached the machine they were written for — because the helper
// self-upgrade only runs when the worker runs, and the worker was never being
// started. Two provisioning requests forty minutes apart both sat unconsumed.
//
// The cause was a line in the installer:
//
//	systemctl enable --now vayupress-provision.path … >/dev/null 2>&1 || true
//
// the same discarded exit status, in the same shape, as the nginx reload bug
// that started the whole thing. If enabling the watcher failed, nobody was told;
// requests queued forever and the daily timer became the only pass that ever
// ran. From the panel that is indistinguishable from a repair that did not work,
// which is exactly how it was read — for a day.
//
// So the mechanism gets a gate. It never had one.

import (
	"strings"
	"testing"
)

// THE DELIVERY DEADLOCK, stated as a property. The watcher is the only thing
// that consumes a request, and the worker is the only thing that upgrades the
// helpers. If enabling the watcher can fail silently, then a machine whose
// watcher is off can never be repaired by shipping anything — the fix and its
// delivery mechanism are the same component.
func TestEnablingTheProvisioningWatcherIsNeverDiscarded(t *testing.T) {
	src := shellCode(readSourceFile(t, "../../scripts/install-provisioning.sh"))
	// ANCHOR ON THE ENABLE, not on the unit name. The first occurrence of
	// "vayupress-provision.path" is the `cat >` that WRITES the unit file, so
	// anchoring there inspects a heredoc opener that can never contain `|| true`
	// — and this gate passed against the exact regression it was written for.
	// Found by mutation-testing it, which is the only reason it is not still
	// green and worthless.
	i := strings.Index(src, "systemctl enable --now")
	if i < 0 {
		t.Fatal("the installer no longer enables the provisioning watcher, so a request from " +
			"the panel is consumed by nothing at all")
	}
	line := src[i:]
	if j := strings.Index(line, "\n"); j > 0 {
		line = line[:j]
	}
	if !strings.Contains(line, "vayupress-provision.path") {
		t.Fatalf("the enable no longer covers the .path watcher, which is the unit that "+
			"consumes a request:\n%s", line)
	}
	if strings.Contains(line, "|| true") {
		t.Fatalf("the enable is followed by `|| true`, so a watcher that fails to start is "+
			"reported as installed. Requests then queue forever and no shipped fix can reach "+
			"this machine, because the helper self-upgrade only runs when the worker runs:\n%s",
			line)
	}
	if strings.Contains(line, "2>&1") && strings.Contains(line, "/dev/null") {
		t.Errorf("systemd's explanation is discarded, so the failure cannot be acted on:\n%s", line)
	}
}

// And the repair must be reachable WITHOUT a shell, or it repairs nobody. The
// operator whose watcher is off is precisely the operator who cannot receive a
// fix; telling them to run a command is the product failing and narrating it.
func TestTheShieldAgentReArmsTheWatcher(t *testing.T) {
	src := shellCode(readSourceFile(t, "../../deploy/vayushield-agent.sh"))
	if !strings.Contains(src, "arm_provisioning_watcher()") {
		t.Fatal("the shield agent cannot re-arm the provisioning watcher, so an install whose " +
			"watcher is off has no panel-reachable route back — and that install is exactly " +
			"the one that cannot be sent a fix")
	}
	if !strings.Contains(src, "arm_provisioning_watcher ||") &&
		!strings.Contains(src, "arm_provisioning_watcher\n") {
		t.Error("arm_provisioning_watcher is defined and never called")
	}

	fn := src[strings.Index(src, "arm_provisioning_watcher()"):]
	if e := strings.Index(fn, "\n}\n"); e > 0 {
		fn = fn[:e]
	}

	// A unit that tripped systemd's start rate limiter stays failed and refuses
	// every later trigger — including the one meant to repair it. Without this
	// the repair button confirms the state rather than changing it.
	if !strings.Contains(fn, "reset-failed") {
		t.Error("the repair never clears a failed unit, so a watcher latched by systemd's " +
			"start limiter stays latched and the button does nothing")
	}
	if !strings.Contains(fn, "enable --now") {
		t.Error("the repair never enables the watcher")
	}
	// The enable's failure must reach the panel. Repairing silently and reporting
	// success is the defect this whole file is about, one layer up.
	if !strings.Contains(fn, "provisionwatch.reason") {
		t.Error("a failed re-arm records no reason, so the panel shows a repair that silently " +
			"did nothing")
	}
	if !strings.Contains(fn, "return 1") {
		t.Error("the re-arm cannot report failure to its caller, so provisionhelpers goes green " +
			"over a watcher that is still off")
	}

	// A request written BEFORE the watcher existed produces no edge for a
	// PathExists unit to see, so it would sit there behind a watcher that is now
	// working — repaired, and still nothing happens.
	if !strings.Contains(fn, "provision.request") {
		t.Error("a request queued before the repair is left queued, so the operator presses " +
			"Repair, the watcher comes up, and their pending request still never runs")
	}
}

// AND IT MUST NOT BE A BUTTON. The repair was reachable only by pressing
// "Repair the certificate helpers", and the operator who needs it is precisely
// the one with no way to know they need it: from the panel, a watcher that is
// off looks exactly like a fix that did not work. You press Provision, nothing
// happens, you press it again.
//
// A self-healing agent that waits to be asked is not self-healing.
func TestTheAgentArmsTheWatcherWithoutBeingAsked(t *testing.T) {
	src := shellCode(readSourceFile(t, "../../deploy/vayushield-agent.sh"))

	if !strings.Contains(src, "reconcile_provisionwatch()") {
		t.Fatal("there is no unconditional watcher reconcile, so a stalled watcher is only ever " +
			"repaired by an operator who already knows which button to press — and nothing on " +
			"the page tells them that")
	}

	// It has to be in the poll loop. Defined-and-never-called is the exact shape
	// of the three consecutive layers shipped earlier in this incident, each one
	// missing the next.
	loop := src[strings.Index(src, "run_agent()"):]
	if e := strings.Index(loop, "\ninstall_agent()"); e > 0 {
		loop = loop[:e]
	}
	if !strings.Contains(loop, "reconcile_provisionwatch") {
		t.Fatal("reconcile_provisionwatch is defined and never reached from the agent's poll " +
			"loop, so it repairs nothing")
	}

	// And it must be gated by the health check rather than re-enabling every
	// tick: a failing enable retried every five seconds is how a repair becomes
	// the load.
	fn := src[strings.Index(src, "reconcile_provisionwatch()"):]
	if e := strings.Index(fn, "\n}\n"); e > 0 {
		fn = fn[:e]
	}
	if !strings.Contains(fn, "is-active") {
		t.Error("the reconcile does not check whether the watcher is already up, so it re-enables " +
			"a healthy unit on every pass")
	}
	if !strings.Contains(fn, "arm_provisioning_watcher") {
		t.Error("the reconcile never actually arms anything")
	}
}
