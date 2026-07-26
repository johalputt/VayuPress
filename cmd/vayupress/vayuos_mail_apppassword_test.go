// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"

	_ "github.com/mattn/go-sqlite3"
)

// appWithMailAccounts builds an App around a fully started VayuMail engine
// (in-memory SQLite, temp storage, no protocol listeners) holding one active
// mailbox, dana@example.com, so the app-password handlers and the auth bridge
// exercise the real account store.
func appWithMailAccounts(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1) // :memory: is per-connection; pin the pool so all queries share one DB
	t.Cleanup(func() { _ = db.Close() })

	cfg := vmail.DefaultConfig()
	cfg.Enabled = true
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.StorageDir = t.TempDir()
	cfg.InboundEnabled = false // no listeners in tests
	e := vmail.NewEngine(&cfg, nil, db)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })

	hash, err := auth.HashSecretArgon2id("main-mailbox-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := e.Accounts().Create(context.Background(), "dana@example.com", hash, "Dana", "mailbox"); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return &App{vayuMail: e}
}

// withUser attaches a session user to the request context, the same way
// requireSessionOrAPIKey does for a real session.
func withUser(req *http.Request, u *users.User) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxUserKey, u))
}

// postAppPwForm drives one of the app-password POST handlers with a URL-encoded
// form, as the Connect tab's HTMX forms submit it.
func postAppPwForm(h http.HandlerFunc, path string, vals url.Values, u *users.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if u != nil {
		req = withUser(req, u)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// appPwSecretRe matches the one-time reveal: five dash-separated blocks of four
// alphanumerics (20 secret characters).
var appPwSecretRe = regexp.MustCompile(`[A-Za-z0-9]{4}(?:-[A-Za-z0-9]{4}){4}`)

// TestAppPasswordCreateVerifyRevoke covers the full lifecycle: create via the
// console handler → the secret (with AND without dashes) authenticates through
// the mail auth bridge → revoke via the console handler → it no longer does.
func TestAppPasswordCreateVerifyRevoke(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	ctx := context.Background()
	const email = "dana@example.com"
	// Device approval (on by default, ADR-0129) retires the raw mailbox
	// password on the mail path; this test is about app-password lifecycle, so
	// switch it off to keep exercising the password fast path too.
	if err := a.vayuMail.Accounts().SetRequireDeviceApproval(ctx, email, false); err != nil {
		t.Fatalf("disable device approval: %v", err)
	}

	rec := postAppPwForm(a.handleVayuOSAppPasswordCreate, "/os/vayumail/accounts/apppassword",
		url.Values{"email": {email}, "label": {"Test Phone"}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "shown only once") {
		t.Error("reveal page must warn the secret is shown only once")
	}
	grouped := appPwSecretRe.FindString(body)
	if grouped == "" {
		t.Fatalf("no grouped secret in create response:\n%s", body)
	}
	dashless := strings.ReplaceAll(grouped, "-", "")
	if len(dashless) != 20 {
		t.Fatalf("secret length = %d, want 20 (%q)", len(dashless), grouped)
	}

	// The stored hash must never appear in any response.
	creds := a.vayuMail.Accounts().AppPasswordCredentials(ctx, email)
	if len(creds) != 1 {
		t.Fatalf("want 1 stored credential, got %d", len(creds))
	}
	if strings.Contains(body, creds[0].Hash) || strings.Contains(body, "argon2id") {
		t.Error("create response leaks the stored hash")
	}

	// The new secret signs in over the mail auth bridge — pasted with dashes
	// (as displayed) or typed without them.
	bridge := &vayuMailBridge{app: a}
	if !bridge.verifyCredential(ctx, email, grouped) {
		t.Error("app password with dashes rejected")
	}
	if !bridge.verifyCredential(ctx, email, dashless) {
		t.Error("app password without dashes rejected")
	}
	if bridge.verifyCredential(ctx, email, "Aaaa-Bbbb-Cccc-Dddd-Eeee") {
		t.Error("wrong app password accepted")
	}

	// The list shows metadata only.
	list := a.vayuMail.Accounts().ListAppPasswords(ctx, email)
	if len(list) != 1 || list[0].Label != "Test Phone" {
		t.Fatalf("list = %+v, want one entry labelled Test Phone", list)
	}
	if !strings.Contains(body, "Test Phone") {
		t.Error("card should list the new credential's label")
	}

	// Revoke → the credential stops authenticating in either spelling.
	rec = postAppPwForm(a.handleVayuOSAppPasswordDelete, "/os/vayumail/accounts/apppassword/delete",
		url.Values{"email": {email}, "id": {intToStr(int(list[0].ID))}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", rec.Code)
	}
	if got := a.vayuMail.Accounts().ListAppPasswords(ctx, email); len(got) != 0 {
		t.Fatalf("after revoke want 0 credentials, got %d", len(got))
	}
	if bridge.verifyCredential(ctx, email, grouped) || bridge.verifyCredential(ctx, email, dashless) {
		t.Error("revoked app password still authenticates")
	}
	// The mailbox's own password is untouched by the revoke.
	if !bridge.verifyCredential(ctx, email, "main-mailbox-pass") {
		t.Error("mailbox password should still authenticate after revoking an app password")
	}
}

// TestAppPasswordDefaultLabelAndUnknownMailbox pins the default label and the
// guard that refuses to mint a credential for an address with no active
// account (which would otherwise become a login for a non-existent mailbox).
func TestAppPasswordDefaultLabelAndUnknownMailbox(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}

	rec := postAppPwForm(a.handleVayuOSAppPasswordCreate, "/os/vayumail/accounts/apppassword",
		url.Values{"email": {"dana@example.com"}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200", rec.Code)
	}
	list := a.vayuMail.Accounts().ListAppPasswords(context.Background(), "dana@example.com")
	if len(list) != 1 || list[0].Label != "VayuMail Mobile" {
		t.Fatalf("default label = %+v, want VayuMail Mobile", list)
	}

	rec = postAppPwForm(a.handleVayuOSAppPasswordCreate, "/os/vayumail/accounts/apppassword",
		url.Values{"email": {"ghost@example.com"}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error rendered in card)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no active mailbox") {
		t.Error("creating for an unknown address should render an error")
	}
	if got := a.vayuMail.Accounts().AppPasswordCredentials(context.Background(), "ghost@example.com"); len(got) != 0 {
		t.Fatal("no credential may be stored for a non-existent mailbox")
	}
}

// TestAppPasswordScopedToOwnMailbox verifies the authorisation boundary: an
// anonymous/API-less caller and a non-admin acting on someone else's mailbox
// are refused, while a mailbox holder may mint one for their own address.
func TestAppPasswordScopedToOwnMailbox(t *testing.T) {
	a := appWithMailAccounts(t)
	// ownMailbox requires a user store; an empty DB is fine (GetByID falls back
	// to the session user, whose MailAddress carries the assigned mailbox).
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.userStore = users.New(db)

	// No session user at all → forbidden.
	rec := postAppPwForm(a.handleVayuOSAppPasswordCreate, "/os/vayumail/accounts/apppassword",
		url.Values{"email": {"dana@example.com"}}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous create status = %d, want 403", rec.Code)
	}

	holder := &users.User{ID: "u2", Email: "dana@example.com", Role: users.RoleAuthor, MailAddress: "dana@example.com"}

	// A non-admin targeting another mailbox → forbidden.
	rec = postAppPwForm(a.handleVayuOSAppPasswordCreate, "/os/vayumail/accounts/apppassword",
		url.Values{"email": {"someone-else@example.com"}}, holder)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-mailbox create status = %d, want 403", rec.Code)
	}

	// The holder minting for their own mailbox → allowed.
	rec = postAppPwForm(a.handleVayuOSAppPasswordCreate, "/os/vayumail/accounts/apppassword",
		url.Values{"email": {"dana@example.com"}, "label": {"My phone"}}, holder)
	if rec.Code != http.StatusOK {
		t.Fatalf("own-mailbox create status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	list := a.vayuMail.Accounts().ListAppPasswords(context.Background(), "dana@example.com")
	if len(list) != 1 || list[0].Label != "My phone" {
		t.Fatalf("list = %+v, want the holder's credential", list)
	}

	// …but revoking another mailbox's credential stays forbidden.
	rec = postAppPwForm(a.handleVayuOSAppPasswordDelete, "/os/vayumail/accounts/apppassword/delete",
		url.Values{"email": {"someone-else@example.com"}, "id": {"1"}}, holder)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-mailbox revoke status = %d, want 403", rec.Code)
	}
}

// TestConnectTabDescribesAppPasswordFlow pins the redesigned Connect tab copy:
// the official-app card describes install → email + app password → autoconfig
// + WKD, embeds the app-password card, and carries no leftover QR wording.
func TestConnectTabDescribesAppPasswordFlow(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}

	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/connect", nil), admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSConnect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("connect status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"app password",
		`id="vm-apppw-card"`,
		"/.well-known/vayumail/autoconfig.json",
		"WKD",
		"/os/vayumail/accounts/apppassword",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Connect tab missing %q", want)
		}
	}
	if strings.Contains(body, "QR") {
		t.Error("Connect tab still carries QR wording (removed in v3.9.16)")
	}
}

// TestGroupAppPasswordSecret pins the display grouping: 4-char dash-separated
// blocks whose dashless form is exactly the stored/verified secret.
func TestGroupAppPasswordSecret(t *testing.T) {
	secret, err := generateAppPasswordSecret()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(secret) != appPasswordLength {
		t.Fatalf("secret length = %d, want %d", len(secret), appPasswordLength)
	}
	for _, c := range secret {
		if !strings.ContainsRune(appPasswordAlphabet, c) {
			t.Fatalf("secret contains %q outside the alphanumeric alphabet", c)
		}
	}
	grouped := groupAppPasswordSecret(secret)
	if !appPwSecretRe.MatchString(grouped) {
		t.Fatalf("grouped form %q not in abcd-efgh-… layout", grouped)
	}
	if strings.ReplaceAll(grouped, "-", "") != secret {
		t.Fatalf("grouping must be presentation-only: %q vs %q", grouped, secret)
	}
}
