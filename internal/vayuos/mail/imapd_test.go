package mail

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// stubBridge implements Bridge for IMAP auth tests.
type stubBridge struct{}

func (stubBridge) AuthUser(username, password string) (bool, error) {
	return username == "bob" && password == "pw", nil
}
func (stubBridge) GetUserByEmail(string) (*MailUser, error)          { return nil, nil }
func (stubBridge) IsLocalRecipient(string) bool                      { return false }
func (stubBridge) SendTransactional(*TransactionalMessage) error     { return nil }
func (stubBridge) EncryptForRecipient([]byte, string) ([]byte, bool) { return nil, false }
func (stubBridge) SignAs([]byte, string) ([]byte, bool)              { return nil, false }

func TestIMAPLoginSelectFetch(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.IMAPListen = "127.0.0.1:0"

	md := NewMaildir(t.TempDir())
	raw := "From: alice@partner.test\r\nSubject: Sovereign Hello\r\n\r\nThe body.\r\n"
	if _, err := md.Deliver("example.com", "bob", []byte(raw)); err != nil {
		t.Fatalf("seed deliver: %v", err)
	}

	// PGP-on-read hook: prove the transform is applied to served bodies.
	srv := NewIMAPServer(cfg, stubBridge{}, md, func(account string, b []byte) []byte {
		if account != "bob@example.com" {
			t.Errorf("unexpected account for decrypt hook: %q", account)
		}
		return b // identity (no PGP in this test)
	})
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop(context.Background())

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	send := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	send("a LOGIN bob pw")
	send("b SELECT INBOX")
	send("c FETCH 1 (FLAGS RFC822.SIZE BODY[])")
	send("d LOGOUT")

	// Drain the whole conversation.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	all, _ := io.ReadAll(conn)
	resp := string(all)

	for _, want := range []string{
		"* OK [CAPABILITY",
		"a OK LOGIN completed",
		"1 EXISTS",
		"b OK [READ-WRITE] SELECT completed",
		"Sovereign Hello", // body literal was streamed
		"c OK FETCH completed",
		"d OK LOGOUT completed",
	} {
		if !strings.Contains(resp, want) {
			t.Errorf("response missing %q\n---\n%s", want, resp)
		}
	}
}

func TestIMAPRejectsBadLogin(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.IMAPListen = "127.0.0.1:0"
	srv := NewIMAPServer(cfg, stubBridge{}, NewMaildir(t.TempDir()), nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop(context.Background())

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = conn.Write([]byte("a LOGIN bob wrongpass\r\nb LOGOUT\r\n"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	all, _ := io.ReadAll(conn)
	if !strings.Contains(string(all), "a NO") {
		t.Fatalf("bad login should be rejected: %s", all)
	}
}
