package mail

import "testing"

func TestUIDStoreAssignsStableAscendingUIDs(t *testing.T) {
	us, err := NewUIDStore(memDB(t))
	if err != nil {
		t.Fatalf("new uid store: %v", err)
	}
	acct, folder := "bob@example.com", "Inbox"

	a1, _ := us.Assign(acct, folder, "msg-a")
	b1, _ := us.Assign(acct, folder, "msg-b")
	if a1 != 1 || b1 != 2 {
		t.Fatalf("expected ascending UIDs 1,2; got %d,%d", a1, b1)
	}
	// Re-assigning the same base names must return the SAME UIDs (the property a
	// real client relies on across reconnects, even after flag changes rename the
	// file's :2, suffix).
	if got, _ := us.Assign(acct, folder, "msg-a"); got != a1 {
		t.Errorf("msg-a UID changed: was %d now %d", a1, got)
	}
	if got, _ := us.Assign(acct, folder, "msg-b"); got != b1 {
		t.Errorf("msg-b UID changed: was %d now %d", b1, got)
	}

	// UIDNEXT is one past the highest assigned UID.
	if n, _ := us.UIDNext(acct, folder); n != 3 {
		t.Errorf("UIDNext = %d, want 3", n)
	}

	// UIDVALIDITY is stable across calls and non-zero.
	v1, _ := us.Validity(acct, folder)
	v2, _ := us.Validity(acct, folder)
	if v1 == 0 || v1 != v2 {
		t.Errorf("UIDVALIDITY unstable/zero: %d vs %d", v1, v2)
	}

	// A different folder has an independent UID space.
	if got, _ := us.Assign(acct, "Sent", "msg-a"); got != 1 {
		t.Errorf("Sent folder should start UIDs at 1; got %d", got)
	}
}

func TestBaseNameStableAcrossFlagChange(t *testing.T) {
	// new/<name> and cur/<name>:2,S must share the same UID key.
	if a, b := idBaseName("new/123.456_7.host"), idBaseName("cur/123.456_7.host:2,S"); a != b {
		t.Errorf("base name not stable across flag change: %q vs %q", a, b)
	}
}
