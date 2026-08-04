// SPDX-License-Identifier: Apache-2.0

package main

// provision_escalate_test.go — the reload-escalation ladder in
// setup-vayudomain.sh, extracted from the real script and RUN.
//
// WHAT IT IS FOR, measured on a live install rather than argued about:
// `nginx -t` passed, `systemctl reload nginx` exited 0, and systemd's MainPID,
// /run/nginx.pid and the running master all named the same process — while
// nginx's workers were five days old and the host was still unreachable. A
// reload that reports success and does not happen is invisible to every check
// that trusts the exit status, and until this ladder existed, every check and
// every repair path this product offered trusted exactly that.
//
// So the ladder is not "retry the reload". It is: stop believing the exit status,
// ask the SERVER, and on a no, use mechanisms that do not share a failure mode
// with the one that just lied — cheapest first, re-asking after each.
//
// Asserted by running it, because the interesting properties are orderings and
// refusals. Text assertions would pass against a ladder that restarts first, or
// one that restarts onto a configuration nginx rejects, and both of those are
// worse than the bug being fixed: they take every other site on the machine down
// to repair one host.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// escalateBlock lifts probe_challenge, probe_https, probe_settles and
// force_apply verbatim out of the shell helper. They are contiguous, so one
// extraction keeps them in the order the script defines them.
func escalateBlock(t *testing.T) string {
	t.Helper()
	src := readSourceFile(t, "../../scripts/setup-vayudomain.sh")
	i := strings.Index(src, "# probe_challenge <host>")
	j := strings.Index(src, "reload_ok() {")
	if i < 0 || j < 0 || j < i {
		t.Fatal("the reload-escalation ladder is gone from setup-vayudomain.sh, so a reload " +
			"that reports success without taking effect is once again undetectable and " +
			"unrepairable from the panel")
	}
	// nginx_ok is defined further up and is what gates the heavy rung. Take the
	// real one: a harness stand-in would let a ladder that skips the config test
	// pass this file.
	k := strings.Index(src, "nginx_ok() {")
	e := strings.Index(src[k:], "\n}\n")
	if k < 0 || e < 0 {
		t.Fatal("nginx_ok is gone from setup-vayudomain.sh")
	}
	return src[k:k+e] + "\n}\n" + src[i:j]
}

// escalateEnv is one scripted machine for the ladder to act on.
type escalateEnv struct {
	// servesAfter is the rung at which this server starts answering for the
	// host: "signal", "restart", or "never".
	servesAfter string
	// configOK is whether `nginx -t` passes.
	configOK bool
	// restartKills makes the restart leave nginx down, which is the outcome that
	// must never be reported as anything but serious.
	restartKills bool
}

// runEscalate executes force_apply against a stubbed machine and returns its
// output, the commands the ladder actually ran, and whether it reported success.
func runEscalate(t *testing.T, env escalateEnv) (out string, ran []string, ok bool) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(filepath.Join(dir, ".well-known", "acme-challenge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"bash", "cat", "rm", "mkdir"} {
		p, err := exec.LookPath(b)
		if err != nil {
			t.Skipf("%s is not available in this environment", b)
		}
		if err := os.Symlink(p, filepath.Join(bin, b)); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "ran")
	state := filepath.Join(dir, "serving") // non-empty once the server answers
	active := filepath.Join(dir, "active") // "down" once a restart has killed it

	tOK := "0"
	if env.configOK {
		tOK = "1"
	}
	kills := "0"
	if env.restartKills {
		kills = "1"
	}

	stub := func(name, body string) {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // a test stub on PATH must be executable
			t.Fatal(err)
		}
	}
	// Records every invocation, so the ORDER of the rungs is assertable rather
	// than inferred from the prose the ladder prints.
	stub("nginx", `echo "nginx $*" >> `+log+`
case "$*" in
  *-t*) [ "`+tOK+`" = "1" ] && exit 0; echo "nginx: [emerg] invalid configuration" >&2; exit 1 ;;
  *"-s reload"*) [ "`+env.servesAfter+`" = "signal" ] && echo yes > `+state+`; exit 0 ;;
esac
exit 0`)
	stub("systemctl", `echo "systemctl $*" >> `+log+`
case "$1" in
  restart) [ "`+env.servesAfter+`" = "restart" ] && echo yes > `+state+`
           [ "`+kills+`" = "1" ] && echo down > `+active+`
           exit 0 ;;
  is-active) [ "$(cat `+active+` 2>/dev/null)" = "down" ] && exit 3; exit 0 ;;
esac
exit 0`)
	// Answers the challenge only once the server is "serving" — the one thing the
	// ladder is allowed to believe.
	stub("curl", `url="${@: -1}"; name="${url##*/}"
[ -s `+state+` ] || exit 22
cat "`+dir+`/.well-known/acme-challenge/$name" 2>/dev/null || exit 22`)
	stub("pgrep", `[ "$(cat `+active+` 2>/dev/null)" = "down" ] && exit 1; exit 0`)
	// The settle delay is not what is under test, and five real seconds per rung
	// makes this file slow enough that people stop running it.
	stub("sleep", `exit 0`)

	harness := "set -u\n" +
		"CACHE_DIR=" + dir + "\n" +
		"ok(){ echo \"OK $*\"; }; info(){ echo \"INFO $*\"; }; warn(){ echo \"WARN $*\"; }\n" +
		escalateBlock(t) +
		"\nif force_apply probe_challenge shop.example; then echo VERDICT_SERVING; " +
		"else echo VERDICT_STILL_DOWN; fi\n"
	script := filepath.Join(dir, "esc.sh")
	if err := os.WriteFile(script, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(bin, "bash"), script)
	cmd.Env = []string{"PATH=" + bin}
	b, _ := cmd.CombinedOutput()
	rb, _ := os.ReadFile(log) //nolint:errcheck // an empty log is a real result: nothing ran
	for _, l := range strings.Split(strings.TrimSpace(string(rb)), "\n") {
		if l != "" {
			ran = append(ran, l)
		}
	}
	return string(b), ran, strings.Contains(string(b), "VERDICT_SERVING")
}

