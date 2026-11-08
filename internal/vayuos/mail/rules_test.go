// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

const ruleBox = "ankush@example.com"

// ruleEngine is an engine with rules storage, a Maildir and a queue whose
// deliveries are counted, as delivery runs at arrival.
func ruleEngine(t *testing.T) (*Engine, *AccountStore, *int) {
	t.Helper()
	s := aliasTestStore(t)
	qdb, _ := sql.Open("sqlite3", ":memory:")
	qdb.SetMaxOpenConns(1)
	t.Cleanup(func() { qdb.Close() })
	sent := 0
	q, _ := NewQueue(qdb, DefaultConfig(), func(_ context.Context, _ string, _ []string, _ []byte) error {
		sent++
		return nil
	})
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	return &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s, queue: q, db: s.db}, s, &sent
}

func saveRule(t *testing.T, s *AccountStore, r FilterRule) int64 {
	t.Helper()
	r.Mailbox, r.Enabled = ruleBox, true
	id, err := s.SaveRule(context.Background(), r)
	if err != nil {
		t.Fatalf("save %+v: %v", r, err)
	}
	return id
}

func ruleFolderCount(t *testing.T, e *Engine, folder string) int {
	t.Helper()
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", folder)
	return len(msgs)
}

// TestEachConditionReadsItsOwnPart — one message per field, so each field's
// reading is what decides.
func TestEachConditionReadsItsOwnPart(t *testing.T) {
	big := "From: A <a@shop.example>\r\nTo: me@x.y\r\nList-Id: <weekly.shop.example>\r\nX-Priority: 1\r\nSubject: Weekly digest\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nyour invoice number 42\r\n--b\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"i.pdf\"\r\n\r\nJVBERi0=\r\n--b--\r\n"
	plain := "From: B <b@friend.example>\r\nTo: me@x.y\r\nSubject: hello\r\n\r\njust words\r\n"
	for _, c := range []struct {
		cond     RuleCondition
		big, pln bool
	}{
		{RuleCondition{Field: "body", Op: "contains", Value: "invoice"}, true, false},
		{RuleCondition{Field: "domain", Op: "is", Value: "shop.example"}, true, false},
		{RuleCondition{Field: "attachment"}, true, false},
		{RuleCondition{Field: "list"}, true, false},
		{RuleCondition{Field: "list", Op: "contains", Value: "weekly"}, true, false},
		{RuleCondition{Field: "header", Name: "X-Priority", Op: "is", Value: "1"}, true, false},
		{RuleCondition{Field: "subject", Op: "ends-with", Value: "digest"}, true, false},
		{RuleCondition{Field: "subject", Op: "not-contains", Value: "digest"}, false, true},
		{RuleCondition{Field: "size", Value: "0.0001"}, true, false},
	} {
		if err := c.cond.normalise(); err != nil {
			t.Fatalf("%+v: %v", c.cond, err)
		}
		r := FilterRule{Conditions: []RuleCondition{c.cond}}
		if got := ruleMatches(r, &ruleMessage{raw: []byte(big)}); got != c.big {
			t.Errorf("%s %s %q on the digest: %v, want %v", c.cond.Field, c.cond.Op, c.cond.Value, got, c.big)
		}
		if got := ruleMatches(r, &ruleMessage{raw: []byte(plain)}); got != c.pln {
			t.Errorf("%s %s %q on the plain one: %v, want %v", c.cond.Field, c.cond.Op, c.cond.Value, got, c.pln)
		}
	}
}

// TestAllOrAny — all needs every condition, any needs one.
func TestAllOrAny(t *testing.T) {
	m := &ruleMessage{raw: []byte("From: a@shop.example\r\nSubject: sale\r\n\r\nx")}
	conds := []RuleCondition{{Field: "from", Op: "contains", Value: "shop"}, {Field: "subject", Op: "contains", Value: "invoice"}}
	if ruleMatches(FilterRule{Conditions: conds}, m) {
		t.Error("all of two, one true, matched")
	}
	if !ruleMatches(FilterRule{Any: true, Conditions: conds}, m) {
		t.Error("any of two, one true, did not match")
	}
}

