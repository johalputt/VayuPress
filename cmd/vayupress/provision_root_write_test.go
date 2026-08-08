// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// SECTION 6 AUDIT — root writing into a directory the unprivileged side owns.
//
// admin_os_provision.go opens by stating the boundary, and it is right about the
// half it describes:
//
//	HOW THE PRIVILEGE BOUNDARY IS CROSSED
//	It is not crossed. This code creates an EMPTY file. […] there is no channel
//	through which a compromised web session could influence what root executes.
//
// What root EXECUTES, yes. Where root WRITES, no.
//
// /var/lib/vayupress is the service's StateDirectory and is `chown -R
// www-data:www-data` by the installer, because the panel must create its request
// there. The root worker then puts its own output in that same directory:
//
//	scripts/provision-subdomains.sh:251,346  cat > "$RESULT"
//	scripts/provision-subdomains.sh:301      >>"${STATE_DIR}/provision.log"
//	scripts/provision-subdomains.sh:50       exec 9>"$LOCK"
//
// A shell redirect follows symlinks. Owning the directory is enough to replace
// any of those names with a link, so in the attacker's voice:
//
//	I do not need to change what your root script runs. I replace
//	provision.result with a symlink and wait — your daily timer is enough. Root
//	opens my link, follows it, and truncates whatever I pointed it at. I choose
//	the path; root does the writing.
//
// This is verified below against real bash rather than asserted, and it needs no
// race: the link is planted once and root writes on the next run. Nor does
// fs.protected_symlinks help — that only covers world-writable STICKY
// directories such as /tmp, and this one is 0755 owned by www-data.
//
// The fix is the only one that actually holds: root's outputs move out of the
// unprivileged directory entirely. Refusing to follow a link — `rm -f` and then
// write — leaves a window between the unlink and the open, and a partial fix to
// a privilege boundary is the kind that reads as done.

