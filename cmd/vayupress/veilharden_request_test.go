// SPDX-License-Identifier: Apache-2.0

package main

// veilharden_request_test.go — the privilege boundary, ADR-0150 §5 S6.
//
// This request makes ROOT edit a systemd unit and restart a live service. The
// whole safety argument is that the request is a pure signal — an empty file
// whose contents nothing reads — so nothing an unprivileged process (or anything
// that has compromised it) can express reaches root except "go". These tests
// guard that property, and the worker's own refusal to read the flag.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayuveil"
)

// The request carries nothing. If this ever fails, an unprivileged process is
// passing arguments to a root one — the classic local escalation, arriving
// looking like a small feature ("let the console choose which directives").
func TestTheHardeningRequestIsAPureSignal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	path := filepath.Join(dir, veilHardenRequestFile)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("the request file carries %d bytes; it must be a pure signal", len(b))
	}
	if st := readVeilHardenState(); !st.Pending {
		t.Error("a fresh request was not reported as pending")
	}
}

// The worker must never read the flag it reacts to. A grep is crude and the
// property is worth an imperfect guard: a shell script nobody looks at twice is
// exactly where this regresses.
func TestTheHardeningWorkerNeverReadsTheRequest(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/vayuveil-harden.sh"))
	if err != nil {
		t.Skipf("worker script not readable from here: %v", err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		if !strings.Contains(trimmed, "REQUEST") {
			continue
		}
		// The only legitimate uses are removing it and naming the variable.
		if strings.HasPrefix(trimmed, "REQUEST=") || strings.Contains(trimmed, "rm -f") {
			continue
		}
		t.Errorf("line %d uses the request file for something other than deleting it: %s", i+1, trimmed)
	}
}

// The worker consumes the request BEFORE it can fail. A .path unit with
// PathExists= only re-arms when the file goes away, so an early exit that left
// the flag in place would mean no future request from the panel ever fires —
// the button silently dies after its first bad run.
func TestTheWorkerRemovesTheRequestBeforeAnythingCanFail(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/vayuveil-harden.sh"))
	if err != nil {
		t.Skipf("worker script not readable from here: %v", err)
	}
	src := string(b)
	remove := strings.Index(src, `rm -f "$REQUEST"`)
	if remove < 0 {
		t.Fatal("the worker never removes the request file, so the watcher can never re-arm")
	}
	// Every exit path must come after it. `exit` inside the functions defined
	// above would be a false positive, so only bare top-level exits count — and
	// there are none above the removal in a correct script.
	for _, marker := range []string{"\nexit 0", "\n  exit 0"} {
		if idx := strings.Index(src, marker); idx >= 0 && idx < remove {
			t.Fatalf("an exit at byte %d precedes the request removal at %d", idx, remove)
		}
	}
}

// A request is refused outright when nothing would consume it. A dead button
// that reports success and leaves a flag file behind is worse than no button.
func TestARequestIsRefusedWhenTheWorkerIsNotInstalled(t *testing.T) {
	if veilHardenUnitsInstalled() {
		t.Skip("this host actually has the worker installed")
	}
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	st := readVeilHardenState()
	if st.Installed {
		t.Fatal("readVeilHardenState reported an installed worker that does not exist")
	}
	// And the card, given that state, offers the install command rather than a
	// button — asserted on the card because that is what the operator sees.
	card := veilHardenCard(st, veilHardenBare(), time.Now())
	if strings.Contains(card, "data-veilharden-run") {
		t.Error("a request button was offered with nothing to consume the request")
	}
}

// An unparseable result file must read as "never run", never as a successful
// one. The zero HardenState has HaveResult false, which is the safe direction:
// a truncated write during a crash cannot silently become a clean report.
func TestAnUnreadableResultIsNotAReport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	if err := os.WriteFile(filepath.Join(dir, veilHardenResultFile),
		[]byte(`{"wrote":["NoNewPri`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	st := readVeilHardenState()
	if st.HaveResult {
		t.Fatal("a truncated result file was accepted as a report")
	}
	// And a torn read must not be laundered into the serious verdict either: with
	// no result at all, the honest answer is that nobody has asked.
	start := time.Now()
	if got := vayuveil.ReconcileHardening(st, veilHardenBare(), start); got != vayuveil.HardenNotRequested {
		t.Fatalf("want HardenNotRequested from a torn result, got %v", got)
	}
}

// The verdict's timestamp comes from the DROP-IN, not from the worker's report.
//
// This is the audit finding that changed the design. The worker writes the
// drop-in, restarts the service, then watches for twenty seconds before writing
// its result — so on every successful run the restarted process starts BEFORE
// the result file exists. Keying the verdict on the result therefore said
// "awaiting restart" about a process that had already restarted into the
// drop-in, turning the one serious finding this row exists to surface into a
// reassuring wait-a-moment, on exactly the path an operator takes.
func TestTheVerdictIsKeyedOnTheDropInFileNotTheWorkersReport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	dropIn := filepath.Join(dir, "20-vayuveil-hardening.conf")
	if err := os.WriteFile(dropIn, []byte("[Service]\nNoNewPrivileges=yes\n"), 0o644); err != nil {
		t.Fatalf("write drop-in: %v", err)
	}
	orig := veilHardenDropInPath
	veilHardenDropInPath = dropIn
	t.Cleanup(func() { veilHardenDropInPath = orig })

	// The worker's report lands AFTER the restart, which is the whole point.
	body := `{"started_at":"2026-08-05T12:00:00Z","finished_at":"2026-08-05T12:00:30Z",` +
		`"wrote":["NoNewPrivileges=yes"],"skipped":[],"reverted":false,"failed":false,"detail":"ok"}`
	if err := os.WriteFile(filepath.Join(dir, veilHardenResultFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write result: %v", err)
	}

	st := readVeilHardenState()
	if !st.DropInPresent {
		t.Fatal("the drop-in was not read")
	}

	// A process started two seconds AFTER the drop-in — the one systemd restarted
	// into. The directive is not in force, so it did not take. Saying "awaiting
	// restart" here would be excusing the failure with a restart that happened.
	after := st.DropInAt.Add(2 * time.Second)
	if got := vayuveil.ReconcileHardening(st, veilHardenBare(), after); got != vayuveil.HardenDidNotTake {
		t.Fatalf("a process started after the drop-in must read as did-not-take, got %v", got)
	}

	// And a process that predates the drop-in genuinely is awaiting a restart.
	before := st.DropInAt.Add(-time.Minute)
	if got := vayuveil.ReconcileHardening(st, veilHardenBare(), before); got != vayuveil.HardenAwaitingRestart {
		t.Fatalf("a process started before the drop-in must read as awaiting restart, got %v", got)
	}
}

// A drop-in with no report beside it is still a drop-in. A result file lost to a
// disk wipe must not make a unit that is still carrying directives read as one
// that never had any.
func TestADropInIsReadEvenWithNoWorkerReport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	dropIn := filepath.Join(dir, "20-vayuveil-hardening.conf")
	if err := os.WriteFile(dropIn, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatalf("write drop-in: %v", err)
	}
	orig := veilHardenDropInPath
	veilHardenDropInPath = dropIn
	t.Cleanup(func() { veilHardenDropInPath = orig })

	st := readVeilHardenState()
	if st.HaveResult {
		t.Fatal("a report was invented from nothing")
	}
	if !st.DropInPresent {
		t.Fatal("the drop-in was ignored because no report sat beside it")
	}
	if got := vayuveil.ReconcileHardening(st, veilHardenBare(), st.DropInAt.Add(time.Second)); got == vayuveil.HardenNotRequested {
		t.Fatal("a unit carrying a drop-in was reported as never having been asked")
	}
}

// AUDIT FINDING — the worker's write paths must not come from configuration.
//
// It runs as root from a unit carrying EnvironmentFile=-/etc/vayupress/env.
// Deriving the directory root writes its result and log into from an environment
// variable would let a configuration value choose where root writes. The sibling
// provisioning worker hardcodes it for exactly this reason, and this one drifted
// from that before the pre-release pass caught it.
func TestTheWorkerDoesNotTakeItsWritePathsFromTheEnvironment(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/vayuveil-harden.sh"))
	if err != nil {
		t.Skipf("worker script not readable from here: %v", err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.HasPrefix(trimmed, "STATE_DIR=") {
			continue
		}
		if strings.Contains(trimmed, "$") {
			t.Errorf("line %d derives the root-written state directory from a variable: %s", i+1, trimmed)
		}
	}
}

// AUDIT FINDING — a result file that will not parse reads as "never run", so
// anything embedded in it must be sanitised before it gets there.
//
// The detail string carries `systemctl status` output, which can hold control
// characters. A raw one makes the JSON invalid, the panel reads no report at
// all, and a revert — the outcome that must never be silent — goes unreported.
func TestTheWorkerSanitisesWhatItEmbedsInItsResult(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/vayuveil-harden.sh"))
	if err != nil {
		t.Skipf("worker script not readable from here: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "systemctl status") {
		return // nothing external is embedded; nothing to sanitise
	}
	for _, line := range strings.Split(src, "\n") {
		if !strings.Contains(line, "systemctl status") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		// Printable-only is the one that matters: a stray control byte is what
		// makes the document unparseable, and quotes only make it wrong.
		if !strings.Contains(line, "[:print:]") {
			t.Errorf("systemctl output is embedded without being reduced to printable characters: %s",
				strings.TrimSpace(line)[:min(120, len(strings.TrimSpace(line)))])
		}
	}
}
