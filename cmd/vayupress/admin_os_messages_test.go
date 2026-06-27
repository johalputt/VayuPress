package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMessagesSurfaceRendersWithoutDB guards the contact inbox against a nil DB
// (worst-case startup): it must render the empty-state shell, not panic.
func TestMessagesSurfaceRendersWithoutDB(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest("GET", "/os/messages", nil)
	rec := httptest.NewRecorder()

	a.handleOSMessages(rec, req) // must not panic

	if rec.Code != 200 {
		t.Fatalf("Messages status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No messages yet") {
		t.Error("Messages surface should show the empty state without a DB")
	}
}

// TestMessagesFilteredEmptyShowsToolbar proves that with an active search the
// page shows the filter toolbar and the "no matching" state, not the pristine
// "no messages yet" empty state.
func TestMessagesFilteredEmptyShowsToolbar(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest("GET", "/os/messages?q=alice", nil)
	rec := httptest.NewRecorder()

	a.handleOSMessages(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `name="q"`) {
		t.Error("filtered view should render the search box")
	}
	if !strings.Contains(body, "No matching messages") {
		t.Error("filtered view with no results should show the no-match state")
	}
	if strings.Contains(body, "No messages yet") {
		t.Error("filtered view must not show the pristine empty state")
	}
}

// TestMessagesCSVExportHeader proves the CSV export always emits the header row
// with the right content-type/disposition, even with no DB.
func TestMessagesCSVExportHeader(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest("GET", "/os/api/messages/export.csv", nil)
	rec := httptest.NewRecorder()

	a.handleOSMessagesExportCSV(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q, want text/csv", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "contact-messages.csv") {
		t.Errorf("content-disposition = %q, want attachment filename", cd)
	}
	if !strings.Contains(rec.Body.String(), "created_at,name,email,page,ip,read,message") {
		t.Errorf("CSV header row missing, got: %q", rec.Body.String())
	}
}
