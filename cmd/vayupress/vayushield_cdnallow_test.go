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
		// The property is that the command does not depend on where the operator's
		// shell happens to be. Three shapes satisfy it: an absolute path, one that
		// locates the script itself, or one that fetches into a directory it
		// creates and cd's into. Anything else inherits the working directory.
		absolute := strings.Contains(cmd, " "+shieldAgentPath)
		selfLocating := strings.Contains(cmd, "find /")
		selfFetching := strings.Contains(cmd, "mktemp -d") && strings.Contains(cmd, `cd "$d"`)
		if !absolute && !selfLocating && !selfFetching {
			t.Errorf("the %s command depends on the working directory: %q", action, cmd)
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
	writeAgentCaps(t, dir)
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
	writeAgentCaps(t, dir)
	row := shieldAgentUpgradeRow()
	if !strings.Contains(row, "hx-post=\"/os/api/shield/agent-upgrade\"") {
		t.Fatal("no upgrade control rendered for a capable helper")
	}
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

// TestARestartingUpgradeDoesNotHangForever.
//
// The agent writes "restarting" and then restarts the unit, so the process that
// would have written the final state no longer exists. Nothing else closes the
// status out, and the panel would show "installing and restarting" permanently —
// a spinner over an operation that finished, which reads as a hang and is worse
// than showing nothing.
func TestARestartingUpgradeDoesNotHangForever(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "resolve_pending_upgrade") {
		t.Fatal("nothing closes out an upgrade whose last act was killing the reporting process")
	}
	// It has to run on STARTUP. Reaching a fresh agent's first line is the proof
	// that the restart completed; anywhere else it would be guessing.
	run := strings.Index(s, "run_agent() {")
	loop := strings.Index(s, "while true; do")
	call := strings.Index(s, "\n  resolve_pending_upgrade")
	if call < run || call > loop {
		t.Error("the pending-upgrade resolution does not run at agent startup, which is the only " +
			"moment that actually proves the restart happened")
	}
	// And it must NOT run inside the poll loop, or it would overwrite a genuine
	// in-progress status every five seconds. Counted as CALL SITES — a bare
	// invocation on its own line — so the definition and the comment above it do
	// not read as extra calls.
	calls := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "resolve_pending_upgrade" {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("resolve_pending_upgrade has %d call sites, want exactly 1 — inside the poll "+
			"loop it would clobber a real in-progress status every five seconds", calls)
	}
}

// TestTheAgentDoesNotDeadlockRestartingItself.
//
// `systemctl restart` on the unit you are running inside waits for the job, and
// the job cannot finish until this process exits: systemd waits for the script,
// the script waits for systemd. The upgrade hangs until something times out.
func TestTheAgentDoesNotDeadlockRestartingItself(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "systemctl restart --no-block vayushield-agent") {
		t.Error("the agent restarts its own unit and waits for the job to complete — it is waiting " +
			"for itself to exit. --no-block queues the job and returns")
	}
}

