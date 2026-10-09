// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// The list's search is kept with its scope bar written into the terms, one
// seed per scope; the search page, and no search, keep nothing.
func TestASavedSearchKeepsItsScope(t *testing.T) {
	for name, c := range map[string]struct {
		sf   searchFilters
		want string
	}{
		"All mail":            {searchFilters{q: "report", scope: "all", inList: true}, "report"},
		"This folder":         {searchFilters{q: "report", folder: "Inbox", inList: true}, "report in:Inbox"},
		"in: over the folder": {searchFilters{q: "in:Sent report", folder: "Inbox", inList: true}, "report in:Sent"},
		"From":                {searchFilters{q: "priya raman", fromScope: true, inList: true}, `from:"priya raman"`},
		"Has attachments":     {searchFilters{q: "x", attach: true, inList: true}, "x has:attachment"},
		"the search page":     {searchFilters{q: "report"}, ""},
		"no search":           {searchFilters{q: "  ", folder: "Inbox", inList: true}, ""},
	} {
		if got := c.sf.savedQuery(); got != c.want {
			t.Errorf("%s: kept %q, want %q", name, got, c.want)
		}
	}
}

func searchesAction(a *App, u *users.User, vals url.Values) *httptest.ResponseRecorder {
	req := withUser(httptest.NewRequest(http.MethodPost, "/os/vayumail/searches/action", strings.NewReader(vals.Encode())), u)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.handleVayuOSSearchesAction(rec, req)
	return rec
}

// Save keeps the search and answers with the results, their button now
// Forget, and the sidebar's searches out of band, linking to the search;
// Forget undoes it. The list's folder is what the sidebar is not told.
func TestSaveThisSearch(t *testing.T) {
	a := scheduledApp(t)
	raw := "From: Priya <priya@h.test>\r\nTo: dana@example.com\r\nSubject: quarterly report\r\n\r\nnumbers\r\n"
	if _, err := a.vayuMail.DeliverInbound("x@h.test", "dana@example.com", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"q": {"report"}, "scope": {"folder"}, "folder": {"Inbox"}, "in": {"list"}}
	before := searchPage(a, form.Encode())
	if !strings.Contains(before, ">Save this search</button>") || !strings.Contains(before, `"folder":"Inbox"`) {
		t.Fatalf("results offer no Save carrying the list's folder:\n%s", before)
	}
	form.Set("action", "save")
	rec := searchesAction(a, danaHolder(), form)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, ">Saved · Forget</button>") || !strings.Contains(body, ">1 result for") {
		t.Fatalf("save: %d\n%s", rec.Code, body)
	}
	if !strings.Contains(body, `<div id="vm-searches" hx-swap-oob="true"><div class="sa-appside__group">Searches</div>`) ||
		!strings.Contains(body, `href="/os/vayumail/inbox?user=dana&amp;search=report+in%3AInbox"`) {
		t.Fatalf("save does not send the sidebar's searches:\n%s", body)
	}
	rd := a.mailReader(withUser(httptest.NewRequest(http.MethodGet, "/", nil), danaHolder()), "")
	if got, _ := a.vayuMail.SavedSearches(rd); strings.Join(got, ",") != "report in:Inbox" {
		t.Fatalf("kept %q", got)
	}
	for _, folder := range []string{"Inbox", scheduledFolder} {
		if nav := a.mailNavFor(rd, folder, "", nil, false); !strings.Contains(nav, `<div id="vm-searches"><div class="sa-appside__group">Searches</div>`) {
			t.Fatalf("the sidebar of %s has no saved searches:\n%s", folder, nav)
		}
	}
	form.Set("action", "forget")
	body = searchesAction(a, danaHolder(), form).Body.String()
	if !strings.Contains(body, ">Save this search</button>") || !strings.Contains(body, `<div id="vm-searches" hx-swap-oob="true"></div>`) {
		t.Fatalf("forget:\n%s", body)
	}
}

// A refused save is said in the toast beside the results; an action that is
// neither is refused outright.
func TestASavedSearchRefusal(t *testing.T) {
	a := scheduledApp(t)
	rec := searchesAction(a, danaHolder(), url.Values{"q": {""}, "scope": {"all"}, "in": {"list"}, "action": {"save"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("HX-Trigger"), "nothing to save") {
		t.Fatalf("an empty save: %d %q", rec.Code, rec.Header().Get("HX-Trigger"))
	}
	if rec := searchesAction(a, danaHolder(), url.Values{"q": {"x"}, "in": {"list"}, "action": {"keep"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown action: %d", rec.Code)
	}
}

// A read-only mailbox is offered no Save, and its save is refused.
func TestAReadOnlyMailboxSavesNoSearch(t *testing.T) {
	a, _, _ := reviewerApp(t, vmail.RoleReviewer)
	form := url.Values{"q": {"report"}, "scope": {"all"}, "in": {"list"}}
	if out := searchPage(a, form.Encode()); strings.Contains(out, "mx-results__save") {
		t.Fatalf("a read-only mailbox is offered Save:\n%s", out)
	}
	form.Set("action", "save")
	if rec := searchesAction(a, danaHolder(), form); !strings.Contains(rec.Header().Get("HX-Trigger"), "Not done") {
		t.Fatalf("a read-only save was not refused: %q", rec.Header().Get("HX-Trigger"))
	}
}
