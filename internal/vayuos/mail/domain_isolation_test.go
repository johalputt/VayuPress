package mail

import (
	"strings"
	"testing"
)

// TestCrossDomainMaildirIsolation proves that two mailboxes with the same local
// part on different domains (bob@example.com and bob@shop.example) are stored and
// read as entirely separate Maildirs — the core VayuDomains Stage 3b guarantee.
func TestCrossDomainMaildirIsolation(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.MailAccepts = func(h string) bool { return strings.EqualFold(h, "shop.example") }
	e := NewEngine(&cfg, nil, nil)
	e.maildir = NewMaildir(t.TempDir())

	primaryRaw := []byte("From: a@partner.test\r\nTo: bob@example.com\r\nSubject: Primary msg\r\n\r\nprimary body\r\n")
	secondaryRaw := []byte("From: a@partner.test\r\nTo: bob@shop.example\r\nSubject: Secondary msg\r\n\r\nsecondary body\r\n")

	if _, err := e.DeliverInbound("bob@example.com", primaryRaw); err != nil {
		t.Fatalf("deliver primary: %v", err)
	}
	if _, err := e.DeliverInbound("bob@shop.example", secondaryRaw); err != nil {
		t.Fatalf("deliver secondary: %v", err)
	}

	pm, err := e.Inbox("", "bob") // "" => primary
	if err != nil || len(pm) != 1 {
		t.Fatalf("primary inbox: n=%d err=%v, want exactly 1", len(pm), err)
	}
	if pm[0].Subject != "Primary msg" {
		t.Fatalf("primary bob got the wrong message: %q", pm[0].Subject)
	}

	sm, err := e.Inbox("shop.example", "bob")
	if err != nil || len(sm) != 1 {
		t.Fatalf("secondary inbox: n=%d err=%v, want exactly 1", len(sm), err)
	}
	if sm[0].Subject != "Secondary msg" {
		t.Fatalf("secondary bob got the wrong message: %q", sm[0].Subject)
	}

	// The two mailboxes must not share storage: the primary read must never return
	// the secondary's message and vice versa.
	if pm[0].ID == sm[0].ID {
		t.Fatal("primary and secondary bob resolved to the same stored message — isolation broken")
	}
}

// TestAcceptsMailDomain checks the shared acceptance predicate: primary always,
// secondaries only via the resolver, byte-identical (primary-only) when nil.
func TestAcceptsMailDomain(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"

	// Primary-only (the single-domain / legacy default).
	if !cfg.AcceptsMailDomain("example.com") {
		t.Error("primary domain must be accepted")
	}
	if !cfg.AcceptsMailDomain("Example.COM") {
		t.Error("primary match must be case-insensitive")
	}
	if cfg.AcceptsMailDomain("shop.example") {
		t.Error("secondary must be rejected with no resolver (byte-identical)")
	}
	if cfg.AcceptsMailDomain("") {
		t.Error("empty host must be rejected")
	}

	// With a resolver accepting one secondary.
	cfg.MailAccepts = func(h string) bool { return strings.EqualFold(h, "shop.example") }
	if !cfg.AcceptsMailDomain("example.com") {
		t.Error("primary still accepted with a resolver present")
	}
	if !cfg.AcceptsMailDomain("shop.example") {
		t.Error("mail_enabled secondary must be accepted")
	}
	if cfg.AcceptsMailDomain("evil.test") {
		t.Error("an unregistered domain must never be accepted (no open relay)")
	}
}

// TestRecipientAcceptedSecondary checks the SMTP receive gate honours the same
// predicate, so a mail_enabled secondary can actually receive external mail.
func TestRecipientAcceptedSecondary(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.MailAccepts = func(h string) bool { return strings.EqualFold(h, "shop.example") }
	s := &SMTPServer{cfg: cfg}
	if !s.recipientAccepted("bob@example.com") {
		t.Error("primary recipient must be accepted")
	}
	if !s.recipientAccepted("sales@shop.example") {
		t.Error("secondary recipient on a mail_enabled domain must be accepted")
	}
	if s.recipientAccepted("bob@evil.test") {
		t.Error("recipient on an unserved domain must be relay-denied")
	}
}

// TestMailboxDomainFor pins the login→Maildir-domain resolution: the primary is
// the default (byte-identical), only a genuinely different login domain switches.
func TestMailboxDomainFor(t *testing.T) {
	t.Parallel()
	cases := []struct{ login, primary, want string }{
		{"bob", "example.com", "example.com"},               // bare local part → primary
		{"bob@example.com", "example.com", "example.com"},   // explicit primary → primary
		{"bob@Example.COM", "example.com", "example.com"},   // primary, mixed case → primary
		{"bob@shop.example", "example.com", "shop.example"}, // secondary → secondary
		{"bob@SHOP.Example", "example.com", "shop.example"}, // secondary, mixed case → lowercased
	}
	for _, c := range cases {
		if got := mailboxDomainFor(c.login, c.primary); got != c.want {
			t.Errorf("mailboxDomainFor(%q,%q)=%q, want %q", c.login, c.primary, got, c.want)
		}
	}
}
