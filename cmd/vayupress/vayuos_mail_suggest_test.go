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

	"github.com/johalputt/vayupress/internal/users"
)

func suggestFor(t *testing.T, a *App, u *users.User, user, q string) string {
	t.Helper()
	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/search/suggest?"+url.Values{"user": {user}, "q": {q}}.Encode(), nil), u)
	rec := httptest.NewRecorder()
	a.handleVayuOSSearchSuggest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("suggest %q: %d", q, rec.Code)
	}
	return rec.Body.String()
}

// A sentence is offered back as the terms it reads as; the word being
// typed finds the mailbox's own contacts, as a From term that keeps what
// came before it; the filters are always there.
func TestSearchSuggestionsReadTheSentenceAndFindPeople(t *testing.T) {
	a := appWithMailAccounts(t)
	ctx := context.Background()
	if err := a.vayuMail.Accounts().AddContact(ctx, "dana@example.com", "priya@example.org", "Priya Raman"); err != nil {
		t.Fatal(err)
	}
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}

	got := suggestFor(t, a, admin, "dana", "pdf from priya last month")
	if !strings.Contains(got, ">Search for<") || !strings.Contains(got, `data-mx-q="pdf from:priya after:`) || !strings.Contains(got, "Has attachment") {
		t.Errorf("the sentence is not offered as its terms:\n%s", got)
	}
	got = suggestFor(t, a, admin, "dana", "invoice pri")
	if !strings.Contains(got, ">People<") || !strings.Contains(got, `data-mx-q="invoice from:priya@example.org"`) || !strings.Contains(got, "Priya Raman") {
		t.Errorf("the contact is not offered for the word being typed:\n%s", got)
	}
	if !strings.Contains(got, `data-mx-add="has:attachment"`) || !strings.Contains(got, `data-mx-add="from:"`) {
		t.Errorf("the filters are missing:\n%s", got)
	}
	// Plain words read as nothing more: no Search for row repeating them.
	if got := suggestFor(t, a, admin, "dana", "quarterly report"); strings.Contains(got, ">Search for<") {
		t.Errorf("plain words are offered back as a reading:\n%s", got)
	}
}

// A contact's name and address are the mailbox owner's own data, drawn
// into the console: each is escaped where it is shown and where it is put
// into the field.
func TestSearchSuggestionsEscapeAContact(t *testing.T) {
	a := appWithMailAccounts(t)
	if err := a.vayuMail.Accounts().AddContact(context.Background(), "dana@example.com", "evil@example.org", `<img src=x onerror=alert(1)>`); err != nil {
		t.Fatal(err)
	}
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	got := suggestFor(t, a, admin, "dana", `"><svg onload=x> ev`)
	if strings.Contains(got, "<img src=x") || strings.Contains(got, "<svg") {
		t.Errorf("a contact's name or the typed words reach the page unescaped:\n%s", got)
	}
	if !strings.Contains(got, "evil@example.org") {
		t.Errorf("the contact is not offered at all, so escaping was not tested:\n%s", got)
	}
}

// Someone who is not an administrator gets their own mailbox's contacts,
// whatever mailbox they name.
func TestSearchSuggestionsAreTheSessionsOwnMailbox(t *testing.T) {
	a := appWithMailAccounts(t)
	if err := a.vayuMail.Accounts().AddContact(context.Background(), "dana@example.com", "secret@example.org", "Dana's contact"); err != nil {
		t.Fatal(err)
	}
	stranger := &users.User{ID: "u2", Email: "stranger@elsewhere.example", Role: users.RoleAuthor}
	if got := suggestFor(t, a, stranger, "dana", "sec"); strings.Contains(got, "secret@example.org") {
		t.Errorf("another mailbox's contact is offered:\n%s", got)
	}
}

// The date filters are counted back from today.
func TestSearchSuggestionFiltersCountFromToday(t *testing.T) {
	a := appWithMailAccounts(t)
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	got := a.mailSuggestions(httptest.NewRequest(http.MethodGet, "/", nil), "dana@example.com", "", now)
	if !strings.Contains(got, `data-mx-add="after:2026-10-30"`) || !strings.Contains(got, `data-mx-add="after:2026-10-07"`) {
		t.Errorf("the date filters:\n%s", got)
	}
}

// Mail's top bar tools: the keyboard button, and quick settings holding the
// Reading choices (when Reading is offered), the colour scheme and the way
// to all settings, which for an administrator is their own mailbox's page.
func TestMailTopToolsOfferTheKeysAndQuickSettings(t *testing.T) {
	holder := mailTopTools(&osMailSide{User: "ankush", Address: "ankush@example.com", Reading: true}, "dark")
	for _, want := range []string{`data-mx-help`, `data-mx-layout-choice="full"`, `data-sa-theme="dark" aria-pressed="true"`, `href="/os/vayumail/settings"`} {
		if !strings.Contains(holder, want) {
			t.Errorf("a holder's top tools lack %q:\n%s", want, holder)
		}
	}
	admin := mailTopTools(&osMailSide{User: "ankush", Address: "ankush@example.com", Admin: true}, "auto")
	if !strings.Contains(admin, `href="/os/vayumail/accounts/settings?user=ankush%40example.com"`) {
		t.Errorf("an administrator's All settings is not their mailbox's page:\n%s", admin)
	}
	if strings.Contains(admin, "data-mx-layout-choice") {
		t.Errorf("Reading choices are offered where Reading is not:\n%s", admin)
	}
}
