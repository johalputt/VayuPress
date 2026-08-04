// SPDX-License-Identifier: Apache-2.0

package main

// provision_preflight_test.go — the loopback pre-flight in setup-vayudomain.sh.
//
// The block is extracted from the real script and run, rather than asserted
// against as text. A structural check would have passed on the first version of
// this guard, which skipped certbot for EVERY host on a box without curl — a
// check added to protect certificate issuance becoming the thing that stopped
// it. Only running it, with curl genuinely absent from PATH, showed that.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// preflightBlock lifts the guard verbatim out of the shell helper.
func preflightBlock(t *testing.T) string {
	t.Helper()
	src := readSourceFile(t, "../../scripts/setup-vayudomain.sh")
	i := strings.Index(src, "  # A pre-flight that cannot RUN")
	j := strings.Index(src, "  # Its OWN certificate lineage")
	if i < 0 || j < 0 || j < i {
		t.Fatal("the loopback pre-flight is gone from setup-vayudomain.sh, so a host that " +
			"cannot answer its own challenge goes straight to certbot again and spends a " +
			"validation attempt to learn nothing")
	}
	return src[i:j]
}

// runPreflight executes the extracted block. withCurl controls whether curl is
// reachable on PATH at all.
func runPreflight(t *testing.T, withCurl bool) (out string, blocked bool) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only the externals the block needs. curl is added or withheld — which is
	// the variable under test, and it cannot be simulated by leaving the system
	// PATH in place, because the system PATH has curl on it.
	// head and cat are used by the systemd-diagnostics dump. They were missing
	// from this list, so the dump emitted "head: command not found" and the
	// assertions below never noticed — which is the same class of defect as the
	// one this file exists to catch, committed by the file itself.
	need := []string{"bash", "mkdir", "rm", "head", "cat"}
	if withCurl {
		need = append(need, "curl")
	}
	for _, b := range need {
		p, err := exec.LookPath(b)
		if err != nil {
			t.Skipf("%s is not available in this environment", b)
		}
		if err := os.Symlink(p, filepath.Join(bin, b)); err != nil {
			t.Fatal(err)
		}
	}
	harness := "set -uo pipefail\n" +
		"HOST=x.example; CACHE_DIR=" + dir + "; HOST_FAILURES=0\n" +
		"ok(){ echo \"OK $*\"; }; info(){ echo \"INFO $*\"; }; warn(){ echo \"WARN $*\"; }\n" +
		"set_tls(){ :; }; systemctl(){ echo stub; }; pgrep(){ return 1; }\n" +
		// The REAL probe, so `withCurl` is exercised end to end rather than
		// simulated. Every one of this file's findings came from running the
		// thing; a stubbed probe would have hidden the curl-guard bug it was
		// written for.
		probeChallengeFn(t) +
		// The ladder is stubbed — it has its own file — but it must be CALLED,
		// and its absence must be loud rather than a silent 127.
		"\nforce_apply(){ echo ESCALATED; return 1; }\n" +
		"mkdir -p \"${CACHE_DIR}/.well-known/acme-challenge\"\n" +
		"for _once in 1; do\n" +
		strings.ReplaceAll(preflightBlock(t), "continue", "echo BLOCKED; exit 9") +
		"\ndone\necho REACHED_CERTBOT\n"
	script := filepath.Join(dir, "pf.sh")
	if err := os.WriteFile(script, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(bin, "bash"), script)
	cmd.Env = []string{"PATH=" + bin}
	b, _ := cmd.CombinedOutput()
	// A MISSING COMMAND MUST NEVER BE A PASS. This harness ran for a release with
	// `probe_challenge: command not found`, and the assertions were all green:
	// bash returns 127, the `if !` inverted it, and the block took its failure
	// branch for a reason that had nothing to do with the guard under test. Every
	// conclusion drawn from it was worthless and looked identical to a real one.
	if strings.Contains(string(b), "command not found") {
		t.Fatalf("the harness is missing a command, so these assertions are passing on a 127 "+
			"and not on the guard:\n%s", b)
	}
	return string(b), strings.Contains(string(b), "BLOCKED")
}

// probeChallengeFn lifts the real loopback probe out of the helper.
func probeChallengeFn(t *testing.T) string {
	t.Helper()
	src := readSourceFile(t, "../../scripts/setup-vayudomain.sh")
	i := strings.Index(src, "probe_challenge() {")
	j := strings.Index(src[i:], "\n}\n")
	if i < 0 || j < 0 {
		t.Fatal("probe_challenge is gone from setup-vayudomain.sh")
	}
	return src[i : i+j+3]
}

