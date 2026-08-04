// SPDX-License-Identifier: Apache-2.0

package main

// binary_health_test.go — the root agent's recovery from a binary that is not a
// program, extracted from deploy/vayushield-agent.sh and RUN.
//
// The failure it exists for: the in-app updater selected the wrong release asset
// and wrote a ZIP archive over /var/lib/vayupress/bin/vayupress. Every check the
// updater made passed — the bytes were exactly what the release published — and
// systemd then could not exec the result. The site returned 502 until an
// operator opened an SSH session and ran cp and systemctl by hand, on a product
// whose whole premise is that an install is operated from the panel.
//
// These cases are run rather than asserted against as text, because the two
// dangerous behaviours here are both about what the agent does NOT do, and no
// amount of reading the source proves a shell function declines to act. In
// particular: an install that is down for a CONFIG error must be left alone.
// Restoring a backup there would hide the real fault and silently downgrade the
// binary, turning a legible error into a mystery.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binaryHealthBlock lifts the real functions out of the agent script.
func binaryHealthBlock(t *testing.T) string {
	t.Helper()
	src := readSourceFile(t, "../../deploy/vayushield-agent.sh")

	states := extractBetween(t, src, "write_state() {", "\nwrite_reason() {",
		"write_state is gone from the agent, so the panel has no way to learn that a binary was restored")
	clear := extractBetween(t, src, "clear_reason() {", "\n}\n",
		"clear_reason is gone from the agent")
	health := extractBetween(t, src, "vayupress_unit_binary() {", "\nreconcile_provisionhelpers() {",
		"the binary-health recovery is gone from the agent, so an install whose binary is not a "+
			"program stays down until somebody with SSH notices")

	if !strings.Contains(health, "reconcile_binaryhealth() {") {
		t.Fatal("reconcile_binaryhealth is gone — nothing repairs a bricked binary any more")
	}
	// Newlines between the chunks, not concatenation: the block above write_reason
	// ends in a comment, and gluing the next function's header onto it made that
	// function's body run at the top level instead of being defined. The harness
	// then failed for a reason that had nothing to do with the code under test.
	return states + "\n" + clear + "\n}\n" + health
}

func extractBetween(t *testing.T, src, from, to, why string) string {
	t.Helper()
	i := strings.Index(src, from)
	if i < 0 {
		t.Fatal(why)
	}
	j := strings.Index(src[i:], to)
	if j < 0 {
		t.Fatal(why)
	}
	return src[i : i+j]
}

type healthScenario struct {
	binary    []byte // contents written to the unit's binary path
	backup    []byte // contents of <binary>.bak; nil means no backup file
	svcActive bool   // whether systemctl reports the unit as running
}

type healthResult struct {
	binaryAfter string // what sits at the binary path when the run finishes
	state       string // <control>/binhealth.state
	reason      string // <control>/binhealth.reason
	systemctl   string // every systemctl verb the agent invoked
	stdout      string
}

const fakeELF = "\x7fELF\x02\x01\x01\x00 a real vayupress binary"
const fakeZIP = "PK\x03\x04\x14\x00 the packaged marketing website"

