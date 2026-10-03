// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// Each time the composer could send that is not a plan, refused for its own
// reason; one seed per rule.
func TestSendLaterTimeRefusesWhatCannotBeMeant(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	for _, c := range []struct{ in, want string }{
		{"tomorrow", "could not be read"},
		{at(-time.Second), "has passed"},
		{at(0), "has passed"},
		{at(sendLaterLimit + time.Second), "within a year"},
		{at(time.Minute), ""},
		{at(sendLaterLimit), ""},
		{"2026-10-04T08:00:00.000+05:30", ""}, // the composer's own form: milliseconds, a zone
	} {
		if _, msg := sendLaterTime(c.in, now); (c.want == "" && msg != "") || !strings.Contains(msg, c.want) {
			t.Errorf("sendLaterTime(%q) = %q, want it to say %q", c.in, msg, c.want)
		}
	}
}

// scheduledApp is a started mail engine with dana@example.com, and a session
// user whose mailbox is hers.
func scheduledApp(t *testing.T) *App {
	a, _, _ := reviewerApp(t, vmail.RoleMailbox)
	return a
}

func danaHolder() *users.User {
	return &users.User{ID: "u-dana", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
}

func postSend(a *App, body string, u *users.User) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/send", strings.NewReader(body)), u)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleVayuOSSend(rec, req)
	return rec
}

// With a send time, Send holds the message for the sender's own mailbox and
// sends nothing yet: no Sent copy, nothing queued.
func TestSendWithATimeHoldsTheMessage(t *testing.T) {
	a := scheduledApp(t)
	due := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	// From names someone else: a mailbox holder sends as themselves only.
	rec := postSend(a, `{"From":"erin@example.com","To":"bob@example.net","Subject":"Later","Body":"b","sendAt":"`+due+`"}`, danaHolder())
	var got struct{ Scheduled bool }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || !got.Scheduled {
		t.Fatalf("send with a time: %d %s", rec.Code, rec.Body.String())
	}
	list, _ := a.vayuMail.ScheduledFor(context.Background(), "dana@example.com")
	if len(list) != 1 || list[0].Subject != "Later" {
		t.Fatalf("dana's scheduled list: %+v", list)
	}
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "")
	if sent, _ := a.vayuMail.ListFolder(rd, "Sent"); len(sent) != 0 {
		t.Fatalf("a held message filed a Sent copy: %d", len(sent))
	}
	if q, _ := a.vayuMail.Sent(context.Background(), 10); len(q) != 0 {
		t.Fatalf("a held message was queued: %+v", q)
	}

	// The same request without a time is sent: the control.
	postSend(a, `{"To":"bob@example.net","Subject":"Now","Body":"b"}`, danaHolder())
	if q, _ := a.vayuMail.Sent(context.Background(), 10); len(q) != 1 {
		t.Fatalf("a send without a time queued %d, want 1", len(q))
	}
}

// A refused time is refused before anything is held.
func TestSendWithAPastTimeHoldsNothing(t *testing.T) {
	a := scheduledApp(t)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	rec := postSend(a, `{"To":"bob@example.net","Subject":"s","Body":"b","sendAt":"`+past+`"}`, danaHolder())
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "has passed") {
		t.Fatalf("a past time: %d %s", rec.Code, rec.Body.String())
	}
	if list, _ := a.vayuMail.ScheduledFor(context.Background(), "dana@example.com"); len(list) != 0 {
		t.Fatalf("a refused time was held: %+v", list)
	}
}

func holdFor(t *testing.T, a *App, owner, subject string) int64 {
	t.Helper()
	id, err := a.vayuMail.Schedule(context.Background(), owner,
		vmail.ComposeMessage{From: owner, To: []string{"bob@example.net"}, Subject: subject, Body: "b"}, "b", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Scheduled lists the open mailbox's held messages and no one else's, and
// the sidebar offers it only while there is something in it.
func TestScheduledListIsTheMailboxsOwn(t *testing.T) {
	a := scheduledApp(t)
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "")
	nav := mailFolderNav(rd.Key(), "Inbox", "", a.folderUnread(rd), nil, false)
	if strings.Contains(nav, "folder=Scheduled") {
		t.Fatal("the sidebar offers Scheduled with nothing scheduled")
	}
	holdFor(t, a, "dana@example.com", "Dana's own")
	holdFor(t, a, "erin@example.com", "Erin's")

	body, _ := a.vayuInboxBody(rd, "Scheduled", "", 0)
	if !strings.Contains(body, "Dana&#39;s own") || strings.Contains(body, "Erin") {
		t.Fatalf("dana's Scheduled list:\n%s", body)
	}
	if !strings.Contains(body, ">Send now<") || !strings.Contains(body, ">Cancel<") {
		t.Fatalf("a held message without its actions:\n%s", body)
	}
	nav = mailFolderNav(rd.Key(), "Inbox", "", a.folderUnread(rd), nil, false)
	if !strings.Contains(nav, "folder=Scheduled") || !strings.Contains(nav, `aria-label="1"`) {
		t.Fatalf("the sidebar does not offer Scheduled (1):\n%s", nav)
	}
}

func postScheduledAction(a *App, vals url.Values, u *users.User) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/scheduled/action", strings.NewReader(vals.Encode())), u)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSScheduledAction(rec, req)
	return rec
}

// A mailbox holder's action reaches their own held messages only, whatever
// mailbox the request names; their own Cancel files the message in Drafts.
func TestScheduledActionsReachTheHoldersOwnOnly(t *testing.T) {
	a := scheduledApp(t)
	erins := holdFor(t, a, "erin@example.com", "Erin's")
	postScheduledAction(a, url.Values{"user": {"erin@example.com"}, "id": {strconv.FormatInt(erins, 10)}, "action": {"cancel"}}, danaHolder())
	if list, _ := a.vayuMail.ScheduledFor(context.Background(), "erin@example.com"); len(list) != 1 {
		t.Fatal("dana cancelled erin's scheduled message")
	}

	danas := holdFor(t, a, "dana@example.com", "Dana's")
	rec := postScheduledAction(a, url.Values{"id": {strconv.FormatInt(danas, 10)}, "action": {"cancel"}}, danaHolder())
	if list, _ := a.vayuMail.ScheduledFor(context.Background(), "dana@example.com"); len(list) != 0 || rec.Code != http.StatusOK {
		t.Fatalf("dana's own cancel: %d, still scheduled %+v", rec.Code, list)
	}
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "")
	if drafts, _ := a.vayuMail.ListFolder(rd, "Drafts"); len(drafts) != 1 {
		t.Fatalf("a cancelled message is not in Drafts: %d", len(drafts))
	}
}
