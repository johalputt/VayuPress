// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/totp"
)

func consoleLoginCode(a *App, pass, code, ip string) *httptest.ResponseRecorder {
	form := url.Values{"email": {"boss@example.com"}, "password": {pass}, "totp": {code}}
	req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = ip + ":5000"
	rec := httptest.NewRecorder()
	a.handleOSLoginSubmit(rec, req)
	return rec
}

// /os/login let a mailbox password in on the mailbox's own second factor
// alone, though the VayuOS account at the same address had two-factor on
// (3cn, the console's half of what 3ck closed on the site). The code is now
// asked for; the account's code lets the mailbox in; and once the mailbox is
// handed over, the account's two-factor no longer applies to it.
func TestConsoleLoginAsksForTheAddresssSecondFactor(t *testing.T) {
	a := resetSessionApp(t)
	ctx := context.Background()
	u, err := a.userStore.Create(ctx, "boss@example.com", "Operator", "a-different-console-password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := totp.GenerateSecret()
	_ = a.userStore.SetTOTPSecret(ctx, u.ID, secret)
	_ = a.userStore.EnableTOTP(ctx, u.ID)

	rec := consoleLoginCode(a, enrolPassword, "", "203.0.113.20")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Enter the 6-digit code") || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("the mailbox password with the account's two-factor on = %d, cookie %q; want the code asked for and no session", rec.Code, rec.Header().Get("Set-Cookie"))
	}
	code, _ := totp.GenerateAt(secret, time.Now())
	if rec := consoleLoginCode(a, enrolPassword, code, "203.0.113.21"); rec.Code != http.StatusSeeOther {
		t.Fatalf("with the account's code = %d, want a sign-in", rec.Code)
	}

	if err := a.vayuMail.HandOver(ctx, "boss@example.com", "operator", ""); err != nil {
		t.Fatal(err)
	}
	if rec := consoleLoginCode(a, enrolPassword, "", "203.0.113.22"); rec.Code != http.StatusSeeOther {
		t.Errorf("a handed-over mailbox with no two-factor of its own = %d, want a sign-in on its password", rec.Code)
	}
}
