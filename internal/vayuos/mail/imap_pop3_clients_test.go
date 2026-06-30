package mail

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func memDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestIMAP starts an IMAP server backed by a temp Maildir and an in-memory
// UID store, returning the server and its Maildir for seeding.
func newTestIMAP(t *testing.T) (*IMAPServer, *Maildir) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.IMAPListen = "127.0.0.1:0"
	md := NewMaildir(t.TempDir())
	us, err := NewUIDStore(memDB(t))
	if err != nil {
		t.Fatalf("uid store: %v", err)
	}
	srv := NewIMAPServer(cfg, stubBridge{}, md, nil).WithUIDStore(us)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("imap start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })
	return srv, md
}

// converse opens a connection, sends each line (CRLF appended), then drains the
// whole reply with a short deadline and returns it. Use "{n+}" literals so no
// server continuation prompt is needed mid-stream.
func converse(t *testing.T, addr string, lines ...string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	for _, l := range lines {
		if _, err := conn.Write([]byte(l + "\r\n")); err != nil {
			t.Fatalf("write %q: %v", l, err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	all, _ := io.ReadAll(conn)
	return string(all)
}

func mustContain(t *testing.T, resp string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(resp, w) {
			t.Errorf("response missing %q\n---\n%s", w, resp)
		}
	}
}

func TestIMAPStableUIDsAcrossReconnect(t *testing.T) {
	t.Parallel()
	srv, md := newTestIMAP(t)
	for i := 1; i <= 3; i++ {
		raw := fmt.Sprintf("From: a%d@partner.test\r\nSubject: Msg %d\r\nDate: Mon, 02 Jan 2006 15:04:0%d -0000\r\n\r\nBody %d\r\n", i, i, i, i)
		if _, err := md.Deliver("example.com", "bob", []byte(raw)); err != nil {
			t.Fatalf("seed: %v", err)
		}
		time.Sleep(2 * time.Millisecond) // distinct mtimes → stable ordering
	}

	first := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX", "c UID FETCH 1:* (UID FLAGS)", "d LOGOUT")
	mustContain(t, first, "3 EXISTS", "UID 1", "UID 2", "UID 3", "[UIDVALIDITY")

	// Reconnecting must yield the SAME UIDs (persisted), which is what lets a
	// real client sync incrementally instead of re-downloading everything.
	second := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX", "c UID FETCH 1:* (UID)", "d LOGOUT")
	mustContain(t, second, "UID 1", "UID 2", "UID 3")
}

func TestIMAPListAllFoldersSpecialUse(t *testing.T) {
	t.Parallel()
	srv, _ := newTestIMAP(t)
	resp := converse(t, srv.Addr(), "a LOGIN bob pw", `b LIST "" "*"`, "c LOGOUT")
	mustContain(t, resp,
		`"INBOX"`, `\Sent`, `"Sent"`, `\Drafts`, `\Junk`, `\Trash`, `\Archive`,
		"b OK LIST completed")
}

func TestIMAPAppendRoundTrip(t *testing.T) {
	t.Parallel()
	srv, _ := newTestIMAP(t)
	msg := "From: me@example.com\r\nSubject: Saved Sent Copy\r\n\r\nHello sent.\r\n"
	resp := converse(t, srv.Addr(),
		"a LOGIN bob pw",
		fmt.Sprintf(`b APPEND "Sent" (\Seen) {%d+}`, len(msg)),
		msg,
		`c SELECT "Sent"`,
		"d FETCH 1 (FLAGS BODY[])",
		"e LOGOUT",
	)
	mustContain(t, resp, "b OK", "1 EXISTS", "Saved Sent Copy", `\Seen`, "d OK FETCH completed")
}

func TestIMAPStoreFlagsPersist(t *testing.T) {
	t.Parallel()
	srv, md := newTestIMAP(t)
	_, _ = md.Deliver("example.com", "bob", []byte("From: x@y.z\r\nSubject: Flagme\r\n\r\nb\r\n"))

	resp := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX",
		`c STORE 1 +FLAGS (\Seen \Flagged)`,
		"d LOGOUT")
	mustContain(t, resp, `\Flagged`, `\Seen`, "c OK STORE completed")

	// Re-select: the flags must still be set (persisted in the Maildir name).
	resp2 := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX", "c FETCH 1 (FLAGS)", "d LOGOUT")
	mustContain(t, resp2, `\Flagged`, `\Seen`)
}

func TestIMAPCopyMoveExpunge(t *testing.T) {
	t.Parallel()
	srv, md := newTestIMAP(t)
	for i := 0; i < 2; i++ {
		_, _ = md.Deliver("example.com", "bob", []byte(fmt.Sprintf("From: x@y.z\r\nSubject: M%d\r\n\r\nb\r\n", i)))
		time.Sleep(2 * time.Millisecond)
	}
	// Copy message 1 to Archive, then verify Archive has it.
	resp := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX",
		"c COPY 1 Archive",
		`d SELECT "Archive"`, "e LOGOUT")
	mustContain(t, resp, "c OK", "1 EXISTS")

	// Delete + expunge message 1 in INBOX.
	resp2 := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX",
		`c STORE 1 +FLAGS (\Deleted)`,
		"d EXPUNGE",
		"e LOGOUT")
	mustContain(t, resp2, "* 1 EXPUNGE", "d OK EXPUNGE completed")
}

