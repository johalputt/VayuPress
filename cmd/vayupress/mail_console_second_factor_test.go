// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
)

// A mailbox session becomes the console account of its address. When that
// account has two-step verification, the mailbox password alone must not open
// the console as it: the password is tested over IMAP with no second factor
// and can be reset through mail recovery. Found on johal.in, 2026-09-28, from a
// reset for admin@ that its operator had not asked for.
func TestAMailboxPasswordDoesNotSkipTheConsoleSecondFactor(t *testing.T) {
	const addr, secret = "boss@example.com", "JBSWY3DPEHPK3PXP"
	ctx := context.Background()
	for _, c := range []struct {
		name                  string
		consoleTOTP, mboxTOTP bool
		wantConsole           bool
	}{
		// The bypass: the console account has a second factor, the mailbox does not.
		{"console two-step, mailbox none", true, false, false},
		// Both sign-in paths for a mailbox ask its own code, so a mailbox with
		// two-step has passed a second factor.
		{"both two-step", true, true, true},
		// Nothing to bypass: no second factor anywhere, unchanged.
		{"no two-step anywhere", false, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := resetSessionApp(t)
			u, err := a.userStore.Create(ctx, addr, "Boss", "a-different-console-password", "admin")
			if err != nil {
				t.Fatal(err)
			}
			if c.consoleTOTP {
				if err := a.userStore.SetTOTPSecret(ctx, u.ID, secret); err != nil {
					t.Fatal(err)
				}
				if err := a.userStore.EnableTOTP(ctx, u.ID); err != nil {
					t.Fatal(err)
				}
			}
			if c.mboxTOTP {
				if err := a.vayuMail.Accounts().SetTOTPSecret(ctx, addr, secret); err != nil {
					t.Fatal(err)
				}
				if err := a.vayuMail.Accounts().EnableTOTP(ctx, addr); err != nil {
					t.Fatal(err)
				}
			}
			got, mailOnly, ok := a.resolveMailSessionUser(ctx, addr)
			if !ok {
				t.Fatal("the mailbox session did not resolve at all; it must still open Mail")
			}
			if console := !mailOnly; console != c.wantConsole {
				t.Fatalf("console = %v, want %v (resolved as %q, role %q)", console, c.wantConsole, got.ID, got.Role)
			}
			if !c.wantConsole && got.ID == u.ID {
				t.Error("the session was unified with the console account whose second factor it never passed")
			}
		})
	}
}

// The member portal signs a mailbox in with the same mailbox password and code,
// and resolveMailMember unifies it the same way, so it carries the same rule
// under its own guard.
func TestAPortalMailboxSessionDoesNotSkipTheConsoleSecondFactor(t *testing.T) {
	const addr, secret = "boss@example.com", "JBSWY3DPEHPK3PXP"
	ctx := context.Background()
	a := resetSessionApp(t)
	a.members = members.New(dbpkg.DB)
	u, err := a.userStore.Create(ctx, addr, "Boss", "a-different-console-password", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.userStore.SetTOTPSecret(ctx, u.ID, secret); err != nil {
		t.Fatal(err)
	}
	if err := a.userStore.EnableTOTP(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	m, err := a.members.Upsert(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.members.CreateSession(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/os", nil)
	req.AddCookie(&http.Cookie{Name: memberCookie, Value: token})
	got, mailOnly, ok := a.resolveMailMember(req)
	if !ok {
		t.Fatal("the portal session did not resolve at all; it must still open Mail")
	}
	if !mailOnly || got.ID == u.ID {
		t.Errorf("a portal session with the mailbox password alone reached the console as %q (mailOnly=%v)", got.ID, mailOnly)
	}
}
