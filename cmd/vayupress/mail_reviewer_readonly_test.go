// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// The console offers "Reviewer — read-only, mail only". The engine now refuses
// a reviewer's send, delete and move (internal/vayuos/mail/readonly_role_test.go
// holds the protocols). These hold the webmail: the refusal is said in the
// operator's words, and a reviewer is not offered the controls that would be
// refused. Every assertion is paired with the same request as a mailbox-role
// holder, so a page that hid the controls from everyone would fail too.

// reviewerApp is a started mail engine holding dana@example.com with role, one
// message in her inbox, and a session user whose mailbox is hers.
func reviewerApp(t *testing.T, role string) (*App, *users.User, string) {
	t.Helper()
	a := appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	if err := a.vayuMail.Accounts().SetRole(context.Background(), "dana@example.com", role); err != nil {
		t.Fatalf("set role: %v", err)
	}
	id, err := a.vayuMail.DeliverInbound("x@example.net", "dana@example.com",
		[]byte("From: x@example.net\r\nTo: dana@example.com\r\nSubject: Keep me\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	holder := &users.User{ID: "u-dana", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	return a, holder, id
}

func TestAReviewerIsRefusedASendInTheirOwnWords(t *testing.T) {
	for role, readOnly := range map[string]bool{vmail.RoleReviewer: true, vmail.RoleMailbox: false} {
		a, holder, _ := reviewerApp(t, role)
		req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/send",
			strings.NewReader(`{"To":"bob@example.net","Subject":"s","Body":"b"}`)), holder)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.handleVayuOSSend(rec, req)
		refused := rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), `"read-only"`)
		if refused != readOnly {
			t.Errorf("send as %s: %d %s; refused as read-only = %v, want %v", role, rec.Code, rec.Body.String(), refused, readOnly)
		}
	}
}

func TestAReviewerIsNotOfferedWhatWouldBeRefused(t *testing.T) {
	for role, readOnly := range map[string]bool{vmail.RoleReviewer: true, vmail.RoleMailbox: false} {
		a, holder, id := reviewerApp(t, role)
		rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder), "")

		// The list's toolbar and bulk bar.
		inbox := a.vayuInboxBody(rd, "Inbox", 0)
		for _, control := range []string{`/os/vayumail/compose?user=`, `{"action":"delete"}`, `{"action":"move"}`} {
			if strings.Contains(inbox, control) == readOnly {
				t.Errorf("inbox as %s: offers %s = %v", role, control, !readOnly)
			}
		}
		// The reader, both as a pane and as its own page.
		for _, pane := range []bool{true, false} {
			card, _ := a.vayuReaderCard(rd, "Inbox", id, pane, false, false)
			for _, control := range []string{`compose?reply=1`, `compose?forward=1`, ` Delete</button>`, ` Trash</button>`} {
				if strings.Contains(card, control) == readOnly {
					t.Errorf("reader (pane=%v) as %s: offers %s = %v", pane, role, control, !readOnly)
				}
			}
		}
		// The compose page itself.
		rec := httptest.NewRecorder()
		a.handleVayuOSCompose(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/compose", nil), holder))
		if got := strings.Contains(rec.Body.String(), "This mailbox is read-only"); got != readOnly {
			t.Errorf("compose page as %s: says read-only = %v", role, got)
		}
	}
}

// The rail's Compose link for a mail-only session, from the session's settings
// as the shell builds them.
func TestAReviewerRailHasNoCompose(t *testing.T) {
	for role, readOnly := range map[string]bool{vmail.RoleReviewer: true, vmail.RoleMailbox: false} {
		a, holder, _ := reviewerApp(t, role)
		ctx := context.WithValue(context.WithValue(context.Background(), ctxUserKey, holder), ctxMailOnlyKey, true)
		s := a.getOSSettings(ctx)
		if s.MailReadOnly != readOnly {
			t.Fatalf("session settings for a %s: MailReadOnly = %v", role, s.MailReadOnly)
		}
		compose := false
		for _, app := range saVisibleApps(s) {
			for _, sec := range app.Sections {
				compose = compose || sec.Href == "/os/vayumail/compose"
			}
		}
		if compose == readOnly {
			t.Errorf("rail for a %s: Compose shown = %v", role, compose)
		}
	}
}

// The delete and move paths a reviewer could still reach by hand say why they
// were refused, and never report a change that did not happen.
func TestAReviewerDeleteIsRefusedHonestlyOnEveryPath(t *testing.T) {
	a, holder, id := reviewerApp(t, vmail.RoleReviewer)

	// The reader pane used to answer "Message deleted." whatever happened.
	form := url.Values{"user": {"dana"}, "folder": {"Inbox"}, "id": {id}, "delete": {"1"}}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/message/pane-action", strings.NewReader(form.Encode())), holder)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSMessagePaneAction(rec, req)
	if body := rec.Body.String(); strings.Contains(body, "Message deleted.") || !strings.Contains(body, "read-only") {
		t.Errorf("pane delete as a reviewer said:\n%s", body)
	}

	// The JSON action: a refusal, not a 500 carrying the engine's error.
	req = withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/message/action",
		strings.NewReader(`{"user":"dana","id":"`+id+`","folder":"Inbox","delete":true}`)), holder)
	rec = httptest.NewRecorder()
	a.handleVayuOSMessageAction(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"read-only"`) {
		t.Errorf("message/action delete as a reviewer: %d %s", rec.Code, rec.Body.String())
	}

	// The bulk bar: the toast is told the cause instead of guessing at one.
	form = url.Values{"user": {"dana"}, "folder": {"Inbox"}, "id": {id}, "action": {"delete"}}
	req = withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/inbox/action", strings.NewReader(form.Encode())), holder)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	a.handleVayuOSInboxAction(rec, req)
	if trig := rec.Header().Get("HX-Trigger"); !strings.Contains(trig, `"readonly":true`) {
		t.Errorf("inbox/action delete as a reviewer: HX-Trigger %q, want readonly:true", trig)
	}

	// And the message is still there.
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
	if _, err := a.vayuMail.ReadFolderMessage(rd, "Inbox", id); err != nil {
		t.Errorf("after three refused deletes the message is gone: %v", err)
	}
}
