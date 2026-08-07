// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Recovering the mail the case bug stranded.
//
// Folding the path in safeSegment stops NEW mail being lost, and does nothing
// for what is already sitting in a case-variant directory on a running install.
// Before the fold, delivery to ALICE@example.com wrote to .../example.com/ALICE/
// and its owner read .../example.com/alice/, so that mail is on disk and
// unreachable. After the fold it is still on disk and still unreachable —
// the fix is not a regression, but it is not a rescue either.
//
// There is also one genuine regression to close: a holder who happened to
// receive mail as "Alice@…" AND sign in as "Alice@…" could read it before, and
// after the fold would read the lowercase directory instead. That person must
// not lose sight of their mail because of a bug fix.
//
// So this merges every case variant into the canonical directory, once, at
// startup. It moves files rather than copying, never removes a source before
// its move succeeded, and is safe to run on every boot.

// seedMessage writes one message file directly into a Maildir subdirectory,
// bypassing Deliver so the test can place mail in a directory the current code
// would never choose — which is precisely the situation being repaired.
func seedMessage(t *testing.T, base, domain, user, sub, name, body string) {
	t.Helper()
	dir := filepath.Join(base, domain, user, sub)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func inboxBodies(t *testing.T, md *Maildir, domain, user string) []string {
	t.Helper()
	msgs, _ := md.ListFolder(domain, user, "Inbox")
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		raw, err := md.ReadRawFolder(domain, user, "Inbox", m.ID)
		if err != nil {
			t.Fatalf("read %s: %v", m.ID, err)
		}
		out = append(out, string(raw))
	}
	sort.Strings(out)
	return out
}

func TestStrandedMailInACaseVariantDirectoryIsRecovered(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)

	// What a real install looks like after the bug: some mail arrived correctly,
	// some arrived display-cased and went somewhere its owner never looks.
	seedMessage(t, base, "example.com", "alice", "new", "1000.a", "the one she can see")
	seedMessage(t, base, "example.com", "ALICE", "new", "1001.b", "her bank statement")
	seedMessage(t, base, "example.com", "Alice", "cur", "1002.c:2,S", "an invoice")

	n, err := md.ReconcileCaseVariants()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 2 {
		t.Errorf("merged %d directories, want 2", n)
	}

	got := inboxBodies(t, md, "example.com", "alice")
	if len(got) != 3 {
		t.Fatalf("alice sees %d messages after reconciliation, want 3: %q\n\n"+
			"Mail delivered to a display-cased address is still on this disk and still "+
			"unreachable.", len(got), got)
	}

	// And the variant directories are gone, so nothing re-strands on the next boot.
	for _, v := range []string{"ALICE", "Alice"} {
		if _, err := os.Stat(filepath.Join(base, "example.com", v)); !os.IsNotExist(err) {
			t.Errorf("%s still exists after reconciliation", v)
		}
	}
}

// Running it on every boot must be free and harmless.
func TestReconciliationIsIdempotent(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)
	seedMessage(t, base, "example.com", "ALICE", "new", "1000.a", "one")

	if _, err := md.ReconcileCaseVariants(); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	before := inboxBodies(t, md, "example.com", "alice")

	n, err := md.ReconcileCaseVariants()
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n != 0 {
		t.Errorf("second pass merged %d directories; nothing was left to merge", n)
	}
	after := inboxBodies(t, md, "example.com", "alice")
	if len(after) != len(before) || len(after) != 1 {
		t.Errorf("message count changed across a second pass: %d then %d", len(before), len(after))
	}
}

// Two Maildir files can legitimately carry the same name in different
// directories. Neither may overwrite the other — losing one message to a name
// clash while rescuing another is not a rescue.
func TestAFilenameClashLosesNoMessage(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)
	seedMessage(t, base, "example.com", "alice", "new", "1000.same", "the original")
	seedMessage(t, base, "example.com", "ALICE", "new", "1000.same", "the stranded one")

	if _, err := md.ReconcileCaseVariants(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := inboxBodies(t, md, "example.com", "alice")
	if len(got) != 2 {
		t.Fatalf("after merging a name clash alice has %d messages, want 2: %q", len(got), got)
	}
	if got[0] == got[1] {
		t.Errorf("both messages have the same body %q — one overwrote the other", got[0])
	}
}

