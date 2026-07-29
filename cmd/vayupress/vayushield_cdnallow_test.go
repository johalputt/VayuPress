// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The allowlist button crosses a privilege boundary: an unprivileged web app
// asks a root agent to fetch ranges and reload a kernel ruleset. The design keeps
// that safe by never letting app-produced CONTENT reach a command — the vendor
// travels in the FILENAME and is re-validated on both sides. These tests pin that
// property, because it is the kind that quietly erodes when someone later wants
// to pass "just one more parameter".

func withControlDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	return dir
}

func TestRequestCDNAllowWritesAnEmptyNamedFlag(t *testing.T) {
	dir := withControlDir(t)
	if err := shieldRequestCDNAllow("cloudflare"); err != nil {
		t.Fatalf("request: %v", err)
	}
	flag := filepath.Join(dir, "cdnallow.cloudflare.want")
	fi, err := os.Stat(flag)
	if err != nil {
		t.Fatalf("flag not written: %v", err)
	}
	// Empty is the point. Anything the app can write into a file the root agent
	// reads is content the agent would have to sanitise; a zero-length file has
	// nothing to sanitise.
	if fi.Size() != 0 {
		t.Errorf("flag carries %d bytes of app-controlled content; it must be empty", fi.Size())
	}
}

// TestFlagPathNeverContainsCallerInput is the property code scanning was pointing
// at (CodeQL "uncontrolled data used in path expression",
// vayushield_hardening.go:155). The original built the filename by concatenation
// — "cdnallow." + vendor + ".want" — behind an exact-match allowlist. That was
// safe as written and still the wrong shape: the guard has to stay exactly where
// it is, and stay an exact match, forever, or a caller-supplied string starts
// naming files in a directory a root agent reads.
//
// The invariant now is stronger and checkable: every path the writer can produce
// is one of a fixed set of constants, whatever it is handed.
func TestFlagPathNeverContainsCallerInput(t *testing.T) {
	dir := withControlDir(t)

	hostile := []string{
		"cloudflare", // the one legitimate value
		"../../../etc/cron.d/x",
		"cloudflare/../../root/.ssh/authorized_keys",
		"..%2f..%2fetc%2fpasswd",
		"cloudflare\x00.want",
		strings.Repeat("a", 4096),
		"", " ", "CLOUDFLARE",
	}
	for _, v := range hostile {
		_ = shieldRequestCDNAllow(v) // errors are fine; escaping the dir is not
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := shieldCDNAllowFlags["cloudflare"]; !ok {
			t.Fatal("the flag table lost its only entry")
		}
		known := false
		for _, want := range shieldCDNAllowFlags {
			if e.Name() == want {
				known = true
			}
		}
		if !known {
			t.Errorf("control dir contains %q — a caller-supplied name reached the filesystem", e.Name())
		}
	}
	// Every flag filename must itself be a plain basename, or the constant table
	// becomes the traversal vector instead of the input.
	for vendor, name := range shieldCDNAllowFlags {
		if name != filepath.Base(name) || strings.Contains(name, "..") {
			t.Errorf("flag for %q is %q, which is not a plain filename", vendor, name)
		}
	}
}

func TestRequestCDNAllowRejectsUnknownVendors(t *testing.T) {
	dir := withControlDir(t)
	for _, bad := range []string{
		"", "aws", "CLOUDFLARE ", "cloudflare; rm -rf /",
		"../../etc/cron.d/x", "cloudflare/../../../root",
	} {
		if err := shieldRequestCDNAllow(bad); err == nil {
			t.Errorf("vendor %q was accepted", bad)
		}
	}
	// Nothing at all should have been created — a rejected vendor must not leave
	// a partial flag, and a traversal attempt must not escape the control dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("rejected vendors created %d file(s) in the control dir", len(entries))
	}
}

// TestCDNAllowEndpointRejectsUnknownVendors — the handler must validate too. A
// caller reaches it directly with a POST body; relying on the button to send only
// good values is not validation.
func TestCDNAllowEndpointRejectsUnknownVendors(t *testing.T) {
	withControlDir(t)
	a := newShieldApp(t, "on")
	for _, tc := range []struct {
		vendor string
		want   int
	}{
		{"cloudflare", http.StatusOK},
		{"aws", http.StatusBadRequest},
		{"", http.StatusBadRequest},
		{"cloudflare; id", http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodPost, "/os/api/shield/cdn-allow",
			strings.NewReader("vendor="+tc.vendor))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("CF-Ray", "abc-DEL")
		rr := httptest.NewRecorder()
		a.handleOSShieldCDNAllow(rr, req)
		if rr.Code != tc.want {
			t.Errorf("vendor %q: status %d, want %d", tc.vendor, rr.Code, tc.want)
		}
	}
}

