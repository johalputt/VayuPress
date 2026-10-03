// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"testing"
)

// A contact's mail stays out of Junk only when this server's own verdict
// shows it came from the contact's domain (pipeline 3ab, hardened in the
// 3.17.96 release audit). Each seed breaks one rule; the message is junk by
// its words, so only the contact rule can keep it out.
func TestAContactIsTrustedOnlyWhenAuthenticated(t *testing.T) {
	s := aliasTestStore(t)
	ctx := context.Background()
	for _, mb := range []string{"dana@example.com", "eve@example.com"} {
		if err := s.Create(ctx, mb, "hash", "", "mailbox"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddContact(ctx, "dana@example.com", "friend@shop.example", "Friend"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.JunkFilterEnabled = true
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s}

	junk := func(from string) string {
		return "From: <" + from + ">\r\nSubject: ACT NOW limited offer!!!\r\nDate: Fri, 3 Oct 2026 09:00:00 +0000\r\n\r\n" +
			"Make money fast, 100% guaranteed, $$$ refinance pre-approved, click now!!!\r\n"
	}
	if !ScoreSpam([]byte(junk("x@y.example"))).IsSpam {
		t.Fatal("the seed is not junk by its words, so nothing below would test anything")
	}
	ar := func(v string) string { return "Authentication-Results: mx.example.com; " + v + "\r\n" }
	where := func(mailbox, from, header string, stamped bool) string {
		local, _ := splitAddress(mailbox)
		before := map[string]int{}
		for _, f := range []string{"Inbox", "Junk"} {
			m, _ := e.ListFolder(ReadAsSystem(local, "test"), f)
			before[f] = len(m)
		}
		raw := []byte(header + junk(from))
		var err error
		if stamped {
			err = e.inboundDeliver(from, []string{mailbox}, raw)
		} else {
			_, err = e.DeliverInbound(from, mailbox, raw)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"Inbox", "Junk"} {
			if m, _ := e.ListFolder(ReadAsSystem(local, "test"), f); len(m) > before[f] {
				return f
			}
		}
		return "nowhere"
	}
	for _, c := range []struct {
		name, mailbox, from, header string
		stamped                     bool
		want                        string
	}{
		{"DMARC passed", "dana@example.com", "friend@shop.example", ar("spf=pass smtp.mailfrom=shop.example; dkim=pass header.d=shop.example; dmarc=pass header.from=shop.example"), true, "Inbox"},
		// A pass that only relaxed alignment would accept, under a policy that asks for strict: DMARC failed.
		{"DMARC failed", "dana@example.com", "friend@shop.example", ar("spf=pass smtp.mailfrom=bounce.shop.example; dkim=none; dmarc=fail header.from=shop.example"), true, "Junk"},
		{"no policy, DKIM aligned", "dana@example.com", "friend@shop.example", ar("spf=none; dkim=pass header.d=shop.example; dmarc=none header.from=shop.example"), true, "Inbox"},
		{"no policy, DKIM for another domain", "dana@example.com", "friend@shop.example", ar("spf=none; dkim=pass header.d=evil.example; dmarc=none header.from=shop.example"), true, "Junk"},
		{"no policy, SPF aligned (a subdomain)", "dana@example.com", "friend@shop.example", ar("spf=pass smtp.mailfrom=bounce.shop.example; dkim=none; dmarc=none header.from=shop.example"), true, "Inbox"},
		{"no policy, SPF for another domain", "dana@example.com", "friend@shop.example", ar("spf=pass smtp.mailfrom=evil.example; dkim=none; dmarc=none header.from=shop.example"), true, "Junk"},
		{"no policy, nothing passed", "dana@example.com", "friend@shop.example", ar("spf=softfail smtp.mailfrom=shop.example; dkim=none; dmarc=none header.from=shop.example"), true, "Junk"},
		{"a verdict the sender wrote, not delivered by the receiver", "dana@example.com", "friend@shop.example", ar("spf=pass smtp.mailfrom=shop.example; dkim=pass header.d=shop.example; dmarc=pass header.from=shop.example"), false, "Junk"},
		{"not a contact", "dana@example.com", "stranger@shop.example", ar("spf=pass smtp.mailfrom=shop.example; dkim=pass header.d=shop.example; dmarc=pass header.from=shop.example"), true, "Junk"},
		{"another mailbox's contact", "eve@example.com", "friend@shop.example", ar("spf=pass smtp.mailfrom=shop.example; dkim=pass header.d=shop.example; dmarc=pass header.from=shop.example"), true, "Junk"},
	} {
		if got := where(c.mailbox, c.from, c.header, c.stamped); got != c.want {
			t.Errorf("%s: filed in %s, want %s", c.name, got, c.want)
		}
	}
}
