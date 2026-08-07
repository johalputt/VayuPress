// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/users"
)

// The audit finding, in the attacker's voice:
//
//	You closed the door I was going to walk through — a mail:write key can no
//	longer create an "administrator" mailbox, and it can no longer submit
//	{"role":"administrator"} to promote one. Both guards read the role I SUBMIT.
//
//	So I submit no role at all.
//
//	  POST /os/vayumail/accounts/update {"email":"boss@example.com","pass":"…"}
//
//	Your install owner's mailbox now has a password I picked. If a second factor
//	is in my way, /accounts/totp takes {"action":"disable"} from the same key. If
//	I would rather have the install than the mail, /accounts/delete removes every
//	administrator and you are locked out of your own console.
//
//	Nothing here promotes anything. I do not need to promote what is already
//	console-capable — I only need to take it.
//
// These tests drive the real handlers. The fix is mailCredentialActionAuthorized,
// which reads the target's CURRENT role from storage rather than the role on the
// request.

// mailKeyReq builds a request carrying an issued key granted only mail:write —
// the credential the VayuMail section of the panel legitimately hands out.
//
// The X-API-Key header is not decoration. isAdminRequest — the predicate these
// handlers used — answers auth.HasValidAPIKey(r), which reads the PRESENTED key
// and knows nothing about the KeyInfo in context. Omitting it makes every attack
// below get a 403 for the wrong reason, and the whole file passes against the
// vulnerable code. That has already happened once in this repo.
func mailKeyReq(method, path, body, contentType string) *http.Request {
	p := apikeys.NewPermissions()
	p.Grant(apikeys.SectionMail, apikeys.ActionWrite)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-API-Key", config.Cfg.APIKey)
	return auth.RequestWithKeyInfo(req, apikeys.KeyInfo{
		ID: "k-mail", Label: "mail integration", Scope: apikeys.ScopeExternal, Perms: p,
	})
}

// mailTakeoverApp seeds a console-capable mailbox (the install owner) alongside
// the ordinary one appWithMailAccounts already creates, and points config at a
// known API key so the header above is genuinely valid.
func mailTakeoverApp(t *testing.T) *App {
	t.Helper()
	a := appWithMailAccounts(t)
	t.Setenv("API_KEY", "test-key")
	t.Setenv("DOMAIN", "example.com")
	config.Load()

	hash, err := auth.HashSecretArgon2id("owner-mailbox-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := a.vayuMail.Accounts().Create(context.Background(),
		"boss@example.com", hash, "Boss", "administrator"); err != nil {
		t.Fatalf("seed owner mailbox: %v", err)
	}
	// The premise of every test below. If "administrator" ever stops granting
	// console access this file is asserting nothing, and should say so loudly
	// rather than pass.
	if !a.mailTargetGrantsConsole(context.Background(), "boss@example.com") {
		t.Fatal("the seeded owner mailbox is not console-capable, so none of these attacks " +
			"would be an escalation and this file proves nothing")
	}
	return a
}

// passwordHashOf reads the stored hash so the assertions can be about what
// CHANGED, not about a status code. A handler that returns 403 and writes anyway
// is the failure this catches.
func passwordHashOf(t *testing.T, a *App, email string) string {
	t.Helper()
	h := a.vayuMail.Accounts().HashFor(context.Background(), email)
	if h == "" {
		t.Fatalf("no stored hash for %s", email)
	}
	return h
}

