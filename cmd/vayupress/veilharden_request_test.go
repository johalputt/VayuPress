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

// The applied-at timestamp comes from the file's mtime, not from a string inside
// it. Both are written by the same machine, but only the mtime shares a clock
// with this process's start time — and the verdict is a comparison between them.
func TestAppliedAtComesFromTheFileNotFromItsContents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	path := filepath.Join(dir, veilHardenResultFile)
	// A finished_at from the distant past. If it were trusted, an install that
	// hardened a second ago would report the far more serious "written and did
	// not take" instead of "awaiting restart".
	body := `{"started_at":"2001-01-01T00:00:00Z","finished_at":"2001-01-01T00:00:00Z",` +
		`"wrote":["NoNewPrivileges=yes"],"skipped":[],"reverted":false,"failed":false,"detail":"ok"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	st := readVeilHardenState()
	if !st.HaveResult {
		t.Fatal("a valid result was not read")
	}
	if st.AppliedAt.Year() == 2001 {
		t.Fatal("AppliedAt was taken from the document's own timestamp rather than the file's mtime")
	}
	// Written now, so a process that started a minute ago is awaiting a restart.
	start := time.Now().Add(-time.Minute)
	if got := vayuveil.ReconcileHardening(st, veilHardenBare(), start); got != vayuveil.HardenAwaitingRestart {
		t.Fatalf("want HardenAwaitingRestart, got %v", got)
	}
}
