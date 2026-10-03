// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// The Outbox is a list of the delivery queue (8.12). Each assertion states
// the markup a rule forbids, one seed per rule.
func TestOutboxIsAListOfItsQueue(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()

	// An empty queue is one sentence, not a table of headers over nothing.
	empty := a.vayuOutboxBody(ctx, "")
	if !strings.Contains(empty, "Nothing has been sent yet") || strings.Contains(empty, "<table") {
		t.Errorf("an empty queue should be its empty state alone:\n%s", empty)
	}

	if _, err := a.vayuMail.SendMail(ctx, "dana@example.com", []string{"someone@elsewhere.test"}, "Hello", "", "Hello", ""); err != nil {
		t.Fatalf("send: %v", err)
	}
	body := a.vayuOutboxBody(ctx, "")
	if !strings.Contains(body, "<table") || !strings.Contains(body, "1 waiting") {
		t.Fatalf("one waiting message should be a row and a count:\n%s", body)
	}
	// A count of nothing is left out of the sentence.
	for _, zero := range []string{"0 failed", "0 delivered"} {
		if strings.Contains(body, zero) {
			t.Errorf("the counts say %q; a zero is left out", zero)
		}
	}
	// State is a dot and a word, never a filled badge.
	if strings.Contains(body, `class="badge`) || !strings.Contains(body, `sa-dot--neutral`) {
		t.Errorf("the message's state should be a dot and a word:\n%s", body)
	}
	// No card around the table: a box never sits inside another.
	if strings.Contains(body, `class="card`) {
		t.Error("the queue's table sits in a card")
	}
}

// The queue is the whole server's: anyone else is taken to their own Sent
// folder, which is all the page used to tell them.
func TestOutboxSendsNonAdminsToTheirSentFolder(t *testing.T) {
	a := appWithMailAccounts(t)
	u := &users.User{ID: "a1", Email: "author@example.com", Role: users.RoleAuthor}
	rec := httptest.NewRecorder()
	a.handleVayuOSSent(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/sent", nil), u))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/os/vayumail/inbox?folder=Sent" {
		t.Errorf("status %d, Location %q; want 303 to the Sent folder", rec.Code, rec.Header().Get("Location"))
	}
}

// Connect a device is a status page: whether apps can connect, said first.
// The test install binds no listener, so that is what it must say.
func TestConnectSaysWhetherAppsCanConnect(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	rec := httptest.NewRecorder()
	a.handleVayuOSConnect(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/connect", nil), admin))
	body := rec.Body.String()
	if !strings.Contains(body, `data-page-kind="status"`) || !strings.Contains(body, "No mail service is listening") {
		t.Errorf("Connect does not open on whether apps can connect")
	}
	if strings.Contains(body, `class="card`) || strings.Contains(body, `class="badge`) {
		t.Error("Connect still carries cards or filled badges")
	}
	// The create form rises in its sheet; the page itself holds no form.
	sheet := strings.Index(body, `<dialog class="sa-sheet" id="new-app-password"`)
	form := strings.Index(body, `data-apppw-create`)
	if sheet < 0 || form < sheet {
		t.Error("the app-password form is not inside its sheet")
	}
}
