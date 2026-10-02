// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/users"
)

// Reply on a message goes to this install's own mail, never a mailto: link,
// which opens whatever mail program the operator's computer has (Outlook, on
// johal.in's). Its address does too, as a new message to the writer.
func TestAMessageIsAnsweredFromThisInstallsMail(t *testing.T) {
	m := contactMessage{ID: "m 1", Name: "Priya", Email: "priya@readers.example", Message: "Hello", Created: time.Now()}
	insp := osMessageInspector(m, nil)
	if strings.Contains(insp, "mailto:") {
		t.Error("the inspector still hands the reply to the computer's mail program")
	}
	for _, want := range []string{`href="/os/vayumail/compose?contact=m+1">`, `href="/os/vayumail/compose?to=priya%40readers.example">`} {
		if !strings.Contains(insp, want) {
			t.Errorf("missing %s", want)
		}
	}
}

// Compose fills a reply to a contact message from the stored message, for an
// administrator only: compose is open to client accounts, the inbox is not,
// and a client who guessed an id must get an empty composer, not the message.
func TestComposeFillsAReplyToAContactMessageForAnAdministratorOnly(t *testing.T) {
	openMigratedDB(t)
	seedMessage(t, "m-1", "Priya", 0, time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC))
	a := &App{}
	req := func(u *users.User, id string) *http.Request {
		return withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/compose?contact="+id, nil), u)
	}

	to, cc, bcc, subject, body := a.composePrefill(req(&users.User{Role: users.RoleAdmin}, "m-1"))
	if to != "priya@readers.example" || cc != "" || bcc != "" || subject != "Re: your message" {
		t.Errorf("to %q cc %q bcc %q subject %q", to, cc, bcc, subject)
	}
	if !strings.Contains(body, "Priya <priya@readers.example> wrote:\r\n> A note from Priya\r\n") {
		t.Errorf("the message is not quoted under its writer:\n%q", body)
	}

	for name, r := range map[string]*http.Request{
		"a client":      req(&users.User{Role: users.RoleClient}, "m-1"),
		"an editor":     req(&users.User{Role: users.RoleEditor}, "m-1"),
		"an unknown id": req(&users.User{Role: users.RoleAdmin}, "m-404"),
	} {
		if to, _, _, subject, body := a.composePrefill(r); to+subject+body != "" {
			t.Errorf("%s: compose filled %q / %q / %q", name, to, subject, body)
		}
	}
}
