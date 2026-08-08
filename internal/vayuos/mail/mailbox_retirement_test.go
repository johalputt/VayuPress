// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SECTION 4 CARRY-FORWARD — deleting a mailbox left every message on disk.
//
// Raised three times during the audit and deliberately left to the operator,
// because the obvious fix destroys mail and destroying mail has no undo. In the
// voice of whoever gets the address next:
//
//	I am the new office manager. My predecessor left; you deleted their
//	mailbox and made me info@theclient.example on the same box.
//
//	Their credentials really are gone — I checked, none of them authenticate.
//	But Delete only ever touched SQLite. The Maildir at
//	<base>/theclient.example/info/ was never touched, and the moment you
//	created my account IMAP handed me the directory that was already there.
//
//	I am now reading their salary negotiation, their doctor, and the note from
//	the lawyer. Nobody granted me anything. The address was reissued and the
//	mail came with it.
//
// This is the ordinary case, not an exotic one: info@, accounts@, admin@ and
// support@ are exactly the addresses a business reissues when someone leaves —
// and on an agency install a whole domain can change hands.
//
// THE FIX IS RETIREMENT, NOT DELETION, and that is a deliberate choice. The hole
// is INHERITANCE — a new holder reading old mail — and moving the directory out
// of the delivery tree closes it completely. Erasing the messages would close it
// too, and would also destroy the only copy of a mailbox someone may have
// deleted by mistake, or that the operator is required to retain. The narrowest
// rule that closes the hole wins, and this one costs nobody their mail.
//
// The retired tree lives OUTSIDE the Maildir base rather than as a dot-directory
// inside it. Inside, every path component passes through safeSegment, which maps
// a domain to a single lowercased segment — so a domain literally named
// ".retired" would land on top of the retired tree. Outside, no domain can name
// it at any capitalisation.

// retirementEngine builds an engine over a temp Maildir and account store.
func retirementEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "maildir")
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(base), accounts: aliasTestStore(t)}
	return e, base
}

// countMessages returns how many message files sit under an account's folders.
func countMessages(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil //nolint:nilerr // a missing tree counts as zero, which is the question
		}
		n++
		return nil
	})
	return n
}

func TestAReissuedAddressDoesNotInheritThePreviousHoldersMail(t *testing.T) {
	e, base := retirementEngine(t)
	ctx := context.Background()
	const addr = "info@example.com"

	if err := e.accounts.Create(ctx, addr, "hash", "Leaver", RoleMailbox); err != nil {
		t.Fatalf("create the first holder: %v", err)
	}
	if _, err := e.maildir.Deliver("example.com", "info", []byte(
		"From: lawyer@firm.example\r\nSubject: private\r\n\r\nthe settlement figure")); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	if _, err := e.DeleteMailbox(ctx, addr); err != nil {
		t.Fatalf("delete the mailbox: %v", err)
	}

	// The new holder. Same address, same Maildir key.
	if err := e.accounts.Create(ctx, addr, "hash2", "Successor", RoleMailbox); err != nil {
		t.Fatalf("create the successor: %v", err)
	}
	if err := e.maildir.Create("example.com", "info"); err != nil {
		t.Fatalf("provision the successor's maildir: %v", err)
	}

	live := filepath.Join(base, "example.com", "info")
	if n := countMessages(t, live); n != 0 {
		t.Errorf("the successor's mailbox contains %d message(s) they were never sent.\n\n"+
			"Deleting the account removed every credential and every database row and "+
			"left the Maildir in place, so reissuing the address handed the new holder "+
			"the previous holder's mail. info@, accounts@ and support@ are exactly the "+
			"addresses a business reissues.", n)
	}
}

