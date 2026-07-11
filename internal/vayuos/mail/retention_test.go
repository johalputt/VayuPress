package mail

// retention_test.go — the auto-delete sweep (ADR-0130) must delete exactly
// the read, unpinned, past-window mail in the sweep folders and nothing
// else. Read time is the file mtime (stamped at the Seen transition by
// setMessageFlags), so the tests place files directly and set mtimes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const retDomain = "johal.test"

// placeMessage writes a message file with the given maildir name and mtime.
func placeMessage(t *testing.T, md *Maildir, user, folder, sub, name string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(md.folderDir(retDomain, user, folder), sub)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("Subject: x\r\n\r\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSweepRetentionDeletesOnlyEligibleMail(t *testing.T) {
	md := NewMaildir(t.TempDir())
	now := time.Now()
	old := now.Add(-91 * 24 * time.Hour)
	fresh := now.Add(-2 * 24 * time.Hour)

	oldRead := placeMessage(t, md, "u", "Inbox", "cur", "100.old:2,S", old)
	freshRead := placeMessage(t, md, "u", "Inbox", "cur", "101.fresh:2,S", fresh)
	oldUnread := placeMessage(t, md, "u", "Inbox", "new", "102.unread", old)
	oldPinned := placeMessage(t, md, "u", "Inbox", "cur", "103.pinned:2,FS", old)
	oldArchived := placeMessage(t, md, "u", "Archive", "cur", "104.saved:2,S", old)
	oldSent := placeMessage(t, md, "u", "Sent", "cur", "105.sent:2,S", old)
	oldJunk := placeMessage(t, md, "u", "Junk", "cur", "106.junk:2,S", old)

	n, err := md.SweepRetention(retDomain, "u", 90, now)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted %d, want 2 (old Inbox read + old Junk read)", n)
	}
	gone := func(p string) bool { _, err := os.Stat(p); return os.IsNotExist(err) }
	if !gone(oldRead) {
		t.Error("old read Inbox mail survived")
	}
	if !gone(oldJunk) {
		t.Error("old read Junk mail survived")
	}
	for name, p := range map[string]string{
		"fresh read": freshRead, "unread": oldUnread, "pinned": oldPinned,
		"archived": oldArchived, "sent": oldSent,
	} {
		if gone(p) {
			t.Errorf("%s mail was deleted — must be exempt", name)
		}
	}
}

func TestSweepRetentionOffIsNoop(t *testing.T) {
	md := NewMaildir(t.TempDir())
	old := time.Now().Add(-400 * 24 * time.Hour)
	p := placeMessage(t, md, "u", "Inbox", "cur", "1.x:2,S", old)
	if n, err := md.SweepRetention(retDomain, "u", 0, time.Now()); err != nil || n != 0 {
		t.Fatalf("sweep(0 days) = %d, %v — want 0, nil", n, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("retention-off sweep deleted mail")
	}
}

func TestSeenTransitionStampsReadTime(t *testing.T) {
	md := NewMaildir(t.TempDir())
	delivered := time.Now().Add(-30 * 24 * time.Hour)
	placeMessage(t, md, "u", "Inbox", "new", "200.msg", delivered)

	id, err := md.setMessageFlags(retDomain, "u", "Inbox", "new/200.msg", map[byte]bool{'S': true})
	if err != nil {
		t.Fatalf("setMessageFlags: %v", err)
	}
	if !strings.HasPrefix(id, "cur/") {
		t.Fatalf("read message not in cur/: %q", id)
	}
	info, err := os.Stat(filepath.Join(md.folderDir(retDomain, "u", "Inbox"), "cur", strings.TrimPrefix(id, "cur/")))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Errorf("Seen transition did not stamp read time: mtime=%v", info.ModTime())
	}

	// A later flag change (pinning) must PRESERVE the read time.
	readAt := info.ModTime()
	id2, err := md.setMessageFlags(retDomain, "u", "Inbox", id, map[byte]bool{'S': true, 'F': true})
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	info2, err := os.Stat(filepath.Join(md.folderDir(retDomain, "u", "Inbox"), "cur", strings.TrimPrefix(id2, "cur/")))
	if err != nil {
		t.Fatal(err)
	}
	if !info2.ModTime().Equal(readAt) {
		t.Errorf("pinning changed the read time: %v → %v", readAt, info2.ModTime())
	}
}
