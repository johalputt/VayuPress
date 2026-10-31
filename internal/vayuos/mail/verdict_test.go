// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"testing"
)

// The From domain's check reads as confirmed only when this install sealed
// it at delivery, over this very message: one seed per way it could be
// believed when it should not be.
func TestTheDomainsCheckIsSealedAtDelivery(t *testing.T) {
	s := aliasTestStore(t)
	if err := s.Create(context.Background(), "dana@example.com", "hash", "", "mailbox"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}
	e.UseVerdictKey([]byte("install key"))
	rd := ReadAsSystem("dana", "test")

	msg := func(verdict, body string) []byte {
		return []byte("Authentication-Results: mx.example.com; spf=pass smtp.mailfrom=shop.example; dkim=pass header.d=shop.example; dmarc=" + verdict + " header.from=shop.example\r\n" +
			"From: Shop <news@shop.example>\r\nTo: dana@example.com\r\nSubject: Your order\r\n\r\n" + body + "\r\n")
	}
	stored := func() []byte {
		t.Helper()
		ms, _ := e.ListFolder(rd, "Inbox")
		raw, err := e.ReadFolderMessageStored(rd, "Inbox", ms[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// Delivered by the inbound server with a passing verdict: confirmed.
	if err := e.inboundDeliver("news@shop.example", []string{"dana@example.com"}, msg("pass", "Shipped.")); err != nil {
		t.Fatal(err)
	}
	genuine := stored()
	if got := e.CheckedDomain(genuine); got != "shop.example" {
		t.Fatalf("a passing message reads as confirmed by %q, want shop.example", got)
	}

	// Each seed alone must not read as confirmed.
	head := genuine[:bytes.Index(genuine, []byte("\r\n"))+2]
	other := &Engine{verdictKey: []byte("another install")}
	for _, c := range []struct {
		name string
		raw  []byte
	}{
		{"a failing verdict", e.sealVerdict(msg("fail", "Shipped."))},
		{"the sealed line copied onto another message", append(append([]byte{}, head...), msg("pass", "Pay here.")...)},
		{"a seal another key made", other.sealVerdict(msg("pass", "Shipped."))},
		{"the sealed line not first", append([]byte("Received: by x\r\n"), genuine...)},
	} {
		if got := e.CheckedDomain(c.raw); got != "" {
			t.Errorf("%s reads as confirmed by %q", c.name, got)
		}
	}

	// Without a key nothing is sealed and nothing reads as confirmed.
	bare := &Engine{}
	if got := bare.sealVerdict(msg("pass", "Shipped.")); !bytes.Equal(got, msg("pass", "Shipped.")) {
		t.Error("an engine with no key sealed a message")
	}
	if got := bare.CheckedDomain(genuine); got != "" {
		t.Errorf("an engine with no key reads %q as confirmed", got)
	}

	// The inbound server removes a checked line the sender wrote.
	if out := stripTrustedHeaders([]byte("X-VayuMail-Checked: shop.example; mac=x\r\nFrom: a@b\r\n\r\nhi\r\n")); bytes.Contains(out, []byte("X-VayuMail-Checked")) {
		t.Error("a sender-written checked line survives the inbound server")
	}
}
