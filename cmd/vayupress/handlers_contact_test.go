package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func postContact(t *testing.T, jsonBody string) *httptest.ResponseRecorder {
	t.Helper()
	a := &App{} // no settings/mailer — exercises the pre-delivery guards
	req := httptest.NewRequest("POST", "/api/v1/contact", strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	a.handleContactSubmit(rec, req)
	return rec
}

// TestContactHoneypotSilentlyDropped proves a filled honeypot returns 200 with
// no delivery attempt (so a bot gets no signal) even with no mailer configured.
func TestContactHoneypotSilentlyDropped(t *testing.T) {
	rec := postContact(t, `{"name":"Bot","email":"b@b.com","message":"spam","website":"http://x"}`)
	if rec.Code != 200 {
		t.Fatalf("honeypot submission = %d, want 200", rec.Code)
	}
}

// TestContactValidation rejects missing fields and bad emails before any
// delivery path is reached.
func TestContactValidation(t *testing.T) {
	if rec := postContact(t, `{"name":"","email":"a@b.com","message":"hi"}`); rec.Code != 400 {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}
	if rec := postContact(t, `{"name":"A","email":"not-an-email","message":"hi"}`); rec.Code != 400 {
		t.Errorf("bad email = %d, want 400", rec.Code)
	}
}

func TestLooksLikeEmail(t *testing.T) {
	good := []string{"a@b.com", "x.y@sub.domain.io"}
	bad := []string{"", "no-at", "a@b", "a b@c.com", "a@@b.com"}
	for _, g := range good {
		if !looksLikeEmail(g) {
			t.Errorf("looksLikeEmail(%q)=false, want true", g)
		}
	}
	for _, b := range bad {
		if looksLikeEmail(b) {
			t.Errorf("looksLikeEmail(%q)=true, want false", b)
		}
	}
}
