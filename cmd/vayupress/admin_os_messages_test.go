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
