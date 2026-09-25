// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The console offers "Reviewer — read-only, mail only". For as long as the role
// existed nothing checked it: a reviewer could send from webmail and over SMTP
// submission, and delete from webmail, IMAP and POP3. The three predicates that
// described the role were called by nothing but their own unit test, which is
// how a claim passes for a control.
//
// Each rule below is held by its own seed and asserts the refusal it expects,
// and each is paired with the same session under a role that is not read-only,
// so a guard that refused everybody would fail too.

func readOnlyIMAP(t *testing.T, readOnly bool) (*IMAPServer, *Maildir) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.IMAPListen = "127.0.0.1:0"
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "bob"); err != nil {
		t.Fatalf("create maildir: %v", err)
	}
	srv := NewIMAPServer(cfg, stubBridge{}, md, nil).
		WithReadOnly(func(login string) bool { return readOnly && login == "bob" })
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("imap start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	if _, err := md.Deliver("example.com", "bob", []byte("From: x@y.z\r\nSubject: Keep\r\n\r\nbody\r\n")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	return srv, md
}

func inboxCount(t *testing.T, md *Maildir) int {
	t.Helper()
	msgs, err := md.ListFolder("example.com", "bob", "Inbox")
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	return len(msgs)
}

func TestAReviewerIMAPSessionIsReadOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		readOnly bool
		select_  string
		refused  bool
	}{
		{"reviewer", true, "s OK [READ-ONLY] SELECT completed", true},
		{"mailbox role", false, "s OK [READ-WRITE] SELECT completed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// One seed per rule: each command runs in its own session against a
			// fresh mailbox, so a refusal is attributable to that command's guard.
			cases := map[string][]string{
				"STORE":   {`x STORE 1 +FLAGS (\Deleted)`},
				"EXPUNGE": {`y EXPUNGE`},
				"MOVE":    {`z MOVE 1 Trash`},
				"CLOSE":   {`w STORE 1 +FLAGS (\Deleted)`, `c CLOSE`},
			}
			tags := map[string]string{"STORE": "x", "EXPUNGE": "y", "MOVE": "z", "CLOSE": "w"}
			for cmd, lines := range cases {
				srv, md := readOnlyIMAP(t, tc.readOnly)
				session := append([]string{"a LOGIN bob pw", "s SELECT INBOX"}, lines...)
				resp := converse(t, srv.Addr(), append(session, "q LOGOUT")...)
				mustContain(t, resp, tc.select_)
				refusal := tags[cmd] + " NO Mailbox is read-only"
				if got := strings.Contains(resp, refusal); got != tc.refused {
					t.Errorf("%s as %s: refused=%v, want %v.\n\nServer said:\n%s", cmd, tc.name, got, tc.refused, resp)
				}
				if tc.readOnly && inboxCount(t, md) != 1 {
					t.Errorf("%s as a reviewer removed a message from the inbox", cmd)
				}
			}
		})
	}
}

func readOnlyPOP3(t *testing.T, readOnly bool) (*POP3Server, *Maildir) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.POP3Listen = "127.0.0.1:0"
	md := NewMaildir(t.TempDir())
	srv := NewPOP3Server(cfg, stubBridge{}, md, nil).
		WithReadOnly(func(login string) bool { return readOnly && login == "bob" })
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("pop3 start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	if _, err := md.Deliver("example.com", "bob", []byte("From: x@y.z\r\nSubject: Keep\r\n\r\nbody\r\n")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	return srv, md
}

func TestAReviewerCannotDeleteOverPOP3(t *testing.T) {
	t.Parallel()
	srv, md := readOnlyPOP3(t, true)
	resp := converse(t, srv.Addr(), "USER bob", "PASS pw", "DELE 1", "QUIT")
	mustContain(t, resp, "-ERR [SYS/PERM] this mailbox is read-only")
	if inboxCount(t, md) != 1 {
		t.Error("a reviewer's DELE + QUIT removed the message")
	}

	srv, md = readOnlyPOP3(t, false)
	resp = converse(t, srv.Addr(), "USER bob", "PASS pw", "DELE 1", "QUIT")
	mustContain(t, resp, "+OK message 1 deleted")
	if inboxCount(t, md) != 0 {
		t.Error("the control session's DELE + QUIT did not delete; the POP3 test proves nothing")
	}
}

func TestAReviewerCannotSendOverSubmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		readOnly bool
		want     string
	}{
		{"reviewer", true, "550 5.7.1 This mailbox is read-only and cannot send"},
		{"mailbox role", false, "250"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			relayed := false
			conn, br := submissionHarness(t, func(string, []string, []byte) error { relayed = true; return nil },
				func(s *SMTPServer) *SMTPServer {
					return s.WithReadOnly(func(u string) bool { return tc.readOnly && u == "alice@example.com" })
				})
			_, _ = conn.Write([]byte("MAIL FROM:<alice@example.com>\r\n"))
			if got := readUntilFinal(t, br); !strings.HasPrefix(got, tc.want) {
				t.Errorf("MAIL as %s: %q, want it to start %q", tc.name, got, tc.want)
			}
			if tc.readOnly {
				// The transaction is cleared, so what follows cannot submit
				// with an empty envelope sender.
				_, _ = conn.Write([]byte("RCPT TO:<bob@example.net>\r\n"))
				if got := readUntilFinal(t, br); !strings.HasPrefix(got, "503") {
					t.Errorf("RCPT after a refused MAIL: %q, want 503", got)
				}
				if relayed {
					t.Error("a reviewer's message was relayed")
				}
			}
		})
	}
}

