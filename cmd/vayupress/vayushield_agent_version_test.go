package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The helper's version has to be observable, because without it the upgrade
// button cannot be checked.
//
// The dead end, in the field: an operator pressed "Upgrade the helper", the
// posture report went on showing the same warning, and NOTHING anywhere
// distinguished "the helper upgraded and the finding is real" from "the upgrade
// silently did not happen". agent.caps cannot settle it — the capability string
// is byte-identical across releases that change behaviour — so the panel, the
// operator and the person reading the report were all guessing.

func TestHelperVersionIsUnknownRatherThanInvented(t *testing.T) {
	withControlDir(t) // empty: a helper predating the version stamp
	if got := shieldAgentVersion(); got != "unknown" {
		t.Fatalf("a helper that reports no version must read as %q, got %q — "+
			"an invented number is worse than an absent one", "unknown", got)
	}
}

func TestHelperVersionIsReported(t *testing.T) {
	dir := withControlDir(t)
	if err := os.WriteFile(filepath.Join(dir, "agent.version"), []byte("3.16.30"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := shieldAgentVersion(); got != "3.16.30" {
		t.Fatalf("version = %q, want %q", got, "3.16.30")
	}
}

// The file is on disk and its contents are rendered into the panel. It is
// written by root and read by the app, so this is a defence-in-depth strip
// rather than the only one — the call site escapes as well — but a value that
// reaches HTML unfiltered is not something to leave to one layer.
func TestHelperVersionRefusesAnythingThatIsNotAVersion(t *testing.T) {
	dir := withControlDir(t)
	if err := os.WriteFile(filepath.Join(dir, "agent.version"),
		[]byte("3.16.30<script>alert(1)</script>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := shieldAgentVersion()
	for _, bad := range []string{"<", ">", "(", ")", "/"} {
		if strings.Contains(got, bad) {
			t.Fatalf("version %q still carries %q", got, bad)
		}
	}
	if len(got) > 32 {
		t.Fatalf("version %q is %d chars, want it truncated to 32", got, len(got))
	}
}

// A version alone is not the fix — it has to be ON the card, next to the button
// it makes checkable, or it answers a question nobody can see the answer to.
func TestTheUpgradeCardShowsWhichHelperIsRunning(t *testing.T) {
	dir := withControlDir(t)
	if err := os.WriteFile(filepath.Join(dir, "agent.caps"),
		[]byte("selfupgrade=1 digest=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.version"), []byte("3.16.28"), 0o600); err != nil {
		t.Fatal(err)
	}
	row := shieldAgentUpgradeRow()
	if !strings.Contains(row, "3.16.28") {
		t.Fatalf("the upgrade card does not show the running helper's version:\n%s", row)
	}
	// Both numbers, or the operator cannot tell that a helper older than the app
	// is why a server-level fix has not landed — which is the whole point.
	if !strings.Contains(row, Version) {
		t.Fatalf("the upgrade card does not show the app version %q for comparison:\n%s", Version, row)
	}
}
