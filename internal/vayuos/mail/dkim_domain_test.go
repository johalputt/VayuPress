package mail

import (
	"strings"
	"testing"
)

// TestPerDomainDKIMSigning proves VayuDomains Stage 3c outbound branding: the
// primary keeps its signer (byte-identical), a mail_enabled secondary gets a
// signer that carries its own d= domain, and both share the selector's key so the
// secondary validates against its own published record.
func TestPerDomainDKIMSigning(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.StorageDir = t.TempDir()
	e := NewEngine(&cfg, nil, nil)
	dk, err := LoadOrCreateDKIM(cfg.StorageDir, cfg.DKIMSelector, cfg.Domain)
	if err != nil {
		t.Fatalf("load dkim: %v", err)
	}
	e.dkim = dk

	// Primary: same signer instance, byte-identical.
	if e.dkimFor("") != e.dkim || e.dkimFor("example.com") != e.dkim || e.dkimFor("Example.COM") != e.dkim {
		t.Fatal("primary sends must use the original signer unchanged")
	}

	// Secondary: a distinct signer carrying its own d= domain.
	sec := e.dkimFor("shop.example")
	if sec == e.dkim {
		t.Fatal("secondary domain must get its own signer")
	}
	if sec.Domain != "shop.example" {
		t.Fatalf("secondary signer domain = %q, want shop.example", sec.Domain)
	}
	// Cached: a second lookup returns the same instance.
	if e.dkimFor("shop.example") != sec {
		t.Fatal("per-domain signer should be memoised")
	}

	// The secondary signature actually tags the secondary domain.
	signed, err := sec.SignMessage([]byte("From: sales@shop.example\r\nSubject: hi\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(string(signed), "d=shop.example") {
		t.Errorf("secondary DKIM-Signature missing d=shop.example:\n%s", firstLines(string(signed), 4))
	}
}

// TestSenderDomainAndMessageID pins the outbound helpers: the sender domain drives
// the Message-ID domain, defaulting to the primary for a bare/primary From.
func TestSenderDomainAndMessageID(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := NewEngine(&cfg, nil, nil)

	if got := e.senderDomain("bob@shop.example"); got != "shop.example" {
		t.Errorf("senderDomain(secondary) = %q, want shop.example", got)
	}
	if got := e.senderDomain("bob"); got != "example.com" {
		t.Errorf("senderDomain(bare) = %q, want primary", got)
	}
	if got := e.senderDomain(`"Sales" <sales@shop.example>`); got != "shop.example" {
		t.Errorf("senderDomain(display form) = %q, want shop.example", got)
	}

	if id := e.messageID("shop.example"); !strings.HasSuffix(id, "@shop.example>") {
		t.Errorf("messageID(secondary) = %q, want @shop.example> suffix", id)
	}
	if id := e.messageID(""); !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("messageID(empty) = %q, want primary suffix", id)
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
