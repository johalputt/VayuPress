// SPDX-License-Identifier: Apache-2.0

package main

// nginx_stray_config_test.go — a backup must never be live configuration.
//
// THE INCIDENT, from a live install.
//
// `ls -la /etc/nginx/sites-enabled/` on a box that had been 502ing during every
// certificate provisioning run:
//
//	lrwxrwxrwx  vayupress-mcp -> /etc/nginx/sites-available/vayupress-mcp
//	-rw-r--r--  vayupress-mcp.vayushield.bak          <-- a REGULAR FILE
//
// Every other entry was a symlink. That one was a backup, written by
// vayushield-agent.sh three days earlier while hardening the MCP vhost — and
// nginx includes that directory with `include /etc/nginx/sites-enabled/*;`,
// which has no extension filter. So the backup had been parsed as live
// configuration on every reload since the moment it was created.
//
// The visible symptom was one line, at warn level, in a file nobody reads:
//
//	[warn] conflicting server name "mcp.johal.in" on 0.0.0.0:443, ignored
//
// nginx keeps whichever server block the glob reached first and silently
// discards the other. Which one loses depends on filename ordering.
//
// These tests EXECUTE the shell functions against a fixture directory. A test
// that reads the script for a string would pass on a script that contains the
// right words and does the wrong thing, which is exactly how a backup ended up
// in an include path in the first place.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runAgentFunc sources vayushield-agent.sh's definitions and runs one call
// against a fixture, without starting its reconcile loop.
//
// The script guards its own main loop behind a run-as-main check; sourcing it
// with VAYUSHIELD_TEST_SOURCE_ONLY set gives us the functions alone.
func runAgentFunc(t *testing.T, env map[string]string, call string) (string, error) {
	t.Helper()
	script := filepath.Clean("../../deploy/vayushield-agent.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("agent script not readable: %v", err)
	}
	// Extract just the function definitions and config block: everything up to
	// the main loop. Sourcing the whole file would start the daemon.
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	src := string(b)
	// Take the real configuration lines and the two functions under test —
	// nothing else. Truncating the file at the main loop leaves the enclosing
	// function unterminated, and hand-copying the definitions into the test
	// would mean testing a copy rather than the script that ships.
	var parts []string
	for _, line := range strings.Split(src, "\n") {
		for _, v := range []string{"BACKUP_DIR=", "SITES_ENABLED=", "CONTROL_DIR="} {
			if strings.HasPrefix(line, v) {
				parts = append(parts, line)
			}
		}
	}
	for _, fn := range []string{"ensure_backup_dir", "shield_backup", "sweep_stray_nginx_backups"} {
		body := extractShellFunc(src, fn)
		if body == "" {
			t.Fatalf("%s() is gone from the agent script; this guard is blind", fn)
		}
		parts = append(parts, body)
	}
	src = strings.Join(parts, "\n")

	tmp := t.TempDir()
	harness := filepath.Join(tmp, "harness.sh")
	var sb strings.Builder
	sb.WriteString("#!/usr/bin/env bash\n")
	for k, v := range env {
		sb.WriteString("export " + k + "=" + shQuote(v) + "\n")
	}
	sb.WriteString(src)
	sb.WriteString("\n" + call + "\n")
	if err := os.WriteFile(harness, []byte(sb.String()), 0o700); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	out, err := exec.Command("bash", harness).CombinedOutput()
	return string(out), err
}

// shQuote lives in dep_freshness_test.go; this file reuses it.

// extractShellFunc returns one shell function definition, closing brace included.
func extractShellFunc(src, name string) string {
	marker := "\n" + name + "() {"
	i := strings.Index(src, marker)
	if i < 0 {
		return ""
	}
	var out []string
	for _, l := range strings.Split(src[i+1:], "\n") {
		out = append(out, l)
		if l == "}" {
			return strings.Join(out, "\n")
		}
	}
	return "" // unterminated: better to fail loudly than to run half a function
}

