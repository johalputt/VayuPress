// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
)

// enrolApp is resetSessionApp with Members: one database for sessions, the
// mail engine and the member list, as an install has.
func enrolApp(t *testing.T) *App {
	t.Helper()
	a := resetSessionApp(t)
	a.members = members.New(dbpkg.DB)
	return a
}

const enrolPassword = "the-password-the-attacker-stole"

func consoleLogin(a *App, pass string) *httptest.ResponseRecorder {
	form := url.Values{"email": {"boss@example.com"}, "password": {pass}}
	req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.9:5000"
	rec := httptest.NewRecorder()
	a.handleOSLoginSubmit(rec, req)
	return rec
}

func listed(t *testing.T, a *App) bool {
	t.Helper()
	return a.members.Exists(context.Background(), "boss@example.com")
}

// The operator's report: people signed in from the website's login button
// with their mailbox, and never appeared in Members. Each web sign-in with a
// mailbox lists its holder, the console's button among them.
func TestEveryMailboxSignInListsItsHolder(t *testing.T) {
	t.Run("console login button", func(t *testing.T) {
		a := enrolApp(t)
		if rec := consoleLogin(a, enrolPassword); rec.Code != http.StatusSeeOther {
			t.Fatalf("the sign-in failed: %d", rec.Code)
		}
		if !listed(t, a) {
			t.Error("a mailbox signed in at the console's login button and is not in Members")
		}
	})
	t.Run("member portal", func(t *testing.T) {
		a := enrolApp(t)
		if rec := postVayuMailLogin(t, a, "198.51.100.7", "boss@example.com", enrolPassword); rec.Code != http.StatusOK {
			t.Fatalf("the sign-in failed: %d %s", rec.Code, rec.Body.String())
		}
		if !listed(t, a) {
			t.Error("a mailbox signed in at the member portal and is not in Members")
		}
	})
	t.Run("VayuMail app", func(t *testing.T) {
		a := enrolApp(t)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/members/vayumail-device",
			strings.NewReader(`{"email":"boss@example.com","password":"`+enrolPassword+`","device_name":"Phone","platform":"android"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "198.51.100.8:40000"
		rec := httptest.NewRecorder()
		a.handleMemberVayuMailDeviceRegister(rec, req)
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("the app sign-in failed: %d %s", rec.Code, rec.Body.String())
		}
		if !listed(t, a) {
			t.Error("a mailbox signed in from the VayuMail app and is not in Members")
		}
	})
}

// A member is made only by proof of the address: a wrong password lists no one.
func TestAWrongMailboxPasswordListsNoOne(t *testing.T) {
	a := enrolApp(t)
	consoleLogin(a, "a guess")
	if listed(t, a) {
		t.Error("a wrong mailbox password made a member")
	}
}

// Listing is the sign-in's side effect, not its condition: with no member
// list to write to, the mailbox still signs in.
func TestASignInDoesNotDependOnTheMemberList(t *testing.T) {
	a := resetSessionApp(t)
	if rec := consoleLogin(a, enrolPassword); rec.Code != http.StatusSeeOther {
		t.Errorf("with no member list the mailbox could not sign in: %d", rec.Code)
	}
}

// The catch-up at start lists the mailboxes still signed in from before, and
// no one else: not an expired session, not a mailbox since deleted.
func TestTheCatchUpListsOnlyMailboxesStillSignedIn(t *testing.T) {
	a := enrolApp(t)
	ctx := context.Background()
	if _, err := a.sessions.Create(ctx, "vmail:boss@example.com"); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	for _, row := range []struct{ token, user string }{
		{"expired", "vmail:old@example.com"},
		{"ghost", "vmail:ghost@example.com"},
	} {
		if _, err := dbpkg.DB.ExecContext(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)`, row.token, row.user, past); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbpkg.DB.ExecContext(ctx, `UPDATE sessions SET expires_at=? WHERE token_hash='ghost'`,
		time.Now().UTC().Add(time.Hour).Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().Create(ctx, "old@example.com", "x", "Old", "mailbox"); err != nil {
		t.Fatal(err)
	}

	if n := a.enrolSignedInMailboxes(ctx, dbpkg.DB); n != 1 {
		t.Errorf("the catch-up listed %d, want 1", n)
	}
	if !listed(t, a) {
		t.Error("a mailbox still signed in was not listed")
	}
	if a.members.Exists(ctx, "old@example.com") {
		t.Error("a mailbox whose session expired was listed")
	}
	if a.members.Exists(ctx, "ghost@example.com") {
		t.Error("a session for a mailbox that no longer exists was listed")
	}

	// And main runs it, after the mail engine it asks is up.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?s)a\.bootVayuOS\(\)\s*\n\s*if n := a\.enrolSignedInMailboxes\(context\.Background\(\), dbpkg\.DB\)`).Match(src) {
		t.Error("main does not run the catch-up after the mail engine starts")
	}
}
