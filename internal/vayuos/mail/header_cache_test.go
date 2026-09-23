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
