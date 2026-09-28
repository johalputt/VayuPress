// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// The Decisions list titles each record from its own heading, because the
// filename slug loses capitals ("Vayuveil"). A heading carries its number in
// one of three forms, and a colon inside the title is part of the title: an
// earlier version cut at the first colon and printed "observation control…"
// for "VayuVeil: observation control…".
func TestADRHeadingIsTheTitleAfterTheNumber(t *testing.T) {
	dir := t.TempDir()
	for file, c := range map[string]struct{ heading, want string }{
		"ADR-9001-a.md": {"# ADR-9001: SQLite as Primary Database", "SQLite as Primary Database"},
		"ADR-9002-b.md": {"# ADR-9002 — VayuVeil: observation control", "VayuVeil: observation control"},
		"ADR-9003-c.md": {"# ADR-9003 - Plain hyphen", "Plain hyphen"},
		"ADR-9004-d.md": {"# A heading without a number", "A heading without a number"},
		"ADR-9005-e.md": {"No heading at all", ""},
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(c.heading+"\n\nBody.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := adrHeading(dir, file); got != c.want {
			t.Errorf("%s: %q gave %q, want %q", file, c.heading, got, c.want)
		}
	}
}

// A stale link to a record answers 404 inside the console. It used to hand the
// operator the public site's 404 page, out of VayuOS altogether.
func TestAnUnknownDecisionIsA404InsideTheConsole(t *testing.T) {
	a := resetSessionApp(t)
	r := withUser(httptest.NewRequest(http.MethodGet, "/os/adr?doc=ADR-0000-no-such-record.md", nil), &users.User{Role: users.RoleAdmin})
	w := httptest.NewRecorder()
	a.handleAdminADR(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-ui="still-air"`) || !strings.Contains(body, "No such decision") {
		t.Error("the 404 is not the console's own page")
	}
}

// Decisions is a list: a row per record, the newest selected with the whole
// record in the inspector, and a search that finds a record by its number.
func TestDecisionsIsAListWithTheRecordInTheInspector(t *testing.T) {
	a := resetSessionApp(t)
	page := func(target string) string {
		w := httptest.NewRecorder()
		a.handleAdminADR(w, withUser(httptest.NewRequest(http.MethodGet, target, nil), &users.User{Role: users.RoleAdmin}))
		return w.Body.String()
	}
	all := len(adrEntries(resolveADRDir()))
	body := page("/os/adr")
	if !strings.Contains(body, `data-page-kind="list"`) {
		t.Error("Decisions does not say it is a list")
	}
	if n := strings.Count(body, "data-list-row"); n != all || all < 2 {
		t.Errorf("%d rows for %d records", n, all)
	}
	newest := adrEntries(resolveADRDir())[0]
	for _, want := range []string{`<article class="adr-doc">`, `href="/os/adr?doc=` + url.QueryEscape(newest.Filename) + `">Read at full width`} {
		if !strings.Contains(body, want) {
			t.Errorf("the newest record is not in the inspector: missing %q", want)
		}
	}
	if n := strings.Count(body, `aria-selected="true"`); n != 1 || !strings.Contains(body, `inspector?doc=`+url.QueryEscape(newest.Filename)+`" tabindex="0" aria-selected="true"`) {
		t.Errorf("%d rows selected on load, want only the newest record's", n)
	}
	found := page("/os/adr?q=adr-0001")
	if n := strings.Count(found, "data-list-row"); n != 1 || !strings.Contains(found, "ADR-0001") {
		t.Errorf("a search for adr-0001 lists %d rows", n)
	}
	if !strings.Contains(page("/os/adr?q=no-such-decision-anywhere"), "No decision matches that") {
		t.Error("a search that finds nothing does not say so")
	}
}

// The inspector serves only records in the listing: a name from anywhere else,
// a path included, is not found.
func TestTheDecisionInspectorServesOnlyListedRecords(t *testing.T) {
	a := resetSessionApp(t)
	get := func(doc string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.handleAdminADRInspector(w, httptest.NewRequest(http.MethodGet, "/os/adr/inspector?doc="+url.QueryEscape(doc), nil))
		return w
	}
	if w := get("ADR-0001-sqlite-first.md"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<article class="adr-doc">`) {
		t.Errorf("a listed record: %d", w.Code)
	}
	for _, doc := range []string{"ADR-0000-no-such-record.md", "../../go.mod", "INDEX.md"} {
		if w := get(doc); w.Code != http.StatusNotFound {
			t.Errorf("%q: %d, want 404", doc, w.Code)
		}
	}
}
