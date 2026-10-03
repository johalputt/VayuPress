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

// Mail's Reading choice (pipeline 3y): a person reads messages beside the
// list or over it at full width, and the choice is theirs, kept on their
// account (migration 104) so it follows them to every browser.
func TestReadingIsKeptOnThePersonsOwnAccount(t *testing.T) {
	store := newTestUserStore(t)
	ctx := context.Background()
	me, err := store.Create(ctx, "reader@example.com", "Reader", "password123", users.RoleAuthor)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(ctx, "other@example.com", "Other", "password123", users.RoleAuthor)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{userStore: store}
	post := func(u *users.User, layout string) int {
		req := httptest.NewRequest(http.MethodPost, "/os/vayumail/layout", strings.NewReader("layout="+layout))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if u != nil {
			req = withUser(req, u)
		}
		rec := httptest.NewRecorder()
		a.handleVayuOSMailLayout(rec, req)
		return rec.Code
	}
	if code := post(me, "full"); code != http.StatusNoContent || store.MailLayout(ctx, me.ID) != users.MailLayoutFull {
		t.Fatalf("full width was not kept: %d, %q", code, store.MailLayout(ctx, me.ID))
	}
	if store.MailLayout(ctx, other.ID) != "" {
		t.Error("one person's Reading changed another's")
	}
	if post(me, ""); store.MailLayout(ctx, me.ID) != "" {
		t.Error("beside the list again was not kept")
	}
	// Anything else is beside the list: no other value reaches the column.
	if post(me, "<b>wide</b>"); store.MailLayout(ctx, me.ID) != "" {
		t.Errorf("an unknown layout was stored: %q", store.MailLayout(ctx, me.ID))
	}
	// A session signed in with the API key has no account to keep it on.
	if code := post(nil, "full"); code != http.StatusForbidden {
		t.Errorf("a session with no account: %d, want 403", code)
	}
}

// The sidebar offers Reading with the person's choice checked, and offers
// nothing to a session with no account to keep it on.
func TestTheMailSidebarOffersReading(t *testing.T) {
	for _, c := range []struct {
		reading, full bool
		want          []string
	}{
		{true, true, []string{`class="sa-pop mx-reading"`, `aria-checked="false" data-mx-layout-choice=""`, `aria-checked="true" data-mx-layout-choice="full"`}},
		{true, false, []string{`aria-checked="true" data-mx-layout-choice=""`, `aria-checked="false" data-mx-layout-choice="full"`}},
	} {
		side := saMailSide(&osMailSide{User: "dana", Address: "dana@example.com", Reading: c.reading, FullOpen: c.full}, "")
		for _, w := range c.want {
			if !strings.Contains(side, w) {
				t.Errorf("full=%v: the sidebar lacks %s", c.full, w)
			}
		}
	}
	if side := saMailSide(&osMailSide{User: "dana", Address: "dana@example.com"}, ""); strings.Contains(side, "mx-reading") {
		t.Error("Reading is offered to a session with no account")
	}
}

// The Mail page tells admin-os-mail.js to open messages full width for a
// person who chose it, and for nobody else.
func TestTheMailboxOpensFullWidthForWhoChoseIt(t *testing.T) {
	a, holder, _ := reviewerApp(t, "mailbox")
	store := newTestUserStore(t)
	a.userStore = store
	ctx := context.Background()
	u, err := store.Create(ctx, "dana-reader@example.com", "Dana", "password123", users.RoleAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMailAddress(ctx, u.ID, holder.MailAddress); err != nil {
		t.Fatal(err)
	}
	holder.ID = u.ID
	page := func() string {
		rec := httptest.NewRecorder()
		a.handleVayuOSInbox(rec, withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/inbox", nil), holder))
		return rec.Body.String()
	}
	// Seen beside the list first, so the next check cannot pass on a page
	// that is not the mailbox at all.
	out := page()
	if !strings.Contains(out, `<div class="vm-split" data-page-kind="app">`) {
		t.Fatal("beside the list, the mailbox is not drawn in columns")
	}
	if !strings.Contains(out, `class="sa-pop mx-reading"`) {
		t.Error("a signed-in person is not offered Reading")
	}
	if err := store.SetMailLayout(ctx, u.ID, users.MailLayoutFull); err != nil {
		t.Fatal(err)
	}
	if out := page(); !strings.Contains(out, `<div class="vm-split" data-page-kind="app" data-mx-layout="full">`) {
		t.Error("a person who reads full width is given the columns")
	}
}
