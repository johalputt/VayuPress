// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/shieldaudit"
)

// The enforcement digest is a new agent->app data direction: the root agent
// observes what is actually in force and writes a fixed-schema file the
// unprivileged app reads. These tests RUN write_digest against stubbed nft and
// nginx rather than reading the shell, because the whole point of the digest is
// that it reports observation rather than intent — and a test that greps the
// script would be checking intent about intent.

type digestEnv struct {
	nginxConf, sysctlConf string
	nftRules, nginxDump   string
	conntrackNow          string
}

// runWriteDigest sources the agent's function library and calls write_digest
// with nft, nginx and sysctl stubbed to return the given observations.
func runWriteDigest(t *testing.T, e digestEnv) map[string]string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	dir := t.TempDir()

	b, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Fatalf("read the agent script: %v", err)
	}
	src := string(b)
	cut := strings.Index(src, "\ncase \"${1:-run}\" in")
	if cut < 0 {
		if cut = strings.Index(src, "\ncase "); cut < 0 {
			t.Fatal("could not find the agent's command dispatcher")
		}
	}
	lib := filepath.Join(dir, "lib.sh")
	if err := os.WriteFile(lib, []byte(src[:cut]), 0o600); err != nil {
		t.Fatal(err)
	}

	control := filepath.Join(dir, "control")
	if err := os.MkdirAll(control, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rulesFile := write("rules.txt", e.nftRules)
	dumpFile := write("nginx-T.txt", e.nginxDump)
	sysctlConf := write("99-vayushield.conf", e.sysctlConf)
	nginxConf := filepath.Join(dir, "vayushield.conf")
	if e.nginxConf != "" {
		if err := os.WriteFile(nginxConf, []byte(e.nginxConf), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	stubs := map[string]string{
		// `nft list table ...` prints the captured ruleset; empty file means the
		// table does not exist, which is how nft behaves (non-zero + no output).
		"nft": "#!/usr/bin/env bash\n" +
			"if [ -s \"" + rulesFile + "\" ]; then cat \"" + rulesFile + "\"; exit 0; fi\nexit 1\n",
		"nginx": "#!/usr/bin/env bash\n" +
			"if [ \"$1\" = \"-T\" ]; then cat \"" + dumpFile + "\"; exit 0; fi\nexit 0\n",
		"sysctl": "#!/usr/bin/env bash\n" +
			"if [ \"$1\" = \"-n\" ]; then printf '%s\\n' \"" + e.conntrackNow + "\"; exit 0; fi\nexit 0\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("bash", "-c", "set -u; source "+lib+"; write_digest")
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"VAYUSHIELD_CONTROL_DIR="+control,
		"VAYUSHIELD_NGINX_DST="+nginxConf,
		"VAYUSHIELD_SYSCTL_CONF="+sysctlConf,
	)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("write_digest failed (%v): %s", err, errBuf.String())
	}

	raw, err := os.ReadFile(filepath.Join(control, "enforcement.digest"))
	if err != nil {
		t.Fatalf("no digest was written: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}
	return got
}

const enforcingNginxConf = `
limit_req_zone  $binary_remote_addr zone=vp_req:16m rate=8r/s;
limit_conn_zone $binary_remote_addr zone=vp_conn:16m;
limit_req  zone=vp_req burst=20 nodelay;
limit_conn vp_conn 32;
`

// The shape of the defect this whole digest exists for: the zones are declared,
// and every directive that would apply them is commented out.
const inertNginxConf = `
limit_req_zone  $binary_remote_addr zone=vp_req:16m rate=8r/s;
limit_conn_zone $binary_remote_addr zone=vp_conn:16m;
# limit_req  zone=vp_req burst=20 nodelay;
# limit_conn vp_conn 32;
`

const dualStackRules = `
table inet vayushield {
  chain vs_web4 {
    meter vs_conn4 { ip saddr ct count over 64 } drop
    meter vs_rate4 { ip saddr limit rate 50/second burst 100 packets } return
  }
  chain vs_web6 {
    meter vs_conn6 { ip6 saddr ct count over 64 } drop
    meter vs_rate6 { ip6 saddr limit rate 50/second burst 100 packets } return
  }
}
`

// TestDigestReportsEnforcementNotInstallation is the reason the digest exists.
// The old Tier 3 state was computed from the destination file EXISTING, so a
// config whose limits were all commented out reported "Active" indefinitely.
func TestDigestReportsEnforcementNotInstallation(t *testing.T) {
	enforcing := runWriteDigest(t, digestEnv{nginxConf: enforcingNginxConf})
	if enforcing["tier3_installed"] != "yes" || enforcing["tier3_enforcing"] != "yes" {
		t.Errorf("an enforcing config reported installed=%q enforcing=%q",
			enforcing["tier3_installed"], enforcing["tier3_enforcing"])
	}

	inert := runWriteDigest(t, digestEnv{nginxConf: inertNginxConf})
	if inert["tier3_installed"] != "yes" {
		t.Errorf("an installed config reported installed=%q", inert["tier3_installed"])
	}
	if inert["tier3_enforcing"] != "no" {
		t.Errorf("a config with every limit_req commented out reported enforcing=%q — "+
			"this is the exact state that showed Active while doing no work", inert["tier3_enforcing"])
	}
}

// TestDigestSeesTheIPv6HalfOfTheRuleset — a v4-only ruleset is the failure that
// looks perfectly healthy from an IPv4 client, so the digest must report the
// families separately rather than answering "are there meters".
func TestDigestSeesTheIPv6HalfOfTheRuleset(t *testing.T) {
	both := runWriteDigest(t, digestEnv{nftRules: dualStackRules})
	if both["tier2_table"] != "yes" || both["tier2_meters_v4"] != "yes" || both["tier2_meters_v6"] != "yes" {
		t.Errorf("a dual-stack ruleset reported %v", both)
	}

	v4only := strings.Split(dualStackRules, "chain vs_web6")[0] + "}\n"
	got := runWriteDigest(t, digestEnv{nftRules: v4only})
	if got["tier2_meters_v4"] != "yes" {
		t.Errorf("v4 meters went unseen: %v", got)
	}
	if got["tier2_meters_v6"] != "no" {
		t.Errorf("missing IPv6 meters reported as %q — a v4-keyed meter never matches a v6 "+
			"packet, so this is the half that can be silently unprotected", got["tier2_meters_v6"])
	}
}

// TestDigestOmitsWhatItCannotObserve — an omitted key means "not observed", and
// the app's parser maps that to Unknown. Writing "no" instead would turn a
// missing tool into a wall of red, and writing "yes" would turn it into a wall
// of green, which is the dangerous direction.
func TestDigestOmitsWhatItCannotObserve(t *testing.T) {
	// No table loaded, and no conntrack value configured to compare against.
	got := runWriteDigest(t, digestEnv{nginxConf: enforcingNginxConf})

	if got["tier2_table"] != "no" {
		t.Errorf("tier2_table = %q when nft reports no table, want no", got["tier2_table"])
	}
	// With no table there is nothing to say about meters — the key must be absent
	// rather than asserted "no", which would read as "the meters are missing"
	// when the truth is "the whole table is".
	if _, present := got["tier2_meters_v4"]; present {
		t.Errorf("tier2_meters_v4 was asserted with no table loaded: %v", got)
	}
	if _, present := got["conntrack_sized"]; present {
		t.Errorf("conntrack_sized was asserted with nothing to compare against: %v", got)
	}
}

// TestDigestReadsConntrackBackFromTheKernel — the drop-in that ASKS for a value
// and the kernel that holds one disagreed on every fresh boot, because the
// net.netfilter.* keys do not exist until the module loads.
func TestDigestReadsConntrackBackFromTheKernel(t *testing.T) {
	const conf = "net.netfilter.nf_conntrack_max = 262144\n"

	match := runWriteDigest(t, digestEnv{sysctlConf: conf, conntrackNow: "262144"})
	if match["conntrack_sized"] != "yes" {
		t.Errorf("conntrack_sized = %q when the kernel agrees with the drop-in", match["conntrack_sized"])
	}

	mismatch := runWriteDigest(t, digestEnv{sysctlConf: conf, conntrackNow: "65536"})
	if mismatch["conntrack_sized"] != "no" {
		t.Errorf("conntrack_sized = %q when the kernel holds a different value than the "+
			"drop-in asked for — this is the silent fresh-boot failure", mismatch["conntrack_sized"])
	}
}

// TestDigestCarriesASchemaVersion — a root process writes this and an
// unprivileged one parses it. They are upgraded independently, so the reader has
// to be able to tell which contract it is looking at.
func TestDigestCarriesASchemaVersion(t *testing.T) {
	got := runWriteDigest(t, digestEnv{nginxConf: enforcingNginxConf})
	if got["schema"] == "" {
		t.Error("the digest carries no schema version")
	}
	if got["generated"] == "" {
		t.Error("the digest carries no timestamp, so the app cannot tell a fresh one from a stale one")
	}
}

// TestAgentAndFirewallAgreeOnTheTableName — the digest observes objects the
// firewall script creates. If the two names drift, the digest reports "no table"
// on a perfectly healthy install and the panel shows a false Fail.
func TestAgentAndFirewallAgreeOnTheTableName(t *testing.T) {
	agent, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Fatal(err)
	}
	fw, err := os.ReadFile("../../deploy/vayushield-firewall.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct{ agentDefault, firewall, what string }{
		{`TABLE_MAIN="${VAYUSHIELD_TABLE:-vayushield}"`, `TABLE="vayushield"`, "nftables table"},
		{`SYSCTL_CONF_PATH="${VAYUSHIELD_SYSCTL_CONF:-/etc/sysctl.d/99-vayushield.conf}"`,
			`SYSCTL_CONF="${SYSCTL_CONF:-/etc/sysctl.d/99-vayushield.conf}"`, "sysctl drop-in"},
	} {
		if !strings.Contains(string(agent), pair.agentDefault) {
			t.Errorf("the agent's %s name changed; the digest would observe the wrong object", pair.what)
		}
		if !strings.Contains(string(fw), pair.firewall) {
			t.Errorf("the firewall's %s name changed; the digest would observe the wrong object", pair.what)
		}
	}
}

// --- App-side reader ----------------------------------------------------------

// writeDigestFile plants a digest in a temp control dir and points the app at it.
func writeDigestFile(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, shieldDigestName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestReaderDegradesToUnverifiedNotToBroken — the digest is written by a root
// process and parsed by an unprivileged one, and the two are upgraded
// independently. A newer app reading an older agent's digest must treat keys it
// does not find as "not observed", never as "no": guessing "no" paints the panel
// red on a routine version skew, which teaches the operator to ignore it.
func TestReaderDegradesToUnverifiedNotToBroken(t *testing.T) {
	writeDigestFile(t, "schema=1\ngenerated=1700000000\ntier3_installed=yes\n")
	d := readShieldDigest()

	if !d.Present {
		t.Fatal("a digest with content parsed as absent")
	}
	if d.Tier3Installed != shieldaudit.Yes {
		t.Errorf("tier3_installed = %v, want Yes", d.Tier3Installed)
	}
	for name, got := range map[string]shieldaudit.Tri{
		"tier2_table":     d.Tier2TablePresent,
		"tier2_meters_6":  d.Tier2MetersV6,
		"conntrack":       d.ConntrackSized,
		"tier3_enforcing": d.Tier3Enforcing,
	} {
		if got != shieldaudit.Unknown {
			t.Errorf("%s = %v for a key the agent never wrote, want Unknown", name, got)
		}
	}
}

// TestMissingDigestIsAbsentNotEmpty — an absent file and a file full of "no" are
// completely different situations, and only one of them is a fault.
func TestMissingDigestIsAbsentNotEmpty(t *testing.T) {
	writeDigestFile(t, "")
	if d := readShieldDigest(); d.Present {
		t.Error("a missing digest was reported as present")
	}
	// A file with nothing parseable in it is also absent, not a set of answers.
	writeDigestFile(t, "\n# only a comment\n\n")
	if d := readShieldDigest(); d.Present {
		t.Error("a digest with no key/value lines was reported as present")
	}
}

// TestDigestAgeComesFromTheAgentsTimestamp — not from the file's mtime. The agent
// installs the digest atomically every cycle, which rewrites mtime even when the
// content is identical, so mtime answers "when did the agent last run" rather
// than "how old is this observation".
func TestDigestAgeComesFromTheAgentsTimestamp(t *testing.T) {
	old := time.Now().Add(-3 * time.Hour).Unix()
	writeDigestFile(t, "schema=1\ngenerated="+strconv.FormatInt(old, 10)+"\ntier3_enforcing=yes\n")

	d := readShieldDigest()
	if d.Age < 2*time.Hour {
		t.Errorf("age = %v for a digest generated three hours ago — the reader is using "+
			"the file's mtime, which the agent rewrites on every cycle", d.Age)
	}
}

// TestPanelChipComparesAgainstTheBaseline — the volumetric row can never pass, so
// a chip that compares the Fail count to zero reports a perfect install as
// failing. That is worse than no chip: an indicator that is always red is an
// indicator nobody reads.
func TestPanelChipComparesAgainstTheBaseline(t *testing.T) {
	if shieldaudit.BaselineFails < 1 {
		t.Fatal("the baseline no longer accounts for the permanent volumetric row")
	}
	// A healthy report: exactly the baseline fails and no warnings.
	in := shieldaudit.Inputs{
		Tier2Wanted: true, Tier3Wanted: true,
		Tier2State: "active", Tier3State: "active",
		AgentAlive: true, BindAddr: "127.0.0.1:8080",
		RateLimit: true, LoadShed: true, AutoBlock: true, CaptureWired: true,
		Digest: shieldaudit.Digest{
			Present: true, Age: time.Minute,
			Tier2TablePresent: shieldaudit.Yes, Tier2MetersV4: shieldaudit.Yes,
			Tier2MetersV6: shieldaudit.Yes, ConntrackSized: shieldaudit.Yes,
			Tier3Installed: shieldaudit.Yes, Tier3Enforcing: shieldaudit.Yes,
			DefaultServer: shieldaudit.Yes, MCPVhostRestricted: shieldaudit.Yes,
		},
	}
	_, warn, _, fail := shieldaudit.Summary(shieldaudit.Run(in))
	if fail-shieldaudit.BaselineFails > 0 || warn > 0 {
		t.Fatalf("test setup is not a healthy install: fail=%d warn=%d", fail, warn)
	}
}
