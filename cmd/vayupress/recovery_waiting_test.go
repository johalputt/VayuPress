// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func waitingNotice(a *App, level int) (osNotification, bool) {
	for _, n := range a.osNotifications(context.Background(), &osSettings{AccessLevel: level}) {
		if strings.HasPrefix(n.Href, "/os/vayumail/accounts") {
			return n, true
		}
	}
	return osNotification{}, false
}

// The operator asked from /mail/recover/ask, was told "your administrator has
// been told", and nothing reached the console: the request sat folded inside
// Mail's Accounts tab. While a request waits, the bell (and Home, which lists
// the same notices) says so to an administrator, and nobody else.
func TestALockedOutMailboxReachesTheAdministrator(t *testing.T) {
	a := resetSessionApp(t)
	ctx := context.Background()
	if _, ok := waitingNotice(a, accessAdmin); ok {
		t.Fatal("the bell reports a locked-out mailbox before anyone asked")
	}
	if err := a.vayuMail.Accounts().FileRecoveryRequest(ctx, "boss@example.com", "lost my phone", "203.0.113.9"); err != nil {
		t.Fatal(err)
	}

	n, ok := waitingNotice(a, accessAdmin)
	if !ok {
		t.Fatal("someone is locked out and asked for help, and the bell says nothing")
	}
	if n.Detail != "1 waiting for you" || n.Href != "/os/vayumail/accounts#recovery" || n.Severity != "warn" {
		t.Errorf("the notice reads %+v", n)
	}
	if _, ok := waitingNotice(a, accessEditor); ok {
		t.Error("an editor is told of a recovery request it cannot act on")
	}

	// It leads somewhere that shows it: the section is open, at its anchor.
	// The mailbox has recovery codes, so nothing but the waiting request opens
	// it (a mailbox with nothing enrolled opens it on its own).
	if _, err := a.vayuMail.Accounts().GenerateRecoveryCodes(ctx, "boss@example.com"); err != nil {
		t.Fatal(err)
	}
	if stuck := a.vayuMail.Accounts().UnrecoverableAccounts(ctx); len(stuck) != 0 {
		t.Fatalf("setup: %v still read as unrecoverable", stuck)
	}
	card := a.recoveryCardHTML(httptest.NewRequest("GET", "/os/vayumail/accounts", nil), "n", []string{"boss@example.com"})
	if !strings.HasPrefix(card, `<div id="recovery"><details class="mon-acc" open>`) {
		t.Errorf("the request waits inside a folded section: %.120s", card)
	}

	// Decided, it is no longer waiting.
	pending := a.vayuMail.Accounts().PendingRecoveryRequests(ctx)
	if len(pending) != 1 {
		t.Fatalf("pending: %d", len(pending))
	}
	if _, err := a.vayuMail.Accounts().DecideRecoveryRequest(ctx, pending[0].ID, "declined", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitingNotice(a, accessAdmin); ok {
		t.Error("a declined request still reads as waiting")
	}
}

// The count keeps to the queue's own retention: a request older than it is
// about to be pruned, not someone waiting.
func TestARecoveryRequestPastRetentionIsNotWaiting(t *testing.T) {
	a := resetSessionApp(t)
	ctx := context.Background()
	if err := a.vayuMail.Accounts().FileRecoveryRequest(ctx, "boss@example.com", "", "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbpkg.DB.ExecContext(ctx, `UPDATE vayumail_recovery_requests SET created_at=datetime('now','-31 days')`); err != nil {
		t.Fatal(err)
	}
	if n := a.vayuMail.Accounts().PendingRecoveryCount(ctx); n != 0 {
		t.Errorf("a request past retention counts as %d waiting", n)
	}
}
