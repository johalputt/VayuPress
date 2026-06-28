package mail

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newInboundEngine(t *testing.T, smtpListen, imapListen string) *Engine {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.StorageDir = t.TempDir()
	cfg.InboundEnabled = true
	cfg.SMTPListen = smtpListen
	cfg.IMAPListen = imapListen
	// Use ephemeral ports for the POP3 listeners too, so the test never tries to
	// bind privileged :110/:995 (which fails as non-root in CI). The plaintext
	// POP3 bind is recorded in InboundError just like SMTP/IMAP, so it must be a
	// free port for the "starts cleanly" assertion to hold.
	cfg.POP3Listen = "127.0.0.1:0"
	cfg.POP3SListen = "127.0.0.1:0"
	e := NewEngine(&cfg, nil, db)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { e.Stop(context.Background()) })
	return e
}

// Inbound must be on by default so a configured domain can receive external
// mail without an extra opt-in step.
func TestInboundEnabledByDefault(t *testing.T) {
	t.Parallel()
	if !DefaultConfig().InboundEnabled {
		t.Fatalf("expected inbound to be enabled by default")
	}
}

// With inbound enabled and bindable ports, the listener comes up and reports
// active with no error.
func TestInboundListenerStartsOnFreePorts(t *testing.T) {
	t.Parallel()
	e := newInboundEngine(t, "127.0.0.1:0", "127.0.0.1:0")
	if !e.InboundActive() {
		t.Fatalf("expected inbound listener to be active")
	}
	if err := e.InboundError(); err != nil {
		t.Fatalf("unexpected inbound error: %v", err)
	}
	if addr := e.smtpd.Addr(); addr == "" {
		t.Fatalf("expected a bound SMTP address")
	}
}

// A bind failure (here: an invalid port) must NOT fail engine startup. The
// engine stays up for outbound/local delivery and surfaces the reason via
// InboundError so the panel can explain it.
func TestInboundBindFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	e := newInboundEngine(t, "127.0.0.1:999999", "127.0.0.1:0")
	if e.InboundActive() {
		t.Fatalf("listener should not be active after a failed bind")
	}
	if e.InboundError() == nil {
		t.Fatalf("expected InboundError to record the failed bind")
	}
	// Outbound/local delivery must still be usable despite the inbound failure.
	if _, err := e.DeliverInbound("bob@example.com", []byte(
		"From: a@partner.test\r\nTo: bob@example.com\r\nSubject: hi\r\n"+
			"Date: Mon, 02 Jan 2006 15:04:05 -0700\r\n\r\nbody\r\n")); err != nil {
		t.Fatalf("local delivery should still work: %v", err)
	}
	msgs, err := e.ListFolder("bob", "Inbox")
	if err != nil || len(msgs) != 1 {
		t.Fatalf("expected 1 inbox message, got %d (err=%v)", len(msgs), err)
	}
}