// FINDING, in the guard's own first draft: a pre-flight that cannot RUN was
// being read as a pre-flight that FAILED.
//
// On a box without curl, every host would have been skipped before certbot and
// no certificate would ever have been issued — the check protecting issuance
// becoming the thing preventing it. No tool, no claim, no blocking.
func TestThePreflightSkipsRatherThanBlocksWhenCurlIsMissing(t *testing.T) {
	out, blocked := runPreflight(t, false)
	if blocked {
		t.Fatalf("with curl absent the pre-flight BLOCKED certbot, so a box without curl would "+
			"never obtain a certificate for any host:\n%s", out)
	}
	if !strings.Contains(out, "REACHED_CERTBOT") {
		t.Errorf("certbot was not reached:\n%s", out)
	}
	if !strings.Contains(out, "skipping the loopback pre-flight") {
		t.Errorf("the skip is silent, so an operator cannot tell the check did not run:\n%s", out)
	}
	// It must be an INFO, not a WARN: nothing is wrong, the check merely did not run.
	if strings.Contains(out, "WARN") {
		t.Errorf("a check that could not run is reported as a fault:\n%s", out)
	}
}

// And when it CAN run and the server does not answer, it must stop — that is the
// entire point. Failed validations are rate-limited per hostname, so spending
// one to discover what a loopback request already knows is pure loss.
func TestThePreflightStopsCertbotWhenTheServerCannotAnswerItself(t *testing.T) {
	out, blocked := runPreflight(t, true)
	if !blocked {
		t.Fatalf("with nothing serving the challenge on port 80, the pre-flight let certbot run "+
			"anyway — spending a rate-limited validation attempt to learn what a loopback "+
			"request already showed:\n%s", out)
	}
	for _, want := range []string{"does not serve its own ACME challenge", "certbot was NOT run"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal never says %q, so the log does not explain itself:\n%s", want, out)
		}
	}
	// AND IT MUST TRY TO REPAIR IT FIRST. Reporting this state was all the helper
	// ever did, and reporting it is worth very little: the operator is told the
	// server does not answer for the host, by the only process on the machine
	// with the privileges to do something about it. Every repair the panel
	// offered ended in the same reload that had already silently failed.
	if !strings.Contains(out, "ESCALATED") {
		t.Errorf("the pre-flight gave up without escalating, so a reload that reports success "+
			"and does not take effect is diagnosed forever and never fixed:\n%s", out)
	}
}

// THE MISSING TECHNOLOGY, and the reason it was missing is worth stating: these
// helpers run as ROOT out of /usr/local/lib/vayupress, which the unprivileged
// web app cannot write. That boundary is correct — a process that can replace
// what root executes is a full privilege escalation — but it had a consequence
// nobody had written down: the in-app updater swaps the BINARY ONLY, so a fix to
// these scripts reached NOBODY.
//
// A real defect proved it. `systemctl reload nginx 2>/dev/null || true`
// discarded the reload's exit status and reported success regardless, so every
// certificate on an install failed with an unexplained connection error. The fix
// was one line and could not reach a single existing install, because the only
// delivery mechanism was an operator with a shell.
//
// So the worker upgrades itself, by the mechanism the VayuShield agent already
// uses (ADR-0123). These assertions are about the properties that make that safe
// to run as root at all.
func TestTheRootWorkerUpgradesItselfAndRefusesUnverifiedCode(t *testing.T) {
	src := readSourceFile(t, "../../scripts/provision-subdomains.sh")
	body := src[strings.Index(src, "self_upgrade_helpers() {"):]

	if !strings.Contains(src, "self_upgrade_helpers\n") {
		t.Fatal("the worker defines a self-upgrade and never calls it")
	}
	// Verification is not optional and has no fallback: what the bundle contains
	// runs as root.
	if !strings.Contains(body, "verify-blob") {
		t.Fatal("the bundle is installed without verifying its signature — an unverified " +
			"archive executed as root is a full compromise of the machine")
	}
	// The identity must be pinned to this project. Verifying "a valid signature"
	// without pinning who signed it accepts anybody's.
	for _, want := range []string{"certificate-identity-regexp", "certificate-oidc-issuer"} {
		if !strings.Contains(body, want) {
			t.Errorf("the verification does not pin %s, so any valid Sigstore signature would "+
				"pass — including an attacker's own", want)
		}
	}
	// Verify BEFORE unpacking. Unpacking an unverified archive as root is already
	// the compromise, whatever is checked afterwards.
	if v, x := strings.Index(body, "verify-blob"), strings.Index(body, "tar -C"); v < 0 || x < 0 || v > x {
		t.Fatal("the archive is unpacked before its signature is checked")
	}
	// No cosign must mean NO INSTALL, never a silent unverified one.
	i := strings.Index(body, "cosign is not installed")
	if i < 0 {
		t.Fatal("a missing cosign is not handled explicitly")
	}
	if !strings.Contains(body[i:i+200], "return 0") {
		t.Error("a missing cosign does not stop the upgrade path")
	}
}

