// SPDX-License-Identifier: Apache-2.0

package mail

// header_cache_test.go — the folder listing reads message headers once and
// remembers them, validated by (size, mtime). The failure mode of a cache like
// this is silent: a rewritten message keeps showing its old subject/from, which
// looks like the mailbox is lying rather than like a cache bug.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListFolderServesFreshHeadersAfterAChange(t *testing.T) {
	t.Parallel()
	e := newLoopbackEngine(t, nil)
	if e.maildir == nil {
		t.Fatal("loopback engine has no maildir")
	}
	dom, local := "example.com", "alice"
	raw := "From: sender@x.test\r\nTo: alice@test\r\nSubject: First subject\r\n" +
		"Date: Mon, 2 Jan 2006 15:04:05 -0700\r\n\r\nbody\r\n"
	if _, err := e.maildir.DeliverTo(dom, local, "Inbox", []byte(raw)); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	rd := ReadAsSystem(local, "test")

	first, err := e.ListFolder(rd, "Inbox")
	if err != nil || len(first) != 1 {
		t.Fatalf("first list: %d messages, err %v", len(first), err)
	}
	if first[0].Subject != "First subject" {
		t.Fatalf("subject = %q, want the delivered one", first[0].Subject)
	}
	if got, want := first[0].Date.Year(), 2006; got != want {
		t.Errorf("date year = %d, want %d — the Date header must win over the file mtime", got, want)
	}

	// A second listing is the cached path: it must be identical, not empty.
	second, err := e.ListFolder(rd, "Inbox")
	if err != nil || len(second) != 1 {
		t.Fatalf("second list: %d messages, err %v", len(second), err)
	}
	if second[0].Subject != "First subject" || !second[0].Date.Equal(first[0].Date) {
		t.Errorf("cached listing drifted: %+v vs %+v", second[0], first[0])
	}

	// Rewrite the file in place, as a flag flip or an edit would, and give it a
	// newer mtime. A cache keyed on the path alone would keep serving "First".
	path := filepath.Join(e.maildir.base, dom, local, first[0].ID)
	updated := "From: other@x.test\r\nTo: alice@test\r\nSubject: Second subject\r\n" +
		"Date: Tue, 3 Jan 2006 15:04:05 -0700\r\n\r\nbody\r\n"
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatalf("rewrite message: %v", err)
	}
	later := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	third, err := e.ListFolder(rd, "Inbox")
	if err != nil || len(third) != 1 {
		t.Fatalf("third list: %d messages, err %v", len(third), err)
	}
	if third[0].Subject != "Second subject" {
		t.Errorf("subject = %q after the file changed — the header cache served a stale summary", third[0].Subject)
	}
	if third[0].From != "other@x.test" {
		t.Errorf("from = %q after the file changed — stale from", third[0].From)
	}
}

