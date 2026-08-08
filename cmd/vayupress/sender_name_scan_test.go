// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// SECTION 4 PRE-RELEASE PASS — found by attacking the surface this release
// widened, which is what the pass is for.
//
// Opening send and draft to agency clients does not add a capability an
// untrusted principal lacked: a mailbox holder already reaches both through the
// mail-only path. What it does is put more principals on a path that costs more
// than it looks, in the composer's voice:
//
//	I am a customer of the studio with a mailbox on their box, which they also
//	use for thirty other clients. The composer autosaves my draft on a timer
//	while I type. Every autosave calls senderDisplayName, which did this:
//
//	    accs, _ := a.vayuMail.Accounts().List(ctx)   // every mail account
//	    for _, ac := range accs { if EqualFold(ac.Email, addr) ... }
//	    users, _ := a.userStore.List(ctx)            // every staff user
//	    for _, u := range users { if EqualFold(u.Email, addr) ... }
//
//	Two full table reads per keystroke-triggered save, through the same SQLite
//	handle that serves the website. I do not have to attack anything. I just
//	have to write a long email, and so does everyone else on the box.
//
// The same two scans run on every send. This is the RCPT-path finding from
// Section 2 in a different function, and it has the same fix: both stores
// already answer a single address. users.Store has GetByEmail; AccountStore has
// a row of scalar WHERE email=? lookups (RoleFor, SignatureFor, QuotaFor,
// HashFor) that FullNameFor now joins. CountForDomain's own comment made the
// argument first — listing every account on the install to filter in Go is
// "forty times the work on every mailbox creation" on a box carrying forty
// clients.
//
// Case handling is preserved rather than assumed: Create normalises the address
// before insert (normEmail) and nothing updates the column, so exact match on
// the stored value finds precisely what EqualFold over the whole table found.

// appWithNMailAccounts builds an app whose mail account store holds n accounts
// plus the fixture mailbox, and whose user store holds n users.
func appWithNMailAccounts(t *testing.T, n int) *App {
	t.Helper()
	app := appWithMailAccounts(t)
	app.userStore = usersStoreWith(t, n)
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := app.vayuMail.Accounts().Create(ctx,
			fmt.Sprintf("box%d@example.com", i), "x", fmt.Sprintf("Box %d", i), vmail.RoleMailbox); err != nil {
			t.Fatalf("seed mail account %d: %v", i, err)
		}
	}
	return app
}

// senderNameAllocs measures what one display-name resolution costs against
// stores holding n rows each.
//
// Allocations rather than wall-clock, for the reason the recipient-scan test
// gives: a timing assertion on a shared runner is a flaky test wearing a
// performance costume. Loading every row allocates per row, so growth is the
// signal — an indexed lookup does the same work at twenty rows and two thousand.
func senderNameAllocs(t *testing.T, n int) float64 {
	t.Helper()
	app := appWithNMailAccounts(t, n)
	ctx := context.Background()
	// An address in neither store: the deterministic worst case, and what a
	// mailbox on a secondary domain hits on every save.
	return testing.AllocsPerRun(20, func() {
		_ = app.senderDisplayName(ctx, "nobody-here@example.com")
	})
}

func TestResolvingASenderNameDoesNotScanBothTables(t *testing.T) {
	small := senderNameAllocs(t, 20)
	large := senderNameAllocs(t, 1000)

	t.Logf("allocations per sender-name resolution: %.0f at 20 rows, %.0f at 1000", small, large)

	if large > small*10 {
		t.Errorf("one sender-name resolution allocated %.0f against 1000 rows versus %.0f "+
			"against 20 — it reads the tables rather than indexing into them.\n\n"+
			"The composer autosaves a draft on a timer, and every save and every send "+
			"pays this twice, through the database handle that also serves the website. "+
			"Thirty mailbox holders writing mail is thirty concurrent full reads of every "+
			"account and every user on the install.", large, small)
	}
}

// THE CONTROL. The From header is what recipients see and what lands in Sent,
// so a faster lookup that stops finding names is a worse outcome than the scan.
func TestASenderNameIsStillFoundForEveryPrincipalItUsedToFind(t *testing.T) {
	app := appWithMailAccounts(t)
	app.userStore = usersStoreWith(t, 5)
	ctx := context.Background()

	// A mail account carries the name.
	if err := app.vayuMail.Accounts().Create(ctx, "Named.Box@Example.com", "x", "Named Box", vmail.RoleMailbox); err != nil {
		t.Fatalf("create account: %v", err)
	}
	// A CMS user with no mail account is the second source, and the fallback
	// order matters: the mail account's name wins when both exist.
	if _, err := app.userStore.Create(ctx, "Staff.Person@Example.com", "Staff Person", "a-long-password", "author"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	for addr, want := range map[string]string{
		"named.box@example.com":    "Named Box",
		"Named.Box@Example.com":    "Named Box",
		"NAMED.BOX@EXAMPLE.COM":    "Named Box",
		"staff.person@example.com": "Staff Person",
		"Staff.Person@Example.com": "Staff Person",
		"nobody-here@example.com":  "",
		"":                         "",
	} {
		if got := app.senderDisplayName(ctx, addr); got != want {
			t.Errorf("senderDisplayName(%q) = %q, want %q.\n\n"+
				"This name goes into the From header the recipient reads and into the "+
				"copy filed in Sent. Losing it to make the lookup faster is not a trade "+
				"worth making.", addr, got, want)
		}
	}
}