// Every failure in the upgrade path must be a SKIP, never a stop.
//
// This runs before the provisioning work. A host that cannot reach GitHub must
// still obtain certificates with the helpers it already has — an upgrade path
// that can prevent the thing it exists to improve is worse than none, and that
// is the same shape as the curl guard on the pre-flight above.
func TestTheSelfUpgradeNeverBlocksProvisioning(t *testing.T) {
	src := readSourceFile(t, "../../scripts/provision-subdomains.sh")
	body := src[strings.Index(src, "self_upgrade_helpers() {"):]
	end := strings.Index(body, "\nself_upgrade_helpers\n")
	if end < 0 {
		t.Fatal("cannot bound the self-upgrade function")
	}
	body = body[:end]
	if strings.Contains(body, "exit 1") || strings.Contains(body, "return 1") {
		t.Error("the self-upgrade can fail the run. A host that cannot reach the release must " +
			"still provision with the helpers it has")
	}
	// And it must say why it skipped, or a silent no-op is indistinguishable from
	// an upgrade that happened — the exact ambiguity this whole track is about.
	if strings.Count(body, "self-upgrade skipped") < 4 {
		t.Error("the skip paths do not each explain themselves")
	}
}

// THE DELIVERY PROBLEM, solved where self-upgrade already works.
//
// The provisioning helpers are root-owned, so the in-app updater cannot replace
// them — and on one install nginx had not reloaded for FOUR DAYS while vhosts
// were written minutes earlier, because the helper's reload step discarded its
// exit status. The one-line fix could not reach that install by any route except
// an operator with a shell, which a beginner does not have.
//
// The VayuShield agent is the only root process on the box that already upgrades
// ITSELF, so it is the only place a new capability can reach an install that is
// already broken. These assertions are about what makes that safe as root.
func TestTheShieldAgentCanRepairTheProvisioningHelpers(t *testing.T) {
	src := readSourceFile(t, "../../deploy/vayushield-agent.sh")
	i := strings.Index(src, "reconcile_provisionhelpers() {")
	if i < 0 {
		t.Fatal("the agent cannot repair the provisioning helpers, so an install whose helpers " +
			"are broken has no route back except a terminal")
	}
	body := src[i:]
	if j := strings.Index(body, "\nreconcile_defaulthost() {"); j > 0 {
		body = body[:j]
	}
	// Wired into the reconcile loop, and advertised, or the panel cannot know it
	// exists on the helper actually installed.
	if !strings.Contains(src, "      reconcile_provisionhelpers\n") {
		t.Error("the handler is never called from the reconcile loop")
	}
	if !strings.Contains(src, "provisionhelpers=1") {
		t.Error("the capability is not advertised, so the panel cannot tell whether the " +
			"installed helper supports it")
	}
	// Same supply-chain properties as every other root install path.
	if !strings.Contains(body, "verify-blob") {
		t.Fatal("the bundle is installed without verifying its signature — unverified code " +
			"executed as root is a full compromise")
	}
	for _, want := range []string{"certificate-identity-regexp", "certificate-oidc-issuer"} {
		if !strings.Contains(body, want) {
			t.Errorf("verification does not pin %s, so any valid Sigstore signature passes", want)
		}
	}
	if v, x := strings.Index(body, "verify-blob"), strings.Index(body, "tar -C"); v < 0 || x < 0 || v > x {
		t.Fatal("the archive is unpacked before its signature is checked")
	}

	// AND THE POINT OF THE WHOLE ACTION: it must reload nginx, and must report
	// that reload's failure rather than discarding it. Discarding it is the exact
	// defect being repaired, and repeating it here would be beyond ironic.
	if !strings.Contains(body, "systemctl reload nginx") {
		t.Fatal("the repair installs the helpers but never reloads nginx — leaving the machine " +
			"in precisely the state it was called to fix")
	}
	if strings.Contains(body, "systemctl reload nginx 2>/dev/null || true") {
		t.Fatal("the repair discards the reload's exit status, which is the defect it exists " +
			"to correct")
	}
	if !strings.Contains(body, "reloading nginx failed") {
		t.Error("a failed reload is not reported, so the operator is told the repair succeeded")
	}
	// nginx -t before reloading: reloading a config that does not pass its own
	// test is how a working site is taken down by a repair.
	if nt, rl := strings.Index(body, "nginx -t"), strings.Index(body, "systemctl reload nginx"); nt < 0 || nt > rl {
		t.Error("the repair reloads without testing the configuration first")
	}
}

