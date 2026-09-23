// SPDX-License-Identifier: Apache-2.0

package main

// mailids_console_test.go — the Premium Mail-ID console's interaction model.
//
// The audit's M-17 finding was that this console reloaded the entire page after
// every approve/disapprove/add/remove, threw away the operator's position, and
// carried inline styles. It now swaps its two cards in place.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

func TestThePremiumIDsConsoleRefreshesInPlace(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}

	req := withUser(httptest.NewRequest(http.MethodGet, "/os/monetization/mailids", nil), admin)
	rec := httptest.NewRecorder()
	a.handleOSMailIDs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	page := rec.Body.String()

	// The panel refreshes itself on demand.
	if !strings.Contains(page, `id="mid-panel"`) {
		t.Error("the console needs a swap target for its management cards")
	}
	if !strings.Contains(page, `hx-get="/os/monetization/mailids?fragment=1"`) {
		t.Error("the panel must be refreshable as a fragment")
	}
	// The behaviour this replaced: a full page reload after every action.
	if strings.Contains(page, "location.reload") {
		t.Error("the console must not reload the whole page after an action — it swapped to a fragment refresh")
	}
	// The house dialog, not the browser's.
	if strings.Contains(page, "confirm(") && !strings.Contains(page, "vpConfirm(") {
		t.Error("disapproving must use vpConfirm, not the native confirm()")
	}
	// No inline styles: they are what the audit flagged, and the house style is
	// classes so the console can theme them.
	if strings.Contains(page, `style="`) {
		t.Errorf("the console still renders inline styles:\n%s", firstInlineStyle(page))
	}
}

func TestThePremiumIDsFragmentIsServedWithoutThePageChrome(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}

	req := withUser(httptest.NewRequest(http.MethodGet, "/os/monetization/mailids?fragment=1", nil), admin)
	rec := httptest.NewRecorder()
	a.handleOSMailIDs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want 200", rec.Code)
	}
	frag := rec.Body.String()
	if !strings.Contains(frag, `settings-block-title`) {
		t.Error("the fragment must carry the management cards")
	}
	if strings.Contains(frag, `<div class="page-header">`) {
		t.Error("the fragment must NOT carry the page chrome — the panel swap would nest a second page inside the panel")
	}
	if strings.Contains(frag, "<script") {
		t.Error("the fragment must not re-emit the page script")
	}
}

// firstInlineStyle returns a little context around the first inline style, so a
// failure points at the markup rather than just saying "somewhere".
func firstInlineStyle(s string) string {
	i := strings.Index(s, `style="`)
	if i < 0 {
		return ""
	}
	start := i - 120
	if start < 0 {
		start = 0
	}
	end := i + 120
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