// TestRulesRunInTheHoldersOrder — the first match decides unless it keeps
// going; the order is the holder's to change.
func TestRulesRunInTheHoldersOrder(t *testing.T) {
	e, s, _ := ruleEngine(t)
	ctx := context.Background()
	a := saveRule(t, s, FilterRule{Conditions: []RuleCondition{{Field: "from", Op: "contains", Value: "shop"}}, Actions: []RuleAction{{Kind: "move", Arg: "Archive"}}})
	saveRule(t, s, FilterRule{Conditions: []RuleCondition{{Field: "subject", Op: "contains", Value: "sale"}}, Actions: []RuleAction{{Kind: "move", Arg: "Junk"}}})
	msg := []byte("From: a@shop.example\r\nSubject: sale\r\nMessage-ID: <m1@x>\r\n\r\nx")
	if out := e.applyFilters(ruleBox, msg); out.folder != "Archive" {
		t.Fatalf("first rule first: %+v", out)
	}
	if err := s.MoveRule(ctx, ruleBox, a, false); err != nil {
		t.Fatal(err)
	}
	if out := e.applyFilters(ruleBox, msg); out.folder != "Junk" {
		t.Fatalf("after moving the first rule down: %+v", out)
	}
	rules, _ := s.FiltersFor(ctx, ruleBox)
	rules[0].KeepGoing = true
	rules[0].Actions = []RuleAction{{Kind: "label", Arg: "Promo"}}
	if _, err := s.SaveRule(ctx, rules[0]); err != nil {
		t.Fatal(err)
	}
	if out := e.applyFilters(ruleBox, msg); out.folder != "Archive" || len(out.labels) != 1 {
		t.Fatalf("a rule that keeps going adds up: %+v", out)
	}
	if err := s.SetRuleEnabled(ctx, ruleBox, a, false); err != nil {
		t.Fatal(err)
	}
	if out := e.applyFilters(ruleBox, msg); len(out.labels) != 1 || out.folder != "" {
		t.Fatalf("a rule switched off still ran: %+v", out)
	}
}

// TestARejectedMessageIsNotKept — nothing stored; the sender is refused only
// when every recipient refused.
func TestARejectedMessageIsNotKept(t *testing.T) {
	e, s, _ := ruleEngine(t)
	if err := s.Create(context.Background(), "dana@example.com", "hash", "Dana", "mailbox"); err != nil {
		t.Fatal(err)
	}
	saveRule(t, s, FilterRule{Conditions: []RuleCondition{{Field: "domain", Op: "is", Value: "promo.example"}}, Actions: []RuleAction{{Kind: "reject"}}})
	msg := []byte("From: x@promo.example\r\nSubject: buy\r\n\r\nx")
	if err := e.inboundDeliver("x@promo.example", []string{ruleBox}, msg); !errors.Is(err, ErrRejectedByRule) {
		t.Fatalf("every recipient rejected: %v, want ErrRejectedByRule", err)
	}
	if err := e.inboundDeliver("x@promo.example", []string{ruleBox, "dana@example.com"}, msg); err != nil {
		t.Fatalf("one of two rejected: %v, want it accepted for the other", err)
	}
	for _, f := range []string{"Inbox", "Junk", "Trash"} {
		if n := ruleFolderCount(t, e, f); n != 0 {
			t.Errorf("the rejecting mailbox kept %d in %s", n, f)
		}
	}
	if msgs, _ := e.maildir.ListFolder("example.com", "dana", "Inbox"); len(msgs) != 1 {
		t.Errorf("the other recipient got %d, want 1", len(msgs))
	}
}

