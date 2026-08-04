// SPDX-License-Identifier: Apache-2.0

package main

// realip_cidr_test.go — the real-IP nginx writer, run against the input that
// broke a live install.
//
// The agent emitted this into /etc/nginx/conf.d/vayushield-realip.conf:
//
//	set_real_ip_from 192.0.2.0/242001:db8::/32;
//
// which is "192.0.2.0/24" and "2001:db8::/32" concatenated with no separator.
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
	"time"
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
	i := strings.Index(src[fn:], "while IFS= read -r line ")
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
	// CONTROL_DIR is where the loop records refused tokens. Without it the
	// extracted code dies on an unbound variable, which is a harness fault
	// reported as a code fault.
	return "set -eu\nCONTROL_DIR=\"${CONTROL_DIR:-$PWD}\"\nn=0\n" + body +
		"\ndone\necho \"emitted=$n\"\n"
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
	out := runRealipLoop(t, "192.0.2.0/24 2001:db8::/32\n")

	if strings.Contains(out, "192.0.2.0/242001:db8::/32") {
		t.Fatalf("the two ranges were concatenated into one token — the exact string nginx "+
			"refused on a live install, taking the whole web server's config down with it:\n%s", out)
	}
	for _, want := range []string{"set_real_ip_from 192.0.2.0/24;", "set_real_ip_from 2001:db8::/32;"} {
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
	out := runRealipLoop(t, "192.0.2.0/242001:db8::/32\n")
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
	out := runRealipLoop(t, "1.2.3.4/99\n2001:db8::/300\n")
	if strings.Contains(out, "set_real_ip_from") {
		t.Fatalf("an out-of-range prefix reached nginx config:\n%s", out)
	}
}

