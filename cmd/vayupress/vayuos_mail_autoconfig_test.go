package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// appWithMail builds an App whose vayuMail engine reports the given coordinates,
// without starting any listener (NewEngine only stores the config).
func appWithMail(t *testing.T) *App {
	t.Helper()
	cfg := vmail.DefaultConfig()
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.IMAPSListen = ":993"
	cfg.POP3SListen = ":995"
	cfg.SubmissionListen = ":587"
	return &App{vayuMail: vmail.NewEngine(&cfg, nil, nil)}
}

// TestVayuMailAutoconfigContract pins the JSON autoconfig document the VayuMail
// app consumes. The field names, TLS spellings, port derivation and schema
// version here MUST match VayuMail-Mobile's autoconfig_contract_test.go — the
// server publishes this shape and the app parses it, so a silent change would
// break email-only onboarding. This is the server half of that contract.
func TestVayuMailAutoconfigContract(t *testing.T) {
	a := appWithMail(t)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/vayumail/autoconfig.json", nil)
	rec := httptest.NewRecorder()
	a.handleVayuMailAutoconfigJSON(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header — cross-origin discovery would be blocked")
	}

	// Raw-byte contract: this is the exact document VayuMail-Mobile pins as
	// canonicalAutoconfigJSON in its test/autoconfig_contract_test.go. Keep the
	// two identical — the app parses these bytes.
	const canonical = `{"schema":"vayumail-autoconfig/1","domain":"example.com","displayName":"example.com Mail","imap":{"host":"mail.example.com","port":993,"tls":"tls"},"pop3":{"host":"mail.example.com","port":995,"tls":"tls"},"smtp":{"host":"mail.example.com","port":587,"tls":"starttls"},"usernameIsEmail":true,"auth":"password","wkd":true}`
	if body := strings.TrimRight(rec.Body.String(), "\n"); body != canonical {
		t.Errorf("autoconfig wire bytes drifted from the VayuMail-Mobile contract:\n got %s\nwant %s", body, canonical)
	}

	var got vayuMailAutoconfig
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not valid autoconfig JSON: %v", err)
	}

	want := vayuMailAutoconfig{
		Schema:          "vayumail-autoconfig/1",
		Domain:          "example.com",
		DisplayName:     "example.com Mail",
		IMAP:            vayuMailServerConfig{Host: "mail.example.com", Port: 993, TLS: "tls"},
		POP3:            vayuMailServerConfig{Host: "mail.example.com", Port: 995, TLS: "tls"},
		SMTP:            vayuMailServerConfig{Host: "mail.example.com", Port: 587, TLS: "starttls"},
		UsernameIsEmail: true,
		Auth:            "password",
		WKD:             true,
	}
	if got != want {
		t.Errorf("autoconfig document drifted from the client contract:\n got %+v\nwant %+v", got, want)
	}

	// The schema constant is the wire contract; guard it explicitly so a rename
	// is a deliberate, reviewed change.
	if VayuMailAutoconfigSchema != "vayumail-autoconfig/1" {
		t.Errorf("VayuMailAutoconfigSchema = %q; a bump requires a coordinated VayuMail-Mobile release", VayuMailAutoconfigSchema)
	}
}

// TestVayuMailAutoconfigTalkHost verifies the talk host is advertised only when
// the relay is enabled AND VAYUOS_TALK_HOST is set (the deploy script sets it
// after provisioning the subdomain cert), and is omitted otherwise so the app
// falls back to the mail domain.
func TestVayuMailAutoconfigTalkHost(t *testing.T) {
	// No talk engine → never advertised, even with the env set.
	t.Setenv("VAYUOS_TALK_HOST", "talk.example.com")
	if h := appWithMail(t).talkAutoconfigHost(); h != "" {
		t.Errorf("talk host advertised with no relay engine: %q", h)
	}

	// Relay enabled but env unset → omitted (app falls back to the mail domain).
	a := appWithMail(t)
	a.vayuTalk = vtalk.NewEngine(vtalk.Config{Enabled: true})
	t.Setenv("VAYUOS_TALK_HOST", "")
	if h := a.talkAutoconfigHost(); h != "" {
		t.Errorf("talk host advertised with VAYUOS_TALK_HOST unset: %q", h)
	}
	if strings.Contains(a.buildVayuMailAutoconfigFor("").Talk, "talk") {
		t.Error("autoconfig advertised a talk host before one was provisioned")
	}

	// Relay enabled AND env set → advertised (lower-cased, trimmed).
	t.Setenv("VAYUOS_TALK_HOST", "  Talk.Example.com ")
	if h := a.talkAutoconfigHost(); h != "talk.example.com" {
		t.Errorf("talk host = %q, want talk.example.com", h)
	}
	if got := a.buildVayuMailAutoconfigFor("").Talk; got != "talk.example.com" {
		t.Errorf("autoconfig Talk = %q, want talk.example.com", got)
	}
}

// TestVayuMailAutoconfigNotFoundWhenMailOff verifies the endpoint 404s (rather
// than panicking) when VayuMail is not configured.
func TestVayuMailAutoconfigNotFoundWhenMailOff(t *testing.T) {
	a := &App{}
	rec := httptest.NewRecorder()
	a.handleVayuMailAutoconfigJSON(rec, httptest.NewRequest(http.MethodGet, "/.well-known/vayumail/autoconfig.json", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when vayuMail is nil", rec.Code)
	}
}