// TestAgentReValidatesTheVendor is the half that matters most, and it RUNS the
// agent's reconcile rather than grepping it. The agent executes as root and the
// control directory is writable by the unprivileged web app, so "the app only
// ever writes good vendor names" is an assumption, not a control. A textual
// version of this test passed against a mutation that left the `case` statement
// in place while making it inert.
//
// The firewall script is replaced with a recorder, so what the agent would have
// executed is observable without touching a real ruleset.
func TestAgentReValidatesTheVendor(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	cut := strings.Index(string(src), `case "${1:-run}" in`)
	if cut < 0 {
		t.Fatal("could not find the agent's command dispatcher")
	}

	dir := t.TempDir()
	control := filepath.Join(dir, "control")
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(dir, "agent.sh")
	if err := os.WriteFile(agent, src[:cut], 0o600); err != nil {
		t.Fatal(err)
	}
	// A recorder in place of the real firewall script: it appends its arguments
	// and always succeeds, so the agent proceeds down the happy path.
	calls := filepath.Join(dir, "calls.txt")
	if err := os.WriteFile(filepath.Join(lib, "vayushield-firewall.sh"),
		[]byte("#!/usr/bin/env bash\necho \"$@\" >> "+calls+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// One legitimate request and several hostile flag names side by side.
	for _, name := range []string{
		"cdnallow.cloudflare.want",
		"cdnallow.aws.want",
		"cdnallow.cloudflare; id.want",
		"cdnallow.$(id).want",
	} {
		if err := os.WriteFile(filepath.Join(control, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out, err := exec.Command("bash", "-c",
		"VAYUSHIELD_CONTROL_DIR="+control+" VAYUSHIELD_LIB_DIR="+lib+
			" source "+agent+" >/dev/null 2>&1; reconcile_cdnallow; echo done").CombinedOutput()
	if err != nil {
		t.Fatalf("running reconcile_cdnallow: %v (%s)", err, out)
	}

	recorded, _ := os.ReadFile(calls)
	got := string(recorded)

	// The legitimate vendor must have been fetched AND re-applied. Without the
	// re-apply the ranges sit on disk while the kernel keeps the old ruleset,
	// which reads as success from the panel.
	if !strings.Contains(got, "cdn-allow cloudflare") {
		t.Errorf("the valid vendor was not fetched; recorded calls:\n%s", got)
	}
	if !strings.Contains(got, "apply") {
		t.Errorf("ranges were fetched but never re-applied to the kernel:\n%s", got)
	}
	// Nothing hostile may reach the firewall script.
	for _, bad := range []string{"aws", "id", "$(", ";"} {
		if strings.Contains(got, bad) {
			t.Errorf("a rejected vendor reached the privileged script (%q):\n%s", bad, got)
		}
	}
	// Every flag must be consumed. A flag left behind re-fetches on every poll.
	entries, err := os.ReadDir(control)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".want") {
			t.Errorf("flag %q survived the reconcile — the fetch would repeat every poll", e.Name())
		}
	}
}

// TestCDNAllowRowDegradesWithoutTheAgent — with no root agent there is nothing to
// action a click, so showing a button would be a lie. It must fall back to the
// command instead.
func TestCDNAllowRowDegradesWithoutTheAgent(t *testing.T) {
	withControlDir(t) // empty: no agent.alive heartbeat
	a := newShieldApp(t, "on")
	got := a.shieldCDNAllowRow("Cloudflare")
	if strings.Contains(got, "hx-post") {
		t.Error("a button is shown with no agent installed — clicking it would do nothing")
	}
	if !strings.Contains(got, "cdn-allow cloudflare") {
		t.Errorf("no manual fallback command offered:\n%s", got)
	}
}

// TestCDNAllowRowHandlesAnUnfetchableProxy — CDN-Loop identifies "a proxy" without
// naming a vendor whose ranges we can fetch. Offering a button there would promise
// something the agent cannot do.
func TestCDNAllowRowHandlesAnUnfetchableProxy(t *testing.T) {
	withControlDir(t)
	a := newShieldApp(t, "on")
	got := a.shieldCDNAllowRow("a proxy")
	if strings.Contains(got, "hx-post") {
		t.Error("a fetch button is offered for a proxy whose ranges cannot be fetched")
	}
	if !strings.Contains(got, "cdn-allow.conf") {
		t.Errorf("no manual path given for an unknown proxy:\n%s", got)
	}
}

// TestTheHardeningPanelNeverPrintsAPlaceholderPath.
//
// The panel told operators to run `cd /path/to/VayuPress && …`. That is not a
// command, it is a diagram of one: pasting it returns "No such file or
// directory", and an instruction an operator cannot paste is an instruction that
// was not given. Every command the panel offers must be either runnable as
// printed or accompanied by the way to find the missing piece.
func TestTheHardeningPanelNeverPrintsAPlaceholderPath(t *testing.T) {
	for _, body := range []string{shieldAgentStaleNotice(), shieldCheckoutHint()} {
		for _, bad := range []string{"/path/to/", "<your", "YOUR_", "&lt;path"} {
			if strings.Contains(body, bad) {
				t.Errorf("the panel prints the placeholder %q. An operator pastes what is on "+
					"screen; a placeholder produces an error and no way forward", bad)
			}
		}
	}
	// Every command with a Copy button must run from ANY directory.
	//
	// This is the second form of the same defect, and it shipped: the button
	// offered `sudo bash deploy/vayushield-agent.sh install`, a RELATIVE path
	// that works only if the operator's shell happens to be sitting in the
	// checkout. From the home directory every SSH session starts in, it returns
	// "No such file or directory" — which is what an operator reported. A copy
	// button is a promise that the thing copied will run.
	for _, action := range []string{"install", "uninstall"} {
		cmd := shieldAgentCmd(action)
		if strings.Contains(cmd, " deploy/") {
			t.Errorf("the %s command uses a relative deploy/ path (%q): it fails everywhere "+
				"except inside the checkout", action, cmd)
		}
		// Either absolute, or it locates the script itself. Nothing in between.
		absolute := strings.Contains(cmd, " /")
		selfLocating := strings.Contains(cmd, "find /")
		if !absolute && !selfLocating {
			t.Errorf("the %s command is neither absolute nor self-locating: %q", action, cmd)
		}
		// A self-locating command must SAY what it found before running it as
		// root. Executing whatever a filesystem search turned up, silently, is not
		// something to hand somebody with sudo in front of it.
		if selfLocating && !strings.Contains(cmd, "echo") {
			t.Errorf("the %s command searches the filesystem and runs the result as root "+
				"without printing what it found first: %q", action, cmd)
		}
	}
}

// TestAStaleAgentIsToldItIsStale.
//
// This is the case an operator actually hits, and it had no notice anywhere. The
// install prompt renders only when the helper is MISSING, so a helper that is
// running but too old to write an enforcement digest left the posture report
// showing four "unverified" rows whose single shared cause was never named, and
// no upgrade path on the page at all.
func TestAStaleAgentIsToldItIsStale(t *testing.T) {
	notice := shieldAgentStaleNotice()
	if notice == "" {
		t.Skip("a digest is present in this environment, so there is nothing stale to report")
	}
	for _, want := range []string{
		"older build",            // names the condition
		"enforcement digest",     // names the specific missing capability
		"vayushield-agent.sh",    // gives the fix
		"unprivileged by design", // says why this one step is not a button
	} {
		if !strings.Contains(notice, want) {
			t.Errorf("the stale-agent notice does not mention %q — without it the operator sees "+
				"four unexplained warnings and no way to clear them", want)
		}
	}
	// It must NOT read as an outage. The tiers are enforcing; only the proof is
	// missing, and overstating that would push someone into unnecessary changes.
	if !strings.Contains(notice, "almost certainly fine") {
		t.Error("the notice does not say the defences are probably working. A warning that reads " +
			"like a failure gets acted on as one")
	}
}

// ── Helper self-upgrade ──────────────────────────────────────────────────────

// TestTheUpgradeRequestCarriesNothingButTheRequest is the property the whole
// design rests on.
//
// The agent runs as root. If the unprivileged web app could put a URL, a
// version, a path or a byte of code in front of it, then compromising the web
// app would mean choosing what a root process executes — the exact escalation
// the privilege separation exists to prevent. The request must therefore be
// carried by the file's EXISTENCE and nothing else.
func TestTheUpgradeRequestCarriesNothingButTheRequest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)

	if err := shieldRequestAgentUpgrade(); err != nil {
		t.Fatalf("request: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "agent.upgrade.want"))
	if err != nil {
		t.Fatalf("the flag was not written: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("the upgrade flag carries %d bytes of content (%q). A root process reading "+
			"anything the web app wrote is the escalation this separation exists to prevent",
			len(b), string(b))
	}
}

// TestTheAgentChoosesItsOwnSourceAndVerifiesBeforeExecuting.
//
// Read against the agent script itself rather than against a description of it.
// Every clause here is something whose absence turns a convenience button into a
// remote-root primitive.
func TestTheAgentChoosesItsOwnSourceAndVerifiesBeforeExecuting(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)

	// The repository is the agent's own constant, never something handed to it.
	if !strings.Contains(s, `UPGRADE_REPO="${VAYUSHIELD_UPGRADE_REPO:-johalputt/VayuPress}"`) {
		t.Error("the agent does not pin its own upgrade source; a source it is TOLD is a source " +
			"an attacker can choose")
	}
	// Verification must precede unpacking. Unpacking an unverified archive as
	// root is already the compromise, whatever is checked afterwards.
	verify := strings.Index(s, "cosign verify-blob")
	untar := strings.Index(s, "tar -xzf")
	if verify < 0 {
		t.Fatal("the agent installs an upgrade without verifying a signature")
	}
	if untar < 0 || untar < verify {
		t.Error("the agent unpacks the bundle before verifying it — by then it has already " +
			"written attacker-chosen files to disk as root")
	}
	// No signature available must mean REFUSE, never "carry on unverified".
	if !strings.Contains(s, "refusing to install unverified code as root") {
		t.Error("the agent does not refuse when it cannot verify. An operator who believes their " +
			"root helper only accepts signed code, and is actually accepting anything over TLS, " +
			"is worse off than one who was told to install cosign")
	}
	// The flag is consumed before the work, or a persistent failure becomes a
	// five-second loop that re-downloads and re-runs an installer forever.
	up := strings.Index(s, "reconcile_upgrade()")
	rm := strings.Index(s[up:], `rm -f "$flag"`)
	run := strings.Index(s[up:], "self_upgrade")
	if up < 0 || rm < 0 || run < 0 || rm > run {
		t.Error("the upgrade flag is not cleared before the upgrade runs, so a failing upgrade " +
			"repeats on every poll")
	}
}

// TestARefusedUpgradeIsNotReportedAsAFailure — declining to install code it
// could not verify is the helper working correctly. Wording it as a breakage
// pushes an operator toward finding a way around it, which is the opposite of
// what the refusal is for.
func TestARefusedUpgradeIsNotReportedAsAFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "agent.upgrade.state"), []byte("unverifiable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.upgrade.detail"),
		[]byte("cosign is not installed, so the bundle's signature cannot be checked"), 0o600); err != nil {
		t.Fatal(err)
	}
	row := shieldAgentUpgradeRow()
	if !strings.Contains(row, "Refused") || !strings.Contains(row, "nothing was installed") {
		t.Errorf("a refusal must read as a refusal with its consequence stated, got: %s", row)
	}
	if strings.Contains(row, "is-err") {
		t.Error("a refusal is styled as an error. The helper did its job; only a real failure " +
			"should read as one")
	}
	// And the fix must be on screen, or the operator is stuck at a wall.
	if !strings.Contains(row, "cosign") {
		t.Error("the refusal does not name what to install to clear it")
	}
}

