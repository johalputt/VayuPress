package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// deliverTestMail drops one minimal RFC822 message into a mailbox's Maildir so it
// lands unseen (in new/), giving the unseen endpoint something to count.
func deliverTestMail(t *testing.T, a *App, to, from, subject string) {
	t.Helper()
	raw := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\n" +
		"Date: Mon, 21 Jul 2026 10:00:00 +0000\r\n\r\nhello\r\n"
	if _, err := a.vayuMail.DeliverInbound(to, []byte(raw)); err != nil {
		t.Fatalf("deliver to %s: %v", to, err)
	}
}

// TestUnseenEndpointAdmin verifies an admin sees a mailbox's live unseen count and
// the deep-link key the notifier opens.
func TestUnseenEndpointAdmin(t *testing.T) {
	a := appWithMailAccounts(t) // has dana@example.com on the primary domain
	deliverTestMail(t, a, "dana@example.com", "alice@friend.com", "Hello Dana")

	req := httptest.NewRequest(http.MethodGet, "/os/vayumail/unseen", nil)
	req = withUser(req, &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin})
	rec := httptest.NewRecorder()
	a.handleVayuOSUnseen(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	var boxes []unseenBox
	if err := json.Unmarshal(rec.Body.Bytes(), &boxes); err != nil {
		t.Fatalf("decode: %v — body=%s", err, rec.Body.String())
	}
	var dana *unseenBox
	for i := range boxes {
		if boxes[i].Address == "dana@example.com" {
			dana = &boxes[i]
			break
		}
	}
	if dana == nil {
		t.Fatalf("dana@example.com missing from %+v", boxes)
	}
	if dana.Key != "dana" {
		t.Errorf("key = %q, want bare local part %q for the primary domain", dana.Key, "dana")
	}
	if dana.Unseen < 1 {
		t.Errorf("unseen = %d, want >= 1 after delivering a new message", dana.Unseen)
	}
}

// TestUnseenEndpointNonAdminNoMailbox confirms a signed-in non-admin with no
// assigned mailbox (and no user store to resolve one) gets an empty array, never
// another operator's counts — the endpoint must not leak across mailboxes.
func TestUnseenEndpointNonAdminNoMailbox(t *testing.T) {
	a := appWithMailAccounts(t)
	deliverTestMail(t, a, "dana@example.com", "alice@friend.com", "Hello Dana")

	req := httptest.NewRequest(http.MethodGet, "/os/vayumail/unseen", nil)
	req = withUser(req, &users.User{ID: "staff1", Email: "staff@example.com", Role: users.RoleEditor})
	rec := httptest.NewRecorder()
	a.handleVayuOSUnseen(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var boxes []unseenBox
	if err := json.Unmarshal(rec.Body.Bytes(), &boxes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(boxes) != 0 {
		t.Fatalf("non-admin with no mailbox saw %d mailboxes, want 0: %+v", len(boxes), boxes)
	}
}