// And the ordinary cases must still work, or the fix is just a stricter way of
// emitting nothing — which would leave every per-IP control metering the edge.
func TestOrdinaryRangesStillEmit(t *testing.T) {
	out := runRealipLoop(t, "# Cloudflare\n103.21.244.0/22\n\n  2001:db8:1::/32  \n")
	for _, want := range []string{"set_real_ip_from 103.21.244.0/22;", "set_real_ip_from 2001:db8:1::/32;"} {
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

// THE ROOT CAUSE, found by the audit rather than by reasoning from the symptom:
// the two published range lists were appended byte-for-byte into one file.
//
//	if ! curl -fsSL "$v4" >>"$tmp" || ! curl -fsSL "$v6" >>"$tmp"; then
//
// The published IPv4 list ends WITHOUT a trailing newline, so the first byte of
// the IPv6 list lands on the same physical line as the last IPv4 CIDR. That is
// where "192.0.2.0/242001:db8::/32" was born — upstream of the emitter that
// merely passed it through.
//
// Concatenating two documents without asserting the boundary between them is the
// defect. It does not stop being one because the far end usually sends a newline.
func TestTheRangeFetchAssertsTheBoundaryBetweenTheTwoLists(t *testing.T) {
	// Comments stripped first. The retired form is quoted in the comment that
	// explains it, and a whole-file search finds it there — the third time today
	// a gate has read prose about the behaviour instead of the behaviour.
	src := shellCode(readSourceFile(t, "../../deploy/vayushield-firewall.sh"))
	if strings.Contains(src, `curl -fsSL "$v4" >>"$tmp" || ! curl -fsSL "$v6" >>"$tmp"`) {
		t.Fatal("the two range lists are still appended straight into one file with nothing " +
			"between them, so an IPv4 list without a trailing newline welds its last CIDR to " +
			"the first IPv6 one — which nginx rejects, taking the whole configuration down")
	}
	if !strings.Contains(src, `printf '%s\n%s\n' "$v4body" "$v6body"`) {
		t.Error("the fetch does not join the two bodies with an explicit newline between them")
	}
	// CR stripping matters as much: a CRLF list produces tokens ending in \r,
	// which fail the character check and would now be silently refused.
	if !strings.Contains(src, `tr -d '\r'`) {
		t.Error("carriage returns are not stripped, so a CRLF-served list yields tokens that " +
			"the shape check refuses one by one")
	}
}

// FINDING AGAINST MY OWN FIX, which is why the audit had to gate the release:
// the stricter shape check DROPS the malformed token — and that token contains
// one genuine IPv4 range and one genuine IPv6 range. Refusing it loses both.
//
// Silently, it converts a loud nginx failure into a quiet gap in coverage: every
// per-IP control then meters the edge for those ranges and nobody is told. That
// is the same defect one layer quieter, and this whole track has been about
// exactly that shape.
func TestRefusedRangesAreReportedRatherThanDroppedSilently(t *testing.T) {
	src := readSourceFile(t, "../../deploy/vayushield-agent.sh")
	i := strings.Index(src, "reconcile_realip() {")
	if i < 0 {
		t.Fatal("reconcile_realip is gone")
	}
	body := src[i:]
	if j := strings.Index(body, "\nreconcile_"); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "realip.skipped") {
		t.Fatal("tokens refused by the shape check are dropped without being recorded, so a " +
			"real range this server will not resolve behind disappears with no trace")
	}
	if !strings.Contains(body, "were refused as malformed") {
		t.Error("the operator is never told that ranges were refused, so a partial apply reads " +
			"as a complete one")
	}
	// It must still report success when nothing was refused — a warning on every
	// clean apply is noise, and noise is what makes a real warning invisible.
	if !strings.Contains(body, `if [ "$skipped" -gt 0 ]; then`) {
		t.Error("the refusal notice is not conditional on something actually being refused")
	}
}

// shellCode drops comment and blank lines so a gate matches what RUNS, not what
// a comment says about what used to run.
func shellCode(src string) string {
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

// THE LAST STATE THAT FITS ALL THE EVIDENCE, and the only one nothing on the
// page could previously tell apart from a failed reload.
//
// On a live install: the vhost file was present and enabled, `nginx -t` passed,
// the reload command reported success, and the host was still answered by the
// default server with nginx's workers dating from four days earlier.
//
// Every one of those is consistent if nginx never includes sites-enabled. The
// config test passes because it tests the configuration nginx actually runs,
// which is valid — it simply does not contain the vhost. The reload succeeds
// because there is nothing wrong with it. And the file on disk is inert.
//
// From outside, "the reload did not happen" and "the reload happened and loaded
// a configuration without your vhost" look identical. This had to be read.
func TestAnNginxThatNeverIncludesSitesEnabledIsNamed(t *testing.T) {
	dir := t.TempDir()
	saved := nginxMainConf
	t.Cleanup(func() { nginxMainConf = saved })

	write := func(body string) {
		t.Helper()
		p := filepath.Join(dir, "nginx.conf")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		nginxMainConf = p
	}

	// A config that does NOT include the vhost directory.
	write("http {\n  include /etc/nginx/conf.d/*.conf;\n  server { listen 80; }\n}\n")
	c := sitesEnabledIncludedCheck()
	if c.OK || !c.Fatal {
		t.Fatalf("an nginx that never includes sites-enabled was not reported as blocking: %q — %s",
			c.Label, c.Detail)
	}
	if !strings.Contains(c.Label+c.Detail, "inert") {
		t.Error("the finding does not say that vhosts written there have no effect")
	}
	// It must explain why nginx -t and the reload both look fine, or the operator
	// reads it as contradicting the two green rows above it.
	for _, want := range []string{"nginx -t", "reload"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("the finding never reconciles itself with %q passing", want)
		}
	}

	// The ordinary Debian/Ubuntu shape must come back clean.
	write("http {\n  include /etc/nginx/conf.d/*.conf;\n  include /etc/nginx/sites-enabled/*;\n}\n")
	if ok := sitesEnabledIncludedCheck(); !ok.OK || ok.Fatal {
		t.Errorf("a standard config with the include was flagged: %s", ok.Detail)
	}

	// A COMMENTED-OUT include is not an include. Reading prose about the
	// behaviour instead of the behaviour has caught this project out three times
	// today, and it would be a confident wrong answer here too.
	write("http {\n  # include /etc/nginx/sites-enabled/*;\n}\n")
	if c := sitesEnabledIncludedCheck(); c.OK {
		t.Error("a commented-out include counted as an include")
	}

	// Unreadable must claim nothing, in either direction.
	nginxMainConf = filepath.Join(dir, "does-not-exist")
	if c := sitesEnabledIncludedCheck(); !c.OK || !strings.Contains(c.Detail, "nothing is claimed") {
		t.Error("an unreadable nginx.conf produced a verdict instead of an abstention")
	}
}

// SECOND SILENT DROP, from the same root cause and found by the same audit.
//
// `read` returns non-zero on a final line with no trailing newline, so the loop
// body never runs for it and the LAST range in the file disappears — from the
// nginx config and from the kernel set alike. The fetch now guarantees the
// newline, but this file is explicitly documented as hand-writable, and an
// editor that omits a final newline is entirely ordinary.
//
// The reported count made it worse: it uses grep, which DOES count an
// unterminated final line, so the panel claimed one more range than either
// consumer actually applied.
func TestAFinalLineWithoutANewlineIsNotDropped(t *testing.T) {
	// No trailing newline on the last entry — the exact shape of the published
	// list, and of a hand-edited file.
	out := runRealipLoop(t, "192.0.2.0/24\n198.51.100.0/24")

	if !strings.Contains(out, "set_real_ip_from 198.51.100.0/24;") {
		t.Fatalf("the last range was dropped because the file had no trailing newline — it "+
			"disappears from nginx and from the kernel set, and the reported count still "+
			"includes it:\n%s", out)
	}
	if !strings.Contains(out, "emitted=2") {
		t.Errorf("expected both ranges:\n%s", out)
	}
}

// Every consumer of the range file must do this, not just the one that was
// reported. The same loop shape reads it for the kernel firewall.
func TestEveryRangeFileConsumerReadsTheFinalLine(t *testing.T) {
	for _, f := range []string{
		"../../deploy/vayushield-agent.sh",
		"../../deploy/vayushield-firewall.sh",
	} {
		src := shellCode(readSourceFile(t, f))
		bare := strings.Count(src, "while IFS= read -r line; do")
		if bare > 0 {
			t.Errorf("%s has %d loop(s) that drop an unterminated final line; each one silently "+
				"loses the last entry of a hand-written range file", f, bare)
		}
		if !strings.Contains(src, `read -r line || [ -n "$line" ]`) {
			t.Errorf("%s never guards against the unterminated final line", f)
		}
	}
}

// AUDIT FINDING against the measurement the whole certificate diagnosis rests
// on. It read:
//
//	time.Duration(ticks) * time.Second / 100
//
// which multiplies FIRST, by 10^9. `ticks` counts USER_HZ units since boot, so
// on a machine with long uptime the product overflows int64 and the function
// returns a nonsense moment.
//
// That is not a harmless glitch here: the output is rendered as "nginx last
// reloaded <date>" with an instruction attached, and an operator is asked to act
// on it. A diagnostic that can be confidently wrong about its own headline
// number is worse than one that abstains.
func TestTheReloadClockCannotOverflow(t *testing.T) {
	src := readSourceFile(t, "admin_os_scoped_diagnose.go")
	// Comments stripped. The retired expression is quoted in the comment that
	// documents it, and this is the FOURTH gate today to match prose about the
	// behaviour instead of the behaviour. It is a reliable enough mistake to be
	// worth a helper rather than a habit.
	body := goCode(goFuncBody(src, "nginxLastReload"))

	if strings.Contains(body, "time.Duration(ticks) * time.Second / 100") {
		t.Fatal("the tick conversion multiplies by 10^9 before dividing, so a long-uptime " +
			"machine overflows int64 and the reported reload time is nonsense — presented as " +
			"fact, with an instruction attached")
	}
	if !strings.Contains(body, "(time.Second / 100)") {
		t.Error("the unit is not scaled before multiplying")
	}

	// Arithmetic, checked rather than asserted about. Three years of uptime.
	const ticks int64 = 3 * 365 * 24 * 60 * 60 * 100
	if got := time.Duration(ticks) * (time.Second / 100); got <= 0 {
		t.Fatalf("three years of uptime still overflows: %v", got)
	} else if want := 3 * 365 * 24 * time.Hour; got != want {
		t.Errorf("tick conversion is wrong: got %v, want %v", got, want)
	}
}

// goCode drops // comment lines so a gate matches what COMPILES, not what a
// comment says about what used to compile. Four separate checks in one day have
// been fooled by quoting the defect they exist to prevent.
func goCode(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
