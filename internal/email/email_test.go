package email

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewNoopWhenUnconfigured(t *testing.T) {
	s := New(Config{})
	if s.Enabled() {
		t.Fatal("expected disabled sender when Host is empty")
	}
	// Send must be a safe no-op (no panic, nil error) so upstream flows survive.
	if err := s.Send(Message{To: "a@b.com", Subject: "hi", Text: "x"}); err != nil {
		t.Fatalf("noop send should return nil, got %v", err)
	}
}

func TestNewDefaults(t *testing.T) {
	s := New(Config{Host: "smtp.example.com"})
	if !s.Enabled() {
		t.Fatal("expected enabled sender")
	}
	if s.cfg.Port != 587 {
		t.Errorf("default port = %d, want 587", s.cfg.Port)
	}
	if s.cfg.TLS != TLSStartTLS {
		t.Errorf("default TLS = %q, want starttls", s.cfg.TLS)
	}
}

func TestSanitizeHeaderStripsCRLF(t *testing.T) {
	got := sanitizeHeader("Subject\r\nBcc: evil@x.com")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header injection not stripped: %q", got)
	}
}

func TestAssemblePlainText(t *testing.T) {
	s := New(Config{Host: "h", From: "VayuPress <hello@example.com>"})
	raw, err := s.assemble("user@example.com", Message{Subject: "Hi", Text: "Hello\nWorld"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "Content-Type: text/plain") {
		t.Error("expected text/plain content type")
	}
	if !strings.Contains(body, "Subject: Hi") {
		t.Error("missing subject header")
	}
	if !strings.Contains(body, "Content-Transfer-Encoding: base64") {
		t.Error("expected base64 transfer encoding")
	}
	// Body is base64-encoded; decode and verify CRLF line endings are present.
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body[strings.Index(body, "\r\n\r\n")+4:]))
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if !strings.Contains(string(decoded), "Hello") {
		t.Error("decoded body missing expected text")
	}
}

func TestAssembleMultipart(t *testing.T) {
	s := New(Config{Host: "h", From: "hello@example.com"})
	raw, err := s.assemble("user@example.com", Message{Subject: "Hi", Text: "plain", HTML: "<b>rich</b>"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "multipart/alternative") {
		t.Error("expected multipart/alternative")
	}
	if !strings.Contains(body, "text/html") {
		t.Error("missing text/html part header")
	}
	if !strings.Contains(body, "Content-Transfer-Encoding: base64") {
		t.Error("expected base64 transfer encoding")
	}
	// HTML part is base64-encoded — verify the encoded form of <b>rich</b> is present.
	if !strings.Contains(body, base64.StdEncoding.EncodeToString([]byte("<b>rich</b>"))) {
		t.Error("missing base64-encoded HTML content")
	}
}

func TestEnvelopeAddress(t *testing.T) {
	if got := envelopeAddress("VayuPress <hello@example.com>"); got != "hello@example.com" {
		t.Errorf("envelopeAddress = %q, want hello@example.com", got)
	}
}

func TestRedactEmail(t *testing.T) {
	got := redactEmail("alice@example.com")
	if strings.Contains(got, "alice") {
		t.Errorf("redactEmail leaked local part: %q", got)
	}
	if !strings.HasSuffix(got, "@example.com") {
		t.Errorf("redactEmail should keep domain: %q", got)
	}
}
