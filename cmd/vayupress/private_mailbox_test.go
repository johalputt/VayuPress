// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

func privateMailboxApp(t *testing.T) (a *App, admin, dana *users.User) {
	t.Helper()
	a = appWithMailAccounts(t)
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)
	admin = &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	dana = &users.User{ID: "vmail:dana@example.com", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}
	return a, admin, dana
}

func setRecovery(t *testing.T, a *App, mbox string) {
	t.Helper()
	ctx := context.Background()
	if err := a.vayuMail.Accounts().SetRecoveryContactPending(ctx, mbox, "dana@elsewhere.test", func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().VerifyRecoveryContact(ctx, mbox); err != nil {
		t.Fatal(err)
	}
}

func postJSONAs(h http.HandlerFunc, body string, u *users.User) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), u)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// A private mailbox's settings are its holder's: every way an administrator
// could change them is refused, with the reason, and written to the holder's
// record. One seed per path.
func TestAnAdministratorCannotChangeAPrivateMailbox(t *testing.T) {
	a, admin, dana := privateMailboxApp(t)
	ctx := context.Background()
	const mbox = "dana@example.com"
	// While it is not private, the administrator may, so each refusal below is
	// the private rule and not some other one.
	if rec := postAppPwForm(a.handleVayuOSAutoreplyAction, "/", url.Values{"email": {mbox}, "enabled": {"1"}, "body": {"x"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("an administrator's vacation reply before: %d", rec.Code)
	}
	setRecovery(t, a, mbox)
	if err := a.vayuMail.Accounts().MakePrivate(ctx, mbox, "dana@elsewhere.test"); err != nil {
		t.Fatal(err)
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"vacation reply": postAppPwForm(a.handleVayuOSAutoreplyAction, "/", url.Values{"email": {mbox}, "enabled": {"0"}}, admin),
		"filter":         postAppPwForm(a.handleVayuOSFilterAction, "/", url.Values{"op": {"create"}, "email": {mbox}, "field": {"subject"}, "contains": {"x"}, "action": {"read"}}, admin),
		"name":           postAppPwForm(a.handleVayuOSAliasAction, "/", url.Values{"op": {"name-set"}, "email": {mbox}, "name": {"Not Dana"}}, admin),
		"devices":        postAppPwForm(a.handleVayuOSDeviceAction, "/", url.Values{"op": {"require-set"}, "email": {mbox}, "on": {"1"}}, admin),
		"signature":      postJSONAs(a.handleVayuOSAccountUpdate, `{"email":"`+mbox+`","signature":"from the admin"}`, admin),
		"two-step":       postJSONAs(a.handleVayuOSAccountTOTP, `{"email":"`+mbox+`","action":"begin"}`, admin),
	} {
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "private") {
			t.Errorf("%s by an administrator on a private mailbox: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if a.vayuMail.Accounts().AutoreplyFor(ctx, mbox).Enabled != true {
		t.Error("the refused vacation reply change took effect")
	}
	ents, _ := a.vayuMail.Ledger(ctx, mbox, 50)
	refused := 0
	for _, e := range ents {
		if e.Action == "refused" {
			refused++
		}
	}
	if refused != 6 {
		t.Errorf("%d refusals written to the holder's record, want 6", refused)
	}
	// The holder is not refused.
	if rec := postAppPwForm(a.handleVayuOSAutoreplyAction, "/", url.Values{"email": {mbox}, "enabled": {"0"}}, dana); rec.Code != http.StatusOK {
		t.Errorf("the holder's own vacation reply on their private mailbox: %d", rec.Code)
	}
}

// The holder makes their own mailbox private with their password, the address
// typed back, and a recovery address set; one seed per condition.
func TestAHolderMakesTheirOwnMailboxPrivate(t *testing.T) {
	a, admin, dana := privateMailboxApp(t)
	const mbox = "dana@example.com"
	try := func(u *users.User, pass, confirm string) string {
		return postAppPwForm(a.handleVayuOSMakePrivate, "/", url.Values{"current": {pass}, "confirm": {confirm}}, u).Body.String()
	}
	if body := try(dana, "main-mailbox-pass", mbox); !strings.Contains(body, "recovery address") || a.vayuMail.IsHandedOver(mbox) {
		t.Fatalf("made private with no recovery address: %s", body)
	}
	setRecovery(t, a, mbox)
	if body := try(dana, "main-mailbox-pass", "dana"); !strings.Contains(body, "Type the mailbox address") || a.vayuMail.IsHandedOver(mbox) {
		t.Fatalf("made private with the address mistyped: %s", body)
	}
	if body := try(dana, "wrong", mbox); !strings.Contains(body, "not your password") || a.vayuMail.IsHandedOver(mbox) {
		t.Fatalf("made private with a wrong password: %s", body)
	}
	if rec := postAppPwForm(a.handleVayuOSMakePrivate, "/", url.Values{"current": {"x"}, "confirm": {mbox}}, admin); rec.Code != http.StatusForbidden || a.vayuMail.IsHandedOver(mbox) {
		t.Fatalf("an administrator with no mailbox here made it private: %d", rec.Code)
	}
	if body := try(dana, "main-mailbox-pass", mbox); !strings.Contains(body, "<strong>Private.</strong>") || !a.vayuMail.IsHandedOver(mbox) {
		t.Fatalf("the holder could not make their mailbox private: %s", body)
	}
	ents, _ := a.vayuMail.Ledger(context.Background(), mbox, 10)
	if len(ents) == 0 || ents[0].Action != "private" || mySiteLedgerLabel(ents[0].Action) != "You made your mailbox private" {
		t.Errorf("the record reads %+v", ents)
	}
}