func TestScopedMailKeyCannotResetAConsoleMailboxPassword(t *testing.T) {
	a := mailTakeoverApp(t)
	before := passwordHashOf(t, a, "boss@example.com")

	rec := httptest.NewRecorder()
	a.handleVayuOSAccountUpdate(rec,
		mailKeyReq(http.MethodPost, "/os/vayumail/accounts/update",
			`{"email":"boss@example.com","pass":"attacker-chosen-password"}`, "application/json"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("a mail:write key reset the install owner's mailbox password and got %d, want 403.\n\n"+
			"That mailbox signs in to the VayuOS console as an administrator. The key now owns the "+
			"install, and revoking the key does not take it back.\n\nbody: %s", rec.Code, rec.Body.String())
	}
	if after := passwordHashOf(t, a, "boss@example.com"); after != before {
		t.Fatal("the stored password hash changed. The status code is not the control — " +
			"what matters is that nothing was written.")
	}
}

func TestScopedMailKeyCannotDisableAConsoleMailboxSecondFactor(t *testing.T) {
	a := mailTakeoverApp(t)
	ctx := context.Background()
	if err := a.vayuMail.Accounts().SetTOTPSecret(ctx, "boss@example.com", "JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatalf("seed totp: %v", err)
	}
	if err := a.vayuMail.Accounts().EnableTOTP(ctx, "boss@example.com"); err != nil {
		t.Fatalf("enable totp: %v", err)
	}

	rec := httptest.NewRecorder()
	a.handleVayuOSAccountTOTP(rec,
		mailKeyReq(http.MethodPost, "/os/vayumail/accounts/totp",
			`{"email":"boss@example.com","action":"disable"}`, "application/json"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("a mail:write key stripped 2FA from a console mailbox and got %d, want 403", rec.Code)
	}
	if _, enabled := a.vayuMail.Accounts().TOTPStatus(ctx, "boss@example.com"); !enabled {
		t.Fatal("the second factor is off. It is the last thing standing between a reset password " +
			"and the console, and a scoped key removed it.")
	}
}

func TestScopedMailKeyCannotDeleteAConsoleMailbox(t *testing.T) {
	a := mailTakeoverApp(t)

	rec := httptest.NewRecorder()
	a.handleVayuOSAccountDelete(rec,
		mailKeyReq(http.MethodPost, "/os/vayumail/accounts/delete",
			`{"email":"boss@example.com"}`, "application/json"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("a mail:write key deleted the owner's mailbox and got %d, want 403", rec.Code)
	}
	if a.vayuMail.Accounts().RoleFor(context.Background(), "boss@example.com") == "" {
		t.Fatal("the administrator mailbox is gone — a scoped key locked the operator out of " +
			"their own console")
	}
}

// The inline HTMX list reaches the same mutations by a different route, and a
// fix applied only to the JSON endpoints would leave this one wide open.
func TestScopedMailKeyCannotDisableAConsoleMailboxFromTheList(t *testing.T) {
	a := mailTakeoverApp(t)

	rec := httptest.NewRecorder()
	a.handleVayuOSAccountsAction(rec,
		mailKeyReq(http.MethodPost, "/os/vayumail/accounts/action",
			"email=boss@example.com&op=toggle&active=false", "application/x-www-form-urlencoded"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("a mail:write key disabled the owner's mailbox from the inline list and got %d, want 403", rec.Code)
	}
	if acs, err := a.vayuMail.Accounts().List(context.Background()); err == nil {
		for _, ac := range acs {
			if ac.Email == "boss@example.com" && !ac.Active {
				t.Fatal("the owner's mailbox is disabled — the inline action is the same mutation " +
					"as /accounts/update and needs the same fence")
			}
		}
	}
}

// PROMOTING through the inline list is the create-an-admin hole by its third
// door: the target is an ordinary mailbox, so the current-role check does not
// fire, and only a check on the SUBMITTED role catches it.
func TestScopedMailKeyCannotPromoteFromTheList(t *testing.T) {
	a := mailTakeoverApp(t)

	rec := httptest.NewRecorder()
	a.handleVayuOSAccountsAction(rec,
		mailKeyReq(http.MethodPost, "/os/vayumail/accounts/action",
			"email=dana@example.com&op=role&role=administrator", "application/x-www-form-urlencoded"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("a mail:write key promoted a mailbox to administrator from the inline list and got %d, want 403", rec.Code)
	}
	if got := a.vayuMail.Accounts().RoleFor(context.Background(), "dana@example.com"); got == "administrator" {
		t.Fatal("dana@example.com is now an administrator mailbox, which signs in to the console")
	}
}

// ---------------------------------------------------------------------------
// THE CONTROLS. Every one of these must keep working, or the remediation has
// broken an operator's install to close a hole.
// ---------------------------------------------------------------------------

// A human administrator session runs the console's own forms. If this fails, the
// panel is broken.
func TestAdminSessionStillResetsAConsoleMailboxPassword(t *testing.T) {
	a := mailTakeoverApp(t)
	admin := &users.User{ID: "a1", Email: "boss@example.com", Role: users.RoleAdmin}

	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/update",
		strings.NewReader(`{"email":"boss@example.com","pass":"a new operator password"}`)), admin)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountUpdate(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("an administrator SESSION was refused a password reset on a console mailbox.\n\n"+
			"That is the console's own form; the remediation has broken the panel.\n\nbody: %s",
			rec.Body.String())
	}
}

// The ORDINARY mailboxes are the ones automation actually manages, and they must
// stay reachable by an API key. This is the whole reason the guard reads the
// target's role instead of tightening the endpoint to a session.
func TestScopedMailKeyStillManagesAnOrdinaryMailbox(t *testing.T) {
	a := mailTakeoverApp(t)
	before := passwordHashOf(t, a, "dana@example.com")

	rec := httptest.NewRecorder()
	a.handleVayuOSAccountUpdate(rec,
		mailKeyReq(http.MethodPost, "/os/vayumail/accounts/update",
			`{"email":"dana@example.com","pass":"rotated by the provisioning script"}`, "application/json"))

	if rec.Code == http.StatusForbidden {
		t.Fatalf("a mail:write key was refused a password reset on an ORDINARY mailbox (role "+
			"\"mailbox\", no console access).\n\nThat is the documented API-key path and every "+
			"provisioning script an operator has written uses it. Closing it is a worse outage "+
			"than the hole it fixes.\n\nbody: %s", rec.Body.String())
	}
	if after := passwordHashOf(t, a, "dana@example.com"); after == before {
		t.Error("the ordinary mailbox's password did not change, so the key's legitimate path " +
			"is broken even though it was not refused")
	}
}

// And the non-credential fields stay open to a key even on a console mailbox:
// quota and retention change no one's ability to sign in, and fencing them would
// cost an operator their storage automation for no security gain.
func TestScopedMailKeyStillSetsQuotaOnAConsoleMailbox(t *testing.T) {
	a := mailTakeoverApp(t)

	rec := httptest.NewRecorder()
	a.handleVayuOSAccountUpdate(rec,
		mailKeyReq(http.MethodPost, "/os/vayumail/accounts/update",
			`{"email":"boss@example.com","quota_mb":2048}`, "application/json"))

	if rec.Code == http.StatusForbidden {
		t.Errorf("a mail:write key was refused a QUOTA change on a console mailbox.\n\n"+
			"Storage limits are not a credential and this guard was meant to be narrow.\n\nbody: %s",
			rec.Body.String())
	}
}