// The engine's own gate, which every webmail delete and move passes through.
// It binds the holder, not an operator acting on the mailbox.
func TestTheEngineRefusesAReviewerDeleteOrMoveOnly(t *testing.T) {
	t.Parallel()
	e := readOnlyEngine(t)
	ctx := context.Background()
	for _, acct := range []struct{ email, role string }{
		{"rev@example.com", RoleReviewer}, {"box@example.com", RoleMailbox},
	} {
		if err := e.accounts.Create(ctx, acct.email, "hash", "", acct.role); err != nil {
			t.Fatalf("create %s: %v", acct.email, err)
		}
		if err := e.maildir.CreateAll("example.com", strings.Split(acct.email, "@")[0]); err != nil {
			t.Fatalf("maildir: %v", err)
		}
	}
	deliver := func(local string) string {
		id, err := e.maildir.Deliver("example.com", local, []byte("From: x@y.z\r\nSubject: s\r\n\r\nb\r\n"))
		if err != nil {
			t.Fatalf("deliver: %v", err)
		}
		return id
	}

	id := deliver("rev")
	if err := e.DeleteMessage(ReadAsOwner("rev"), "Inbox", id); !errors.Is(err, ErrReadOnlyMailbox) {
		t.Errorf("a reviewer deleting their own mail: %v, want ErrReadOnlyMailbox", err)
	}
	if err := e.MoveMessage(ReadAsOwner("rev"), id, "Inbox", "Trash"); !errors.Is(err, ErrReadOnlyMailbox) {
		t.Errorf("a reviewer moving their own mail: %v, want ErrReadOnlyMailbox", err)
	}
	if !e.ReaderReadOnly(ReadAsOwner("rev@example.com")) {
		t.Error("a full address did not resolve to the reviewer's role")
	}
	// An operator acting on the reviewer's mailbox is not the reviewer.
	if err := e.DeleteMessage(ReadAsOperator("rev", "admin@example.com"), "Inbox", id); err != nil {
		t.Errorf("an operator delete on a reviewer's mailbox: %v", err)
	}
	// And a holder whose role is not read-only is unaffected.
	if err := e.DeleteMessage(ReadAsOwner("box"), "Inbox", deliver("box")); err != nil {
		t.Errorf("a mailbox-role delete: %v", err)
	}
}

func readOnlyEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	cfg := testEngineConfig()
	cfg.Enabled = true
	cfg.InboundEnabled = true
	cfg.Domain = "example.com"
	cfg.StorageDir = t.TempDir()
	e := NewEngine(&cfg, quotaBridge{}, db)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	return e
}

// The wiring, held separately from the guards: every test above builds its
// server and calls WithReadOnly itself, so all of them pass with the engine
// never attaching it (the quota guard shipped exactly that way).
func TestTheEngineWiresReadOnlyIntoEveryListener(t *testing.T) {
	t.Parallel()
	e := readOnlyEngine(t)
	if err := e.accounts.Create(context.Background(), "rev@example.com", "hash", "", RoleReviewer); err != nil {
		t.Fatalf("create: %v", err)
	}
	if e.imapd == nil || e.pop3d == nil {
		t.Fatal("the engine started no IMAP or POP3 listener; this test would prove nothing")
	}
	// The wired function is the real one: it must answer from the account store.
	for name, f := range map[string]func(string) bool{"IMAP": e.imapd.readOnlyFor, "POP3": e.pop3d.readOnlyFor} {
		if f == nil || !f("rev") || f("someone-else") {
			t.Errorf("the engine's %s listener does not carry the read-only role check", name)
		}
	}

	// The TLS listeners (993, 995, 587) start only with a certificate, so their
	// wiring is held on the source: every server the engine builds is built
	// with the check.
	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	built := regexp.MustCompile(`New(IMAPServer|POP3Server|SubmissionServer)\(.*`).FindAllString(string(src), -1)
	if len(built) != 5 {
		t.Fatalf("engine.go builds %d protocol servers, want 5 (IMAP, IMAPS, POP3, POP3S, submission); update this test with the change", len(built))
	}
	for _, line := range built {
		if !strings.Contains(line, ".WithReadOnly(e.MailboxReadOnly)") {
			t.Errorf("engine.go builds a server without the read-only check:\n  %s", strings.TrimSpace(line))
		}
	}
}
