package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
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

// TestContactStoredWithoutEmailDelivery proves a valid submission is accepted and
// persisted to the /os inbox even when no mailer/SMTP is configured — the form
// must not fail the visitor just because email delivery is unavailable.
func TestContactStoredWithoutEmailDelivery(t *testing.T) {
	config.Cfg.DBPath = ":memory:"
	if err := dbpkg.Init(); err != nil {
		t.Fatalf("db init: %v", err)
	}
	// App with NO mailer and NO site settings → email delivery is unconfigured.
	a := &App{}
	req := httptest.NewRequest("POST", "/api/v1/contact",
		strings.NewReader(`{"name":"Jane","email":"jane@example.com","message":"Hello there"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.42:5555" // fresh IP, independent of the shared limiter
	rec := httptest.NewRecorder()
	a.handleContactSubmit(rec, req)

	if rec.Code != 200 {
		t.Fatalf("submit without mailer = %d, want 200 (message must still be stored); body=%s", rec.Code, rec.Body.String())
	}
	var n int
	if err := dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM contact_messages WHERE email='jane@example.com'`).Scan(&n); err != nil {
		t.Fatalf("query inbox: %v", err)
	}
	if n != 1 {
		t.Errorf("stored messages = %d, want 1 (form must persist to the inbox even without email delivery)", n)
	}
}

func TestPageSlugFromPath(t *testing.T) {
	cases := map[string]string{
		"/contact":         "contact",
		"/contact?ref=nav": "contact",
		"/about/":          "about",
		"/":                "",
		"":                 "",
		"/a/b":             "", // multi-segment is not a page slug
	}
	for in, want := range cases {
		if got := pageSlugFromPath(in); got != want {
			t.Errorf("pageSlugFromPath(%q)=%q, want %q", in, got, want)
		}
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