// provisionScriptPaths evaluates the worker's own path expressions in bash and
// returns them, so this gate reasons about what the script really computes
// rather than about the text that computes it.
func provisionScriptPaths(t *testing.T) map[string]string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "scripts", "provision-subdomains.sh"))
	if err != nil {
		t.Fatalf("read the root worker: %v", err)
	}

	// Only the assignments, evaluated in isolation: running the worker itself
	// needs root, nginx and certbot.
	var assigns []string
	for _, line := range strings.Split(string(src), "\n") {
		s := strings.TrimSpace(line)
		for _, name := range []string{"STATE_DIR=", "RESULT=", "LOCK=", "LOG=", "OUT_DIR=", "REQUEST="} {
			if strings.HasPrefix(s, name) {
				assigns = append(assigns, s)
			}
		}
	}
	script := strings.Join(assigns, "\n") + "\n" +
		`printf 'STATE_DIR=%s\nRESULT=%s\nLOCK=%s\nLOG=%s\nREQUEST=%s\nOUT_DIR=%s\n' ` +
		`"$STATE_DIR" "$RESULT" "$LOCK" "${LOG:-}" "$REQUEST" "${OUT_DIR:-}"`

	out, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("evaluate the worker's path assignments: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

// THE CONTROL. Nothing root writes may live where the unprivileged process can
// replace the name with a symlink.
func TestRootNeverWritesInsideTheUnprivilegedStateDirectory(t *testing.T) {
	p := provisionScriptPaths(t)
	state := p["STATE_DIR"]
	if state == "" {
		t.Fatal("the worker no longer defines STATE_DIR; this gate cannot locate the " +
			"unprivileged directory")
	}
	// The request is the one thing that BELONGS there — the panel writes it, and
	// root only ever unlinks it. Everything else root creates must be elsewhere.
	if req := p["REQUEST"]; !strings.HasPrefix(req, state) {
		t.Errorf("REQUEST=%q is not under STATE_DIR=%q; the panel and the unit would be "+
			"writing and watching different files", req, state)
	}

	// LOG included by evaluated path, not only by the literal redirect below:
	// mutation showed that moving the assignment back into STATE_DIR while the
	// redirect still read "$LOG" slipped past a text-only check.
	for _, name := range []string{"RESULT", "LOCK", "LOG"} {
		v := p[name]
		if v == "" {
			t.Errorf("the worker no longer defines %s", name)
			continue
		}
		if strings.HasPrefix(v, state+"/") {
			t.Errorf("root writes %s=%q, inside %q, which the installer gives to www-data\n"+
				"(`chown -R www-data:www-data`) because the panel must create its request there.\n\n"+
				"Owning that directory is enough to replace the name with a symlink, and a shell\n"+
				"redirect follows it: root then truncates and writes whatever the unprivileged\n"+
				"side chose. No race is needed — the daily timer will do it.", name, v, state)
		}
	}

	// The log is appended to by root on every run, so it is the same hazard.
	src, err := os.ReadFile(filepath.Join("..", "..", "scripts", "provision-subdomains.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `>>"${STATE_DIR}/provision.log"`) {
		t.Error(`root appends to "${STATE_DIR}/provision.log" with a shell redirect.` + "\n\n" +
			"Append follows a symlink exactly as truncation does, and the content includes " +
			"helper output — so the unprivileged side chooses a target and root adds text to it.")
	}
}

// THE HAZARD, demonstrated with real bash rather than described, so the rule
// above is anchored to behaviour and not to my say-so.
func TestAShellRedirectFollowsAPlantedSymlink(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "provision.result")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// Byte-for-byte the worker's own idiom.
	cmd := exec.Command("bash", "-c", `umask 022; cat > "$1" <<JSON
{"ran":1}
JSON`, "bash", link)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "ORIGINAL" {
		t.Skip("this shell does not follow symlinks on redirect; the rule above is then " +
			"belt-and-braces on this platform rather than load-bearing")
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the redirect replaced the link instead of following it (%v)", err)
	}
}

// A REGRESSION THIS AUDIT INTRODUCED, caught by attacking its own fix.
//
// Moving the lock out of the state directory made it depend on a directory that
// must first be created. If that creation fails, `exec 9>"$LOCK"` fails, `flock
// -n 9` then errors on a bad file descriptor, and the worker takes the branch
// that exists for a DIFFERENT reason: it logs "another provisioning run is in
// progress" and exits 0.
//
// Nothing is wrong with the lock; nothing else is running. Provisioning simply
// stops for ever, announcing a healthy state while it does. Before the move the
// lock lived in the systemd StateDirectory, which always exists, so this could
// not happen — a fix that broke something that worked, which is the failure mode
// the standing rules name first.
//
// The two conditions must be told apart and neither may be silent.
func TestTheWorkerDoesNotBlameAConcurrentRunForAMissingDirectory(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "scripts", "provision-subdomains.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// Everything up to the point the request is consumed: assignments, the
	// directory, the lock. Running further needs root, nginx and certbot.
	lines := strings.Split(string(src), "\n")
	end := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), `rm -f "$REQUEST"`) {
			end = i
			break
		}
	}
	if end < 0 {
		t.Fatal("the worker no longer consumes the request with rm -f; this gate cannot " +
			"find the end of the preamble")
	}
	preamble := strings.Join(lines[:end], "\n")

	// A path that cannot be created: its parent is a regular file. Portable, and
	// it fails for an unprivileged CI runner exactly as it would for root.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(preamble, "OUT_DIR=/var/lib/vayupress-provision",
		"OUT_DIR="+filepath.Join(blocker, "out"))

	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()

	if strings.Contains(string(out), "another provisioning run is in progress") {
		t.Errorf("the worker blamed a concurrent run for a directory it could not create.\n\n"+
			"Nothing else is running. Provisioning stops for ever and the log says everything\n"+
			"is fine — the operator has no reason to look, and no reason to look HERE.\n\noutput:\n%s", out)
	}
	if err == nil {
		t.Errorf("the worker continued after failing to create the directory holding its\n"+
			"lock and its result.\n\nWithout the lock, two certbot runs can overlap, which is\n"+
			"how rate limits get burned — the exact thing the lock exists to prevent.\n\noutput:\n%s", out)
	}
	// Asserted on the SPECIFIC reason, not merely on failing. Two guards stand
	// here — the directory check and the lock-open check — and either alone
	// produces a non-zero exit, so an outcome-only assertion cannot tell which
	// one fired and kills neither under mutation. The operator needs the one that
	// names the directory.
	if !strings.Contains(string(out), "could not create") {
		t.Errorf("the failure is reported without naming the directory that could not be\n"+
			"created, so the operator is told the lock is the problem when the directory is.\n\noutput:\n%s", out)
	}
	// And it stops THERE. Falling through to the lock guard would print a second
	// fatal about the lock, which is true but is not the cause, and an operator
	// reading two fatals chases the wrong one first.
	if strings.Contains(string(out), "could not open the run lock") {
		t.Errorf("the directory failure fell through to the lock guard and reported both.\n\n"+
			"The lock is unopenable BECAUSE the directory is missing; leading with that sends\n"+
			"the operator to the wrong place.\n\noutput:\n%s", out)
	}
}

// COVERAGE NOTE, stated rather than left implied: the second guard — the one
// that fires when OUT_DIR exists but the lock still cannot be opened — is not
// driven here. Reaching it needs `install -d -o root -g root` to SUCCEED, which
// it does for root and not for an unprivileged CI runner, and a harness that
// behaves differently in the two places is how a broken test passes locally.
// It stays because its message is the right one for a read-only filesystem or a
// lock path that is not a regular file; it is defence in depth, and it is not
// proven by this suite.
