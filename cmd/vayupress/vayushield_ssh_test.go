// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/vayushield/offload"
)

// The agent reads sshd's failures from the journal only while ssh.want is
// there, writes one "<unix> <address> <user>" line for each, keeps its
// cursor, and lets nothing sshd logged on an attacker's behalf through: an
// address that is not one is dropped, and a user name keeps only
// [A-Za-z0-9._-].
func TestTheAgentWritesSSHFailuresAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	lib := agentLib(t, dir)
	control := filepath.Join(dir, "control")
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{control, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	journal := `1795400000.111111 host sshd[101]: Failed password for root from 198.51.100.9 port 52011 ssh2
1795400001.222222 host sshd-session[102]: Failed password for invalid user admin from 2001:db8::9 port 40022 ssh2
1795400002.333333 host sshd[103]: Invalid user te$(id)st from 203.0.113.7 port 41000
1795400003.444444 host sshd[104]: Failed password for root from 999.1.1.1 port 1 ssh2
1795400004.555555 host sshd[105]: Accepted publickey for ops from 192.0.2.5 port 2 ssh2
-- cursor: s=abc;i=42`
	fake := "#!/bin/sh\ncat <<'J'\n" + journal + "\nJ\n"
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		cmd := exec.Command("bash", "-c", "set -u; source "+lib+"; export_ssh_failures")
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "VAYUSHIELD_CONTROL_DIR="+control)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("export_ssh_failures: %v %s", err, out)
		}
	}
	run()
	if _, err := os.Stat(filepath.Join(control, "ssh-failures.txt")); err == nil {
		t.Fatal("the journal was read without ssh.want")
	}
	if err := os.WriteFile(filepath.Join(control, "ssh.want"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	run()
	got, _ := os.ReadFile(filepath.Join(control, "ssh-failures.txt"))
	want := "1795400000 198.51.100.9 root\n1795400001 2001:db8::9 admin\n1795400002 203.0.113.7 teidst\n"
	if string(got) != want {
		t.Fatalf("the agent wrote:\n%s\nwant:\n%s", got, want)
	}
	if c, _ := os.ReadFile(filepath.Join(control, "ssh.cursor")); string(c) != "s=abc;i=42" {
		t.Errorf("the cursor kept is %q", c)
	}
	if st, _ := os.ReadFile(filepath.Join(control, "ssh.state")); string(st) != "on" {
		t.Errorf("the state written is %q", st)
	}
}

func sshApp(t *testing.T) (*App, string) {
	t.Helper()
	openMigratedDB(t)
	control := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", control)
	a := &App{siteSettings: settings.New(dbpkg.DB), shieldOffload: offload.New(control), sshWatch: newSSHWatch()}
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyShieldSSH: "on"}); err != nil {
		t.Fatal(err)
	}
	return a, control
}

func appendSSH(t *testing.T, control string, at time.Time, ip string, n int) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(control, "ssh-failures.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 0; i < n; i++ {
		_, _ = f.WriteString(strconv.FormatInt(at.Unix(), 10) + " " + ip + " root\n")
	}
}

func banlisted(t *testing.T, a *App, control, ip string) (bool, time.Duration) {
	t.Helper()
	a.shieldOffload.Flush()
	b, _ := os.ReadFile(filepath.Join(control, "banlist.txt"))
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == ip {
			exp, _ := strconv.ParseInt(f[1], 10, 64)
			return true, time.Until(time.Unix(exp, 0))
		}
	}
	return false, 0
}

// Five failures within ten minutes drop an address for an hour; four do
// not, failures older than the window do not count, and a second ban lasts
// a day.
func TestFiveSSHFailuresBanAnAddressAndARepeatBansItLonger(t *testing.T) {
	a, control := sshApp(t)
	st := withSignins(t, a)
	now := time.Now()
	appendSSH(t, control, now, "198.51.100.20", 4)
	appendSSH(t, control, now.Add(-20*time.Minute), "203.0.113.21", 6)
	a.readSSHFailures(now)
	if on, _ := banlisted(t, a, control, "198.51.100.20"); on {
		t.Fatal("four failures banned an address")
	}
	if on, _ := banlisted(t, a, control, "203.0.113.21"); on {
		t.Fatal("failures older than the window banned an address")
	}
	// Read again after a restart, the file's old lines are not attempts
	// made now: they are not recorded on Sign-in attempts.
	if err := st.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Attempts(context.Background(), "", "", 50)
	for _, r := range rows {
		if r.Masked == "203.0.x.x" {
			t.Fatalf("a failure older than the window was recorded as an attempt: %+v", r)
		}
	}
	if len(rows) == 0 || rows[0].Door != "ssh" {
		t.Fatalf("the SSH failures were not recorded at the SSH door: %+v", rows)
	}
	appendSSH(t, control, now, "198.51.100.20", 1)
	a.readSSHFailures(now)
	on, left := banlisted(t, a, control, "198.51.100.20")
	if !on || left > time.Hour || left < 50*time.Minute {
		t.Fatalf("the fifth failure banned it %v for %v, want about an hour", on, left)
	}
	appendSSH(t, control, now, "198.51.100.20", 5)
	a.readSSHFailures(now)
	if _, left := banlisted(t, a, control, "198.51.100.20"); left < 23*time.Hour {
		t.Fatalf("a second ban lasts %v, want a day", left)
	}
}

// Switched off, nothing the agent wrote is read or banned.
func TestSSHProtectionOffReadsNothing(t *testing.T) {
	a, control := sshApp(t)
	if err := a.siteSettings.SetMany(context.Background(), settings.ForPrimary(), map[string]string{settings.KeyShieldSSH: "off"}); err != nil {
		t.Fatal(err)
	}
	appendSSH(t, control, time.Now(), "198.51.100.30", 9)
	a.readSSHFailures(time.Now())
	if on, _ := banlisted(t, a, control, "198.51.100.30"); on {
		t.Fatal("SSH protection switched off banned an address")
	}
	a.applySSHSetting(context.Background())
	if _, err := os.Stat(filepath.Join(control, "ssh.want")); err == nil {
		t.Fatal("ssh.want is there with SSH protection off")
	}
}
