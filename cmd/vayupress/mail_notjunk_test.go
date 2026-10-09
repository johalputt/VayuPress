// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// junkFrom is a message the built-in filter files as junk, from sender.
func junkFrom(sender string, extra string) []byte {
	return []byte("From: Deals <" + sender + ">\r\nTo: dana@example.com\r\nSubject: ACT NOW limited offer!!!\r\nDate: Fri, 3 Oct 2026 09:00:00 +0000\r\n" + extra +
		"\r\nMake money fast, 100% guaranteed, $$$ refinance pre-approved, click now!!!\r\n")
}

func folderIDs(t *testing.T, a *App, rd vmail.Reader, folder string) []string {
	t.Helper()
	msgs, err := a.vayuMail.ListFolder(rd, folder)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	return ids
}

// "Not junk" (pipeline 3ab): the message goes back to the Inbox and the
// sender joins the mailbox's contacts.
func TestNotJunkRescuesTheMessageAndItsSender(t *testing.T) {
	if !vmail.ScoreSpam(junkFrom("deals@shop.example", "")).IsSpam {
		t.Fatal("the seed is not junk to the filter, so nothing below would test anything")
	}
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder), "")
	id, err := a.vayuMail.DeliverInbound("deals@shop.example", "dana@example.com", junkFrom("deals@shop.example", ""))
	if err != nil {
		t.Fatal(err)
	}
	if ids := folderIDs(t, a, rd, "Junk"); len(ids) != 1 || ids[0] != id {
		t.Fatalf("the seed was not filed as junk: %v", ids)
	}
	// The reader in Junk offers it, on !, and nothing files it as junk again.
	card, _ := a.vayuReaderCard(rd, "Junk", id, readerView{Pane: true})
	if !strings.Contains(card, `aria-label="Not junk"`) || !strings.Contains(card, `aria-keyshortcuts="!"`) || strings.Contains(card, `aria-label="Junk"`) {
		t.Error("the reader in Junk does not offer Not junk on !")
	}
	form := url.Values{"user": {"dana"}, "folder": {"Junk"}, "id": {id}, "notjunk": {"1"}}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/message/pane-action", strings.NewReader(form.Encode())), holder)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSMessagePaneAction(rec, req)
	if !strings.Contains(rec.Body.String(), "deals@shop.example is no longer filed as junk") {
		t.Errorf("Not junk does not say what it did: %s", rec.Body.String())
	}
	if len(folderIDs(t, a, rd, "Junk")) != 0 || len(folderIDs(t, a, rd, "Inbox")) != 2 { // the seed account's own message, and this
		t.Errorf("the message did not go back to the Inbox: junk %v, inbox %v", folderIDs(t, a, rd, "Junk"), folderIDs(t, a, rd, "Inbox"))
	}
	if !a.vayuMail.Accounts().HasContact(context.Background(), "dana@example.com", "deals@shop.example") {
		t.Fatal("the sender was not remembered")
	}
	// What delivery then does with the sender's mail, and when it believes
	// the From, is the engine's: TestAContactIsTrustedOnlyWhenAuthenticated.
}

// A read-only mailbox can neither move the message nor add the contact, and
// is not offered Not junk.
func TestAReviewerCannotSayNotJunk(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleReviewer)
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder), "")
	id, err := a.vayuMail.DeliverInbound("deals@shop.example", "dana@example.com", junkFrom("deals@shop.example", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuMail.NotJunk(rd, id); err == nil {
		t.Error("a read-only mailbox said Not junk")
	}
	if len(folderIDs(t, a, rd, "Junk")) != 1 || a.vayuMail.Accounts().HasContact(context.Background(), "dana@example.com", "deals@shop.example") {
		t.Error("a refused Not junk still moved the message or saved the sender")
	}
	if card, _ := a.vayuReaderCard(rd, "Junk", id, readerView{Pane: true}); strings.Contains(card, "Not junk") {
		t.Error("a read-only mailbox is offered Not junk")
	}
}

// Selection mode in Junk offers Not junk for everything picked, and the list
// action does it to each.
func TestNotJunkForWhatIsPicked(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder), "")
	var ids []string
	for _, s := range []string{"a@shop.example", "b@shop.example"} {
		id, err := a.vayuMail.DeliverInbound(s, "dana@example.com", junkFrom(s, ""))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	list, _ := a.vayuInboxBody(rd, "Junk", "", 0, "")
	if !strings.Contains(list, `{"action":"notjunk"}`) {
		t.Error("selection in Junk does not offer Not junk")
	}
	if inbox, _ := a.vayuInboxBody(rd, "Inbox", "", 0, ""); strings.Contains(inbox, `"notjunk"`) {
		t.Error("selection outside Junk offers Not junk")
	}
	form := url.Values{"user": {"dana"}, "folder": {"Junk"}, "action": {"notjunk"}, "id": ids}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/inbox/action", strings.NewReader(form.Encode())), holder)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handleVayuOSInboxAction(httptest.NewRecorder(), req)
	if n := len(folderIDs(t, a, rd, "Junk")); n != 0 {
		t.Errorf("%d picked messages stayed in Junk", n)
	}
	for _, s := range []string{"a@shop.example", "b@shop.example"} {
		if !a.vayuMail.Accounts().HasContact(context.Background(), "dana@example.com", s) {
			t.Errorf("%s was not remembered", s)
		}
	}
}