// TestAForwardWaitsForItsAddressToSayYes — a forwarding rule sends nothing
// until the address has confirmed, and a link works once.
func TestAForwardWaitsForItsAddressToSayYes(t *testing.T) {
	e, s, sent := ruleEngine(t)
	ctx := context.Background()
	saveRule(t, s, FilterRule{Conditions: []RuleCondition{{Field: "to", Op: "contains", Value: "billing@"}}, Actions: []RuleAction{{Kind: "forward", Arg: "Accounts <accounts@ledger.example>"}}})
	msg := []byte("From: v@vendor.example\r\nTo: billing@example.com\r\nSubject: invoice\r\n\r\nx")
	if _, err := e.DeliverInbound("v@vendor.example", ruleBox, msg); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := e.queue.ProcessDue(ctx, time.Now().Add(time.Hour)); d != 0 || *sent != 0 {
		t.Fatalf("forwarded %d before the address confirmed", *sent)
	}
	tok, err := s.RequestRuleForward(ctx, ruleBox, "accounts@ledger.example")
	if err != nil || tok == "" {
		t.Fatalf("request: %q, %v", tok, err)
	}
	if _, _, err := s.ConfirmRuleForward(ctx, "not-the-token"); !errors.Is(err, ErrNoRuleForward) {
		t.Fatalf("a wrong token: %v", err)
	}
	if mb, addr, err := s.ConfirmRuleForward(ctx, tok); err != nil || mb != ruleBox || addr != "accounts@ledger.example" {
		t.Fatalf("confirm: %q %q %v", mb, addr, err)
	}
	if _, _, err := s.ConfirmRuleForward(ctx, tok); !errors.Is(err, ErrNoRuleForward) {
		t.Fatalf("a link used twice: %v", err)
	}
	if _, err := e.DeliverInbound("v@vendor.example", ruleBox, msg); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := e.queue.ProcessDue(ctx, time.Now().Add(2*time.Hour)); d != 1 {
		t.Fatalf("after confirming, %d forwarded, want 1", d)
	}
}

// TestRuleActionsAtDelivery — label, pin and mark read land on the message.
func TestRuleActionsAtDelivery(t *testing.T) {
	e, s, _ := ruleEngine(t)
	saveRule(t, s, FilterRule{Conditions: []RuleCondition{{Field: "subject", Op: "contains", Value: "receipt"}},
		Actions: []RuleAction{{Kind: "label", Arg: "Receipts"}, {Kind: "pin"}, {Kind: "markread"}}})
	if _, err := e.DeliverInbound("s@shop.example", ruleBox, []byte("From: s@shop.example\r\nSubject: your receipt\r\nMessage-ID: <r1@shop.example>\r\n\r\nx")); err != nil {
		t.Fatal(err)
	}
	msgs, _ := e.maildir.ListFolder("example.com", "ankush", "Inbox")
	if len(msgs) != 1 || !msgs[0].Seen || !msgs[0].Flagged {
		t.Fatalf("the message is %+v, want seen and pinned", msgs)
	}
	labels, _ := e.LabelsByMessage(ReadAsOwner("ankush"))
	if got := labels["r1@shop.example"]; len(got) != 1 || got[0] != "Receipts" {
		t.Fatalf("labels %v", labels)
	}
}

// TestRuleShapesThatAreRefused — one seed per refusal.
func TestRuleShapesThatAreRefused(t *testing.T) {
	_, s, _ := ruleEngine(t)
	c := []RuleCondition{{Field: "from", Op: "contains", Value: "x"}}
	for name, r := range map[string]FilterRule{
		"reject and more":   {Conditions: c, Actions: []RuleAction{{Kind: "reject"}, {Kind: "pin"}}},
		"forward to itself": {Conditions: c, Actions: []RuleAction{{Kind: "forward", Arg: ruleBox}}},
		"no conditions":     {Actions: []RuleAction{{Kind: "pin"}}},
		"a header unnamed":  {Conditions: []RuleCondition{{Field: "header", Op: "is", Value: "1"}}, Actions: []RuleAction{{Kind: "pin"}}},
		"a size not a size": {Conditions: []RuleCondition{{Field: "size", Value: "big"}}, Actions: []RuleAction{{Kind: "pin"}}},
	} {
		r.Mailbox = ruleBox
		if _, err := s.SaveRule(context.Background(), r); err == nil {
			t.Errorf("%s was saved", name)
		}
	}
}

