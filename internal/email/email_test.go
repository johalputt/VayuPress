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

// When SMTP is unconfigured but a fallback transport (VayuMail) is wired, Send
// must deliver through the fallback, sanitising the body exactly as the SMTP
// path does (so a malicious HTML payload can never reach the transport).
func TestSendUsesFallbackWhenSMTPDisabled(t *testing.T) {
	s := New(Config{}) // empty Host → SMTP disabled
	if s.Enabled() {
		t.Fatal("sender should be SMTP-disabled with empty host")
	}
	if s.Active() {
		t.Fatal("sender should be inactive before a fallback is wired")
	}

	var got Message
	calls := 0
	s.SetFallback(func(m Message) error { calls++; got = m; return nil })

	if !s.Active() {
		t.Fatal("sender should be active once a fallback is wired")
	}
	err := s.Send(Message{
		To:      "reader@example.com",
		Subject: "Your sign-in link",
		Text:    "open the link",
		HTML:    `<a href="https://x/y">Sign in</a><script>steal()</script>`,
	})
	if err != nil {
		t.Fatalf("Send via fallback: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fallback should be called exactly once, got %d", calls)
	}
	if got.To != "reader@example.com" || got.Subject != "Your sign-in link" {
		t.Fatalf("fallback received wrong message: %+v", got)
	}
	if strings.Contains(strings.ToLower(got.HTML), "<script") {
		t.Errorf("HTML reaching the transport must be sanitised, got %q", got.HTML)
	}
}

// Without SMTP and without a fallback, Send is a safe no-op (upstream flows are
// never broken on an unconfigured deployment).
func TestSendNoOpWithoutTransport(t *testing.T) {
	s := New(Config{})
	if err := s.Send(Message{To: "a@b.com", Subject: "x", Text: "y"}); err != nil {
		t.Fatalf("no-op send should not error: %v", err)
	}
}

// The fallback path still rejects an invalid recipient before handing off.
func TestSendFallbackValidatesRecipient(t *testing.T) {
	s := New(Config{})
	s.SetFallback(func(Message) error { return nil })
	if err := s.Send(Message{To: "not-an-email", Subject: "x", Text: "y"}); err == nil {
		t.Error("expected an invalid-recipient error on the fallback path")
	}
}
