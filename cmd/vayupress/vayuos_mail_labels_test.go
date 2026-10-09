// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// labelledApp is a writable mailbox holding a message with a Message-ID,
// and that message's id as Mail's list gives it.
func labelledApp(t *testing.T) (*App, *users.User, string) {
	t.Helper()
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	if _, err := a.vayuMail.DeliverInbound("x@example.net", "dana@example.com", []byte("Message-ID: <lbl@example.net>\r\nFrom: x@example.net\r\nTo: dana@example.com\r\nSubject: Plans\r\n\r\nbody\r\n")); err != nil {
		t.Fatal(err)
	}
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
	msgs, _ := a.vayuMail.ListFolder(rd, "Inbox")
	for _, m := range msgs {
		if m.MessageID == "lbl@example.net" {
			return a, holder, m.ID
		}
	}
	t.Fatal("the labelled message is not listed")
	return nil, nil, ""
}

func labelsAction(a *App, u *users.User, vals url.Values) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/labels/action", strings.NewReader(vals.Encode())), u)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSLabelsAction(rec, req)
	return rec
}

// A label put on from the reader shows under the subject, ticked in the
// menu, on the message's list row and in the sidebar out of band, and the
// list is told to refresh; taking it off clears all of them.
func TestALabelFromTheReader(t *testing.T) {
	a, holder, id := labelledApp(t)
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), "")
	rec := labelsAction(a, holder, url.Values{"folder": {"Inbox"}, "id": {id}, "label": {"To do"}, "on": {"1"}})
	body := rec.Body.String()
	href := `/os/vayumail/inbox?user=dana&amp;search=label%3A%22To+do%22`
	if rec.Header().Get("HX-Trigger") != "vm-mail-changed" ||
		!strings.Contains(body, `<p class="mx-labels"><a class="mx-label" href="`+href+`">To do</a></p>`) ||
		!strings.Contains(body, `role="menuitemcheckbox" aria-checked="true"`) ||
		!strings.Contains(body, `<div id="vm-labels" hx-swap-oob="true"><div class="sa-appside__group">Labels</div><a class="sa-appside__item" href="`+href+`">`) {
		t.Fatalf("label on: %q\n%s", rec.Header().Get("HX-Trigger"), body)
	}
	if list, _ := a.vayuInboxBody(rd, "Inbox", "", 50); !strings.Contains(list, `<span class="mx-labels"><span class="mx-label">To do</span></span>`) {
		t.Fatalf("the list row has no label:\n%s", list)
	}
	if nav := a.mailNavFor(rd, "Inbox", "", nil, false); !strings.Contains(nav, `<div id="vm-labels"><div class="sa-appside__group">Labels</div>`) {
		t.Fatalf("the sidebar has no labels:\n%s", nav)
	}
	body = labelsAction(a, holder, url.Values{"folder": {"Inbox"}, "id": {id}, "label": {"to do"}, "on": {"0"}}).Body.String()
	if strings.Contains(body, `class="mx-label"`) || !strings.Contains(body, `<div id="vm-labels" hx-swap-oob="true"></div>`) {
		t.Fatalf("label off:\n%s", body)
	}
}

// A refused label is said in the toast; a message the folder does not hold
// is not labelled; a read-only mailbox is offered no Label menu.
func TestWhatTheLabelActionRefuses(t *testing.T) {
	a, holder, id := labelledApp(t)
	rec := labelsAction(a, holder, url.Values{"folder": {"Inbox"}, "id": {id}, "label": {"a:b"}, "on": {"1"}})
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "Not labelled: a label is letters") {
		t.Fatalf("an invalid label: %q", rec.Header().Get("HX-Trigger"))
	}
	if rec := labelsAction(a, holder, url.Values{"folder": {"Inbox"}, "id": {"nope"}, "label": {"Work"}, "on": {"1"}}); !strings.Contains(rec.Body.String(), "Message not available.") || rec.Header().Get("HX-Trigger") != "" {
		t.Fatalf("a message not in the folder: %q\n%s", rec.Header().Get("HX-Trigger"), rec.Body.String())
	}
	ro, roHolder, roID := reviewerApp(t, vmail.RoleReviewer)
	rd := ro.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), roHolder), "")
	if card, _ := ro.vayuReaderCard(rd, "Inbox", roID, readerView{Pane: true}); strings.Contains(card, `aria-label="Label"`) {
		t.Fatal("a read-only mailbox is offered the Label menu")
	}
	if card, _ := a.vayuReaderCard(a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), holder), ""), "Inbox", id, readerView{Pane: true}); !strings.Contains(card, `aria-label="Label"`) {
		t.Fatal("a writable mailbox has no Label menu")
	}
}