// FINDING — the agent capability shipped WITHOUT a control that asks for it.
//
// A root-side action was added, verified, gated and released, and no button
// anywhere wrote its flag. Nothing could ever request it. That is the same
// defect as a button that does nothing, reached from the opposite direction,
// and it cost an operator another round of being told to press something that
// was not there.
//
// So the rule this gate encodes: every capability the agent advertises must have
// a control, and every control must name a capability the agent has.
func TestEveryAgentCapabilityHasAControlThatAsksForIt(t *testing.T) {
	agent := readSourceFile(t, "../../deploy/vayushield-agent.sh")
	i := strings.Index(agent, "AGENT_CAPS=\"")
	if i < 0 {
		t.Fatal("the agent advertises no capabilities")
	}
	caps := agent[i+len("AGENT_CAPS=\"") : i+len("AGENT_CAPS=\"")+strings.Index(agent[i+len("AGENT_CAPS=\""):], "\"")]

	// Capabilities that are not operator-triggered remediations: they are
	// mechanisms the agent uses internally, not buttons.
	internal := map[string]bool{"selfupgrade": true, "digest": true, "cosignpin": true, "rescue": true}

	for _, tok := range strings.Fields(caps) {
		name := strings.SplitN(tok, "=", 2)[0]
		if internal[name] {
			continue
		}
		found := false
		for _, f := range shieldFixes {
			if f.Cap == name+"=1" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the agent advertises %q and no control writes its flag — a root-side "+
				"capability nobody can ask for is the same dead end as a button that does "+
				"nothing", name)
		}
	}

	// And the reverse: a control naming a capability the agent never advertises
	// renders a button that the helper will silently ignore.
	for key, f := range shieldFixes {
		if !strings.Contains(caps, strings.TrimSuffix(f.Cap, "=1")+"=1") {
			t.Errorf("the %q control requires capability %q, which the agent does not "+
				"advertise", key, f.Cap)
		}
		if !strings.Contains(agent, "reconcile_"+strings.TrimSuffix(f.Cap, "=1")) {
			t.Errorf("the %q control has no reconcile handler in the agent", key)
		}
	}
}

// The console must point at the control, not merely describe the problem.
// Surfacing a fault without the action that resolves it is the defect this whole
// console was rebuilt to remove.
func TestTheReloadFindingNamesTheControlThatFixesIt(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_scoped_diagnose.go"), "reloadLagCheck")
	if !strings.Contains(body, "Repair the certificate helpers") {
		t.Fatal("the reload finding does not name the control that fixes it, so an operator " +
			"reads a precise diagnosis and still has nowhere to go")
	}
	if strings.Contains(body, "Re-run the provisioning installer") || strings.Contains(body, "sudo") {
		t.Error("the finding still sends the operator to a terminal")
	}
}

// THE SAME DEFECT, THIRD LAYER. The parity gate above checks that a capability
// has a registry entry and that an entry names a real capability — and both
// passed while the entry was never RENDERED, so the button still did not exist.
//
// A registry entry nobody draws is exactly as dead as a capability nobody asks
// for, and a gate that stops one layer short of the screen is a gate that proves
// the operator can reach something they cannot.
func TestEveryRegisteredFixIsActuallyRendered(t *testing.T) {
	src := readSourceFile(t, "vayushield_hardening.go")
	for key := range shieldFixes {
		if !strings.Contains(src, `shieldFixRow("`+key+`")`) {
			t.Errorf("the %q remediation is registered and never rendered, so no button for it "+
				"exists on any page — the operator is told to press something that is not there", key)
		}
	}
	// And it must render something real for this key rather than an empty string.
	if row := shieldFixRow("provisionhelpers"); !strings.Contains(row, "Certificate helpers") {
		t.Errorf("the certificate-helper row renders nothing usable: %q", row)
	}
}

