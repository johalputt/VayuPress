// SPDX-License-Identifier: Apache-2.0

package customsite

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func deployVersion(t *testing.T, base, v string) {
	t.Helper()
	if _, err := deployBytes(base, zipOf(t, map[string]string{"index.html": v})); err != nil {
		t.Fatal(err)
	}
}

// Five earlier deployments are kept, newest first, and any one of them can
// be restored — not only the last. The second bad upload in a row no longer
// destroys the last good site.
func TestFiveEarlierDeploymentsAreKeptAndAnyCanBeRestored(t *testing.T) {
	base := t.TempDir()
	for i := 1; i <= 8; i++ {
		deployVersion(t, base, "V"+strconv.Itoa(i))
	}
	gens := History(base)
	if len(gens) != KeptGenerations {
		t.Fatalf("%d generations kept, want %d", len(gens), KeptGenerations)
	}
	for i := 1; i < len(gens); i++ {
		if gens[i-1].ID <= gens[i].ID {
			t.Fatalf("history is not newest first: %v", gens)
		}
	}
	want := []string{"V7", "V6", "V5", "V4", "V3"}
	for i, g := range gens {
		b, err := os.ReadFile(filepath.Join(base, "history", g.ID, "index.html"))
		if err != nil || string(b) != want[i] {
			t.Errorf("generation %d holds %q, want %s", i, b, want[i])
		}
		if g.Manifest.DeployedAt.IsZero() {
			t.Errorf("generation %d has no record of when it was deployed", i)
		}
	}
	if err := Restore(base, gens[3].ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := serveGet(t, base, "/"); got != "V4" {
		t.Errorf("after restoring V4 the site serves %q", got)
	}
	after := History(base)
	if len(after) != KeptGenerations {
		t.Errorf("%d generations after restore, want %d", len(after), KeptGenerations)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "history", after[0].ID, "index.html")); string(b) != "V8" {
		t.Errorf("the site live before the restore is not the newest generation (%q), so the restore cannot be undone", b)
	}
	if !ReadManifest(base).HasPrev {
		t.Error("the manifest says there is nothing to restore")
	}
}

func TestRollingBackTwiceReturns(t *testing.T) {
	base := t.TempDir()
	deployVersion(t, base, "V1")
	deployVersion(t, base, "V2")
	for _, want := range []string{"V1", "V2"} {
		if err := Rollback(base); err != nil {
			t.Fatal(err)
		}
		if got := serveGet(t, base, "/"); got != want {
			t.Errorf("rollback served %q, want %s", got, want)
		}
	}
}

// A site deployed before generations has its one earlier version in
// "previous"; it joins the history rather than being lost.
func TestTheOldPreviousDirectoryJoinsTheHistory(t *testing.T) {
	base := t.TempDir()
	deployVersion(t, base, "CURRENT")
	if err := os.MkdirAll(filepath.Join(base, "previous"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "previous", "index.html"), []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	// History already holds nothing but what migration adds.
	for _, g := range History(base) {
		_ = os.RemoveAll(filepath.Join(base, "history", g.ID))
	}
	if err := Rollback(base); err != nil {
		t.Fatalf("Rollback with only the old previous directory: %v", err)
	}
	if got := serveGet(t, base, "/"); got != "OLD" {
		t.Errorf("the old previous version was not restored: %q", got)
	}
	if _, err := os.Stat(filepath.Join(base, "previous")); !os.IsNotExist(err) {
		t.Error("the old previous directory is still there beside the history")
	}
}

func TestRestoreRefusesAnythingButAGeneration(t *testing.T) {
	base := t.TempDir()
	deployVersion(t, base, "V1")
	deployVersion(t, base, "V2")
	for _, id := range []string{"../../etc", "current", "", "0000000000000000000", History(base)[0].ID + "0"} {
		if err := Restore(base, id); err == nil {
			t.Errorf("Restore(%q) succeeded", id)
		}
	}
	if got := serveGet(t, base, "/"); got != "V2" {
		t.Errorf("a refused restore changed the site: %q", got)
	}
}
