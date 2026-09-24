// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
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