func TestIMAPMove(t *testing.T) {
	t.Parallel()
	srv, md := newTestIMAP(t)
	_, _ = md.Deliver("example.com", "bob", []byte("From: x@y.z\r\nSubject: MoveMe\r\n\r\nb\r\n"))
	resp := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX",
		"c MOVE 1 Trash",
		`d SELECT "Trash"`, "e LOGOUT")
	mustContain(t, resp, "* 1 EXPUNGE", "c OK MOVE completed", "1 EXISTS")
}

func TestIMAPSearch(t *testing.T) {
	t.Parallel()
	srv, md := newTestIMAP(t)
	_, _ = md.Deliver("example.com", "bob", []byte("From: alice@p.test\r\nSubject: Needle here\r\n\r\nb\r\n"))
	time.Sleep(2 * time.Millisecond)
	_, _ = md.Deliver("example.com", "bob", []byte("From: bob@p.test\r\nSubject: Other\r\n\r\nb\r\n"))
	resp := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX",
		"c SEARCH SUBJECT Needle",
		"d SEARCH UNSEEN",
		"e LOGOUT")
	mustContain(t, resp, "* SEARCH 1", "c OK SEARCH completed", "d OK SEARCH completed")
}

func TestIMAPFetchEnvelopeBodystructure(t *testing.T) {
	t.Parallel()
	srv, md := newTestIMAP(t)
	raw := "From: Alice <alice@partner.test>\r\nTo: bob@example.com\r\n" +
		"Subject: Structured Mail\r\nDate: Mon, 02 Jan 2006 15:04:05 -0000\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\nHello structured.\r\n"
	_, _ = md.Deliver("example.com", "bob", []byte(raw))
	resp := converse(t, srv.Addr(),
		"a LOGIN bob pw", "b SELECT INBOX",
		"c FETCH 1 (ENVELOPE BODYSTRUCTURE)",
		"d LOGOUT")
	mustContain(t, resp, "ENVELOPE", "Structured Mail", "alice", "BODYSTRUCTURE", `"TEXT" "PLAIN"`)
}