// One seed per check the cache makes. The test above changes both the size and
// the mtime of the rewritten message, so either check alone passes it and
// deleting one would go unnoticed; each seed here changes exactly one thing.
func TestTheHeaderCacheChecksSizeAndTimeEachOnItsOwn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Maildir{base: dir}
	path := filepath.Join(dir, "msg")
	write := func(subject string, mod time.Time) (int64, time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte("Subject: "+subject+"\r\n\r\nbody\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size(), fi.ModTime()
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	size, mod := write("Aaaa", base)
	if h := m.headersFor(path, size, mod); h.subject != "Aaaa" {
		t.Fatalf("first read: %q", h.subject)
	}
	// Same size, later time.
	size, mod = write("Bbbb", base.Add(time.Second))
	if h := m.headersFor(path, size, mod); h.subject != "Bbbb" {
		t.Errorf("same size, new mtime served %q — the time check is not deciding", h.subject)
	}
	// Same time, different size.
	size, mod = write("Cccccc", base.Add(time.Second))
	if h := m.headersFor(path, size, mod); h.subject != "Cccccc" {
		t.Errorf("same mtime, new size served %q — the size check is not deciding", h.subject)
	}
}

// A read that fails is not remembered: the next listing reads again. Seeded by
// asking for a file that is not there yet, then creating it with exactly the
// size and time the first call was given.
func TestAFailedHeaderReadIsNotRemembered(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Maildir{base: dir}
	path := filepath.Join(dir, "msg")
	content := []byte("Subject: Arrived\r\n\r\nbody\r\n")
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if h := m.headersFor(path, int64(len(content)), mod); h.subject != "" {
		t.Fatalf("a missing file produced a subject: %q", h.subject)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	if h := m.headersFor(path, int64(len(content)), mod); h.subject != "Arrived" {
		t.Errorf("after a failed read the message lists with subject %q — the failure was cached", h.subject)
	}
}

// writeMessages puts n messages with the given subject into a folder's cur/,
// all with one fixed mtime so a same-size rewrite keeps the cache key.
func writeMessages(t *testing.T, dir string, n int, subject string, mod time.Time) []string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "17000000"+string(rune('a'+i/26))+string(rune('a'+i%26))+".host:2,S")
		raw := "From: a@x.test\r\nTo: b@x.test\r\nSubject: " + subject + "\r\n\r\nbody\r\n"
		if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

func subjectsOf(t *testing.T, m *Maildir, folder string) map[string]int {
	t.Helper()
	msgs, err := m.ListFolder("example.com", "big", folder)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, msg := range msgs {
		out[msg.Subject]++
	}
	return out
}

// TestTheFolderBeingListedStaysCachedWhenTheInstallOutgrowsTheBound — one
// bound over the whole install, reset whole when full, made a warm listing of a
// big folder as slow as a cold one. Now the least-recently-listed OTHER folder
// goes. Same-size rewrites under an unchanged mtime show which listings were
// served from the cache (old subject) and which re-read the files (new one).
func TestTheFolderBeingListedStaysCachedWhenTheInstallOutgrowsTheBound(t *testing.T) {
	t.Parallel()
	m := NewMaildir(t.TempDir())
	m.hdrLimit = 50
	mod := time.Now().Add(-time.Hour)
	a := writeMessages(t, filepath.Join(m.folderDir("example.com", "big", "Archive"), "cur"), 40, "old-a", mod)
	b := writeMessages(t, filepath.Join(m.folderDir("example.com", "big", "Inbox"), "cur"), 40, "old-b", mod)

	subjectsOf(t, m, "Archive")
	subjectsOf(t, m, "Inbox") // 80 entries > 50: Archive, the older folder, must go

	rewrite := func(paths []string, subject string) {
		for _, p := range paths {
			raw := "From: a@x.test\r\nTo: b@x.test\r\nSubject: " + subject + "\r\n\r\nbody\r\n"
			if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, mod, mod); err != nil {
				t.Fatal(err)
			}
		}
	}
	rewrite(b, "new-b")
	if got := subjectsOf(t, m, "Inbox"); got["old-b"] != 40 {
		t.Errorf("the folder just listed was evicted and re-read: %v", got)
	}
	rewrite(a, "new-a")
	if got := subjectsOf(t, m, "Archive"); got["new-a"] != 40 {
		t.Errorf("the least-recently-listed folder was kept past the bound: %v", got)
	}
}

