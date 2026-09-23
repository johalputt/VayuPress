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

// TestEveryPerMailboxSettingLivesOnItsOwnPage replaces the old "one card holds
// everything" pin.
//
// The design changed deliberately: forwarding, vacation, aliases, recovery,
// handover, PGP, filters and the picture picker moved to a routed page, because a
// many-mailbox install was one very tall page whose every inline action re-rendered
// the whole list. The guarantee that must survive the move is that NOTHING became
// unreachable — so this asserts the card links out and the page carries it all.
func TestEveryPerMailboxSettingLivesOnItsOwnPage(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}

	// The list card points at the page.
	reqList := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/accounts/fragment", nil), admin)
	recList := httptest.NewRecorder()
	a.handleVayuOSAccountsFragment(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want 200", recList.Code)
	}
	if list := recList.Body.String(); !strings.Contains(list, "/os/vayumail/accounts/settings?user=") {
		t.Error("the mailbox card must link to its settings page, or every setting below became unreachable")
	}

	// The page carries every setting.
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/accounts/settings?user=dana@example.com", nil), admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSMailboxSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want 200", rec.Code)
	}
	page := rec.Body.String()
	for _, want := range []string{
		`id="vm-mbox-settings"`,
		"Auto-forward a copy to",                     // forwarding sub-section
		"Vacation autoresponder",                     // vacation sub-section
		"<span class=\"field-label\">Aliases</span>", // aliases sub-section
		"Filter rules",                               // filters sub-section
		`hx-post="/os/vayumail/aliases/action"`,      // per-mailbox alias endpoint
		`hx-post="/os/vayumail/filters/action"`,      // per-mailbox filter endpoint
		`name="target" value="dana@example.com"`,     // alias target fixed to this mailbox
	} {
		if !strings.Contains(page, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
	// The guard on the shared builders: every control on this page must come back
	// to this page. A single missed target would silently swap the accounts list
	// into the settings section.
	if strings.Contains(page, `hx-target="#vm-accounts-list"`) {
		t.Error("a control on the settings page still targets the accounts list — it would swap the wrong surface")
	}
	// This page hosts the recovery card, whose controls are driven by a separate
	// script; without it they render and do nothing.
	if !strings.Contains(page, "admin-os-mail-recovery.js") {
		t.Error("the settings page must load the recovery script, or its recovery controls are dead buttons")
	}
}

// TestASettingsControlComesBackToTheSettingsSurface pins the surface decision.
func TestASettingsControlComesBackToTheSettingsSurface(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	body := "op=create&email=dana@example.com&field=from&contains=news&action=move:Junk"

	// A control inside the settings page: HTMX names the element it is replacing.
	req := httptest.NewRequest(http.MethodPost, "/os/vayumail/filters/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Target", "vm-mbox-settings")
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSFilterAction(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings-surface filter action = %d, want 200", rec.Code)
	}
	got := rec.Body.String()
	if !strings.Contains(got, `id="vm-mbox-settings"`) {
		t.Error("a control inside the settings page must get the settings section back")
	}
	if strings.Contains(got, `<details class="vm-acct`) {
		t.Error("the settings page got the accounts list back — the two surfaces were confused")
	}

	// The same action without that header (the original list surface) is unchanged.
	req2 := httptest.NewRequest(http.MethodPost, "/os/vayumail/filters/action", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2 = withUser(req2, admin)
	rec2 := httptest.NewRecorder()
	a.handleVayuOSFilterAction(rec2, req2)
	if got2 := rec2.Body.String(); !strings.Contains(got2, `<details class="vm-acct`) {
		t.Error("a list-surface control must still receive the accounts list")
	}
}

// TestTheSettingsPageSaysWhenAMailboxIsUnknown — an unknown address must say so
// rather than render a page of empty controls.
func TestTheSettingsPageSaysWhenAMailboxIsUnknown(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/accounts/settings?user=nobody@example.com", nil), admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSMailboxSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a rendered explanation)", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "No mailbox with that address") {
		t.Error("an unknown mailbox must be explained, not rendered blank")
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

// The settings page holds forwarding, recovery and handover for ANY mailbox, so
// it is an administrator's page. A mailbox user signed in to the console must be
// turned away — even for their own address, which they manage elsewhere — and
// see none of another mailbox's forwarding or recovery settings.
func TestTheSettingsPageIsForAdministratorsOnly(t *testing.T) {
	a := appWithMailAccounts(t)
	user := &users.User{ID: "u1", Email: "erin@example.com", Role: users.RoleEditor, MailAddress: "erin@example.com"}
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/accounts/settings?user=dana@example.com", nil), user)
	rec := httptest.NewRecorder()
	a.handleVayuOSMailboxSettings(rec, req)
	if body := rec.Body.String(); strings.Contains(body, `id="vm-mbox-settings"`) || strings.Contains(body, "Auto-forward a copy to") {
		t.Errorf("a non-admin was shown another mailbox's settings (status %d)", rec.Code)
	}
}
