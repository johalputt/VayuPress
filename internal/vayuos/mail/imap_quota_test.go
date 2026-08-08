// SPDX-License-Identifier: Apache-2.0

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
)

// SECTION 2 AUDIT FINDING — the storage quota is enforced on one path out of
// several, and the two it misses are the scriptable ones.
//
// In the operator's voice, because this is a control they set deliberately:
//
//	I gave this mailbox a 10 MB quota. That is the number on the page, and it
//	is why I am comfortable handing a mailbox to someone I do not know well.
//
//	It binds inbound delivery, and it binds webmail send and draft-save. It does
//	not bind APPEND, which is how every IMAP client uploads a message, and it
//	does not bind COPY, which duplicates one that is already here. Both are one
//	line in a script and neither is rate-limited by anything but the network.
//
//	The disk they fill is the disk SQLite is on — the website, the blog, the
//	analytics and the write-ahead log. When it fills, the install stops writing,
//	and the quota I set to prevent exactly that was never in the way.
//
// The default is unlimited (0), so an install that never set a quota is no worse
// off than it was. The defect is narrower and worse than "a mailbox can grow":
// an operator who set the limit was told it applied.
//
// MOVE is deliberately NOT bounded, and that is not an oversight. A move is
// net-neutral — one file, one directory to another — and it is how a person
// gets back under quota: everything into Trash, then expunge. Refusing it while
// full would take away the one path out of the state, which is the shape of
// mistake §17 names.

// quotaBridge authenticates the one mailbox these tests use.
type quotaBridge struct{}

func (quotaBridge) AuthUser(username, password string) (bool, error) {
	return username == "bob" && password == "pw", nil
}
func (quotaBridge) GetUserByEmail(string) (*MailUser, error)          { return nil, nil }
func (quotaBridge) IsLocalRecipient(string) bool                      { return false }
func (quotaBridge) SendTransactional(*TransactionalMessage) error     { return nil }
func (quotaBridge) EncryptForRecipient([]byte, string) ([]byte, bool) { return nil, false }
func (quotaBridge) EncryptForRecipients([]byte, []string) ([]byte, []string, bool) {
	return nil, nil, false
}
func (quotaBridge) SignAs([]byte, string) ([]byte, bool) { return nil, false }

