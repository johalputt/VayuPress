// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"strings"
	"testing"
)

// TestMailboxesForDomain proves the panel enumerates each mail domain's accounts
// from its own domain-partitioned Maildir tree, with the Domain field set so the
// caller can render the full address — the fix for secondary-domain mailboxes not
// appearing in the Mailbox list.
func TestMailboxesForDomain(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.MailAccepts = func(h string) bool { return strings.EqualFold(h, "shop.example") }
	e := NewEngine(&cfg, nil, nil)
	e.maildir = NewMaildir(t.TempDir())

	// Delivery provisions the Maildir, so each account then shows up under its
	// own domain.
	if _, err := e.DeliverInbound("sender@other.test", "alice@example.com", []byte("From: x@p.test\r\nTo: alice@example.com\r\nSubject: p\r\n\r\nb\r\n")); err != nil {
		t.Fatalf("deliver primary: %v", err)
	}
	if _, err := e.DeliverInbound("sender@other.test", "shopkeeper@shop.example", []byte("From: x@p.test\r\nTo: shopkeeper@shop.example\r\nSubject: s\r\n\r\nb\r\n")); err != nil {
		t.Fatalf("deliver secondary: %v", err)
	}

	prim, err := e.MailboxesForDomain("example.com")
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	if !hasMailbox(prim, "alice", "example.com") {
		t.Fatalf("primary list missing alice@example.com: %+v", prim)
	}

	sec, err := e.MailboxesForDomain("shop.example")
	if err != nil {
		t.Fatalf("secondary: %v", err)
	}
	if !hasMailbox(sec, "shopkeeper", "shop.example") {
		t.Fatalf("secondary list missing shopkeeper@shop.example: %+v", sec)
	}

	// Isolation: the primary list must never leak a secondary-domain account.
	if hasMailbox(prim, "shopkeeper", "shop.example") {
		t.Fatal("primary mailbox list leaked a secondary-domain account")
	}

	// Mailboxes() is the primary shorthand and must carry Domain too, so links
	// stay byte-identical for the primary.
	def, _ := e.Mailboxes()
	for _, m := range def {
		if m.Domain != "example.com" {
			t.Errorf("Mailboxes() summary has wrong Domain: %+v", m)
		}
	}
}

func hasMailbox(list []MailboxSummary, user, domain string) bool {
	for _, m := range list {
		if m.Username == user && m.Domain == domain {
			return true
		}
	}
	return false
}
