package mode

import (
	"os"
	"testing"
)

func TestJournalPersistsTransitions(t *testing.T) {
	path := t.TempDir() + "/mode.db"

	mgr := New()
	j, past, err := OpenJournal(path, mgr)
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	if len(past) != 0 {
		t.Fatalf("expected no past transitions on fresh DB, got %d", len(past))
	}

	if err := mgr.Transition(ModeDegraded, "test reason", "test.cause"); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if err := mgr.Transition(ModeQuarantined, "test reason 2", "test.cause2"); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	history, err := j.History()
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 transitions, got %d", len(history))
	}
	if history[0].From != ModeNormal || history[0].To != ModeDegraded {
		t.Errorf("unexpected first transition: %+v", history[0])
	}
	if history[1].From != ModeDegraded || history[1].To != ModeQuarantined {
		t.Errorf("unexpected second transition: %+v", history[1])
	}
	j.Close()
}

func TestJournalReplayOnReopen(t *testing.T) {
	path := t.TempDir() + "/mode.db"

	// Write two transitions.
	mgr1 := New()
	j1, _, err := OpenJournal(path, mgr1)
	if err != nil {
		t.Fatal(err)
	}
	_ = mgr1.Transition(ModeDegraded, "r1", "c1")
	_ = mgr1.Transition(ModeQuarantined, "r2", "c2")
	j1.Close()

	// Reopen and confirm history is replayed.
	mgr2 := New()
	j2, past, err := OpenJournal(path, mgr2)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()

	if len(past) != 2 {
		t.Fatalf("expected 2 past transitions on reopen, got %d", len(past))
	}
	if past[0].Cause != "c1" || past[1].Cause != "c2" {
		t.Errorf("unexpected history on reopen: %+v", past)
	}
}

func TestJournalSurvivesMissingDir(t *testing.T) {
	_, _, err := OpenJournal("/nonexistent/path/mode.db", New())
	if err == nil {
		t.Fatal("expected error opening journal in non-existent directory")
	}
}

func TestJournalClose(t *testing.T) {
	path := t.TempDir() + "/mode.db"
	mgr := New()
	j, _, err := OpenJournal(path, mgr)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Double-close should not panic.
	_ = j.Close()
	// Remove the file to confirm it was created.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("journal file not created: %v", err)
	}
}
