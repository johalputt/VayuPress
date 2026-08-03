// SPDX-License-Identifier: Apache-2.0

package main

// realip_cidr_test.go — the real-IP nginx writer, run against the input that
// broke a live install.
//
// The agent emitted this into /etc/nginx/conf.d/vayushield-realip.conf:
//
//	set_real_ip_from 131.0.72.0/222400:cb00::/32;
//
// which is "131.0.72.0/22" and "2400:cb00::/32" concatenated with no separator.
// nginx answered "host not found in set_real_ip_from" and refused to load —
// and because that file lives in conf.d, the failure is the WHOLE web server's
// configuration, not one vhost.
//
// The cause was a trim that deleted whitespace instead of splitting on it, and
// two validations that a mashed pair of valid values walked straight through.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realipEmitLoop lifts the emitting loop out of the agent and wraps it so it can
// be fed on stdin. Extracted from the real script rather than reimplemented: a
// copy in this file would pass while the shipped code stayed broken.
func realipEmitLoop(t *testing.T) string {
	t.Helper()
	src := readSourceFile(t, "../../deploy/vayushield-agent.sh")
	// Anchored INSIDE reconcile_realip. The file has an earlier `while IFS= read`
	// in a different function, and the first draft of this helper grabbed that
	// one — so the test exercised unrelated code and failed on an unbound
	// variable rather than on anything it was written to check.
	fn := strings.Index(src, "reconcile_realip() {")
	if fn < 0 {
		t.Fatal("reconcile_realip is gone from vayushield-agent.sh")
	}
	i := strings.Index(src[fn:], "    while IFS= read -r line; do")
	if i < 0 {
		t.Fatal("the real-IP emit loop is gone from vayushield-agent.sh")
	}
	i += fn
	j := strings.Index(src[i:], `    done <"$src"`)
	if j < 0 {
		t.Fatal("cannot bound the real-IP emit loop")
	}
	body := src[i : i+j]
	// Feed it stdin instead of the root-owned range file.
	body = strings.Replace(body, `    done <"$src"`, "    done", 1)
	return "set -eu\nn=0\n" + body + "\ndone\necho \"emitted=$n\"\n"
}

func runRealipLoop(t *testing.T, input string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "emit.sh")
	if err := os.WriteFile(script, []byte(realipEmitLoop(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the emit loop failed to run: %v\n%s", err, out)
	}
	return string(out)
}

// THE LIVE FAILURE. Two ranges on one line must become two directives, not one
// concatenated token.
func TestTwoRangesOnOneLineBecomeTwoDirectives(t *testing.T) {
	out := runRealipLoop(t, "131.0.72.0/22 2400:cb00::/32\n")

	if strings.Contains(out, "131.0.72.0/222400:cb00::/32") {
		t.Fatalf("the two ranges were concatenated into one token — the exact string nginx "+
			"refused on a live install, taking the whole web server's config down with it:\n%s", out)
	}
	for _, want := range []string{"set_real_ip_from 131.0.72.0/22;", "set_real_ip_from 2400:cb00::/32;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "emitted=2") {
		t.Errorf("expected two directives:\n%s", out)
	}
}

// Defence in depth: even if a mashed token arrives already joined — from a
// source file written by something else, or a future regression upstream — the
// shape check must refuse it. A validation that a concatenated pair of valid
// values passes is not checking the shape, and both of the original checks did.
func TestAMashedCIDRIsRefusedEvenIfItArrivesJoined(t *testing.T) {
	out := runRealipLoop(t, "131.0.72.0/222400:cb00::/32\n")
	if strings.Contains(out, "set_real_ip_from") {
		t.Fatalf("a token carrying two slashes was emitted into nginx config:\n%s", out)
	}
	if !strings.Contains(out, "emitted=0") {
		t.Errorf("expected nothing emitted:\n%s", out)
	}
}

// A prefix length outside its family's range is the other shape a bad join
// produces, and nginx rejects it just as fatally.
func TestAnImpossiblePrefixLengthIsRefused(t *testing.T) {
	out := runRealipLoop(t, "1.2.3.4/99\n2400:cb00::/300\n")
	if strings.Contains(out, "set_real_ip_from") {
		t.Fatalf("an out-of-range prefix reached nginx config:\n%s", out)
	}
}

// And the ordinary cases must still work, or the fix is just a stricter way of
// emitting nothing — which would leave every per-IP control metering the edge.
func TestOrdinaryRangesStillEmit(t *testing.T) {
	out := runRealipLoop(t, "# Cloudflare\n103.21.244.0/22\n\n  2606:4700::/32  \n")
	for _, want := range []string{"set_real_ip_from 103.21.244.0/22;", "set_real_ip_from 2606:4700::/32;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "emitted=2") {
		t.Errorf("expected two directives:\n%s", out)
	}
	// A comment-only line must contribute nothing rather than an empty directive.
	if strings.Contains(out, "set_real_ip_from ;") {
		t.Error("an empty directive was emitted")
	}
}
