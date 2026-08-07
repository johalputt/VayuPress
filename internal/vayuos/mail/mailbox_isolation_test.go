// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SECTION 2 AUDIT — cross-mailbox isolation. This one is a CLEAN result, and it
// is written down as a test rather than a sentence because "we checked and it
// was fine" is worth nothing a year from now.
//
// The attack, in the attacker's voice:
//
//	I have a mailbox here. I want yours. The IMAP session knows who I am, so I
//	will not argue with it — I will argue with the one string it lets me choose:
//	the folder name.
//
//	  SELECT ../../bob@example.com/
//	  SELECT .../../../etc
//	  COPY 1 ../bob/INBOX
//	  APPEND ../../../../root/.ssh/authorized_keys
//
// It does not work, and the reason is worth stating precisely, because a weaker
// version of the same code would fail: canonicalFolder is an ALLOWLIST. Anything
// that is not a known folder name collapses to "Inbox" — my own inbox. It never
// reaches the filesystem as a path component at all.
//
// The account identity is not attackable either: every Maildir call in imapd.go
// passes sess.authedDomain and sess.authedUser, which are set from the
// AUTHENTICATED login and are never read from a client command.
//
// These tests exist so that stays true. Replace the allowlist with a sanitiser —
// the obvious "improvement" when someone wants custom folders — and they fail.

// isolationDirs returns a Maildir rooted in a temp dir plus the on-disk paths
// for two separate accounts.
func isolationDirs(t *testing.T) (md *Maildir, base, aliceDir, bobDir string) {
	t.Helper()
	base = t.TempDir()
	md = NewMaildir(base)
	if err := md.CreateAll("example.com", "alice"); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := md.CreateAll("example.com", "bob"); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	return md, base, filepath.Join(base, "example.com", "alice"), filepath.Join(base, "example.com", "bob")
}

// TestAFolderNameCannotEscapeTheAccountDirectory is the finding-shaped test: the
// folder is the only path component a client picks, so it is the only place a
// traversal can start.
func TestAFolderNameCannotEscapeTheAccountDirectory(t *testing.T) {
	md, base, aliceDir, _ := isolationDirs(t)

	hostile := []string{
		"../bob",
		"../../example.com/bob",
		"../../../etc",
		"..",
		"./../bob",
		`..\bob`,                // windows-style, in case a separator slips through
		"Sent/../../bob",        // a legitimate prefix followed by an escape
		"INBOX/../../../../tmp", // and the same through the folder that maps to root
		strings.Repeat("../", 12) + "root",
	}

	for _, folder := range hostile {
		dir := md.folderDir("example.com", "alice", folder)

		abs, err := filepath.Abs(dir)
		if err != nil {
			t.Fatalf("abs(%q): %v", dir, err)
		}
		absAlice, _ := filepath.Abs(aliceDir)
		if abs != absAlice && !strings.HasPrefix(abs, absAlice+string(filepath.Separator)) {
			t.Errorf("folder %q resolved to %q, which is outside alice's account directory %q.\n\n"+
				"The folder name is the one path component an authenticated client chooses. "+
				"Escaping it means reading, writing or deleting another mailbox's mail with "+
				"nothing but a SELECT.", folder, abs, absAlice)
		}
		// Belt and braces: it must also stay inside the Maildir root, so a bug in
		// the account-directory computation cannot be the thing that saves it.
		absBase, _ := filepath.Abs(base)
		if !strings.HasPrefix(abs, absBase+string(filepath.Separator)) && abs != absBase {
			t.Errorf("folder %q escaped the Maildir root entirely, to %q", folder, abs)
		}
	}
}

// The mechanism, asserted directly: unknown names collapse to Inbox. If this
// ever becomes a sanitiser instead of an allowlist, the test above becomes the
// only thing standing between a client and the filesystem — so the property is
// pinned here too, where the failure message can say what changed.
func TestUnknownFolderNamesCollapseToInbox(t *testing.T) {
	for _, name := range []string{"../bob", "Whatever", "", "..", "x/y/z", ".hidden"} {
		if got := canonicalFolder(name); !isStandardFolder(got) {
			t.Errorf("canonicalFolder(%q) = %q, which is not one of the standard folders.\n\n"+
				"This function is an allowlist on purpose: an arbitrary string must never "+
				"become a path component. Sanitising instead of allowlisting is how "+
				"traversal bugs get reintroduced.", name, got)
		}
	}
	// And the standard names must survive, or the mail client loses its folders.
	for _, f := range StandardFolders {
		if got := canonicalFolder(strings.ToLower(f)); got != f {
			t.Errorf("canonicalFolder(%q) = %q, want %q — a real folder stopped resolving",
				strings.ToLower(f), got, f)
		}
	}
}

// safeSegment guards the OTHER two components. They come from the authenticated
// login rather than a command, so this is depth — but a login domain is
// attacker-influenced on a multi-domain install, and the cost of being wrong is
// the whole mail store.
func TestAccountPathComponentsCannotEscape(t *testing.T) {
	md, base, _, _ := isolationDirs(t)
	absBase, _ := filepath.Abs(base)

	for _, tc := range []struct{ domain, user string }{
		{"../..", "alice"},
		{"example.com", "../../bob"},
		{"../../../etc", "passwd"},
		{"", ""},
		{"/", "/"},
	} {
		dir := md.accountDir(tc.domain, tc.user)
		abs, _ := filepath.Abs(dir)
		if !strings.HasPrefix(abs, absBase+string(filepath.Separator)) {
			t.Errorf("accountDir(%q, %q) = %q, outside the Maildir root %q",
				tc.domain, tc.user, abs, absBase)
		}
	}
}

// The control that makes all of the above meaningful: two accounts must actually
// be separate on disk, and one must not be able to read the other's message.
// Without this, a Maildir that put everyone in one directory would pass every
// traversal test above and still leak all the mail.
func TestTwoAccountsDoNotShareAMessageStore(t *testing.T) {
	md, _, aliceDir, bobDir := isolationDirs(t)
	if aliceDir == bobDir {
		t.Fatal("alice and bob resolve to the same directory")
	}

	if _, err := md.Deliver("example.com", "bob", []byte("From: x@y\r\n\r\nbob's private mail")); err != nil {
		t.Fatalf("deliver to bob: %v", err)
	}

	// Alice's inbox must be empty, and must stay empty however she names it.
	for _, folder := range []string{"Inbox", "", "../bob", "../../example.com/bob"} {
		msgs, _ := md.ListFolder("example.com", "alice", folder)
		if len(msgs) != 0 {
			t.Errorf("alice sees %d message(s) in folder %q — bob's mail is reachable from "+
				"alice's session", len(msgs), folder)
		}
	}

	// And bob really does have the message, so the test above is not passing
	// because delivery silently failed.
	if msgs, _ := md.ListFolder("example.com", "bob", "Inbox"); len(msgs) != 1 {
		t.Fatalf("bob has %d messages, want 1 — the fixture did not deliver, so the "+
			"isolation assertions above proved nothing", len(msgs))
	}

	// Nothing was created outside the two account directories.
	entries, err := os.ReadDir(filepath.Dir(aliceDir))
	if err != nil {
		t.Fatalf("read domain dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "alice" && e.Name() != "bob" {
			t.Errorf("unexpected directory %q created under the domain root", e.Name())
		}
	}
}
