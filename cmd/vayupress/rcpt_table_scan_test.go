// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"

	_ "github.com/mattn/go-sqlite3"
)

// SECTION 2 AUDIT FINDING — an unauthenticated compute and database sink on
// port 25, in the attacker's voice:
//
//	Port 25 has to answer strangers. That is what an MX is for, so I do not
//	need an account, a password or a single valid address.
//
//	I open a connection, send MAIL FROM, and then send RCPT TO a hundred times
//	— maxRcptsPerTxn lets me, and none of the addresses has to exist. Each one
//	makes you run isLocalRecipient, which asks the bridge, which calls
//	GetUserByEmail, which does this:
//
//	    users, err := b.app.userStore.List(ctx)   // SELECT every column, every row
//	    for _, u := range users { if strings.EqualFold(u.Email, addr) ... }
//
//	You load your ENTIRE users table and walk it, per recipient. A hundred
//	recipients on one small transaction is a hundred full table reads and a
//	hundred full allocations of every user record you have. I can hold sixteen
//	connections from one host and 256 across the install.
//
//	Every one of those reads goes through the same SQLite handle that serves the
//	website and the blog. I do not have to knock you over to hurt you.
//
// The fix is not new machinery. users.Store already has GetByEmail — an indexed
// equality lookup on a UNIQUE column — and vayuos.go already uses it a few
// functions away in twoFactorEnabled. The recipient path just never did.
//
// Case handling is preserved rather than assumed: every INSERT into users
// lowercases the address (Create and CreateClient both do, and nothing updates
// the column), so exact match on the stored value finds precisely what
// EqualFold over the whole table found.

// usersStoreWith builds a user store holding n accounts on its own scratch
// database. The user store is independent of the mail engine's storage, so this
// needs no accessor on the engine — and adding one for a test's convenience
// would be API that exists for nobody.
func usersStoreWith(t *testing.T, n int) *users.Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open users db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// The SHIPPED migrations, not a restatement of them. A hand-written users
	// table is missing the profile columns that profileCols selects, so List and
	// GetByEmail both fail instantly — and a scan test whose scan never happens
	// passes against the very code it is meant to catch. That is exactly what the
	// first version of this file did.
	for _, m := range []string{
		"020-users-sessions", "038-user-profiles", "039-user-mailbox",
		"050-user-must-change-password", "051-user-username", "079-client-domain",
	} {
		applyMigration(t, db, m)
	}
	// Inserted directly: Create runs Argon2id per account, and this needs a
	// populated table rather than a realistic enrolment.
	for i := 0; i < n; i++ {
		if _, err := db.Exec(`INSERT OR IGNORE INTO users(id,email,name,password_hash,role) VALUES(?,?,?,?,?)`,
			fmt.Sprintf("u%d", i), fmt.Sprintf("user%d@example.com", i), "U", "x", "author"); err != nil {
			t.Fatalf("seed user %d: %v", i, err)
		}
	}
	return users.New(db)
}

// applyMigration runs one shipped migration file against db.
func applyMigration(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "db", "migrations", name+".up.sql"))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		// Re-applied columns are tolerated: these migrations are cumulative and a
		// couple add a column another already created.
		if _, err := db.Exec(line); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			t.Fatalf("migration %s: %v (%s)", name, err, line)
		}
	}
}

// recipientCheckAllocs measures the allocations one RCPT-time recipient check
// costs against a table holding n users.
//
// Allocations rather than wall-clock on purpose: a timing assertion on a shared
// CI runner is a flaky test wearing a performance costume, while allocation
// counts are deterministic. Loading every row allocates per row, so the shape of
// the growth is the whole signal — an indexed lookup does the same work whether
// the table holds twenty users or two thousand.
func recipientCheckAllocs(t *testing.T, n int) float64 {
	t.Helper()
	app := appWithMailAccounts(t)
	app.userStore = usersStoreWith(t, n)
	b := &vayuMailBridge{app: app}

	// A recipient that does not exist: the worst case, and exactly what a
	// dictionary attack sends. It also has to walk the entire table before it
	// can conclude anything.
	return testing.AllocsPerRun(20, func() {
		_ = b.IsLocalRecipient("nobodyhere@example.com")
	})
}

func TestARecipientCheckDoesNotScanTheWholeUsersTable(t *testing.T) {
	small := recipientCheckAllocs(t, 20)
	large := recipientCheckAllocs(t, 2000)

	t.Logf("allocations per recipient check: %.0f with 20 users, %.0f with 2000", small, large)

	// A hundredfold table growing the per-check cost even tenfold means the
	// lookup is reading the table rather than indexing into it. An indexed
	// lookup is flat, so this threshold is generous and still unmissable.
	if large > small*10 {
		t.Errorf("one recipient check allocated %.0f against 2000 users versus %.0f against 20 "+
			"— it grows with the size of the table.\n\n"+
			"Port 25 answers strangers by design, and maxRcptsPerTxn allows 100 recipients "+
			"per transaction. Anyone can turn a small SMTP conversation into a hundred full "+
			"reads of every user record on the install, through the same database handle "+
			"that serves the website.", large, small)
	}
}

// THE CONTROL, and the reason the fix is an exact match rather than a scan:
// the check must still find the people it found before. Addresses are stored
// lowercased, and a sending server may present any capitalisation it likes.
func TestRecipientLookupStillFindsUsersWhateverTheCase(t *testing.T) {
	app := appWithMailAccounts(t)
	store := usersStoreWith(t, 5)
	if _, err := store.Create(context.Background(), "Real.Person@Example.com", "Real", "a-long-password", "author"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	app.userStore = store
	b := &vayuMailBridge{app: app}

	for _, addr := range []string{
		"real.person@example.com",
		"Real.Person@Example.com",
		"REAL.PERSON@EXAMPLE.COM",
	} {
		if !b.IsLocalRecipient(addr) {
			t.Errorf("RCPT TO:<%s> was refused for a real account.\n\n"+
				"A sending MTA capitalises the address however the sender's address book "+
				"holds it. Refusing it bounces their mail.", addr)
		}
	}
	if b.IsLocalRecipient("definitely-not-here@example.com") {
		t.Error("an address with no account was accepted — the check stopped discriminating")
	}
	// A domain this install does not serve is still refused, whatever exists.
	if b.IsLocalRecipient("real.person@somewhere-else.test") {
		t.Error("a recipient on an unserved domain was accepted")
	}
}