func joined(ran []string) string { return strings.Join(ran, " | ") }

// THE CASE THE LADDER EXISTS FOR. systemctl reload returned 0 and the server
// does not answer. `nginx -s reload` signals the master through its pid file
// with systemd out of the path entirely — a genuinely different mechanism, and
// one reload_ok never reaches, because it only falls back when systemctl FAILS.
//
// And having worked, it must stop. A ladder that restarts anyway interrupts
// every site on the machine to repeat something that already succeeded.
func TestTheDirectSignalIsTriedBeforeAnyRestartAndEndsTheLadder(t *testing.T) {
	out, ran, ok := runEscalate(t, escalateEnv{servesAfter: "signal", configOK: true})
	if !ok {
		t.Fatalf("the direct signal fixed the server and the ladder still reported failure:\n%s", out)
	}
	if !strings.Contains(joined(ran), "nginx -s reload") {
		t.Fatalf("`nginx -s reload` was never tried, so the only mechanism that does not go "+
			"through systemd is unreachable — and systemd returning 0 is the bug:\n%s", joined(ran))
	}
	if strings.Contains(joined(ran), "systemctl restart") {
		t.Errorf("nginx was restarted after the direct signal had already worked; that is a "+
			"machine-wide interruption for nothing:\n%s", joined(ran))
	}
}

// When the master will not re-read no matter how it is asked, replace it. A
// reload is a request to a process that may itself be the broken thing; a
// restart does not ask. It must come SECOND, and it must actually happen.
func TestTheRestartRungRunsWhenTheSignalDoesNotTakeEffect(t *testing.T) {
	out, ran, ok := runEscalate(t, escalateEnv{servesAfter: "restart", configOK: true})
	if !ok {
		t.Fatalf("the restart fixed the server and the ladder still reported failure:\n%s", out)
	}
	j := joined(ran)
	si, ri := strings.Index(j, "nginx -s reload"), strings.Index(j, "systemctl restart")
	if ri < 0 {
		t.Fatalf("nginx was never restarted, so a master that ignores the signal is unrepairable "+
			"from the panel — which is the whole complaint:\n%s", j)
	}
	if si < 0 || si > ri {
		t.Errorf("the heavy rung ran before the free one; the ordering is the point:\n%s", j)
	}
}

// NEVER restart onto a configuration nginx rejects. `nginx -t` passing is the
// only reason the restart is safe: it guarantees nginx comes back. Without this
// gate the ladder would take every site on the machine down to fix one host,
// which is strictly worse than the missing certificate it was sent to repair.
func TestAnInvalidConfigurationIsNeverRestartedOnto(t *testing.T) {
	out, ran, ok := runEscalate(t, escalateEnv{servesAfter: "restart", configOK: false})
	if ok {
		t.Fatalf("the ladder claimed success against a configuration nginx rejects:\n%s", out)
	}
	j := joined(ran)
	if strings.Contains(j, "systemctl restart") {
		t.Fatalf("nginx was RESTARTED onto a configuration it had just rejected — it would not "+
			"come back, and every other site on this machine goes with it:\n%s", j)
	}
	if strings.Contains(j, "-s reload") {
		t.Errorf("the invalid configuration was signalled into the running master:\n%s", j)
	}
}

