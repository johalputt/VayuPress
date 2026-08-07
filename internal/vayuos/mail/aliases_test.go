// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func aliasTestStore(t *testing.T) *AccountStore {
	t.Helper()
	db, _ := sql.Open("sqlite3", ":memory:")
	db.SetMaxOpenConns(1) // :memory: is per-connection; pin the pool
	t.Cleanup(func() { db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := s.Create(context.Background(), "ankush@example.com", "hash", "Ankush", "administrator"); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return s
}

func TestAliasCreateResolveDelete(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	ctx := context.Background()

	if err := s.CreateAlias(ctx, "Sales@Example.com", "ankush@example.com"); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	// Resolution is case-insensitive and returns the target.
	if got := s.ResolveAlias(ctx, "SALES@example.com"); got != "ankush@example.com" {
		t.Fatalf("resolve = %q, want ankush@example.com", got)
	}
	// A real account address is not an alias.
	if got := s.ResolveAlias(ctx, "ankush@example.com"); got != "" {
		t.Fatalf("account resolved as alias: %q", got)
	}

	// Referential rules.
	if err := s.CreateAlias(ctx, "sales@example.com", "ankush@example.com"); err == nil {
		t.Fatal("duplicate alias must be rejected")
	}
	if err := s.CreateAlias(ctx, "x@example.com", "ghost@example.com"); err == nil {
		t.Fatal("alias to a non-existent mailbox must be rejected")
	}
	if err := s.CreateAlias(ctx, "ankush@example.com", "ankush@example.com"); err == nil {
		t.Fatal("alias shadowing a mailbox / self must be rejected")
	}
	// An alias can never target another alias (single-level guarantee).
	if err := s.CreateAlias(ctx, "info@example.com", "sales@example.com"); err == nil {
		t.Fatal("alias chaining must be rejected")
	}

	if list, _ := s.ListAliases(ctx); len(list) != 1 || list[0].Alias != "sales@example.com" {
		t.Fatalf("list = %+v, want the one alias", list)
	}
	if err := s.DeleteAlias(ctx, "sales@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := s.ResolveAlias(ctx, "sales@example.com"); got != "" {
		t.Fatalf("deleted alias still resolves: %q", got)
	}
}

func TestForwardSetAndRules(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	ctx := context.Background()

	if got := s.ForwardFor(ctx, "ankush@example.com"); got != "" {
		t.Fatalf("forward defaults on: %q", got)
	}
	if err := s.SetForward(ctx, "ankush@example.com", "Backup@Other.com"); err != nil {
		t.Fatalf("set forward: %v", err)
	}
	if got := s.ForwardFor(ctx, "ankush@example.com"); got != "backup@other.com" {
		t.Fatalf("forward = %q, want backup@other.com", got)
	}
	// Self-forwarding is rejected; unknown accounts error; clearing works.
	if err := s.SetForward(ctx, "ankush@example.com", "ankush@example.com"); err == nil {
		t.Fatal("self-forward must be rejected")
	}
	if err := s.SetForward(ctx, "ghost@example.com", "x@y.com"); err == nil {
		t.Fatal("forward on unknown account must error")
	}
	if err := s.SetForward(ctx, "ankush@example.com", ""); err != nil {
		t.Fatalf("clear forward: %v", err)
	}
	if got := s.ForwardFor(ctx, "ankush@example.com"); got != "" {
		t.Fatalf("forward not cleared: %q", got)
	}
}

func TestForwardLoopHeaderHelpers(t *testing.T) {
	t.Parallel()
	raw := []byte("From: a@b.c\r\nSubject: hi\r\n\r\nbody " + forwardLoopHeader + ": spoof\r\n")
	// A mention in the BODY must not trip the loop guard.
	if hasHeader(raw, forwardLoopHeader) {
		t.Fatal("body text spoofed the header check")
	}
	tagged := prependHeader(raw, forwardLoopHeader+": ankush@example.com")
	if !hasHeader(tagged, forwardLoopHeader) {
		t.Fatal("tagged message not detected")
	}
	// Case-insensitive detection.
	lower := []byte("x-vayumail-forwarded: a@b.c\r\nSubject: s\r\n\r\nbody")
	if !hasHeader(lower, forwardLoopHeader) {
		t.Fatal("lower-case header not detected")
	}
}

// TestDeliverInboundAliasAndForward exercises the full inbound path: an alias
// resolves to its target mailbox, and a configured auto-forward enqueues a
// loop-tagged copy while never forwarding an already-forwarded message.
func TestDeliverInboundAliasAndForward(t *testing.T) {
	t.Parallel()
	s := aliasTestStore(t)
	ctx := context.Background()
	if err := s.CreateAlias(ctx, "sales@example.com", "ankush@example.com"); err != nil {
		t.Fatalf("alias: %v", err)
	}
	if err := s.SetForward(ctx, "ankush@example.com", "backup@other.com"); err != nil {
		t.Fatalf("forward: %v", err)
	}

	var enq [][]byte
	var enqTo [][]string
	qdb, _ := sql.Open("sqlite3", ":memory:")
	qdb.SetMaxOpenConns(1)
	t.Cleanup(func() { qdb.Close() })
	q, err := NewQueue(qdb, DefaultConfig(), func(_ context.Context, _ string, to []string, raw []byte) error {
		enqTo = append(enqTo, to)
		enq = append(enq, raw)
		return nil
	})
	if err != nil {
		t.Fatalf("queue: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	e := &Engine{cfg: cfg, maildir: NewMaildir(t.TempDir()), accounts: s, queue: q}

	// Alias acceptance at RCPT time.
	if !e.isLocalRecipient("sales@example.com") {
		t.Fatal("alias must be accepted as a local recipient")
	}
	if e.isLocalRecipient("ghost@elsewhere.com") {
		t.Fatal("foreign domain accepted")
	}

	// Deliver to the ALIAS: it must land in the TARGET mailbox and enqueue a
	// tagged forward copy.
	raw := []byte("From: x@y.z\r\nSubject: order\r\n\r\nhello")
	if _, err := e.DeliverInbound("sender@other.test", "sales@example.com", raw); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	st, _ := e.maildir.Stats("example.com", "ankush")
	if st.Messages != 1 {
		t.Fatalf("target mailbox has %d messages, want 1", st.Messages)
	}
	// Drain the queue and inspect the forwarded copy.
	if d, f, _ := q.ProcessDue(ctx, time.Now().Add(time.Hour)); d != 1 || f != 0 {
		t.Fatalf("forward queue: delivered=%d failed=%d", d, f)
	}
	if len(enq) != 1 || len(enqTo) != 1 || enqTo[0][0] != "backup@other.com" {
		t.Fatalf("forward recipient = %+v, want backup@other.com", enqTo)
	}
	if !hasHeader(enq[0], forwardLoopHeader) {
		t.Fatal("forwarded copy is not loop-tagged")
	}

	// A message that ALREADY carries the tag must not be forwarded again.
	tagged := prependHeader(raw, forwardLoopHeader+": elsewhere@x.y")
	if _, err := e.DeliverInbound("sender@other.test", "ankush@example.com", tagged); err != nil {
		t.Fatalf("deliver tagged: %v", err)
	}
	if d, _, _ := q.ProcessDue(ctx, time.Now().Add(2*time.Hour)); d != 0 {
		t.Fatal("loop-tagged message was forwarded again")
	}
}
