// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// ownMailboxApp is a mail install with the mailboxes dana@example.com and
// eli@example.com and a users table the test fills.
func ownMailboxApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	a := calendarApp(t)
	if err := a.vayuMail.Accounts().Create(t.Context(), "eli@example.com", "x", "Eli", "mailbox"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE users(id TEXT PRIMARY KEY,email TEXT NOT NULL UNIQUE,name TEXT NOT NULL DEFAULT '',password_hash TEXT NOT NULL,role TEXT NOT NULL DEFAULT 'author',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,last_login DATETIME,avatar_url TEXT NOT NULL DEFAULT '',bio TEXT NOT NULL DEFAULT '',socials TEXT NOT NULL DEFAULT '{}',mail_address TEXT NOT NULL DEFAULT '',must_change_password INTEGER NOT NULL DEFAULT 0,username TEXT NOT NULL DEFAULT '',client_domain_id TEXT NOT NULL DEFAULT '');
		CREATE VIEW user_profiles AS SELECT id, email, name, role, avatar_url, bio, socials, mail_address, created_at, last_login, must_change_password AS must_change_signin, username, client_domain_id FROM users;`); err != nil {
		t.Fatal(err)
	}
	a.userStore = users.New(db)
	return a, db
}

func asAdminMailUser(u *users.User) *http.Request {
	return withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), u)
}

// TestAnAdministratorsOwnMailboxIsTheirsInMail — the mailbox at the address
// an administrator signs in with, which nobody assigned to them, is theirs:
// read as its owner, with the Calendar and Drive beside it and its own
// Settings. Someone else's mailbox is still an operator's read.
func TestAnAdministratorsOwnMailboxIsTheirsInMail(t *testing.T) {
	a, _ := ownMailboxApp(t)
	owner := &users.User{ID: "u1", Email: "dana@example.com", Role: users.RoleAdmin}
	req := asAdminMailUser(owner)

	if _, own := a.ownMailbox(req); own != "dana@example.com" {
		t.Fatalf("the administrator's own mailbox is %q, want the one at their address", own)
	}
	if rd := a.mailReader(req, ""); !rd.IsOwner() || rd.Key() != "dana" {
		t.Errorf("their own mail is read as owner=%v of %q", rd.IsOwner(), rd.Key())
	}
	if rd := a.mailReader(req, "dana"); !rd.IsOwner() {
		t.Error("their own mailbox named by ?user= is not an owner read")
	}
	if rd := a.mailReader(req, "eli"); rd.IsOwner() {
		t.Error("someone else's mailbox is read as its owner")
	}
	if got := a.holderPlaces(req); !got.Calendar || !got.Drive {
		t.Errorf("the administrator is offered %+v beside their own mail", got)
	}
	side := a.mailSide(req, a.mailReader(req, ""), "Inbox", "", nil)
	if !side.Own || !strings.Contains(mailPlaces(side), `href="/os/vayumail/calendar"`) || !strings.Contains(mailPlaces(side), `href="/os/vayumail/drive"`) {
		t.Errorf("Mail's top bar for the administrator's own mailbox: %s", mailPlaces(side))
	}
	if tools := mailTopTools(side, ""); !strings.Contains(tools, `href="/os/vayumail/settings"`) {
		t.Errorf("All settings for their own mailbox does not open their own Settings: %s", tools)
	}
	other := a.mailSide(req, a.mailReader(req, "eli"), "Inbox", "", nil)
	if other.Own || strings.Contains(mailPlaces(other), "calendar") {
		t.Errorf("someone else's mailbox offers its places: %s", mailPlaces(other))
	}
	if tools := mailTopTools(other, ""); !strings.Contains(tools, `href="/os/vayumail/accounts/settings?user=eli%40example.com"`) {
		t.Errorf("All settings in someone else's mailbox is not that mailbox's page: %s", tools)
	}
	// The Calendar opens for them, on their own mailbox.
	if rd, ok := a.calendarOwner(req); !ok || rd.Key() != "dana" {
		t.Errorf("the Calendar for the administrator: %v %q", ok, rd.Key())
	}
	if _, err := a.vayuMail.DriveList(vmail.ReadAsOwner("dana"), 0); err != nil {
		t.Fatal(err)
	}
}

// TestOnlyAnAdministratorIsGivenAMailboxByTheirAddress — a member whose
// site address matches a mailbox does not open it with their site password;
// an administrator does not take one assigned to someone else, or one that
// is switched off; and an assignment still wins over the address.
func TestOnlyAnAdministratorIsGivenAMailboxByTheirAddress(t *testing.T) {
	a, db := ownMailboxApp(t)
	for _, role := range []string{users.RoleEditor, users.RoleAuthor} {
		req := asAdminMailUser(&users.User{ID: "m-" + role, Email: "dana@example.com", Role: role})
		if _, own := a.ownMailbox(req); own != "" {
			t.Errorf("a %s whose address is a mailbox holds it: %q", role, own)
		}
		if got := a.holderPlaces(req); got.Calendar || got.Drive {
			t.Errorf("a %s whose address is a mailbox is offered %+v", role, got)
		}
	}
	owner := &users.User{ID: "u1", Email: "dana@example.com", Role: users.RoleAdmin}
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,mail_address) VALUES('u9','sam@home.test','x','dana@example.com')`); err != nil {
		t.Fatal(err)
	}
	if _, own := a.ownMailbox(asAdminMailUser(owner)); own != "" {
		t.Errorf("an administrator holds a mailbox assigned to another member: %q", own)
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id='u9'`); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().SetActive(t.Context(), "dana@example.com", false); err != nil {
		t.Fatal(err)
	}
	if _, own := a.ownMailbox(asAdminMailUser(owner)); own != "" {
		t.Errorf("an administrator holds a switched-off mailbox: %q", own)
	}
	if err := a.vayuMail.Accounts().SetActive(t.Context(), "dana@example.com", true); err != nil {
		t.Fatal(err)
	}
	// Their own record says so: assigned to them, it is still theirs.
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash,mail_address,role) VALUES('u1','dana@example.com','x','dana@example.com','admin')`); err != nil {
		t.Fatal(err)
	}
	if _, own := a.ownMailbox(asAdminMailUser(owner)); own != "dana@example.com" {
		t.Errorf("an administrator's own assigned mailbox: %q", own)
	}
	// An assignment wins over the address.
	if _, err := db.Exec(`UPDATE users SET mail_address='eli@example.com' WHERE id='u1'`); err != nil {
		t.Fatal(err)
	}
	if _, own := a.ownMailbox(asAdminMailUser(owner)); own != "eli@example.com" {
		t.Errorf("an administrator assigned another mailbox holds %q", own)
	}
	// When who holds it cannot be read, it is nobody's to take.
	if _, err := db.Exec(`UPDATE users SET mail_address='' WHERE id='u1'; ALTER TABLE users RENAME COLUMN mail_address TO gone;`); err != nil {
		t.Fatal(err)
	}
	if _, own := a.ownMailbox(asAdminMailUser(owner)); own != "" {
		t.Errorf("an administrator holds the mailbox at their address while who holds it cannot be read: %q", own)
	}
	if _, err := db.Exec(`ALTER TABLE users RENAME COLUMN gone TO mail_address`); err != nil {
		t.Fatal(err)
	}
	// A role taken away is taken away: the stored role decides, not the
	// session's copy of it.
	if _, err := db.Exec(`UPDATE users SET mail_address='', role='editor' WHERE id='u1'`); err != nil {
		t.Fatal(err)
	}
	if _, own := a.ownMailbox(asAdminMailUser(owner)); own != "" {
		t.Errorf("an administrator demoted to editor still holds the mailbox at their address: %q", own)
	}
}
