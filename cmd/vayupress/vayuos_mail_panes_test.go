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

// Mail in three panes (render 04): while a mailbox is open the Mail sidebar
// is its folders, then the install's other mailboxes, then the rest of Mail;
// the page is the list and the reader.

func TestTheMailSidebarIsTheOpenMailbox(t *testing.T) {
	s := saSession(accessAdmin)
	s.Route = "/os/vayumail/inbox"
	s.MailSide = &osMailSide{
		Address: "dana@example.com",
		Folders: mailFolderNav("dana", "Sent", map[string]int{"Inbox": 3}, false),
		Boxes:   []osMailBox{{Key: "dana", Address: "dana@example.com", Current: true}, {Key: "eli", Address: "eli@example.com", Unseen: 2}},
	}
	out := stillAirShellHead("n", "Mailbox", "vayuos", s)
	side := out[strings.Index(out, `<nav class="sa-appside"`):]
	side = side[:strings.Index(side, "</nav>")]

	if !strings.Contains(side, `<div class="sa-appside__title" title="dana@example.com">dana@example.com</div>`) {
		t.Errorf("the sidebar does not name the open mailbox:\n%s", side)
	}
	folders := side[strings.Index(side, `id="vm-folders"`):strings.Index(side, ">Mailboxes on this install<")]
	if strings.Count(folders, `aria-current="page"`) != 1 || !strings.Contains(folders, `folder=Sent" aria-current="page">`) {
		t.Errorf("the open folder, Sent, is not the one current folder:\n%s", folders)
	}
	if !strings.Contains(folders, `<span class="sa-appside__label">Inbox</span><span class="sa-appside__count" aria-label="3 unread">3</span>`) {
		t.Errorf("the Inbox does not say it holds 3 unread:\n%s", folders)
	}
	if !strings.Contains(side, `href="/os/vayumail/inbox?user=eli">`) || !strings.Contains(side, `aria-label="2 unread">2</span>`) {
		t.Errorf("another mailbox, with its unread, is not offered:\n%s", side)
	}
	if !strings.Contains(side, `href="/os/vayumail/inbox?user=dana" aria-current="true">`) {
		t.Errorf("the open mailbox is not marked among the install's:\n%s", side)
	}
	if !strings.Contains(side, `href="/os/vayumail/inbox?all=1"`) {
		t.Error("the sidebar offers no way to every mailbox")
	}
	// The folders are the mailbox, so the section that opened it is not
	// listed twice; the rest of Mail follows under its own label.
	if strings.Contains(side, `<span class="sa-appside__label">Mailbox</span>`) {
		t.Error("the sidebar lists Mailbox beside the mailbox's own folders")
	}
	if !strings.Contains(side, `<div class="sa-appside__group">Mail</div><a class="sa-appside__item" href="/os/vayumail/compose"`) {
		t.Errorf("the rest of Mail is not labelled under the folders:\n%s", side)
	}

	// Anywhere else in Mail, the sidebar is Mail's sections as before.
	s.MailSide = nil
	if out := stillAirShellHead("n", "Mailbox", "vayuos", s); !strings.Contains(out, `<span class="sa-appside__label">Mailbox</span>`) || strings.Contains(out, "vm-folders") {
		t.Error("with no mailbox open, the Mail sidebar is not Mail's sections")
	}
}

// Every list refresh — a folder switch, a row action, the poll — brings the
// folders with it out of band, so the current folder and the unread counts
// cannot go stale beside the list. The full page carries them once, in the
// sidebar, never out of band.
func TestAListRefreshBringsTheFoldersWithIt(t *testing.T) {
	a, holder, id := reviewerApp(t, vmail.RoleMailbox) // dana, one unread message
	rd := vmail.ReadAsOwner("dana")
	if body := a.vayuInboxBody(rd, "Inbox", 0); strings.Contains(body, "vm-folders") {
		t.Error("the list body carries the folders itself; the full page would hold them twice")
	}
	swap := a.vayuInboxSwap(rd, "Inbox", 0)
	if !strings.Contains(swap, `<div id="vm-folders" class="sa-appside__folders" hx-swap-oob="true">`) || !strings.Contains(swap, `aria-label="1 unread"`) {
		t.Errorf("a refresh does not bring the folders with their unread count:\n%s", swap)
	}

	form := url.Values{"action": {"mark"}, "mark": {"read"}, "user": {"dana"}, "folder": {"Inbox"}, "id": {id}}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/inbox/action", strings.NewReader(form.Encode())), holder)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSInboxAction(rec, req)
	if out := rec.Body.String(); !strings.Contains(out, `hx-swap-oob="true"`) || strings.Contains(out, "unread</span>") || strings.Contains(out, `aria-label="1 unread"`) {
		t.Errorf("after the only message is read, the refreshed folders still count it, or are missing:\n%s", out)
	}

	rec = httptest.NewRecorder()
	a.handleVayuOSInboxFragment(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox/fragment?folder=Sent", nil), holder))
	if out := rec.Body.String(); !strings.Contains(out, `folder=Sent" aria-current="page">`) {
		t.Errorf("a folder switch does not move the current folder in the sidebar:\n%s", out)
	}
}

// An administrator with a mailbox of their own opens it; the directory of
// every mailbox is one link away.
func TestAnAdministratorOpensTheirOwnMailbox(t *testing.T) {
	a, holder, _ := reviewerApp(t, vmail.RoleMailbox)
	admin := &users.User{ID: "u-admin", Email: "dana@example.com", Role: users.RoleAdmin, MailAddress: "dana@example.com"}
	getAs := func(u *users.User, target string) string {
		rec := httptest.NewRecorder()
		a.handleVayuOSInbox(rec, withUser(httptest.NewRequest(http.MethodGet, target, nil), u))
		return rec.Body.String()
	}
	get := func(target string) string { return getAs(admin, target) }
	page := get("/os/vayumail/inbox")
	if !strings.Contains(page, `id="vm-inbox-list"`) || strings.Contains(page, "vm-dom-card") {
		t.Fatal("an administrator with a mailbox lands on the directory, not their mailbox")
	}
	if strings.Count(page, `id="vm-folders"`) != 1 || strings.Contains(page, "hx-swap-oob") {
		t.Errorf("the page does not carry the folders exactly once, in the sidebar")
	}
	if !strings.Contains(page, `href="/os/vayumail/inbox?user=dana" aria-current="true">`) {
		t.Error("the administrator's sidebar does not mark the open mailbox among the install's")
	}
	// The install's other mailboxes are an administrator's to open.
	if own := getAs(holder, "/os/vayumail/inbox"); !strings.Contains(own, `id="vm-folders"`) || strings.Contains(own, "Mailboxes on this install") {
		t.Error("a mailbox holder is offered the install's other mailboxes, or no folders")
	}
	if all := get("/os/vayumail/inbox?all=1"); !strings.Contains(all, "vm-dom-card") {
		t.Error("All mailboxes does not open the directory")
	}
}
