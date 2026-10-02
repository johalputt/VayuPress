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

// Mail in three panes (Mail plan §3.1): while a mailbox is open the Mail
// sidebar is that mailbox: who it is, its folders and views, the rest of Mail
// folded away, and a foot with contacts, its rules and its storage.

func TestTheMailSidebarIsTheOpenMailbox(t *testing.T) {
	s := saSession(accessAdmin)
	s.Route = "/os/vayumail/inbox"
	s.MailSide = &osMailSide{
		User: "dana", Address: "dana@example.com", Name: "Dana Kaur", Admin: true,
		Folders: mailFolderNav("dana", "Sent", "pinned", map[string]int{"Inbox": 3}, map[string]int{"unread": 2, "pinned": 1}, false),
		Used:    900 << 20, Quota: 1 << 30,
	}
	out := stillAirShellHead("n", "Mailbox", "vayuos", s)
	side := out[strings.Index(out, `<nav class="sa-appside"`):]
	side = side[:strings.Index(side, "</nav>")]

	// The account: name, then the whole address; for an administrator it
	// leads to every mailbox.
	if !strings.Contains(side, `<a class="mx-account" href="/os/vayumail/inbox?all=1"`) ||
		!strings.Contains(side, `<span class="mx-account__name">Dana Kaur</span><span class="mx-account__addr">dana@example.com</span>`) {
		t.Errorf("the sidebar does not open on the mailbox's account:\n%s", side)
	}
	folders := side[strings.Index(side, `id="vm-folders"`):strings.Index(side, `>Views</div>`)]
	if strings.Count(folders, `aria-current="page"`) != 1 || !strings.Contains(folders, `folder=Sent" aria-current="page">`) {
		t.Errorf("the open folder, Sent, is not the one current folder:\n%s", folders)
	}
	if !strings.Contains(folders, `<span class="sa-appside__label">Inbox</span><span class="sa-appside__count" aria-label="3">3</span>`) {
		t.Errorf("the Inbox does not say it holds 3 unread:\n%s", folders)
	}
	// The views filter the open folder; the one in force leads back to it.
	views := side[strings.Index(side, `>Views</div>`):]
	if !strings.Contains(views, `href="/os/vayumail/inbox?user=dana&folder=Sent&view=unread"`) ||
		!strings.Contains(views, `href="/os/vayumail/inbox?user=dana&folder=Sent" hx-get=`) || strings.Count(views, `aria-current="true"`) != 1 {
		t.Errorf("the views do not filter the open folder, or the one in force does not lead back:\n%s", views)
	}
	// Nothing lists the install's other mailboxes beside this one.
	if strings.Contains(side, "Mailboxes on this install") {
		t.Error("the sidebar lists every mailbox again")
	}
	// The rest of Mail is folded away, and the section that opened the
	// mailbox is not among it.
	more := side[strings.Index(side, `<details class="mx-more">`):strings.Index(side, `<div class="mx-foot">`)]
	if !strings.Contains(more, `href="/os/vayumail/compose"`) || !strings.Contains(more, `href="/os/vayumail/accounts"`) || strings.Contains(more, `>Mailbox</span>`) {
		t.Errorf("the rest of Mail is not folded under the mailbox:\n%s", more)
	}
	// The foot: contacts, the mailbox's rules, and its storage, warned at 75%.
	foot := side[strings.Index(side, `<div class="mx-foot">`):]
	for _, want := range []string{`hx-get="/os/vayumail/contacts?user=dana"`, `href="/os/vayumail/accounts/settings?user=dana%40example.com">`,
		`900.0 MiB of 1.0 GiB`, `aria-label="87% of the mailbox's storage used"`, `class="sa-meter__fill sa-meter__fill--warn" width="87"`} {
		if !strings.Contains(foot, want) {
			t.Errorf("the foot is missing %q:\n%s", want, foot)
		}
	}

	// Someone who holds only this mailbox has nowhere else to go and no rules
	// to change: the account block is not a control, and no meter without a quota.
	s.MailSide.Admin, s.MailSide.Quota = false, 0
	out = stillAirShellHead("n", "Mailbox", "vayuos", s)
	if !strings.Contains(out, `<div class="mx-account">`) || strings.Contains(out, "inbox?all=1") || strings.Contains(out, "accounts/settings") || strings.Contains(out, "sa-meter") {
		t.Error("a mailbox holder is offered every mailbox, the rules page, or a meter with no quota")
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
	if body, _ := a.vayuInboxBody(rd, "Inbox", "", 0); strings.Contains(body, "vm-folders") {
		t.Error("the list body carries the folders itself; the full page would hold them twice")
	}
	swap := a.vayuInboxSwap(rd, "Inbox", "", 0)
	if !strings.Contains(swap, `<div id="vm-folders" class="sa-appside__folders" hx-swap-oob="true">`) || !strings.Contains(swap, `<span class="sa-appside__label">Inbox</span><span class="sa-appside__count" aria-label="1">1</span>`) {
		t.Errorf("a refresh does not bring the folders with their unread count:\n%s", swap)
	}

	form := url.Values{"action": {"mark"}, "mark": {"read"}, "user": {"dana"}, "folder": {"Inbox"}, "id": {id}}
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/inbox/action", strings.NewReader(form.Encode())), holder)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSInboxAction(rec, req)
	if out := rec.Body.String(); !strings.Contains(out, `hx-swap-oob="true"`) || strings.Contains(out, `<span class="sa-appside__label">Inbox</span><span class="sa-appside__count"`) {
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
	if !strings.Contains(page, `<a class="mx-account" href="/os/vayumail/inbox?all=1"`) {
		t.Error("the administrator's account block does not lead to every mailbox")
	}
	// The install's other mailboxes are an administrator's to open.
	if own := getAs(holder, "/os/vayumail/inbox"); !strings.Contains(own, `id="vm-folders"`) || strings.Contains(own, "inbox?all=1") {
		t.Error("a mailbox holder is offered the install's other mailboxes, or no folders")
	}
	if all := get("/os/vayumail/inbox?all=1"); !strings.Contains(all, "vm-dom-card") {
		t.Error("All mailboxes does not open the directory")
	}
}
