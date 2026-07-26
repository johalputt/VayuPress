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

// postAcctAction drives the HTMX inline-action handler with a URL-encoded form.
func postAcctAction(a *App, vals string, u *users.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/accounts/action", strings.NewReader(vals))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if u != nil {
		req = withUser(req, u)
	}
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountsAction(rec, req)
	return rec
}

// TestAccountsListFragmentRendersCollapsibleCards pins the redesigned list: a
// collapsible <details> card per mailbox, the stat strip, and the HTMX action
// wiring — served only to admins.
func TestAccountsListFragmentRendersCollapsibleCards(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}

	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/accounts/fragment", nil), admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountsFragment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="vm-stats"`,             // enterprise stat strip
		`<details class="vm-acct`,      // collapsible card
		"dana@example.com",             // the seeded mailbox
		"/os/vayumail/accounts/action", // inline HTMX action target
		`hx-target="#vm-accounts-list"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("accounts fragment missing %q", want)
		}
	}
}

// TestAccountsFragmentAdminOnly ensures a non-admin cannot read the list.
func TestAccountsFragmentAdminOnly(t *testing.T) {
	a := appWithMailAccounts(t)
	holder := &users.User{ID: "u2", Email: "dana@example.com", Role: users.RoleAuthor}
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/accounts/fragment", nil), holder)
	rec := httptest.NewRecorder()
	a.handleVayuOSAccountsFragment(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin fragment status = %d, want 403", rec.Code)
	}
}

// TestAccountsActionToggleAndDelete drives the inline HTMX actions and asserts
// they mutate the account and return the refreshed list fragment.
func TestAccountsActionToggleAndDelete(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	ctx := context.Background()

	// Disable the mailbox via the action handler.
	rec := postAcctAction(a, "op=toggle&email=dana@example.com&active=false", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<details class="vm-acct`) {
		t.Error("toggle did not return the refreshed list fragment")
	}
	accs, _ := a.vayuMail.Accounts().List(ctx)
	if len(accs) != 1 || accs[0].Active {
		t.Fatalf("account should be disabled after toggle: %+v", accs)
	}

	// Delete the mailbox.
	rec = postAcctAction(a, "op=delete&email=dana@example.com", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", rec.Code)
	}
	accs, _ = a.vayuMail.Accounts().List(ctx)
	if len(accs) != 0 {
		t.Fatalf("account should be gone after delete: %+v", accs)
	}
}

// TestAccountsActionAdminOnly ensures the mailbox holder cannot mutate accounts.
func TestAccountsActionAdminOnly(t *testing.T) {
	a := appWithMailAccounts(t)
	holder := &users.User{ID: "u2", Email: "dana@example.com", Role: users.RoleAuthor}
	rec := postAcctAction(a, "op=delete&email=dana@example.com", holder)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin action status = %d, want 403", rec.Code)
	}
	accs, _ := a.vayuMail.Accounts().List(context.Background())
	if len(accs) != 1 {
		t.Fatal("non-admin must not be able to delete an account")
	}
}

// TestDevicesFragmentBacksPoller pins the GET fragment that lets a newly pending
// device surface without a full-page reload, and that it stays admin-only.
func TestDevicesFragmentBacksPoller(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	if _, _, _, status := registerDevice(t, a, "dana@example.com", "main-mailbox-pass", "Dana's phone", "android"); status != "pending" {
		t.Fatalf("register status = %q, want pending", status)
	}

	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/devices/fragment", nil), admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSDevicesFragment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("devices fragment status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`hx-get="/os/vayumail/devices/fragment"`, // the self-refresh poller
		`badge--pending`,                         // the pending device chip
		"awaiting approval",                      // the attention banner
		"Dana&#39;s phone",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("devices fragment missing %q", want)
		}
	}

	// Admin-only.
	holder := &users.User{ID: "u2", Email: "dana@example.com", Role: users.RoleAuthor}
	req2 := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/devices/fragment", nil), holder)
	rec2 := httptest.NewRecorder()
	a.handleVayuOSDevicesFragment(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("non-admin devices fragment = %d, want 403", rec2.Code)
	}
}