// TestARuleShowsWhatItWouldCatch — before it runs.
func TestARuleShowsWhatItWouldCatch(t *testing.T) {
	e, _, _ := ruleEngine(t)
	for _, subj := range []string{"weekly digest", "hello", "the weekly digest again"} {
		if _, err := e.maildir.Deliver("example.com", "ankush", []byte("From: a@b.c\r\nSubject: "+subj+"\r\n\r\nx")); err != nil {
			t.Fatal(err)
		}
	}
	got, looked, err := e.RuleMatches(ReadAsOwner("ankush"), FilterRule{Mailbox: ruleBox,
		Conditions: []RuleCondition{{Field: "subject", Op: "contains", Value: "digest"}}, Actions: []RuleAction{{Kind: "markread"}}}, 500)
	if err != nil || looked != 3 || len(got) != 2 {
		t.Fatalf("matched %d of %d (%v), want 2 of 3", len(got), looked, err)
	}
	for _, m := range got {
		if !strings.Contains(m.Subject, "digest") {
			t.Errorf("matched %q", m.Subject)
		}
	}
}

// TestTheSenderIsToldARuleRefused — 550 with a fixed text, never the rule's
// words; any other failure stays a 451 to retry.
func TestTheSenderIsToldARuleRefused(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{{ErrRejectedByRule, "550 5.7.1 Message refused by the recipient"}, {errors.New("disk"), "451"}} {
		cfg := DefaultConfig()
		cfg.Domain = "example.com"
		cfg.Hostname = "mail.example.com"
		cfg.SMTPListen = "127.0.0.1:0"
		herr := c.err
		srv := NewSMTPServer(cfg, func(string, []string, []byte) error { return herr })
		if err := srv.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		conn, br := dialLines(t, srv.Addr())
		send := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		expectPrefix(t, br, "220")
		for _, l := range []string{"HELO client.test", "MAIL FROM:<x@promo.example>", "RCPT TO:<alice@example.com>"} {
			send(l)
			expectPrefix(t, br, "250")
		}
		send("DATA")
		expectPrefix(t, br, "354")
		send("From: x@promo.example\r\nSubject: buy\r\n\r\nx\r\n.")
		if l := expectPrefix(t, br, c.want[:3]); !strings.HasPrefix(l, c.want) {
			t.Errorf("handler error %v answered %q, want %q", c.err, l, c.want)
		}
		conn.Close()
		srv.Stop(context.Background())
	}
}