// TestTheUpgradeButtonExplainsWhyItIsOnlyAButton — an operator told "click here"
// with no reason learns nothing about why the OTHER privileged steps are not
// buttons, and reads the difference as inconsistency rather than as design.
func TestTheUpgradeButtonExplainsWhyItIsOnlyAButton(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	row := shieldAgentUpgradeRow()
	for _, want := range []string{"verifies the signature", "never supplies the code", "root process"} {
		if !strings.Contains(row, want) {
			t.Errorf("the upgrade control does not say %q, so the operator cannot tell a designed "+
				"boundary from an arbitrary one", want)
		}
	}
}

// TestTheReleasePublishesWhatTheAgentGoesLookingFor — the agent fetches
// vayushield-agent.tar.gz and its cosign bundle from the latest release. If the
// workflow does not publish both, the button fails for everyone, and it fails
// with a signature error that reads like an attack.
func TestTheReleasePublishesWhatTheAgentGoesLookingFor(t *testing.T) {
	wf, err := os.ReadFile("../../.github/workflows/tag-release.yml")
	if err != nil {
		t.Skipf("workflow not readable here: %v", err)
	}
	s := string(wf)
	for _, want := range []string{
		"dist/vayushield-agent.tar.gz",
		"dist/vayushield-agent.tar.gz.cosign.bundle",
		"cosign sign-blob --yes dist/vayushield-agent.tar.gz",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the release workflow does not produce %q — the self-upgrade button would "+
				"fail on every install", want)
		}
	}
}