// imapConverse runs one IMAP session and returns everything the server said.
func imapConverse(t *testing.T, addr string, lines ...string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	for _, l := range lines {
		if _, err := conn.Write([]byte(l + "\r\n")); err != nil {
			t.Fatalf("write %q: %v", l, err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	all, _ := io.ReadAll(conn)
	return string(all)
}

// quotaServer starts an IMAP server for bob@example.com with a quota wired in.
func quotaServer(t *testing.T, quota int64) (*IMAPServer, *Maildir) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.IMAPListen = "127.0.0.1:0"
	md := NewMaildir(t.TempDir())
	if err := md.CreateAll("example.com", "bob"); err != nil {
		t.Fatalf("create maildir: %v", err)
	}
	srv := NewIMAPServer(cfg, quotaBridge{}, md, nil).
		WithQuota(func(string) int64 { return quota })
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	return srv, md
}

// appendLiteral builds an APPEND command carrying a message of n bytes.
func appendLiteral(tag, folder string, n int) []string {
	body := strings.Repeat("x", n)
	return []string{
		fmt.Sprintf("%s APPEND %s {%d+}", tag, folder, len(body)),
		body,
	}
}

func TestAPPENDCannotPushAMailboxPastItsQuota(t *testing.T) {
	t.Parallel()
	const quota = 64 << 10
	srv, md := quotaServer(t, quota)

	// Fill most of the quota, then try to go well past it in one upload.
	session := []string{"a LOGIN bob pw"}
	session = append(session, appendLiteral("b", "INBOX", 48<<10)...)
	session = append(session, appendLiteral("c", "INBOX", 48<<10)...)
	session = append(session, "d LOGOUT")
	resp := imapConverse(t, srv.Addr(), session...)

	if !strings.Contains(resp, "c NO") {
		t.Errorf("the second APPEND was not refused.\n\nServer said:\n%s", resp)
	}
	if used := md.AccountSize("example.com", "bob"); used > quota {
		t.Errorf("the mailbox holds %d bytes against a %d byte quota.\n\n"+
			"APPEND is how every IMAP client uploads mail, and it is one line in a "+
			"script. The disk it fills is the one SQLite is on.", used, quota)
	}
}

// COPY duplicates bytes that are already stored, so it grows the mailbox just
// as APPEND does — and a client can repeat it without ever sending a message.
func TestCOPYCannotPushAMailboxPastItsQuota(t *testing.T) {
	t.Parallel()
	const quota = 64 << 10
	srv, md := quotaServer(t, quota)

	if _, err := md.Deliver("example.com", "bob", []byte("Subject: seed\r\n\r\n"+strings.Repeat("y", 40<<10))); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp := imapConverse(t, srv.Addr(),
		"a LOGIN bob pw",
		"b SELECT INBOX",
		"c COPY 1 Archive",
		"d COPY 1 Sent",
		"e LOGOUT")

	if used := md.AccountSize("example.com", "bob"); used > quota {
		t.Errorf("repeated COPY grew the mailbox to %d bytes against a %d byte quota.\n\n"+
			"Server said:\n%s", used, quota, resp)
	}
}

// THE CONTROL that matters most, because getting it wrong locks somebody out of
// their own mailbox with no way back.
//
// MOVE is net-neutral and it is the escape hatch: a person over quota clears
// space by moving mail to Trash. A quota check that refuses MOVE would leave
// them full, unable to receive, and unable to do the one thing that fixes it.
func TestMOVEStillWorksWhenTheMailboxIsFull(t *testing.T) {
	t.Parallel()
	const quota = 32 << 10
	srv, md := quotaServer(t, quota)

	// Already over quota before the session starts.
	for i := 0; i < 3; i++ {
		if _, err := md.Deliver("example.com", "bob",
			[]byte(fmt.Sprintf("Subject: m%d\r\n\r\n%s", i, strings.Repeat("z", 20<<10)))); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	before, _ := md.ListFolder("example.com", "bob", "Inbox")
	if len(before) != 3 {
		t.Fatalf("fixture: inbox has %d messages, want 3", len(before))
	}

	resp := imapConverse(t, srv.Addr(),
		"a LOGIN bob pw",
		"b SELECT INBOX",
		"c MOVE 1 Trash",
		"d LOGOUT")

	if !strings.Contains(resp, "c OK") {
		t.Errorf("MOVE was refused on a full mailbox.\n\n"+
			"Moving mail to Trash is how a person gets back under quota. Refusing it "+
			"leaves them full, unable to receive, and with no way out.\n\nServer said:\n%s", resp)
	}
	after, _ := md.ListFolder("example.com", "bob", "Inbox")
	if len(after) != 2 {
		t.Errorf("inbox holds %d messages after MOVE, want 2 — the move did not happen", len(after))
	}
}

// THE OTHER CONTROL. An install with no quota set (0 = unlimited, the default)
// must behave exactly as it always has.
func TestWithNoQuotaSetAppendIsUnbounded(t *testing.T) {
	t.Parallel()
	srv, md := quotaServer(t, 0)

	session := []string{"a LOGIN bob pw"}
	for _, tag := range []string{"b", "c", "d"} {
		session = append(session, appendLiteral(tag, "INBOX", 32<<10)...)
	}
	session = append(session, "e LOGOUT")
	resp := imapConverse(t, srv.Addr(), session...)

	for _, tag := range []string{"b OK", "c OK", "d OK"} {
		if !strings.Contains(resp, tag) {
			t.Errorf("APPEND %q was refused on a mailbox with no quota.\n\n"+
				"Unlimited is the default, so this is every install that never set one.\n\n%s",
				tag, resp)
		}
	}
	if msgs, _ := md.ListFolder("example.com", "bob", "Inbox"); len(msgs) != 3 {
		t.Errorf("inbox holds %d messages, want 3", len(msgs))
	}
}

// The wiring, tested separately from the guard.
//
// Every test above constructs the IMAP server itself and calls WithQuota, so all
// of them pass with the engine never attaching it — deleting `.WithQuota(...)`
// from engine.go survived the entire package. That is the third time in this
// audit that a guard was covered and its wiring was not, so it gets its own
// test: a real Engine, started the way the product starts it, with a real
// account carrying a real quota.
func TestTheEngineWiresTheQuotaIntoItsIMAPListeners(t *testing.T) {
	t.Parallel()
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

	if e.imapd == nil {
		t.Fatal("the engine started no IMAP listener; this test would prove nothing")
	}
	if e.imapd.quotaFor == nil {
		t.Fatal("the engine built its IMAP listener without a quota.\n\n" +
			"APPEND and COPY then accept unlimited bytes on every real install, " +
			"however carefully the guard itself is written — the guard is only ever " +
			"reached through this wiring.")
	}
	// And the wired function is the real one, not a stub that always says
	// unlimited: it must report what the account store holds.
	if err := e.accounts.Create(context.Background(), "quotaed@example.com", "hash", "Q", "mailbox"); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := e.accounts.SetQuota(context.Background(), "quotaed@example.com", 4096); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	if got := e.imapd.quotaFor("quotaed@example.com"); got != 4096 {
		t.Errorf("the wired quota function reported %d for a mailbox limited to 4096.\n\n"+
			"A function that always answers 'unlimited' satisfies the nil check above "+
			"and enforces nothing.", got)
	}
}
