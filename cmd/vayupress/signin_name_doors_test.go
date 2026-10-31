// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

const bossName = "boss-7k2q"

// nameDoors are the doors a mailbox's own password signs in at, each trying
// who (an address or a sign-in name) with the right password.
func nameDoors() []struct {
	name string
	try  func(t *testing.T, a *App, who string) bool
} {
	return []struct {
		name string
		try  func(t *testing.T, a *App, who string) bool
	}{
		{"the site's sign-in", func(t *testing.T, a *App, who string) bool {
			_, got := postVayuMailLoginCode(t, a, "198.51.100.120", who, lockedRight, "")
			return got == "authenticated"
		}},
		{"the console's sign-in", func(t *testing.T, a *App, who string) bool {
			form := url.Values{"email": {who}, "password": {lockedRight}}
			req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.RemoteAddr = "198.51.100.121:5000"
			rec := httptest.NewRecorder()
			a.handleOSLoginSubmit(rec, req)
			return rec.Code == http.StatusSeeOther
		}},
		{"a mail app", func(t *testing.T, a *App, who string) bool {
			ok, _ := (&vayuMailBridge{app: a}).AuthUser(who, lockedRight, vmail.Origin{Addr: "198.51.100.122:1", Door: "imap"})
			return ok
		}},
		{"contacts and calendars", func(t *testing.T, a *App, who string) bool {
			_, ok := a.davSignIn(who, lockedRight, "198.51.100.123")
			return ok
		}},
		{"VayuTalk", func(t *testing.T, a *App, who string) bool {
			return a.talkVerify(context.Background(), who, lockedRight)
		}},
	}
}

// With a sign-in name, the address no longer signs in with the right
// password at any door, and the name does.
func TestAMailboxWithASigninNameSignsInByTheNameAlone(t *testing.T) {
	for _, d := range nameDoors() {
		t.Run(d.name, func(t *testing.T) {
			a := credentialFloodApp(t)
			ctx := context.Background()
			if err := a.vayuMail.Accounts().SetRequireDeviceApproval(ctx, lockedMailbox, false); err != nil {
				t.Fatal(err)
			}
			if !d.try(t, a, lockedMailbox) {
				t.Fatalf("the address and its right password did not sign in at %s before a name was set", d.name)
			}
			auth.LiftAllAccountLockouts()
			if err := a.vayuMail.Accounts().SetSigninName(ctx, lockedMailbox, bossName); err != nil {
				t.Fatal(err)
			}
			if d.try(t, a, lockedMailbox) {
				t.Errorf("%s let the address in with the right password after a sign-in name was set", d.name)
			}
			if !d.try(t, a, bossName) {
				t.Errorf("%s refused the sign-in name with the right password", d.name)
			}
		})
	}
}

// A mail app set up with the address and an app password keeps working when
// a sign-in name is set: an app password is not guessable.
func TestAnAppPasswordStillSignsInWithTheAddress(t *testing.T) {
	a := credentialFloodApp(t)
	ctx := context.Background()
	const secret = "abcdabcdabcdabcdabcd"
	hash, _ := auth.HashSecretArgon2id(secret)
	if _, err := a.vayuMail.Accounts().CreateAppPassword(ctx, lockedMailbox, "phone", hash); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().SetSigninName(ctx, lockedMailbox, bossName); err != nil {
		t.Fatal(err)
	}
	if ok, _ := (&vayuMailBridge{app: a}).AuthUser(lockedMailbox, secret, vmail.Origin{Addr: "198.51.100.124:1", Door: "imap"}); !ok {
		t.Error("an app password stopped working with the address when a sign-in name was set")
	}
}

// The holder sets and clears the name from the card; a name another mailbox
// has is refused in words.
func TestTheHolderSetsTheSigninName(t *testing.T) {
	a := credentialFloodApp(t)
	withSignins(t, a)
	boss := holderOf(lockedMailbox)
	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"name"}, "name": {bossName}}); rec.Code != http.StatusOK || a.vayuMail.Accounts().SigninName(context.Background(), lockedMailbox) != bossName {
		t.Fatalf("setting the name = %d", rec.Code)
	}
	if rec := postAsFrom(a, holderOf("erin@example.com"), "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"name"}, "name": {""}}); rec.Code != http.StatusForbidden {
		t.Errorf("another holder clearing the name = %d, want 403", rec.Code)
	}
	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"name"}, "name": {"x"}}); !strings.Contains(rec.Body.String(), "6 to 64") {
		t.Error("a name too short was not refused in words")
	}
	if rec := postAsFrom(a, boss, "192.0.2.10", url.Values{"email": {lockedMailbox}, "op": {"name"}, "name": {""}}); rec.Code != http.StatusOK || a.vayuMail.Accounts().SigninName(context.Background(), lockedMailbox) != "" {
		t.Error("clearing the name did not clear it")
	}
}

// At the console, an install account at the address of a mailbox with a
// sign-in name signs in with the name too: its own password with the address
// is refused.
func TestTheConsoleAccountAtANamedAddressSignsInByTheName(t *testing.T) {
	a := credentialFloodApp(t)
	ctx := context.Background()
	const consolePass = "the-console-password-1"
	if _, err := a.userStore.Create(ctx, lockedMailbox, "Boss", consolePass, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.Accounts().SetSigninName(ctx, lockedMailbox, bossName); err != nil {
		t.Fatal(err)
	}
	try := func(who string) int {
		form := url.Values{"email": {who}, "password": {consolePass}}
		req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "198.51.100.125:5000"
		rec := httptest.NewRecorder()
		a.handleOSLoginSubmit(rec, req)
		return rec.Code
	}
	if code := try(lockedMailbox); code == http.StatusSeeOther {
		t.Error("the console account's password with the named address signed in")
	}
	if code := try(bossName); code != http.StatusSeeOther {
		t.Errorf("the console account's password with the sign-in name = %d, want a sign-in", code)
	}
}
