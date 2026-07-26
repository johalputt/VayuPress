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

// postForm drives an HTMX action handler with a URL-encoded form body.
func postForm(h http.HandlerFunc, path, vals string, u *users.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(vals))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if u != nil {
		req = withUser(req, u)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// TestAccountCardHasEverySetting pins the consolidation: expanding a mailbox
// shows all of its own controls — forwarding, vacation, aliases and filters —
// inside the one card, each wired to refresh the accounts list in place.
func TestAccountCardHasEverySetting(t *testing.T) {
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
		"Auto-forward a copy to",                     // forwarding sub-section
		"Vacation autoresponder",                     // vacation sub-section
		"<span class=\"field-label\">Aliases</span>", // aliases sub-section
		"Filter rules",                               // filters sub-section
		`hx-post="/os/vayumail/aliases/action"`,      // per-card alias endpoint
		`hx-post="/os/vayumail/filters/action"`,      // per-card filter endpoint
		`name="target" value="dana@example.com"`,     // alias target fixed to this mailbox
	} {
		if !strings.Contains(body, want) {
			t.Errorf("account card missing %q", want)
		}
	}
	// The standalone alias/filter cards are gone — no separate swap targets remain.
	for _, gone := range []string{`id="vm-alias-card"`, `id="vm-filter-card"`} {
		if strings.Contains(body, gone) {
			t.Errorf("account list should not carry the removed standalone card %q", gone)
		}
	}
}

// TestCardAliasCreateUsesMailboxDomain drives the alias action from inside a card
// and asserts the alias is built on the target mailbox's own domain (so a
// secondary-domain mailbox gets secondary-domain aliases), and that the handler
// returns the refreshed accounts list — not a standalone card.
func TestCardAliasCreateUsesMailboxDomain(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	ctx := context.Background()

	rec := postForm(a.handleVayuOSAliasAction, "/os/vayumail/aliases/action",
		"op=alias-create&local=sales&target=dana@example.com", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("alias-create status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<details class="vm-acct`) {
		t.Error("alias-create should return the refreshed accounts list fragment")
	}
	aliases, _ := a.vayuMail.Accounts().ListAliases(ctx)
	if len(aliases) != 1 || aliases[0].Alias != "sales@example.com" || aliases[0].Target != "dana@example.com" {
		t.Fatalf("alias not created on the mailbox domain: %+v", aliases)
	}
}

// TestCardFilterCreateRefreshesList drives the filter action from inside a card
// and asserts the rule is stored and the accounts list comes back.
func TestCardFilterCreateRefreshesList(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	ctx := context.Background()

	rec := postForm(a.handleVayuOSFilterAction, "/os/vayumail/filters/action",
		"op=create&email=dana@example.com&field=from&contains=newsletter@&action=move:Junk", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("filter-create status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<details class="vm-acct`) {
		t.Error("filter-create should return the refreshed accounts list fragment")
	}
	rules, _ := a.vayuMail.Accounts().FiltersFor(ctx, "dana@example.com")
	if len(rules) != 1 || rules[0].Field != "from" || rules[0].Action != "move" || rules[0].Target != "Junk" {
		t.Fatalf("filter rule not stored as expected: %+v", rules)
	}
}