// TestIMAPThunderbirdAndroidSync reproduces the command sequence the new
// Thunderbird for Android / K-9 uses on first sync: ENABLE, then extended-LIST
// with a (SUBSCRIBED) selection option and a RETURN (SPECIAL-USE) group, then
// SELECT INBOX and a UID fetch. Before the extended-LIST fix, the selection
// option was mistaken for the pattern, LIST returned no INBOX, and the client
// synced nothing.
func TestIMAPThunderbirdAndroidSync(t *testing.T) {
	t.Parallel()
	srv, md := newTestIMAP(t)
	_, _ = md.Deliver("example.com", "bob", []byte("From: a@partner.test\r\nSubject: Hello TfA\r\n\r\nbody\r\n"))

	resp := converse(t, srv.Addr(),
		"a LOGIN bob pw",
		"b ENABLE UTF8=ACCEPT",
		`c LIST (SUBSCRIBED) "" "*" RETURN (SPECIAL-USE)`,
		`d LIST "" "*" RETURN (SPECIAL-USE)`,
		`e LSUB "" "*"`,
		`f SUBSCRIBE "INBOX"`,
		"g SELECT INBOX",
		"h UID FETCH 1:* (UID FLAGS)",
		"i LOGOUT")
	mustContain(t, resp,
		"b OK ENABLE completed",
		`"INBOX"`, "c OK LIST completed",
		`\Subscribed`,             // K-9/TfA only syncs folders it sees as subscribed
		"f OK SUBSCRIBE completed", // explicit SUBSCRIBE must not be a fatal BAD
		"1 EXISTS", "UID 1")
}

func TestIMAPAuthenticatePlain(t *testing.T) {
	t.Parallel()
	srv, _ := newTestIMAP(t)
	// base64 of "\x00bob\x00pw"
	resp := converse(t, srv.Addr(), "a AUTHENTICATE PLAIN AGJvYgBwdw==", "b LIST \"\" \"*\"", "c LOGOUT")
	mustContain(t, resp, "a OK AUTHENTICATE completed", `"INBOX"`)
}

// ── POP3 ─────────────────────────────────────────────────────────────────────

func newTestPOP3(t *testing.T) (*POP3Server, *Maildir) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.POP3Listen = "127.0.0.1:0"
	md := NewMaildir(t.TempDir())
	srv := NewPOP3Server(cfg, stubBridge{}, md, nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("pop3 start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })
	return srv, md
}

func TestPOP3Session(t *testing.T) {
	t.Parallel()
	srv, md := newTestPOP3(t)
	_, _ = md.Deliver("example.com", "bob", []byte("From: x@y.z\r\nSubject: POP One\r\n\r\nfirst\r\n"))
	time.Sleep(2 * time.Millisecond)
	_, _ = md.Deliver("example.com", "bob", []byte("From: x@y.z\r\nSubject: POP Two\r\n\r\nsecond\r\n"))

	resp := converse(t, srv.Addr(),
		"USER bob", "PASS pw", "STAT", "LIST", "UIDL", "RETR 1", "QUIT")
	mustContain(t, resp,
		"+OK VayuMail POP3 ready",
		"+OK mailbox ready, 2 messages",
		"+OK 2 ", // STAT: 2 messages
		"POP One",
		"+OK VayuMail POP3 signing off",
	)
}

func TestPOP3DeleteOnQuit(t *testing.T) {
	t.Parallel()
	srv, md := newTestPOP3(t)
	_, _ = md.Deliver("example.com", "bob", []byte("From: x@y.z\r\nSubject: Bye\r\n\r\nx\r\n"))

	resp := converse(t, srv.Addr(), "USER bob", "PASS pw", "DELE 1", "QUIT")
	mustContain(t, resp, "+OK message 1 deleted")

	// After QUIT, the message file is gone, so a fresh session sees zero.
	resp2 := converse(t, srv.Addr(), "USER bob", "PASS pw", "STAT", "QUIT")
	mustContain(t, resp2, "+OK mailbox ready, 0 messages")
}

func TestPOP3RejectsBadCredentials(t *testing.T) {
	t.Parallel()
	srv, _ := newTestPOP3(t)
	resp := converse(t, srv.Addr(), "USER bob", "PASS wrong", "STAT", "QUIT")
	mustContain(t, resp, "-ERR")
	if strings.Contains(resp, "mailbox ready") {
		t.Errorf("bad credentials must not authenticate:\n%s", resp)
	}
}