// TestAForwardLinkExpires — a link older than its week confirms nothing.
func TestAForwardLinkExpires(t *testing.T) {
	_, s, _ := ruleEngine(t)
	ctx := context.Background()
	tok, err := s.RequestRuleForward(ctx, ruleBox, "accounts@ledger.example")
	if err != nil || tok == "" {
		t.Fatal(tok, err)
	}
	if _, err := s.db.Exec(`UPDATE vayumail_rule_forwards SET created=?`, time.Now().UTC().Add(-ruleForwardLinkLife-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ConfirmRuleForward(ctx, tok); !errors.Is(err, ErrNoRuleForward) {
		t.Fatalf("an expired link: %v", err)
	}
	if s.RuleForwardConfirmed(ctx, ruleBox, "accounts@ledger.example") {
		t.Fatal("an expired link confirmed the forward")
	}
}

// TestForwardConfirmationsAreNotSentOverAndOver — saving a rule again does not
// mail its address again within the day, and one mailbox cannot mail many
// addresses with confirmations in a day.
func TestForwardConfirmationsAreNotSentOverAndOver(t *testing.T) {
	_, s, _ := ruleEngine(t)
	ctx := context.Background()
	if tok, err := s.RequestRuleForward(ctx, ruleBox, "a@ledger.example"); err != nil || tok == "" {
		t.Fatal(tok, err)
	}
	if tok, err := s.RequestRuleForward(ctx, ruleBox, "a@ledger.example"); tok != "" || !errors.Is(err, ErrRuleForwardWait) {
		t.Fatalf("a second request the same day: %q, %v", tok, err)
	}
	for i := 1; i < maxRuleForwardAsksPerDay; i++ {
		if tok, err := s.RequestRuleForward(ctx, ruleBox, "b"+strconv.Itoa(i)+"@ledger.example"); err != nil || tok == "" {
			t.Fatalf("request %d: %q, %v", i, tok, err)
		}
	}
	if tok, err := s.RequestRuleForward(ctx, ruleBox, "one-too-many@ledger.example"); tok != "" || !errors.Is(err, ErrRuleForwardWait) {
		t.Fatalf("past the day's asks: %q, %v", tok, err)
	}
	if _, err := s.db.Exec(`UPDATE vayumail_rule_forwards SET created=?`, time.Now().UTC().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if tok, err := s.RequestRuleForward(ctx, ruleBox, "a@ledger.example"); err != nil || tok == "" {
		t.Fatalf("the next day: %q, %v", tok, err)
	}
}

// TestADeletedMailboxsForwardsAreNotHandedOn — an address given again does
// not inherit the addresses that agreed to take its last holder's mail.
func TestADeletedMailboxsForwardsAreNotHandedOn(t *testing.T) {
	_, s, _ := ruleEngine(t)
	ctx := context.Background()
	tok, _ := s.RequestRuleForward(ctx, ruleBox, "old-holder@home.example")
	if _, _, err := s.ConfirmRuleForward(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, ruleBox); err != nil {
		t.Fatal(err)
	}
	if s.RuleForwardConfirmed(ctx, ruleBox, "old-holder@home.example") {
		t.Fatal("the next holder of the address inherits a confirmed forward")
	}
}

// TestOneCopyPerForwardAddress — two rules that forward to one address send
// it one copy.
func TestOneCopyPerForwardAddress(t *testing.T) {
	e, s, _ := ruleEngine(t)
	ctx := context.Background()
	for _, w := range []string{"invoice", "billing"} {
		saveRule(t, s, FilterRule{KeepGoing: true, Conditions: []RuleCondition{{Field: "subject", Op: "contains", Value: w}},
			Actions: []RuleAction{{Kind: "forward", Arg: "accounts@ledger.example"}}})
	}
	tok, _ := s.RequestRuleForward(ctx, ruleBox, "accounts@ledger.example")
	if _, _, err := s.ConfirmRuleForward(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DeliverInbound("v@vendor.example", ruleBox, []byte("From: v@vendor.example\r\nSubject: billing invoice\r\n\r\nx")); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := e.queue.ProcessDue(ctx, time.Now().Add(time.Hour)); d != 1 {
		t.Fatalf("%d copies sent to one address, want 1", d)
	}
}

// TestABodyIsLoweredOnceForAllItsConditions — the body conditions of every
// rule read one lowered copy of the message, made at the first of them: a
// message seen with a copy already made is read from that copy, not again.
func TestABodyIsLoweredOnceForAllItsConditions(t *testing.T) {
	m := &ruleMessage{raw: []byte("From: a@b.c\r\nSubject: s\r\n\r\nSome INVOICE text\r\n")}
	if !matchCondition(RuleCondition{Field: "body", Op: "contains", Value: "invoice"}, m) {
		t.Fatal("the body condition did not match")
	}
	if !m.bodyRead || !strings.Contains(m.body, "some invoice text") {
		t.Fatalf("the lowered body kept is %q", m.body)
	}
	m.body = "only the kept copy says this"
	if !matchCondition(RuleCondition{Field: "body", Op: "contains", Value: "kept copy"}, m) {
		t.Fatal("a second body condition lowered the message again instead of reading the kept copy")
	}
}