// Folders travel with the account, or a rescue would recover the inbox and
// silently drop everything filed away.
func TestFoldersInsideAVariantAreMergedToo(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)
	seedMessage(t, base, "example.com", "ALICE", ".Sent/cur", "1000.s:2,S", "something she sent")

	if _, err := md.ReconcileCaseVariants(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	msgs, _ := md.ListFolder("example.com", "alice", "Sent")
	if len(msgs) != 1 {
		t.Errorf("alice's Sent folder has %d messages after reconciliation, want 1.\n\n"+
			"Recovering the inbox and leaving the folders behind loses the filed mail "+
			"instead of the delivered mail.", len(msgs))
	}
}

// THE CONTROL. Folding case must never merge two different people, and a
// reconciliation that moves mail between mailboxes is the worst possible place
// to get that wrong.
func TestReconciliationNeverMergesDifferentMailboxes(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)
	seedMessage(t, base, "example.com", "alice", "new", "1000.a", "alice's")
	seedMessage(t, base, "example.com", "bob", "new", "1001.b", "bob's private mail")
	seedMessage(t, base, "example.net", "alice", "new", "1002.c", "a different alice entirely")

	if _, err := md.ReconcileCaseVariants(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got := inboxBodies(t, md, "example.com", "alice"); len(got) != 1 || got[0] != "alice's" {
		t.Errorf("alice@example.com sees %q — reconciliation moved someone else's mail in", got)
	}
	if got := inboxBodies(t, md, "example.com", "bob"); len(got) != 1 || got[0] != "bob's private mail" {
		t.Errorf("bob sees %q — his mail was moved or lost", got)
	}
	if got := inboxBodies(t, md, "example.net", "alice"); len(got) != 1 || got[0] != "a different alice entirely" {
		t.Errorf("alice@example.net sees %q — two domains were merged", got)
	}
}

// A case-variant DOMAIN is the same defect one level up.
func TestACaseVariantDomainIsMergedToo(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)
	seedMessage(t, base, "EXAMPLE.COM", "alice", "new", "1000.a", "arrived at the shouty domain")

	if _, err := md.ReconcileCaseVariants(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := inboxBodies(t, md, "example.com", "alice"); len(got) != 1 {
		t.Errorf("alice sees %d messages after a domain-case merge, want 1", len(got))
	}
}

// An empty store, and a store already canonical, must both be no-ops rather
// than errors — this runs on every boot of every install.
func TestReconciliationOnACleanStoreDoesNothing(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)
	if n, err := md.ReconcileCaseVariants(); err != nil || n != 0 {
		t.Fatalf("empty store: merged %d, err %v", n, err)
	}
	if err := md.CreateAll("example.com", "alice"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if n, err := md.ReconcileCaseVariants(); err != nil || n != 0 {
		t.Fatalf("canonical store: merged %d, err %v", n, err)
	}
}

// The safety property that matters most, and the one a happy-path test cannot
// reach: when a message CANNOT be moved, it must still be there afterwards.
//
// This is why the source directory is pruned of empty directories rather than
// removed outright. os.RemoveAll would tidy up perfectly on every successful
// run and destroy exactly the messages that failed to move — the single outcome
// this whole file exists to prevent. A mutation swapping the two survives every
// other test here, because in all of them every move succeeds.
//
// The failure is induced the way a corrupted store would produce it: a plain
// FILE sitting where the merge needs to create a directory, so MkdirAll fails
// and the message underneath it cannot be placed.
func TestAMessageThatCannotBeMovedIsNotDeleted(t *testing.T) {
	base := t.TempDir()
	md := NewMaildir(base)

	seedMessage(t, base, "example.com", "ALICE", ".Sent/cur", "1000.s:2,S", "the filed message")
	// Canonical side: ".Sent" exists as a FILE, so creating .Sent/cur under it
	// cannot succeed.
	if err := os.MkdirAll(filepath.Join(base, "example.com", "alice"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "example.com", "alice", ".Sent"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}

	// The repair reports the problem rather than pretending it worked.
	if _, err := md.ReconcileCaseVariants(); err == nil {
		t.Error("reconciliation reported success while a message could not be moved")
	}

	// And the message is still on disk, where the next boot can retry it.
	stranded := filepath.Join(base, "example.com", "ALICE", ".Sent", "cur", "1000.s:2,S")
	if _, err := os.Stat(stranded); err != nil {
		t.Fatalf("the message that could not be moved was DELETED (%v).\n\n"+
			"Tidying the source directory must never destroy mail whose move failed. "+
			"That turns a repair into the data loss it was written to undo.", err)
	}
}
