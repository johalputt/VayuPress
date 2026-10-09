// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func blockedAction(a *App, vals url.Values) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/blocked/action", strings.NewReader(vals.Encode())), danaHolder())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSBlockedAction(rec, req)
	return rec
}

// The reader offers Block for a received message's sender, and not to a
// read-only mailbox.
func TestTheReaderOffersBlock(t *testing.T) {
	for role, readOnly := range map[string]bool{vmail.RoleMailbox: false, vmail.RoleReviewer: true} {
		a, holder, id := reviewerApp(t, role)
		rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
		card, _ := a.vayuReaderCard(rd, "Inbox", id, readerView{Pane: true})
		if strings.Contains(card, ">Block x@example.net<") == readOnly {
			t.Errorf("%s: Block offered = %v", role, !readOnly)
		}
	}
}

// Block answers in a sentence and lists the sender under Contacts, where
// Unblock takes it off.
func TestBlockThenUnblockFromContacts(t *testing.T) {
	a := scheduledApp(t)
	rec := blockedAction(a, url.Values{"action": {"block"}, "sender": {"x@example.net"}})
	if rec.Code != http.StatusNoContent || !strings.Contains(rec.Header().Get("HX-Trigger"), "Blocked x@example.net") {
		t.Fatalf("block: %d %q", rec.Code, rec.Header().Get("HX-Trigger"))
	}
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/contacts", nil), danaHolder())
	panel := a.vayuContactsPanel(req, "dana@example.com", "")
	if !strings.Contains(panel, ">Blocked<") || !strings.Contains(panel, "x@example.net") || !strings.Contains(panel, ">Unblock<") {
		t.Fatalf("Contacts does not list the block:\n%s", panel)
	}
	rec = blockedAction(a, url.Values{"action": {"unblock"}, "sender": {"x@example.net"}})
	if strings.Contains(rec.Body.String(), "x@example.net") {
		t.Fatalf("after Unblock the panel still lists it:\n%s", rec.Body.String())
	}
	// A refused block says why.
	rec = blockedAction(a, url.Values{"action": {"block"}, "sender": {"dana@example.com"}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "Not blocked: a mailbox cannot block itself") {
		t.Fatalf("blocking itself: %q", rec.Header().Get("HX-Trigger"))
	}
}

// Trash offers Empty, with how many go; Inbox does not, and the action
// empties only the folder it names.
func TestEmptyIsOfferedInTrashAndJunk(t *testing.T) {
	a := scheduledApp(t)
	rd := danaReader(a)
	inbox, _ := a.vayuMail.ListFolder(rd, "Inbox")
	if err := a.vayuMail.MoveMessage(rd, inbox[0].ID, "Inbox", "Trash"); err != nil {
		t.Fatal(err)
	}
	trash, _ := a.vayuInboxBody(rd, "Trash", "", 0, "")
	if !strings.Contains(trash, ">Empty Trash<") || !strings.Contains(trash, "Delete the 1 message in Trash for good?") {
		t.Fatal("Trash offers no Empty, or does not say how many go")
	}
	if _, err := a.vayuMail.DeliverInbound("y@example.net", "dana@example.com", []byte("From: y@example.net\r\nSubject: stays\r\n\r\nb\r\n")); err != nil {
		t.Fatal(err)
	}
	if body, _ := a.vayuInboxBody(rd, "Inbox", "", 0, ""); strings.Contains(body, ">Empty ") {
		t.Fatal("Inbox offers Empty")
	}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/inbox/action", strings.NewReader(url.Values{"action": {"empty"}, "folder": {"Trash"}}.Encode())), danaHolder())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	a.handleVayuOSInboxAction(httptest.NewRecorder(), req)
	if left, _ := a.vayuMail.ListFolder(rd, "Trash"); len(left) != 0 {
		t.Fatalf("Trash holds %d after Empty", len(left))
	}
}