// A restart that leaves nginx DOWN is the one outcome that must never be
// narrated calmly. At that point no site on the machine is being served, which
// is a bigger problem than the one host that started this, and the ladder must
// say so and report failure rather than fall through to certbot.
func TestARestartThatLeavesNginxDownIsReportedAsSerious(t *testing.T) {
	out, _, ok := runEscalate(t, escalateEnv{
		servesAfter: "never", configOK: true, restartKills: true})
	if ok {
		t.Fatalf("the ladder reported success with nginx not running:\n%s", out)
	}
	if !strings.Contains(out, "no site on this machine is being served") {
		t.Errorf("nginx is down and the output does not say the blast radius is now every "+
			"site, so the operator reads it as one host still missing a certificate:\n%s", out)
	}
}

// AUDIT FINDING. Both probes return success when curl is absent, because "the
// check could not run" must not be read as "the server said no" — that guard is
// correct, and it exists because its absence once skipped certbot for every host
// on a box without curl.
//
// The ladder verifies by probing, so with no probe every rung passes vacuously:
// force_apply would announce that the direct signal took effect, restart
// nothing, repair nothing, and hand back success. A ladder that cannot see is
// worse than no ladder, because the panel then reports a host as served.
func TestTheLadderRefusesToRunWhenItCannotVerifyAnything(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Everything the ladder needs EXCEPT curl. Withholding it is the variable
	// under test and cannot be simulated with the system PATH in place.
	for _, b := range []string{"bash", "cat", "rm"} {
		p, err := exec.LookPath(b)
		if err != nil {
			t.Skipf("%s is not available in this environment", b)
		}
		if err := os.Symlink(p, filepath.Join(bin, b)); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "ran")
	for _, n := range []string{"nginx", "systemctl", "pgrep", "sleep"} {
		body := "#!/usr/bin/env bash\necho \"" + n + " $*\" >> " + log + "\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, n), []byte(body), 0o755); err != nil { //nolint:gosec // a test stub on PATH must be executable
			t.Fatal(err)
		}
	}
	harness := "set -u\nCACHE_DIR=" + dir + "\n" +
		"ok(){ echo \"OK $*\"; }; info(){ echo \"INFO $*\"; }; warn(){ echo \"WARN $*\"; }\n" +
		escalateBlock(t) +
		"\nif force_apply probe_challenge shop.example; then echo VERDICT_SERVING; " +
		"else echo VERDICT_STILL_DOWN; fi\n"
	script := filepath.Join(dir, "esc.sh")
	if err := os.WriteFile(script, []byte(harness), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(bin, "bash"), script)
	cmd.Env = []string{"PATH=" + bin}
	b, _ := cmd.CombinedOutput()
	out := string(b)

	if strings.Contains(out, "VERDICT_SERVING") {
		t.Fatalf("with no way to probe, the ladder reported the host repaired — the panel would "+
			"show it served and the operator would be sent looking anywhere but here:\n%s", out)
	}
	if strings.Contains(out, "took effect") {
		t.Errorf("the ladder claimed a rung took effect on a check that never ran:\n%s", out)
	}
	rb, _ := os.ReadFile(log) //nolint:errcheck // an empty log is the expected result
	if strings.Contains(string(rb), "restart") {
		t.Errorf("nginx was restarted with no way to tell whether it helped:\n%s", rb)
	}
	if !strings.Contains(out, "curl is not installed") {
		t.Errorf("the refusal does not say why, so it reads as an unexplained failure:\n%s", out)
	}
}

// And when nothing works, say nothing worked. The ladder returning success on a
// server that still does not answer would send certbot at a validation that
// cannot pass, spending rate-limited attempts to learn what was already known.
func TestAServerThatNeverAnswersIsNotReportedAsRepaired(t *testing.T) {
	out, ran, ok := runEscalate(t, escalateEnv{servesAfter: "never", configOK: true})
	if ok {
		t.Fatalf("the ladder reported the host repaired while the probe never passed:\n%s", out)
	}
	// Both rungs must genuinely have been attempted before giving up.
	for _, want := range []string{"-s reload", "systemctl restart"} {
		if !strings.Contains(joined(ran), want) {
			t.Errorf("gave up without trying %q:\n%s", want, joined(ran))
		}
	}
}