// Retirement must MOVE the mail, not erase it. A fix that silently destroys the
// only copy of a mailbox is a worse outcome than the inheritance it closes.
func TestRetiredMailIsSetAsideRatherThanDestroyed(t *testing.T) {
	e, base := retirementEngine(t)
	ctx := context.Background()
	const addr = "leaver@example.com"

	if err := e.accounts.Create(ctx, addr, "hash", "Leaver", RoleMailbox); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, body := range []string{"one", "two", "three"} {
		if _, err := e.maildir.Deliver("example.com", "leaver", []byte("Subject: x\r\n\r\n"+body)); err != nil {
			t.Fatalf("deliver: %v", err)
		}
	}

	retired, err := e.DeleteMailbox(ctx, addr)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if retired == "" {
		t.Fatal("DeleteMailbox reported no retirement path for a mailbox that held mail.\n\n" +
			"The operator is told where the mail went. An empty answer means either it " +
			"was destroyed or it is still in the delivery tree, and they cannot tell which.")
	}
	if n := countMessages(t, retired); n != 3 {
		t.Errorf("the retired copy holds %d of 3 messages (%s) — retirement lost mail", n, retired)
	}

	// Outside the Maildir base, so no domain segment can address it.
	if strings.HasPrefix(filepath.Clean(retired), filepath.Clean(base)+string(filepath.Separator)) {
		t.Errorf("the retired tree %q sits inside the Maildir base %q.\n\n"+
			"Every path component under the base passes through safeSegment, which "+
			"reduces a domain to one lowercased segment — so a domain named after this "+
			"directory would be delivered straight into it.", retired, base)
	}
}

// Deleting a mailbox that never received anything is ordinary, not an error, and
// must not invent a retirement path.
func TestDeletingAnEmptyMailboxIsNotAnError(t *testing.T) {
	e, _ := retirementEngine(t)
	ctx := context.Background()
	const addr = "never-used@example.com"

	if err := e.accounts.Create(ctx, addr, "hash", "Nobody", RoleMailbox); err != nil {
		t.Fatalf("create: %v", err)
	}
	retired, err := e.DeleteMailbox(ctx, addr)
	if err != nil {
		t.Fatalf("deleting a mailbox with no mail failed: %v", err)
	}
	if retired != "" {
		t.Errorf("reported a retirement path %q for a mailbox that never received mail", retired)
	}
}

// The account rows must go even when there is mail to move, and they must go
// FIRST — while the account exists, SMTP still delivers into the directory being
// moved, and a message landing in that window is inherited by the next holder.
func TestTheAccountIsGoneBeforeTheMailIsMoved(t *testing.T) {
	e, _ := retirementEngine(t)
	ctx := context.Background()
	const addr = "gone@example.com"

	if err := e.accounts.Create(ctx, addr, "hash", "Gone", RoleMailbox); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.maildir.Deliver("example.com", "gone", []byte("Subject: x\r\n\r\nbody")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if _, err := e.DeleteMailbox(ctx, addr); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if h := e.accounts.HashFor(ctx, addr); h != "" {
		t.Error("the account still authenticates after DeleteMailbox — the credential " +
			"outlived the mailbox, which is the defect this whole audit keeps finding")
	}
	if r := e.accounts.RoleFor(ctx, addr); r != "" {
		t.Errorf("the account row survives with role %q", r)
	}
}

// Two mailboxes retired from the same address (deleted, recreated, deleted) must
// not overwrite each other — the second retirement would otherwise destroy the
// first holder's mail, which is the thing retirement exists to avoid.
func TestRetiringTheSameAddressTwiceKeepsBothCopies(t *testing.T) {
	e, _ := retirementEngine(t)
	ctx := context.Background()
	const addr = "twice@example.com"

	var paths []string
	for _, body := range []string{"first holder", "second holder"} {
		if err := e.accounts.Create(ctx, addr, "hash", "H", RoleMailbox); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := e.maildir.Deliver("example.com", "twice", []byte("Subject: x\r\n\r\n"+body)); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		p, err := e.DeleteMailbox(ctx, addr)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if p == "" {
			t.Fatal("no retirement path")
		}
		paths = append(paths, p)
	}
	if paths[0] == paths[1] {
		t.Fatalf("both retirements landed on %s — the second overwrote the first", paths[0])
	}
	for i, p := range paths {
		if n := countMessages(t, p); n != 1 {
			t.Errorf("retirement %d (%s) holds %d messages, want 1 — a later deletion "+
				"destroyed an earlier holder's mail", i, p, n)
		}
	}
}
