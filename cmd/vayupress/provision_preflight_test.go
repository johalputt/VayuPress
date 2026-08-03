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
	need := []string{"bash", "mkdir", "rm"}
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
	harness := "set -euo pipefail\n" +
		"HOST=x.example; CACHE_DIR=" + dir + "; HOST_FAILURES=0\n" +
		"info(){ echo \"INFO $*\"; }; warn(){ echo \"WARN $*\"; }; set_tls(){ :; }\n" +
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
	return string(b), strings.Contains(string(b), "BLOCKED")
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
}
