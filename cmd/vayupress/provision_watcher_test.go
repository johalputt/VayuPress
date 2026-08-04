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

// THE THIRD INSTANCE of the discarded exit status in one incident, and the
// worst-placed of the three. The line that restarts the APPLICATION was:
//
//	systemctl try-restart vayupress 2>/dev/null || true
//
// so if the app did not come back, the helper exited 0 having taken the whole
// install down and recorded a successful provisioning run.
//
// What makes it worse than the other two is the shape of the failure. nginx
// stays up either way, so from outside there is no outage to see: the site
// accepts connections and never answers, and the panel that would explain it is
// the thing that is down. It cost a live install ten minutes of being dark with
// nothing anywhere saying why.
func TestTheAppRestartIsVerifiedRatherThanAssumed(t *testing.T) {
	src := shellCode(readSourceFile(t, "../../scripts/setup-vayudomain.sh"))

	i := strings.Index(src, "try-restart vayupress")
	if i < 0 {
		t.Skip("the helper no longer restarts the app at the end of a run")
	}
	if !strings.Contains(src, "restart_app_verified") {
		t.Fatal("the app restart is fired and never confirmed, so a run that leaves VayuPress " +
			"down reports success — and nginx stays up, so it does not even look like an outage")
	}

	fn := src[strings.Index(src, "restart_app_verified() {"):]
	if e := strings.Index(fn, "\n}\n"); e > 0 {
		fn = fn[:e]
	}
	// `is-active` is NOT the check. systemd calls a hung process active, and a
	// process that is alive and not answering is precisely this failure.
	if strings.Contains(fn, "is-active") && !strings.Contains(src, "app_answers") {
		t.Error("liveness is judged by systemd's view of the process rather than by asking the " +
			"app, so a hung process passes the check that exists to catch it")
	}
	if !strings.Contains(src, "127.0.0.1:8080") {
		t.Error("nothing asks the app over loopback, so 'it came back' is an assumption again")
	}
	// And the failure has to reach the run's result, or the panel reports a clean
	// provisioning pass over an install that is serving nothing.
	if !strings.Contains(src, "restart_app_verified || HOST_FAILURES") {
		t.Error("a failed app restart does not count toward the run's failures, so the run still " +
			"exits 0 and the panel calls it a success")
	}
}

// A CERTIFICATE THAT NOTHING SERVES, caused by the restart cap meant to protect
// the machine. Observed on a live install, in the run's own log:
//
//	Congratulations! Your certificate and chain have been saved at: ...
//	nginx has already been restarted once in this run; not restarting again.
//	certificate issued for test.johal.in, but this server does not serve it yet.
//
// The pre-flight escalation spent the run's single restart allowance, certbot
// then succeeded, and the post-issuance step was refused the restart that would
// have made the certificate usable. The run ended with a valid certificate on
// disk, the host recorded as failed, and the next run set to repeat it exactly.
//
// That is worse than the interruption the cap existed to prevent. The cap was
// the right instinct at the wrong granularity: per-host it multiplies with the
// host count, per-phase it does not.
func TestACertificateIsNeverIssuedAndLeftUnserved(t *testing.T) {
	src := shellCode(readSourceFile(t, "../../scripts/setup-vayudomain.sh"))

	if !strings.Contains(src, "settle_pending_hosts") {
		t.Fatal("there is no end-of-run apply, so a host whose vhost was not picked up after " +
			"issuance is left with a certificate nothing serves")
	}
	if !strings.Contains(src, "settle_pending_hosts\n") {
		t.Error("settle_pending_hosts is defined and never called")
	}

	// The post-issuance step must DEFER rather than restart per host, or the
	// bound this exists to keep is gone again.
	i := strings.Index(src, "PENDING_SERVE+=(")
	if i < 0 {
		t.Fatal("nothing is ever deferred, so the end-of-run apply has no work and the stranding " +
			"is unchanged")
	}

	fn := src[strings.Index(src, "settle_pending_hosts() {"):]
	if e := strings.Index(fn, "\n}\n"); e > 0 {
		fn = fn[:e]
	}
	// One restart for the whole set, not one per host.
	//
	// Counted by the INVOCATION form, not the bare string: the failure message
	// beside it names the same command, so a plain count is 2 against correct
	// code. Asserting on prose rather than behaviour is the defect this whole
	// file exists to catch, and it was committed here first.
	if strings.Count(fn, `$(systemctl restart nginx`) != 1 {
		t.Errorf("the end-of-run apply does not restart exactly once for all pending hosts, so "+
			"the per-host multiplication is back:\n%s", fn)
	}
	// It must try the free mechanism first, and it must re-check per host after,
	// or "settled" is an assumption rather than a measurement.
	if !strings.Contains(fn, "nginx -s reload") {
		t.Error("the end-of-run apply restarts without first trying the signal, so a machine " +
			"where reload works is interrupted for nothing")
	}
	if !strings.Contains(fn, "probe_settles probe_https") {
		t.Error("hosts are marked active without confirming the server actually serves them — " +
			"the exact assumption this whole helper was rewritten to stop making")
	}
	if !strings.Contains(fn, "nginx_ok") {
		t.Error("the end-of-run apply can restart onto a configuration nginx rejects")
	}
}

// A WORKING SITE SCORED AS BROKEN, by the check that was added to stop
// assuming. Observed on a live install after everything else was already right.
//
// probe_https used `curl -f`, which fails on any 4xx or 5xx. The host had no
// posts yet, and the shield challenges an unrecognised client on its first
// request, so the answer was a 404 or a 403 — both of them a perfectly working
// HTTPS site. The helper read that as "this server does not serve it", recorded
// the host as failed, and RESTARTED NGINX trying to repair a server block that
// had been correct all along.
//
// The question worth asking is whether a server block ANSWERS for this host over
// TLS, not whether the page is a 200. Only the absence of an HTTP response means
// no vhost matched: the catch-all default closes without one.
func TestAServingHostIsNotScoredOnItsStatusCode(t *testing.T) {
	src := shellCode(readSourceFile(t, "../../scripts/setup-vayudomain.sh"))

	fn := src[strings.Index(src, "probe_https() {"):]
	if e := strings.Index(fn, "\n}\n"); e > 0 {
		fn = fn[:e]
	}
	if fn == "" {
		t.Fatal("probe_https is gone")
	}

	// `-f` is the defect. A site that answers 404 or 403 is serving.
	if strings.Contains(fn, "curl -f") || strings.Contains(fn, "-fsSk") {
		t.Fatalf("probe_https still fails the host on a 4xx/5xx, so an empty site or one whose "+
			"first request is challenged is reported unserved — and nginx gets restarted to "+
			"repair a vhost that was always correct:\n%s", fn)
	}
	if !strings.Contains(fn, "http_code") {
		t.Fatalf("probe_https does not look at the status at all, so it cannot tell a server "+
			"that answered from one that never did:\n%s", fn)
	}
	// 000 is curl's "no HTTP response", which is exactly the catch-all default
	// server closing the connection — the one state that means no vhost matched.
	if !strings.Contains(fn, "000") {
		t.Error("nothing distinguishes 'no HTTP response at all' from a real status, which is " +
			"the only distinction that matters here")
	}

	// And the failure must say what came back. "does not serve it" over a 403 is
	// a true sentence about a false conclusion, and it cost a live install two
	// runs and an nginx restart to find.
	if !strings.Contains(src, "probe_https_code") {
		t.Error("the failure message cannot report the status observed, so 'no response at all' " +
			"and 'answered 403' are reported as the same problem again")
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