// TestAnUnreachableTrustRootIsNotReportedAsAnAttack.
//
// Found by running the real verification against the real published bundle: it
// failed with "tuf-repo-cdn.sigstore.dev: Forbidden" — the trust root was
// unreachable, so NOTHING about the signature had been determined. The code
// reported that as "the bundle was not signed by this project's release
// workflow", which is an accusation of supply-chain attack.
//
// The two conclusions are opposites and lead to opposite actions. An operator
// told they are under attack does not go and check their egress firewall. This
// is the same defect class the posture report exists to prevent: a claim that
// overstates what was actually established.
func TestAnUnreachableTrustRootIsNotReportedAsAnAttack(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)

	// The failure must be classified before it is described.
	if !strings.Contains(s, "sigstore\\.dev") || !strings.Contains(s, "tuf") {
		t.Error("the agent does not distinguish an unreachable trust root from a bad signature, " +
			"so a firewalled host is told it is under supply-chain attack")
	}
	// Unreachable is a REFUSAL ("neither proved nor disproved"), not an accusation.
	if !strings.Contains(s, "neither proved nor disproved") {
		t.Error("the unreachable-infrastructure path does not say that nothing was determined")
	}
	// And the real signature failure must still say so plainly — softening both
	// cases into one gentle message loses the alarm that matters.
	if !strings.Contains(s, "is not signed by this project's release workflow") {
		t.Error("a genuine signature failure no longer reads as one")
	}
	// Either way: nothing installed. That claim must appear on both paths.
	unreachable := strings.Index(s, "neither proved nor disproved")
	failed := strings.Index(s, "is not signed by this project's release workflow")
	for name, at := range map[string]int{"unreachable": unreachable, "failed": failed} {
		if at < 0 {
			continue
		}
		if !strings.Contains(s[at:at+400], "Nothing was installed") &&
			!strings.Contains(s[max0(at-200):at+400], "nothing was installed") {
			t.Errorf("the %q path does not state that nothing was installed", name)
		}
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func writeAgentCaps(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "agent.caps"), []byte("selfupgrade=1 digest=1"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAnOldHelperIsNotOfferedAButtonItCannotHonour.
//
// The feature was, briefly, dead on arrival for every existing install: an older
// helper has no code that reads the request flag, so the panel would record the
// click, nothing would act on it, and no status would ever appear. The operator
// waits, decides it is slow, and stops trusting the panel.
//
// A control that silently does nothing is worse than one that is absent —
// absence at least tells the truth. So there is no button at all until the
// running helper advertises that it can act on one.
func TestAnOldHelperIsNotOfferedAButtonItCannotHonour(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)

	// No agent.caps file: an older helper, which is every install before this.
	row := shieldAgentUpgradeRow()
	if strings.Contains(row, "hx-post") {
		t.Error("the panel offers an upgrade button to a helper that has no code to receive the " +
			"request — the click would vanish with no feedback at all")
	}
	if !strings.Contains(row, "predates the self-upgrade feature") {
		t.Error("the panel does not explain why the button is missing, so its absence reads as a bug")
	}

	// With capabilities advertised, the button appears.
	writeAgentCaps(t, dir)
	if !strings.Contains(shieldAgentUpgradeRow(), "hx-post") {
		t.Error("a helper that advertises selfupgrade=1 is still not offered the button")
	}
}

// TestTheEndpointRefusesAnOldHelperToo — the panel not rendering a button is not
// a control. A POST can arrive from a stale page, a retried request, or anything
// else that did not just re-read the page.
func TestTheEndpointRefusesAnOldHelperToo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	// A live heartbeat, so the request gets past the "no agent" check and reaches
	// the capability check this test is actually about.
	if err := os.WriteFile(filepath.Join(dir, "agent.alive"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{}
	rr := httptest.NewRecorder()
	a.handleOSShieldAgentUpgrade(rr, httptest.NewRequest("POST", "/os/api/shield/agent-upgrade", nil))
	if rr.Code == http.StatusOK || rr.Code == http.StatusNoContent {
		t.Fatalf("the endpoint accepted an upgrade request a pre-feature helper can never see (%d)", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.upgrade.want")); err == nil {
		t.Error("the flag was written anyway, so it sits there forever waiting for code that " +
			"does not exist on this machine")
	}
}

// TestTheAgentAdvertisesItsOwnCapabilities — read from what the HELPER says, not
// inferred from the app's version. The two upgrade independently, which is the
// entire reason an old helper can be running under a new binary.
func TestTheAgentAdvertisesItsOwnCapabilities(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, `AGENT_CAPS="selfupgrade=1`) {
		t.Error("the agent does not advertise its capabilities, so the panel has to guess")
	}
	// Written on start AND on poll: the control dir is created by the app and may
	// not exist yet when the agent first comes up.
	if strings.Count(s, "write_caps") < 3 {
		t.Error("write_caps is not called both at startup and in the poll loop — the control dir " +
			"is app-created and may not exist when the agent starts")
	}
}

// TestTheInstallCommandCannotBeANoOp.
//
// The worst failure an installer can have: it succeeded loudly and changed
// nothing. The panel offered `sudo bash /usr/local/lib/vayushield/
// vayushield-agent.sh install`, and install_agent copies from the directory the
// script lives in — so that copies the installed files onto themselves, prints
// "✓ VayuShield agent installed and started", and upgrades nothing. An operator
// upgrading a stale helper ran it, saw success, and still had the stale helper.
//
// Pointing at a checkout is no better as a default: the updater clones to a
// temporary directory, so whatever checkout is on disk is usually older than the
// release being run.
func TestTheInstallCommandCannotBeANoOp(t *testing.T) {
	// Point the installed-agent path at a file that EXISTS, so a "reuse the
	// installed copy" shortcut is actually reachable from this test. Without
	// this, the check silently evaluates the branch that was never the bug.
	real := shieldAgentPath
	t.Cleanup(func() { shieldAgentPath = real })
	present := filepath.Join(t.TempDir(), "vayushield-agent.sh")
	if err := os.WriteFile(present, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	shieldAgentPath = present

	cmd := shieldAgentCmd("install")
	// Checked STRUCTURALLY, not against this machine's filesystem. The first
	// version of this test only failed when /usr/local/lib/vayushield existed,
	// which it does not in CI — so it passed against a reintroduction of the very
	// bug it was written for. A test whose verdict depends on the host it runs on
	// is not a test of the code.
	if strings.Contains(cmd, shieldAgentPath) {
		t.Fatalf("the install command references the ALREADY INSTALLED script (%s), which copies "+
			"those files onto themselves and reports success without upgrading anything: %q",
			shieldAgentPath, cmd)
	}
	// It must bring in a new agent from somewhere, and check it before running it.
	if !strings.Contains(cmd, "releases/latest/download/vayushield-agent.tar.gz") {
		t.Error("the install command does not fetch the published agent, so it cannot upgrade a " +
			"helper that is older than the release")
	}
	if !strings.Contains(cmd, "sha256sum -c") {
		t.Error("the command executes a downloaded script as root without checking it first")
	}
	// The digest check must gate the execution, not follow it.
	sum := strings.Index(cmd, "sha256sum -c")
	run := strings.Index(cmd, "bash ./vayushield-agent.sh")
	if sum < 0 || run < 0 || sum > run {
		t.Error("the checksum is verified after the script has already run")
	}
}

// TestThePanelAndTheHelperUseTheSameSupplyChain — a panel telling operators to
// install from one place while the helper upgrades itself from another is two
// supply chains where everybody believes there is one.
func TestThePanelAndTheHelperUseTheSameSupplyChain(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	if !strings.Contains(string(src), shieldAgentRepo) {
		t.Errorf("the panel installs from %q but the helper upgrades itself from somewhere else",
			shieldAgentRepo)
	}
	if !strings.Contains(shieldAgentCmd("install"), shieldAgentRepo) {
		t.Error("the install command does not use the pinned repository")
	}
}

// TestTheBootstrapDoesNotClaimASignatureCheck — it verifies a checksum, which is
// weaker than the signature the installed helper verifies later. Describing the
// two as the same thing is the overstatement this project's posture report
// exists to avoid.
func TestTheBootstrapDoesNotClaimASignatureCheck(t *testing.T) {
	hint := shieldCheckoutHint()
	if !strings.Contains(hint, "SHA-256") {
		t.Error("the hint does not say what the bootstrap actually verifies")
	}
	if !strings.Contains(hint, "weaker check") {
		t.Error("the hint does not admit the bootstrap is weaker than the helper's own upgrade " +
			"verification, so an operator reads them as equivalent")
	}
}

// TestInstallRestartsAnAlreadyRunningAgent.
//
// The bug that made every previous fix look like it had not worked.
//
// install_agent ended with `systemctl enable --now`. `--now` starts a unit that
// is STOPPED and does nothing whatsoever to one that is already running. So
// re-installing over a running agent copied the new script to disk while the old
// process carried on executing the old one from memory — and printed
// "installed and started".
//
// That is the worst shape a bug can take in an installer: repeated, confident
// success with no effect. An operator upgrading a stale helper ran it three
// times, saw three successes, and still had the stale helper — with no signal
// anywhere that anything was wrong.
func TestInstallRestartsAnAlreadyRunningAgent(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-agent.sh")
	if err != nil {
		t.Skipf("agent script not readable here: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "systemctl restart vayushield-agent") {
		t.Fatal("install does not restart the unit, so installing over a running agent leaves the " +
			"old process executing the old script while reporting success")
	}
	// `enable --now` must not be the thing that is relied on to start it.
	if strings.Contains(s, "systemctl enable --now vayushield-agent") {
		t.Error("install still uses `enable --now`, which is a no-op against a running unit")
	}
	// And the restart has to come after the files are in place, or it restarts
	// into the old script.
	inst := strings.Index(s, `install -m 0755 "${src}/vayushield-agent.sh"`)
	restart := strings.Index(s, "systemctl restart vayushield-agent")
	if inst < 0 || restart < inst {
		t.Error("the unit is restarted before the new script is installed, so it restarts into " +
			"the old one")
	}
}

// TestEveryInstallPathRestartsTheAgent.
//
// The restart bug existed in TWO places, and the second is why it went unnoticed
// for so long: the normal root updater had it as well. So no operator on any
// update path was ever actually receiving a new agent — the script on disk moved
// forward while the running process did not, on every install, silently.
//
// Fixing one and not the other would have left the commonest path broken while
// the rarer one worked, which is the harder failure to diagnose of the two.
func TestEveryInstallPathRestartsTheAgent(t *testing.T) {
	for _, f := range []string{
		"../../deploy/vayushield-agent.sh",
		"../../scripts/update-vayupress.sh",
	} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Skipf("%s not readable here: %v", f, err)
		}
		s := string(src)
		if strings.Contains(s, "enable --now vayushield-agent") {
			t.Errorf("%s uses `enable --now`, a no-op against a running unit — it installs a new "+
				"agent and leaves the old process running it", f)
		}
		if !strings.Contains(s, "systemctl restart vayushield-agent") {
			t.Errorf("%s never restarts the agent, so the code it just installed never runs", f)
		}
	}
}

// TestTheUpgradeControlSurvivesAHealthyHelper.
//
// The control used to live only inside the stale-agent warning, which fires on
// one specific symptom: a helper too old to write an enforcement digest. A
// helper merely a few versions behind shows none of that — so the button existed
// exactly once in a helper's life and then disappeared for good.
//
// "Nothing to upgrade right now" and "no way to upgrade" are different states
// and they looked identical.
func TestTheUpgradeControlSurvivesAHealthyHelper(t *testing.T) {
	src, err := os.ReadFile("../../cmd/vayupress/vayushield_hardening.go")
	if err != nil {
		src, err = os.ReadFile("vayushield_hardening.go")
	}
	if err != nil {
		t.Skipf("source not readable here: %v", err)
	}
	s := string(src)
	// The healthy branch is the one guarded by shieldAgentAlive(); the upgrade row
	// must be rendered there and not only from inside the stale notice.
	alive := strings.Index(s, "if shieldAgentAlive() {")
	stale := strings.Index(s, "} else {")
	if alive < 0 || stale < alive {
		t.Skip("panel structure changed; this test needs updating with it")
	}
	if !strings.Contains(s[alive:stale], "shieldAgentUpgradeRow()") {
		t.Error("the upgrade control is not offered to a healthy helper, so once the stale-agent " +
			"warning clears there is no way to upgrade it ever again")
	}
}

// TestTheFirewallCanBeReAppliedOverItself.
//
// Reported from a live install: pressing "Allowlist the edge ranges" fetched 21
// Cloudflare ranges successfully and then failed, with nftables rejecting every
// meter as "Device or resource busy".
//
// The cause was the ORDER of the safety check. `nft -c` validates against the
// LIVE kernel, and the `nft delete table` ran AFTER it — so on a machine where
// the table already existed, the dry run was asked to add meters that were
// already there. The first apply on a clean box worked and every apply after it
// failed, which is exactly the moment an operator re-runs the firewall or
// allowlists their proxy.
//
// Putting the delete inside the ruleset makes the dry run evaluate what the
// apply will actually do, and makes the apply atomic.
func TestTheFirewallCanBeReAppliedOverItself(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-firewall.sh")
	if err != nil {
		t.Skipf("firewall script not readable here: %v", err)
	}
	s := string(src)

	// The ruleset must carry its own delete, and declare the table first so the
	// delete is idempotent on a clean box.
	decl := strings.Index(s, "table inet ${TABLE}\ndelete table inet ${TABLE}")
	if decl < 0 {
		t.Fatal("the ruleset does not delete the table inside its own transaction, so `nft -c` " +
			"validates an add against a kernel that already has it and every re-apply fails")
	}
	// And there must be no separate delete between the check and the load: that
	// is both the bug and a window where the machine has no firewall at all.
	check := strings.Index(s, "nft -c -f")
	load := strings.LastIndex(s, `if ! nft -f "$rules"`)
	if check < 0 || load < 0 {
		t.Fatal("check/load sequence not found; this test needs updating with it")
	}
	between := s[check:load]
	if strings.Contains(between, `nft delete table inet "${TABLE}"`) {
		t.Error("a bare `nft delete table` still runs between the dry run and the load — the dry " +
			"run therefore validates a different state than the one the load runs in, and a failed " +
			"load leaves the host with NO firewall")
	}
}

// TestAFailedLoadDoesNotDisarmTheHost — the old sequence deleted the table and
// then loaded the new one as a separate command. If the load failed, the machine
// was left with no firewall while the script reported a load failure. The
// message has to match what actually happens.
func TestAFailedLoadDoesNotDisarmTheHost(t *testing.T) {
	src, err := os.ReadFile("../../deploy/vayushield-firewall.sh")
	if err != nil {
		t.Skipf("firewall script not readable here: %v", err)
	}
	if !strings.Contains(string(src), "the previous rules are still in force") {
		t.Error("the load-failure message does not state that the host is still protected, which " +
			"is only true because the transaction is atomic — say it, so an operator does not " +
			"assume they are exposed and start improvising")
	}
}