// THE test. A stray backup in sites-enabled is removed, and everything that is
// legitimately there is left alone.
func TestAStrayBackupIsSweptOutOfTheDirectoryNginxLoads(t *testing.T) {
	tmp := t.TempDir()
	sites := filepath.Join(tmp, "sites-enabled")
	avail := filepath.Join(tmp, "sites-available")
	backups := filepath.Join(tmp, "nginx-backups")
	control := filepath.Join(tmp, "control")
	for _, d := range []string{sites, avail, control} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// A real vhost, as a symlink — the normal shape.
	real := filepath.Join(avail, "vayupress-mcp")
	if err := os.WriteFile(real, []byte("server { server_name mcp.example; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(sites, "vayupress-mcp")); err != nil {
		t.Fatal(err)
	}
	// The stray, byte-for-byte the shape found on the live box.
	stray := filepath.Join(sites, "vayupress-mcp.vayushield.bak")
	if err := os.WriteFile(stray, []byte("server { server_name mcp.example; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A regular file that is NOT a backup. Some operators keep a real vhost
	// here rather than a symlink; moving it would take their site down to fix
	// a tidiness problem.
	keep := filepath.Join(sites, "handwritten-vhost")
	if err := os.WriteFile(keep, []byte("server { server_name kept.example; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runAgentFunc(t, map[string]string{
		"VAYUSHIELD_SITES_ENABLED": sites,
		"VAYUSHIELD_BACKUP_DIR":    backups,
		"VAYUSHIELD_CONTROL_DIR":   control,
	}, "sweep_stray_nginx_backups")
	if err != nil {
		t.Fatalf("sweep failed: %v\n%s", err, out)
	}

	if _, err := os.Lstat(stray); !os.IsNotExist(err) {
		t.Errorf("the stray backup is STILL in the directory nginx includes. It is live "+
			"configuration: nginx parses every file in sites-enabled regardless of extension, so "+
			"this is a duplicate server block that silently overrides or is overridden by the real "+
			"vhost. (lstat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(backups, "vayupress-mcp.vayushield.bak")); err != nil {
		t.Errorf("the stray was removed but not preserved; a repair that destroys the operator's "+
			"only copy of a prior config is worse than the problem: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(sites, "vayupress-mcp")); err != nil {
		t.Errorf("the REAL vhost symlink was swept: %v", err)
	}
	if _, err := os.Lstat(keep); err != nil {
		t.Errorf("a hand-written vhost with no backup suffix was swept. A regular file in "+
			"sites-enabled is unusual but not wrong, and moving it takes a site down: %v", err)
	}
}

// Every conventional backup suffix, because the next one will not be `.bak`.
func TestEveryConventionalBackupSuffixIsSwept(t *testing.T) {
	tmp := t.TempDir()
	sites := filepath.Join(tmp, "sites-enabled")
	backups := filepath.Join(tmp, "backups")
	control := filepath.Join(tmp, "control")
	for _, d := range []string{sites, control} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"a.bak", "b.save", "c.orig", "d.dpkg-old", "e.dpkg-dist", "f~"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(sites, n), []byte("server {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runAgentFunc(t, map[string]string{
		"VAYUSHIELD_SITES_ENABLED": sites,
		"VAYUSHIELD_BACKUP_DIR":    backups,
		"VAYUSHIELD_CONTROL_DIR":   control,
	}, "sweep_stray_nginx_backups")
	if err != nil {
		t.Fatalf("sweep failed: %v\n%s", err, out)
	}
	for _, n := range names {
		if _, err := os.Lstat(filepath.Join(sites, n)); !os.IsNotExist(err) {
			t.Errorf("%q survived the sweep and is being parsed as nginx configuration", n)
		}
	}
}

// A symlink is somebody's deliberate choice even with an odd name.
func TestASymlinkIsNeverSweptEvenWithABackupName(t *testing.T) {
	tmp := t.TempDir()
	sites := filepath.Join(tmp, "sites-enabled")
	backups := filepath.Join(tmp, "backups")
	control := filepath.Join(tmp, "control")
	for _, d := range []string{sites, control} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(tmp, "deliberate.conf")
	if err := os.WriteFile(target, []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sites, "operator-choice.bak")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := runAgentFunc(t, map[string]string{
		"VAYUSHIELD_SITES_ENABLED": sites,
		"VAYUSHIELD_BACKUP_DIR":    backups,
		"VAYUSHIELD_CONTROL_DIR":   control,
	}, "sweep_stray_nginx_backups"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("a SYMLINK named .bak was swept. Enabling a vhost is done by symlinking, so this "+
			"is an operator's deliberate act however oddly named, and removing it disables a site: %v", err)
	}
}

// The sweep runs constantly, so it must be silent and cheap when there is
// nothing to do — and must not create anything.
func TestTheSweepIsANoOpOnAHealthyInstall(t *testing.T) {
	tmp := t.TempDir()
	sites := filepath.Join(tmp, "sites-enabled")
	control := filepath.Join(tmp, "control")
	for _, d := range []string{sites, control} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	backups := filepath.Join(tmp, "backups")
	out, err := runAgentFunc(t, map[string]string{
		"VAYUSHIELD_SITES_ENABLED": sites,
		"VAYUSHIELD_BACKUP_DIR":    backups,
		"VAYUSHIELD_CONTROL_DIR":   control,
	}, "sweep_stray_nginx_backups")
	if err != nil {
		t.Fatalf("sweep on a clean install failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) != "0" {
		t.Errorf("a clean install reported %q swept, want 0", strings.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join(control, "nginx_strays_swept")); err == nil {
		t.Error("a clean install wrote the swept marker, so the panel would report a repair that " +
			"never happened")
	}
	if _, err := os.Stat(backups); err == nil {
		t.Error("the sweep created a backup directory with nothing to put in it")
	}
}

// THE other half. shield_backup must write OUTSIDE the include path — this is
// the line that caused the incident, so it is asserted directly.
func TestABackupIsNeverWrittenBesideTheFileItBacksUp(t *testing.T) {
	tmp := t.TempDir()
	sites := filepath.Join(tmp, "sites-enabled")
	backups := filepath.Join(tmp, "backups")
	control := filepath.Join(tmp, "control")
	for _, d := range []string{sites, control} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	victim := filepath.Join(sites, "vayupress-mcp")
	if err := os.WriteFile(victim, []byte("server { server_name mcp.example; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runAgentFunc(t, map[string]string{
		"VAYUSHIELD_SITES_ENABLED": sites,
		"VAYUSHIELD_BACKUP_DIR":    backups,
		"VAYUSHIELD_CONTROL_DIR":   control,
	}, `shield_backup `+shQuote(victim))
	if err != nil {
		t.Fatalf("shield_backup failed: %v\n%s", err, out)
	}

	// Nothing new may appear in the include path.
	ents, err := os.ReadDir(sites)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != "vayupress-mcp" {
			t.Errorf("shield_backup left %q in the directory nginx includes. That file is live "+
				"configuration from the moment it is written — a duplicate server block nginx "+
				"resolves by discarding one of the two, with only a warn-level line to say so.",
				e.Name())
		}
	}
	// And it must actually have preserved the file somewhere useful.
	got, err := os.ReadFile(filepath.Join(backups, "vayupress-mcp.vayushield.bak"))
	if err != nil {
		t.Fatalf("no backup was kept anywhere: %v", err)
	}
	if !strings.Contains(string(got), "mcp.example") {
		t.Errorf("the backup does not contain the original config: %q", got)
	}
	// The returned path is what a caller reports to the operator; it must be real.
	if p := strings.TrimSpace(out); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("shield_backup returned %q, which does not exist: %v", p, err)
		}
	}
}

// ── The reload storm ─────────────────────────────────────────────────────────
//
// From the same install's nginx error log, at the exact minute the provisioning
// run wrote its vhosts:
//
//	09:11:13 [alert] *9550 open socket #17 left in connection 10
//	09:11:13 [alert] aborting
//	09:11:17 [alert] aborting
//	09:11:20 [alert] aborting
//	09:11:31 [alert] aborting
//
// Four worker generations aborted in eighteen seconds. "open socket left in
// connection" then "aborting" is a worker forced to exit while it still holds
// live connections — every request in flight on it dies mid-response, which the
// client and anything in front of it render as 502.
//
// setup-vayudomain.sh reloads twice per host, so six domains is twelve reloads
// plus whatever the other helpers add. These tests run the real drain guard
// against a stubbed `ps`.

// runDomainFunc extracts a function from setup-vayudomain.sh and runs it with a
// caller-supplied prelude (used here to stub `ps` and `sleep`).
func runDomainFunc(t *testing.T, prelude, call string) (string, error) {
	t.Helper()
	script := filepath.Clean("../../scripts/setup-vayudomain.sh")
	b, err := os.ReadFile(script)
	if err != nil {
		t.Skipf("domain script not readable: %v", err)
	}
	body := extractShellFunc(string(b), "await_nginx_drain")
	if body == "" {
		t.Fatal("await_nginx_drain() is gone from setup-vayudomain.sh; the reload storm guard " +
			"has been removed and this test is blind")
	}
	harness := filepath.Join(t.TempDir(), "h.sh")
	src := "#!/usr/bin/env bash\nwarn() { echo \"WARN: $*\"; }\n" + prelude + "\n" + body + "\n" + call + "\n"
	if err := os.WriteFile(harness, []byte(src), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", harness).CombinedOutput()
	return string(out), err
}

// THE test. With a worker still draining, the next reload WAITS.
func TestAReloadWaitsForThePreviousGenerationToDrain(t *testing.T) {
	// `ps` reports a draining worker for the first three calls, then a clean
	// process table — exactly the shape of a generation retiring.
	prelude := `
COUNT_FILE="$(mktemp)"; echo 0 > "$COUNT_FILE"
ps() {
  local n; n=$(cat "$COUNT_FILE"); echo $((n+1)) > "$COUNT_FILE"
  if [ "$n" -lt 3 ]; then
    echo "nginx: worker process is shutting down"
  else
    echo "nginx: worker process"
  fi
}
SLEPT_FILE="$(mktemp)"; echo 0 > "$SLEPT_FILE"
sleep() { local n; n=$(cat "$SLEPT_FILE"); echo $((n+1)) > "$SLEPT_FILE"; }
`
	out, err := runDomainFunc(t, prelude, `await_nginx_drain; echo "slept=$(cat "$SLEPT_FILE")"`)
	if err != nil {
		t.Fatalf("await_nginx_drain failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "slept=3") {
		t.Errorf("the guard did not wait for the draining worker (%s). Reloading over a generation "+
			"that still holds connections is what aborted four workers in eighteen seconds and "+
			"killed every request in flight on them.", strings.TrimSpace(out))
	}
	if strings.Contains(out, "WARN:") {
		t.Errorf("a normal drain produced a warning: %s", out)
	}
}

// On the common case — nothing draining — it must cost nothing at all. A
// single-domain install reloads once and must not be slowed by this.
func TestTheDrainGuardIsFreeWhenNothingIsDraining(t *testing.T) {
	prelude := `
ps() { echo "nginx: master process"; echo "nginx: worker process"; }
SLEPT_FILE="$(mktemp)"; echo 0 > "$SLEPT_FILE"
sleep() { local n; n=$(cat "$SLEPT_FILE"); echo $((n+1)) > "$SLEPT_FILE"; }
`
	out, err := runDomainFunc(t, prelude, `await_nginx_drain; echo "slept=$(cat "$SLEPT_FILE")"`)
	if err != nil {
		t.Fatalf("await_nginx_drain failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "slept=0") {
		t.Errorf("the guard waited with nothing draining (%s); every install would pay for a "+
			"problem only multi-domain provisioning has", strings.TrimSpace(out))
	}
}

// A genuinely long-lived connection must not block provisioning forever — but
// proceeding anyway is the one path that can still drop a request, so it must
// say so rather than passing silently.
func TestAnEndlessDrainProceedsButSaysSo(t *testing.T) {
	prelude := `
export NGINX_DRAIN_MAX_SECONDS=3
ps() { echo "nginx: worker process is shutting down"; }
sleep() { :; }
`
	out, err := runDomainFunc(t, prelude, `await_nginx_drain; echo "rc=$?"`)
	if err != nil {
		t.Fatalf("await_nginx_drain failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rc=0") {
		t.Error("an endless drain blocked provisioning entirely; a download or websocket would " +
			"stop certificates from ever being issued")
	}
	if !strings.Contains(out, "WARN:") {
		t.Error("the guard gave up waiting and said nothing. This is the one remaining path that " +
			"can drop a request, and a silent one is indistinguishable from the original bug.")
	}
}

// The guard must actually be wired into the reload, not merely defined.
func TestTheReloadPathCallsTheDrainGuard(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/setup-vayudomain.sh"))
	if err != nil {
		t.Skipf("not readable: %v", err)
	}
	body := extractShellFunc(string(b), "reload_ok")
	if body == "" {
		t.Fatal("reload_ok() is gone")
	}
	if !strings.Contains(body, "await_nginx_drain") {
		t.Error("reload_ok does not wait for the previous generation to drain. The guard can be " +
			"perfect and unreferenced — which is how twelve reloads in ninety seconds happened.")
	}
}

// AUDIT FINDING, in this change's own code.
//
// This agent runs as ROOT. The first version of the fix put its backup
// directory under /var/lib/vayupress, which is owned by the UNPRIVILEGED
// service user — so that user could pre-create `nginx-backups` as a symlink and
// have root's own `mv` deposit files wherever they pointed it. The fix for a
// 502 would have shipped a local privilege-escalation primitive.
//
// The directory moved out of the service user's tree entirely. This test pins
// the remaining guard: a symlinked destination is refused rather than followed.
func TestASymlinkedBackupDirectoryIsRefusedRatherThanFollowed(t *testing.T) {
	tmp := t.TempDir()
	sites := filepath.Join(tmp, "sites-enabled")
	control := filepath.Join(tmp, "control")
	elsewhere := filepath.Join(tmp, "somewhere-root-should-not-write")
	for _, d := range []string{sites, control, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The attacker's plant: the backup path is a link to a directory of their
	// choosing, created before root ever runs.
	planted := filepath.Join(tmp, "planted-backups")
	if err := os.Symlink(elsewhere, planted); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(sites, "victim.bak")
	if err := os.WriteFile(stray, []byte("server { server_name x.example; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runAgentFunc(t, map[string]string{
		"VAYUSHIELD_SITES_ENABLED": sites,
		"VAYUSHIELD_BACKUP_DIR":    planted,
		"VAYUSHIELD_CONTROL_DIR":   control,
	}, "sweep_stray_nginx_backups"); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	ents, err := os.ReadDir(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Errorf("root wrote %d file(s) through a symlink into %s. A less-privileged user who can "+
			"create that path chooses where a root process deposits files — the fix for an outage "+
			"must not hand out a privilege-escalation primitive.", len(ents), elsewhere)
	}
	// And the stray is left alone rather than deleted: refusing to move it is
	// correct, destroying it is not.
	if _, err := os.Lstat(stray); err != nil {
		t.Errorf("the stray was removed even though it could not be preserved: %v", err)
	}
}

// The default backup location must not live in the service user's own tree.
func TestTheBackupDirectoryIsOutsideTheServiceUsersTree(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../deploy/vayushield-agent.sh"))
	if err != nil {
		t.Skipf("not readable: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "BACKUP_DIR=") {
			if strings.Contains(line, "/var/lib/vayupress") {
				t.Errorf("the backup directory defaults into /var/lib/vayupress, which the "+
					"unprivileged service user owns, while this agent runs as root: %s", line)
			}
			return
		}
	}
	t.Error("BACKUP_DIR is no longer defined in the agent")
}
