// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"net/textproto"
	"testing"
)

// One seed per rule, each failing only that rule, and the orderings that
// decide between two.
func TestCategorize(t *testing.T) {
	h := func(kv ...string) textproto.MIMEHeader {
		m := textproto.MIMEHeader{}
		for i := 0; i+1 < len(kv); i += 2 {
			m.Set(kv[i], kv[i+1])
		}
		return m
	}
	for _, c := range []struct {
		name, from, subject string
		hdr                 textproto.MIMEHeader
		want                string
	}{
		{"a person", "Priya <priya@halcyon.dev>", "Saturday?", h(), CategoryPrimary},
		{"a receipt", "Shop <orders@shop.example>", "Your receipt from Shop", h(), CategoryReceipts},
		{"an order number", "Shop <orders@shop.example>", "Order #20391 confirmed", h(), CategoryReceipts},
		{"a network's notice", "LinkedIn <notifications-noreply@linkedin.com>", "You appeared in 4 searches", h(), CategorySocial},
		{"a network's subdomain", "x <info@mail.instagram.com>", "New follower", h(), CategorySocial},
		{"a list", "News <news@paper.example>", "This week", h("List-Unsubscribe", "<mailto:u@paper.example>"), CategoryNewsletters},
		{"a list by its id", "News <news@paper.example>", "This week", h("List-Id", "<weekly.paper.example>"), CategoryNewsletters},
		{"bulk mail", "News <news@paper.example>", "This week", h("Precedence", "bulk"), CategoryNewsletters},
		{"a no-reply sender", "CI <noreply@ci.example>", "Build passed", h(), CategoryUpdates},
		{"an automatic message", "CI <builds@ci.example>", "Build passed", h("Auto-Submitted", "auto-generated"), CategoryUpdates},
		{"a person who says it is not automatic", "Dana <dana@ci.example>", "Build", h("Auto-Submitted", "no"), CategoryPrimary},
		{"a receipt sent to a list", "Shop <orders@shop.example>", "Your receipt", h("List-Unsubscribe", "<mailto:u@shop.example>"), CategoryReceipts},
		{"a notice sent to a list", "x <notify@facebookmail.com>", "Someone posted", h("List-Unsubscribe", "<mailto:u@facebookmail.com>"), CategorySocial},
		{"a list from a no-reply sender", "News <noreply@paper.example>", "This week", h("List-Id", "<w.paper.example>"), CategoryNewsletters},
	} {
		if got := categorize(c.hdr, c.from, c.subject); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The category and the list mark come with the folder's listing.
func TestTheListingCarriesTheCategory(t *testing.T) {
	s := aliasTestStore(t)
	if err := s.Create(context.Background(), "dana@example.com", "hash", "", "mailbox"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}
	raw := "From: News <news@paper.example>\r\nTo: dana@example.com\r\nSubject: This week\r\nList-Unsubscribe: <mailto:u@paper.example>\r\n\r\nhi\r\n"
	if _, err := e.DeliverInbound("news@paper.example", "dana@example.com", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	ms, _ := e.ListFolder(ReadAsSystem("dana", "test"), "Inbox")
	if len(ms) != 1 || ms[0].Category != CategoryNewsletters || !ms[0].List {
		t.Fatalf("listed as %+v", ms)
	}
}