func runBinaryHealth(t *testing.T, sc healthScenario) healthResult {
	t.Helper()
	dir := t.TempDir()
	control := filepath.Join(dir, "control")
	binDir := filepath.Join(dir, "bin")
	stub := filepath.Join(dir, "stub")
	for _, d := range []string{control, binDir, stub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Only real externals the block uses. head/od/tr are how it reads the magic
	// bytes; a missing one would silently make every file look non-executable.
	for _, b := range []string{"bash", "head", "od", "tr", "sed", "cp", "chmod", "printf", "echo", "rm"} {
		p, err := exec.LookPath(b)
		if err != nil {
			continue // shell builtins (printf/echo) need no symlink
		}
		_ = os.Symlink(p, filepath.Join(stub, b))
	}

	binPath := filepath.Join(binDir, "vayupress")
	if err := os.WriteFile(binPath, sc.binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if sc.backup != nil {
		if err := os.WriteFile(binPath+".bak", sc.backup, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	sysLog := filepath.Join(dir, "systemctl.log")
	active := "1"
	if sc.svcActive {
		active = "0"
	}
	// A systemctl stub that answers the way the real one does, including the
	// exact `show -p ExecStart --value` shape — the path is parsed out of it, and
	// a stub that returned a bare path would prove nothing about that parsing.
	systemctlStub := "#!/usr/bin/env bash\n" +
		"echo \"$*\" >> " + sysLog + "\n" +
		"case \"$*\" in\n" +
		"  'list-unit-files vayupress.service') exit 0 ;;\n" +
		"  'is-active --quiet vayupress') exit " + active + " ;;\n" +
		"  'show -p ExecStart --value vayupress')\n" +
		"     echo \"{ path=" + binPath + " ; argv[]=" + binPath + " serve ; ignore_errors=no ; start_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }\"\n" +
		"     exit 0 ;;\n" +
		"esac\nexit 0\n"
	sp := filepath.Join(stub, "systemctl")
	if err := os.WriteFile(sp, []byte(systemctlStub), 0o755); err != nil {
		t.Fatal(err)
	}

	script := "set -uo pipefail\n" +
		"CONTROL_DIR=" + control + "\n" +
		binaryHealthBlock(t) + "\n" +
		"reconcile_binaryhealth\n"

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+stub)
	out, _ := cmd.CombinedOutput()

	read := func(n string) string {
		b, _ := os.ReadFile(filepath.Join(control, n))
		return strings.TrimSpace(string(b))
	}
	after, _ := os.ReadFile(binPath)
	sysBytes, _ := os.ReadFile(sysLog)
	return healthResult{
		binaryAfter: string(after),
		state:       read("binhealth.state"),
		reason:      read("binhealth.reason"),
		systemctl:   string(sysBytes),
		stdout:      string(out),
	}
}

// The outage, replayed on a machine: a zip at the binary path, the service down,
// a good backup beside it. Nobody should have to log in for this.
func TestABrickedBinaryIsRestoredWithoutAnSSHSession(t *testing.T) {
	r := runBinaryHealth(t, healthScenario{binary: []byte(fakeZIP), backup: []byte(fakeELF), svcActive: false})

	if r.binaryAfter != fakeELF {
		t.Fatalf("the binary was not restored — it still holds %q. The site stays 502 until "+
			"an operator finds an SSH client.\nagent output:\n%s", firstBytes(r.binaryAfter), r.stdout)
	}
	if !strings.Contains(r.systemctl, "restart vayupress") {
		t.Errorf("the binary was restored but the service was never restarted, so nothing changed "+
			"for a visitor.\nsystemctl calls:\n%s", r.systemctl)
	}
	if !strings.Contains(r.systemctl, "reset-failed vayupress") {
		t.Errorf("no reset-failed, so systemd's start-limit can refuse the restart after a "+
			"crash loop.\nsystemctl calls:\n%s", r.systemctl)
	}
	if r.state != "restored" {
		t.Errorf("binhealth.state = %q, want \"restored\" — the panel cannot report what happened", r.state)
	}
	if !strings.Contains(r.reason, "not an executable") {
		t.Errorf("binhealth.reason = %q — it must say what was wrong, or the operator learns "+
			"nothing about why their install restarted", r.reason)
	}
}

// The dangerous case, and the reason this is a test and not a code review. An
// install can be down for a hundred reasons that have nothing to do with the
// binary — a bad config, a locked database, a port already bound. Restoring a
// backup there hides a legible error AND silently downgrades the install.
func TestAWorkingBinaryIsNeverReplacedWhenTheServiceIsDownForSomeOtherReason(t *testing.T) {
	current := fakeELF + " — version 3.17.0, freshly installed"
	r := runBinaryHealth(t, healthScenario{
		binary:    []byte(current),
		backup:    []byte(fakeELF + " — version 3.16.84, the old one"),
		svcActive: false,
	})

	if r.binaryAfter != current {
		t.Fatalf("a perfectly good binary was rolled back because the service happened to be "+
			"down. The operator's real fault is now hidden AND they are running an older "+
			"version than they installed.\nbinary now: %q", firstBytes(r.binaryAfter))
	}
	if strings.Contains(r.systemctl, "restart vayupress") {
		t.Error("the agent restarted a service it had no business touching")
	}
}

// A healthy install must not be inspected, restored, or restarted — whatever
// else is true about the files on disk.
func TestAHealthyInstallIsLeftCompletelyAlone(t *testing.T) {
	r := runBinaryHealth(t, healthScenario{binary: []byte(fakeELF), backup: []byte(fakeELF), svcActive: true})
	if strings.Contains(r.systemctl, "restart vayupress") {
		t.Error("a running install was restarted")
	}
	if r.state != "ok" {
		t.Errorf("binhealth.state = %q, want \"ok\"", r.state)
	}
}

// The state that proves the running check happens FIRST, and it is not
// hypothetical: it is the window every bad update opens. The updater replaces
// the file on disk and does not restart, so the process keeps serving from the
// old, now-unlinked inode while the path holds a zip. The site is up.
//
// Restarting it here would take a working site down in order to fix a file — the
// agent turning itself into the outage. It waits for the service to be down,
// which it will be at the next restart, and repairs it then.
func TestALiveSiteIsNotRestartedJustBecauseTheFileOnDiskIsBad(t *testing.T) {
	r := runBinaryHealth(t, healthScenario{binary: []byte(fakeZIP), backup: []byte(fakeELF), svcActive: true})
	if strings.Contains(r.systemctl, "restart vayupress") {
		t.Fatalf("the agent restarted a site that was serving traffic, to repair a file that was "+
			"not stopping it. That is an outage caused by the recovery.\nsystemctl calls:\n%s", r.systemctl)
	}
	if r.binaryAfter != fakeZIP {
		t.Error("the binary path was rewritten under a running service")
	}
}

// No usable backup: the agent must say so plainly and must NOT write garbage
// over the binary path trying to fix it.
func TestWithoutAUsableBackupItReportsRatherThanGuesses(t *testing.T) {
	for _, c := range []struct {
		name   string
		backup []byte
	}{
		{"no backup file at all", nil},
		{"the backup is also not a program", []byte(fakeZIP)},
	} {
		r := runBinaryHealth(t, healthScenario{binary: []byte(fakeZIP), backup: c.backup, svcActive: false})
		if r.binaryAfter != fakeZIP {
			t.Errorf("%s: the binary path was written to anyway (now %q)", c.name, firstBytes(r.binaryAfter))
		}
		if r.state != "unrecoverable" {
			t.Errorf("%s: binhealth.state = %q, want \"unrecoverable\" — the panel must be able to "+
				"tell the operator this one needs them", c.name, r.state)
		}
		if !strings.Contains(r.reason, "backup") {
			t.Errorf("%s: binhealth.reason = %q does not mention the missing backup", c.name, r.reason)
		}
	}
}

// It runs every minute forever. If a restore that does not fix the service
// re-triggers, the agent turns one bad update into a restart loop.
func TestItCannotTurnIntoARestartLoop(t *testing.T) {
	// After a restore the binary IS a program, so the second pass must decline
	// even though the service is still reported as down.
	r := runBinaryHealth(t, healthScenario{binary: []byte(fakeELF), backup: []byte(fakeELF), svcActive: false})
	if strings.Contains(r.systemctl, "restart vayupress") {
		t.Fatalf("a second pass restarted the service again — an install that stays down for an "+
			"unrelated reason would be restarted every minute forever.\nsystemctl calls:\n%s", r.systemctl)
	}
}

func firstBytes(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

// ── What the panel says about it ──────────────────────────────────────────────
//
// The recovery happens while the panel is down, so the notice afterwards is the
// only account the operator gets. It has to be accurate in both directions: it
// must appear when something happened, and it must NOT appear on the millions of
// installs where nothing ever has.

func TestTheRepairNoticeSaysNothingOnAnInstallThatWasNeverRepaired(t *testing.T) {
	t.Setenv("VAYUSHIELD_CONTROL_DIR", t.TempDir())
	if got := binaryRepairNotice(); got != "" {
		t.Fatalf("a healthy install is being told its binary was replaced: %q", got)
	}
}

func TestTheRepairNoticeReportsWhatTheAgentActuallyDid(t *testing.T) {
	for _, c := range []struct {
		state, reason string
		mustSay       []string
		mustNotSay    string
	}{
		{
			state:  "restored",
			reason: "the binary was not an executable (a failed update); the previous binary was restored",
			// The version on the page is the RESTORED one, not the one they clicked
			// Update to install. Not saying so leaves them reading a number they did
			// not choose and concluding the update simply did nothing.
			mustSay:    []string{"repaired itself", "previous binary was restored", "version shown above is the restored one"},
			mustNotSay: "sudo",
		},
		{
			state:      "unrecoverable",
			reason:     "/var/lib/vayupress/bin/vayupress is not an executable and there is no usable backup",
			mustSay:    []string{"not usable", "could not be repaired automatically", "no usable backup"},
			mustNotSay: "sudo",
		},
	} {
		dir := t.TempDir()
		t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "binhealth.state"), []byte(c.state), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "binhealth.reason"), []byte(c.reason), 0o644); err != nil {
			t.Fatal(err)
		}
		got := binaryRepairNotice()
		for _, want := range c.mustSay {
			if !strings.Contains(got, want) {
				t.Errorf("state %q: the notice never says %q, so the operator cannot tell what happened.\ngot: %s",
					c.state, want, got)
			}
		}
		// The standing rule: the panel reports, it does not hand out shell commands.
		if strings.Contains(strings.ToLower(got), c.mustNotSay) {
			t.Errorf("state %q: the notice tells the operator to open a terminal", c.state)
		}
	}
}

// The reason string comes from a root-owned file on disk, but it is rendered
// into the page — so it goes through escaping like anything else.
func TestTheRepairNoticeEscapesTheReasonItWasGiven(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	_ = os.WriteFile(filepath.Join(dir, "binhealth.state"), []byte("restored"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "binhealth.reason"), []byte(`<img src=x onerror=alert(1)>`), 0o644)
	got := binaryRepairNotice()
	if strings.Contains(got, "<img") {
		t.Fatalf("the reason was interpolated into the page unescaped: %s", got)
	}
	if !strings.Contains(got, "&lt;img") {
		t.Fatalf("the reason is missing entirely rather than escaped: %s", got)
	}
}
