// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/totp"
)

func postVayuMailLoginCode(t *testing.T, a *App, ip, email, pass, code string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"email": email, "password": pass, "code": code})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/members/vayumail-login", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":40000"
	rec := httptest.NewRecorder()
	a.handleMemberVayuMailLogin(rec, req)
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Authenticated bool `json:"authenticated"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Authenticated {
		return rec.Code, "authenticated"
	}
	return rec.Code, out.Error.Code
}

func nowCode(t *testing.T, secret string) string {
	t.Helper()
	c, err := totp.GenerateAt(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The site's "Sign in with VayuMail" knew only mail-only mailboxes. The owner,
// staff and authors, whose mailbox is their console account, were told their
// password did not match, so with two-factor on they never reached the code
// field and had to sign in at /os (3ck). Each case is its own address, so each
// asserts one rule.
func TestSiteMailSignInTakesTheConsoleAccountAndItsSecondFactor(t *testing.T) {
	a := credentialFloodApp(t)
	ctx := context.Background()
	const pass = "owner-password-123"

	// A console account without two-factor signs in.
	if _, err := a.userStore.Create(ctx, "plain@example.com", "Plain", pass, "admin"); err != nil {
		t.Fatal(err)
	}
	if code, got := postVayuMailLoginCode(t, a, "198.51.100.10", "plain@example.com", pass, ""); got != "authenticated" {
		t.Errorf("a console account's own password = %d %s, want a sign-in", code, got)
	}

	// With two-factor on, the code is asked for, then accepted once.
	u, err := a.userStore.Create(ctx, "owner@example.com", "Owner", pass, "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := totp.GenerateSecret()
	_ = a.userStore.SetTOTPSecret(ctx, u.ID, secret)
	_ = a.userStore.EnableTOTP(ctx, u.ID)
	if _, got := postVayuMailLoginCode(t, a, "198.51.100.11", "owner@example.com", pass, ""); got != "totp-required" {
		t.Errorf("a console account with two-factor and no code = %s, want totp-required", got)
	}
	c := nowCode(t, secret)
	if code, got := postVayuMailLoginCode(t, a, "198.51.100.11", "owner@example.com", pass, c); got != "authenticated" {
		t.Errorf("the right code = %d %s, want a sign-in", code, got)
	}
	if _, got := postVayuMailLoginCode(t, a, "198.51.100.12", "owner@example.com", pass, c); got != "totp-invalid" {
		t.Errorf("the same code again = %s, want totp-invalid", got)
	}
}

// The second factor belongs to the address. A mail-only mailbox sharing the
// address of a console account with two-factor on signed in on the mailbox's
// password with no code at all.
func TestTheMailboxPasswordDoesNotSkipTheConsoleAccountsSecondFactor(t *testing.T) {
	a := credentialFloodApp(t)
	ctx := context.Background()
	u, err := a.userStore.Create(ctx, "editor@example.com", "Editor", "console-password-1", "editor")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := totp.GenerateSecret()
	_ = a.userStore.SetTOTPSecret(ctx, u.ID, secret)
	_ = a.userStore.EnableTOTP(ctx, u.ID)
	h, _ := auth.HashSecretArgon2id("mailbox-password-1")
	if err := a.vayuMail.Accounts().Create(ctx, "editor@example.com", h, "Editor", "mailbox"); err != nil {
		t.Fatal(err)
	}
	if _, got := postVayuMailLoginCode(t, a, "198.51.100.20", "editor@example.com", "mailbox-password-1", ""); got != "totp-required" {
		t.Errorf("the mailbox password with the console account's two-factor on = %s, want totp-required", got)
	}
}

// A mailbox's own two-factor still works as it did.
func TestAMailboxsOwnSecondFactorStillSignsIn(t *testing.T) {
	a := credentialFloodApp(t)
	ctx := context.Background()
	secret, _ := totp.GenerateSecret()
	_ = a.vayuMail.Accounts().SetTOTPSecret(ctx, "boss@example.com", secret)
	_ = a.vayuMail.Accounts().EnableTOTP(ctx, "boss@example.com")
	const pass = "the-password-the-attacker-stole"
	if _, got := postVayuMailLoginCode(t, a, "198.51.100.30", "boss@example.com", pass, ""); got != "totp-required" {
		t.Errorf("a mailbox with two-factor and no code = %s, want totp-required", got)
	}
	if code, got := postVayuMailLoginCode(t, a, "198.51.100.30", "boss@example.com", pass, nowCode(t, secret)); got != "authenticated" {
		t.Errorf("the mailbox's code = %d %s, want a sign-in", code, got)
	}
}

// A handed-over mailbox belongs to its client: the console account of the same
// address no longer opens it, here as over IMAP.
func TestAHandedOverMailboxIsNotOpenedByTheConsoleAccount(t *testing.T) {
	a := credentialFloodApp(t)
	ctx := context.Background()
	if _, err := a.userStore.Create(ctx, "boss@example.com", "Operator", "console-password-2", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.HandOver(ctx, "boss@example.com", "operator", ""); err != nil {
		t.Fatal(err)
	}
	if _, got := postVayuMailLoginCode(t, a, "198.51.100.40", "boss@example.com", "console-password-2", ""); got != "invalid-credentials" {
		t.Errorf("the console password on a handed-over mailbox = %s, want invalid-credentials", got)
	}
}