// FINDING, and it invalidated every repair this product offered: they all ended
// in `systemctl reload nginx` — the exact command that had been failing.
//
// On the install this was written for, nginx went FIVE DAYS without reading a
// new configuration while every run reported success. Each fix added on top
// inherited the single failure it existed to correct, because none of them had a
// second way to reach nginx.
//
// `nginx -s reload` reads the master's pid file and signals it directly,
// involving systemd not at all. It is a different mechanism, not a retry of
// something that just failed a moment ago.
func TestEveryReloadPathHasASecondMechanism(t *testing.T) {
	// Comment lines are stripped before matching. The first version of this test
	// searched the whole file, so a mutation that disabled the fallback while
	// leaving the comment describing it PASSED — a check reading prose about the
	// behaviour instead of the behaviour, which is the same defect as everything
	// else this file gates.
	code := func(src string) string {
		var b strings.Builder
		for _, line := range strings.Split(src, "\n") {
			if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		return b.String()
	}
	for _, f := range []string{
		"../../scripts/setup-vayudomain.sh",
		"../../deploy/vayushield-agent.sh",
	} {
		src := code(readSourceFile(t, f))
		sysIdx := strings.Index(src, "systemctl reload nginx")
		if sysIdx < 0 {
			continue
		}
		// The fallback must be INVOKED, in a condition that can succeed — not
		// merely mentioned in an error string.
		if !strings.Contains(src, `out2="$(nginx -s reload 2>&1)"`) {
			t.Errorf("%s reloads nginx only through systemd. When systemd is not what "+
				"supervises nginx — a master started by hand, a unit never installed, a unit in a "+
				"failed state — that call fails and the repair silently inherits the failure it "+
				"exists to fix", f)
			continue
		}
		if strings.Index(src, `out2="$(nginx -s reload 2>&1)"`) < sysIdx {
			t.Errorf("%s signals the master before trying systemd; where systemd IS supervising "+
				"nginx, its reload is the correct one", f)
		}
		if !strings.Contains(src, "BOTH ways") {
			t.Errorf("%s does not report a failure of both mechanisms distinctly, so the second "+
				"mechanism just moves the silence one step along", f)
		}
	}
}

// FINDING, straight off a live console: the blocking count fell from seven to
// one and four checks vanished, because they were gated on the PREVIOUS run's
// error mentioning a connection problem.
//
// The moment a new run started, its log segment held one line and no error yet,
// the condition went false, and the vhost check, the listener check, the
// challenge probe and the reload comparison all disappeared — precisely when
// somebody was watching a run and wanted to know what it was doing. A
// diagnostic conditioned on a stale error is one that is absent whenever
// something is actually happening.
func TestTheStructuralChecksDoNotDependOnThePreviousError(t *testing.T) {
	body := goFuncBody(readSourceFile(t, "admin_os_scoped_diagnose.go"), "diagnoseCertificate")
	for _, call := range []string{"vhostCheck(d.Host)", "challengeProbe(ctx, d.Host)",
		"port80ListenerCheck()", "reloadLagCheck("} {
		i := strings.Index(body, call)
		if i < 0 {
			t.Fatalf("%s is no longer called at all", call)
		}
		// Nothing between the log-segment read and the call may branch on the
		// previous error: that is what made them vanish mid-run.
		seg := body[strings.Index(body, "seg := hostLogSegment"):i]
		if strings.Contains(seg, "certbotErrorKind(seg)") && strings.Contains(seg, "if ") {
			t.Errorf("%s is still gated on the previous run's error text, so it disappears the "+
				"moment a new run starts", call)
		}
	}
}

// FINDING THAT COULD NOT BE DIAGNOSED REMOTELY, so the helper records it.
//
// On a live install `systemctl reload nginx` returned SUCCESS and nginx did not
// reload — its workers predated the vhost by five days. Every remote guess at
// why has been wrong, and the three facts that settle it are only available on
// the machine: whether the unit is active, which PID systemd believes is the
// master, and which PID the running nginx actually is.
//
// If those disagree, systemd is reloading something that is not the running
// nginx, and its success is true for the unit and meaningless for the server.
func TestAFailedPreflightRecordsSystemdsViewOfNginx(t *testing.T) {
	src := readSourceFile(t, "../../scripts/setup-vayudomain.sh")
	i := strings.Index(src, "pre-flight failed for ${HOST}")
	if i < 0 {
		t.Fatal("the pre-flight failure path is gone")
	}
	seg := src[i:]
	if j := strings.Index(seg, "certbot was NOT run"); j > 0 {
		seg = seg[:j+400]
	}
	for _, want := range []string{"is-active", "MainPID", "/run/nginx.pid", "pgrep"} {
		if !strings.Contains(seg, want) {
			t.Errorf("a failed pre-flight does not record %q, so the one state that cannot be "+
				"diagnosed from outside — a reload reporting success while nginx does not "+
				"reload — stays undiagnosable", want)
		}
	}
	// And it must say what a disagreement MEANS, or the operator is handed four
	// numbers and no reading of them.
	if !strings.Contains(seg, "disagree") {
		t.Error("the dump does not explain what the numbers mean when they differ")
	}
}