// sameSizeRewrite rewrites each message with a subject of the same length and
// the original mtime, so the cache key is unchanged: a listing that shows the
// new subject re-read the file, one that shows the old subject was cached.
func sameSizeRewrite(t *testing.T, paths []string, subject string, mod time.Time) {
	t.Helper()
	for _, p := range paths {
		raw := "From: a@x.test\r\nTo: b@x.test\r\nSubject: " + subject + "\r\n\r\nbody\r\n"
		if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

// TestEvictionTakesTheLeastRecentlyListedFolder — three folders, room for two.
// The one listed longest ago goes; the one listed in between stays.
func TestEvictionTakesTheLeastRecentlyListedFolder(t *testing.T) {
	t.Parallel()
	m := NewMaildir(t.TempDir())
	m.hdrLimit = 50
	mod := time.Now().Add(-time.Hour)
	dir := func(f string) string { return filepath.Join(m.folderDir("example.com", "big", f), "cur") }
	a := writeMessages(t, dir("Archive"), 20, "old-a", mod)
	b := writeMessages(t, dir("Junk"), 20, "old-b", mod)
	writeMessages(t, dir("Inbox"), 20, "old-c", mod)
	subjectsOf(t, m, "Archive")
	subjectsOf(t, m, "Junk")
	subjectsOf(t, m, "Inbox") // 60 > 50: Archive, listed first, is the one to go

	sameSizeRewrite(t, b, "new-b", mod)
	if got := subjectsOf(t, m, "Junk"); got["old-b"] != 20 {
		t.Errorf("a more recently listed folder was evicted instead of the oldest: %v", got)
	}
	sameSizeRewrite(t, a, "new-a", mod)
	if got := subjectsOf(t, m, "Archive"); got["new-a"] != 20 {
		t.Errorf("the least-recently-listed folder survived eviction: %v", got)
	}
}

// TestReadingAFolderFromTheCacheCountsAsUsingIt — a folder the operator keeps
// returning to is served entirely from the cache, and must not be the one
// evicted just because nothing in it was new.
func TestReadingAFolderFromTheCacheCountsAsUsingIt(t *testing.T) {
	t.Parallel()
	m := NewMaildir(t.TempDir())
	m.hdrLimit = 50
	mod := time.Now().Add(-time.Hour)
	dir := func(f string) string { return filepath.Join(m.folderDir("example.com", "big", f), "cur") }
	a := writeMessages(t, dir("Archive"), 20, "old-a", mod)
	writeMessages(t, dir("Junk"), 20, "old-b", mod)
	writeMessages(t, dir("Inbox"), 20, "old-c", mod)
	subjectsOf(t, m, "Archive")
	subjectsOf(t, m, "Junk")
	subjectsOf(t, m, "Archive") // every entry a hit: Archive is now the more recent
	subjectsOf(t, m, "Inbox")   // 60 > 50: Junk must go, not Archive

	sameSizeRewrite(t, a, "new-a", mod)
	if got := subjectsOf(t, m, "Archive"); got["old-a"] != 20 {
		t.Errorf("a folder just read from the cache was evicted as if unused: %v", got)
	}
}

// TestAFolderBiggerThanTheBoundIsStillCached — a single folder larger than the
// whole bound is the case the old reset-everything cache failed hardest on, and
// evicting the folder being listed would repeat that failure.
func TestAFolderBiggerThanTheBoundIsStillCached(t *testing.T) {
	t.Parallel()
	m := NewMaildir(t.TempDir())
	m.hdrLimit = 50
	mod := time.Now().Add(-time.Hour)
	paths := writeMessages(t, filepath.Join(m.folderDir("example.com", "big", "Inbox"), "cur"), 60, "old-x", mod)
	subjectsOf(t, m, "Inbox")
	sameSizeRewrite(t, paths, "new-x", mod)
	if got := subjectsOf(t, m, "Inbox"); got["old-x"] != 60 {
		t.Errorf("a folder over the bound was evicted while being listed: %v", got)
	}
}

// TestRenamedAndDeletedMessagesLeaveTheCache — a flag change renames a message
// file and a delete removes it; a long-lived folder must not keep every name it
// ever had.
func TestRenamedAndDeletedMessagesLeaveTheCache(t *testing.T) {
	t.Parallel()
	m := NewMaildir(t.TempDir())
	paths := writeMessages(t, filepath.Join(m.folderDir("example.com", "big", "Inbox"), "cur"), 3, "s", time.Now())
	subjectsOf(t, m, "Inbox")
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(paths[1], paths[1]+"F"); err != nil {
		t.Fatal(err)
	}
	subjectsOf(t, m, "Inbox")
	if m.hdrTotal != 2 {
		t.Errorf("cache holds %d entries for a folder of 2 files", m.hdrTotal)
	}
}

// TestTheInboxListingAlsoForgetsWhatIsGone — the top-level inbox has its own
// listing (Maildir.List), and it must prune like the folder view does.
func TestTheInboxListingAlsoForgetsWhatIsGone(t *testing.T) {
	t.Parallel()
	m := NewMaildir(t.TempDir())
	paths := writeMessages(t, filepath.Join(m.accountDir("example.com", "big"), "cur"), 3, "s", time.Now())
	if _, err := m.List("example.com", "big"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.List("example.com", "big"); err != nil {
		t.Fatal(err)
	}
	if m.hdrTotal != 2 {
		t.Errorf("cache holds %d entries for an inbox of 2 files", m.hdrTotal)
	}
}
