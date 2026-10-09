// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

func ymd(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

// The form's filters fill what the typed terms leave unsaid, and a typed
// term wins over the same filter; one seed per filter.
func TestTypedSearchTermsWinOverTheForm(t *testing.T) {
	for name, c := range map[string]struct {
		sf   searchFilters
		want vmail.SearchQuery
	}{
		"folder from the form":           {searchFilters{q: "report", folder: "Inbox"}, vmail.SearchQuery{Words: []string{"report"}, Folder: "Inbox"}},
		"in: over the folder":            {searchFilters{q: "in:Sent report", folder: "Inbox"}, vmail.SearchQuery{Words: []string{"report"}, Folder: "Sent"}},
		"from: over the field":           {searchFilters{q: "from:priya", from: "ops"}, vmail.SearchQuery{From: "priya"}},
		"the field alone":                {searchFilters{from: "ops"}, vmail.SearchQuery{From: "ops"}},
		"after: over the field":          {searchFilters{q: "after:2026-09-10", after: "2026-09-01"}, vmail.SearchQuery{After: ymd("2026-09-10")}},
		"the form's After":               {searchFilters{after: "2026-09-01"}, vmail.SearchQuery{After: ymd("2026-09-01")}},
		"before: over the field":         {searchFilters{q: "before:2026-09-10", before: "2026-09-20"}, vmail.SearchQuery{Before: ymd("2026-09-10")}},
		"the form's Before is inclusive": {searchFilters{before: "2026-09-20"}, vmail.SearchQuery{Before: ymd("2026-09-21")}},
		"Unread only":                    {searchFilters{q: "x", unreadOnly: true}, vmail.SearchQuery{Words: []string{"x"}, Unread: true}},
		"is:unread without the box":      {searchFilters{q: "is:unread"}, vmail.SearchQuery{Unread: true}},
		"the attachments scope":          {searchFilters{q: "x", attach: true}, vmail.SearchQuery{Words: []string{"x"}, Attachment: true}},
		"has:attachment alone":           {searchFilters{q: "has:attachment"}, vmail.SearchQuery{Attachment: true}},
		"the From scope":                 {searchFilters{q: "priya raman", fromScope: true}, vmail.SearchQuery{From: "priya raman"}},
		"the From scope, from: typed":    {searchFilters{q: "from:ops report", fromScope: true}, vmail.SearchQuery{From: "ops", Words: []string{"report"}}},
	} {
		if got := c.sf.query(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

func searchPage(a *App, query string) string {
	r := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/search/fragment?"+query, nil), danaHolder())
	return a.vayuSearchResults(a.mailReader(r, ""), parseSearchFilters(r))
}

// Terms run end to end: a search of terms alone finds by them, its words
// alone are highlighted, and a search that finds nothing names the terms.
func TestSearchTermsFromTheField(t *testing.T) {
	a := scheduledApp(t)
	for _, raw := range []string{
		"From: Priya <priya@h.test>\r\nTo: dana@example.com\r\nSubject: quarterly report\r\n\r\nnumbers\r\n",
		"From: Ops <ops@h.test>\r\nTo: dana@example.com\r\nSubject: quarterly report\r\n\r\nnumbers\r\n",
	} {
		if _, err := a.vayuMail.DeliverInbound("x@h.test", "dana@example.com", []byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	out := searchPage(a, "q=from%3Apriya+report&in=list")
	if !strings.Contains(out, ">1 result for") || !strings.Contains(out, ">Priya<") {
		t.Fatalf("from:priya report:\n%s", out)
	}
	if out := searchPage(a, "q=from%3Apriya+%22quarterly+report%22"); strings.Contains(out, "<mark>from") || !strings.Contains(out, "<mark>quarterly</mark>") {
		t.Fatalf("the page highlights other than the words:\n%s", out)
	}
	if out := searchPage(a, "q=zzzz&in=list"); !strings.Contains(out, "<code>from:</code>") {
		t.Fatalf("an empty result does not name the terms:\n%s", out)
	}
	if out := searchPage(a, ""); !strings.Contains(out, "Type a search above") {
		t.Fatalf("an empty search page does not ask for one:\n%s", out)
	}
	if out := searchPage(a, "from=ops"); !strings.Contains(out, ">1 result") {
		t.Fatalf("the sender field alone, with no words, did not search:\n%s", out)
	}
}
