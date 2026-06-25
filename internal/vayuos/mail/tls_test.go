package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Hostname = "mail.test"
	tc, err := loadTLSConfig(cfg)
	if err != nil {
		t.Fatalf("loadTLSConfig: %v", err)
	}
	return tc
}

func testEngineConfig() Config {
	c := DefaultConfig()
	c.Hostname = "mail.test"
	c.Domain = "test"
	c.MaxMessageBytes = 1 << 20
	c.SMTPListen = "127.0.0.1:0"
	c.SubmissionListen = "127.0.0.1:0"
	c.IMAPListen = "127.0.0.1:0"
	c.IMAPSListen = "127.0.0.1:0"
	return c
}

// readUntilFinal reads SMTP reply lines until the final one ("NNN <space>").
func readUntilFinal(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		b.WriteString(line)
		if len(line) >= 4 && line[3] == ' ' {
			return b.String()
		}
	}
}

func TestLoadTLSConfigSelfSigned(t *testing.T) {
	t.Parallel()
	tc := testTLSConfig(t)
	if len(tc.Certificates) != 1 {
		t.Fatalf("expected one certificate")
	}
}

// SMTP must advertise STARTTLS and complete a TLS handshake after the command.
func TestSMTPStartTLS(t *testing.T) {
	t.Parallel()
	cfg := testEngineConfig()
	srv := NewSMTPServer(cfg, func(string, []string, []byte) error { return nil }).WithTLS(testTLSConfig(t))
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	readUntilFinal(t, br) // 220 greeting
	conn.Write([]byte("EHLO client\r\n"))
	ehlo := readUntilFinal(t, br)
	if !strings.Contains(ehlo, "STARTTLS") {
		t.Fatalf("EHLO did not advertise STARTTLS: %q", ehlo)
	}
	conn.Write([]byte("STARTTLS\r\n"))
	if resp := readUntilFinal(t, br); !strings.HasPrefix(resp, "220") {
		t.Fatalf("STARTTLS not accepted: %q", resp)
	}
	tconn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "mail.test"})
	if err := tconn.Handshake(); err != nil {
		t.Fatalf("TLS handshake: %v", err)
	}
	// A command over the encrypted channel must still work.
	tconn.Write([]byte("EHLO client\r\n"))
	if resp := readUntilFinal(t, bufio.NewReader(tconn)); !strings.Contains(resp, "250") {
		t.Fatalf("post-TLS EHLO failed: %q", resp)
	}
}

// The submission server must require STARTTLS+AUTH, then relay an authenticated
// message to the relay handler.
func TestSubmissionAuthAndRelay(t *testing.T) {
	t.Parallel()
	cfg := testEngineConfig()
	type captured struct {
		from  string
		rcpts []string
	}
	got := make(chan captured, 1)
	auth := func(u, p string) (bool, error) { return u == "bob" && p == "pw", nil }
	relay := func(from string, rcpts []string, _ []byte) error {
		got <- captured{from, rcpts}
		return nil
	}
	srv := NewSubmissionServer(cfg, testTLSConfig(t), auth, relay)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	readUntilFinal(t, br) // greeting
	conn.Write([]byte("EHLO c\r\n"))
	readUntilFinal(t, br)
	conn.Write([]byte("STARTTLS\r\n"))
	readUntilFinal(t, br)
	tconn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "mail.test"})
	if err := tconn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	tbr := bufio.NewReader(tconn)
	tconn.Write([]byte("EHLO c\r\n"))
	if e := readUntilFinal(t, tbr); !strings.Contains(e, "AUTH") {
		t.Fatalf("submission did not advertise AUTH after STARTTLS: %q", e)
	}
	cred := base64.StdEncoding.EncodeToString([]byte("\x00bob\x00pw"))
	tconn.Write([]byte("AUTH PLAIN " + cred + "\r\n"))
	if r := readUntilFinal(t, tbr); !strings.HasPrefix(r, "235") {
		t.Fatalf("AUTH failed: %q", r)
	}
	tconn.Write([]byte("MAIL FROM:<bob@test>\r\n"))
	readUntilFinal(t, tbr)
	tconn.Write([]byte("RCPT TO:<someone@elsewhere.example>\r\n"))
	if r := readUntilFinal(t, tbr); !strings.HasPrefix(r, "250") {
		t.Fatalf("authenticated relay RCPT rejected: %q", r)
	}
	tconn.Write([]byte("DATA\r\n"))
	readUntilFinal(t, tbr) // 354
	tconn.Write([]byte("Subject: hi\r\n\r\nbody\r\n.\r\n"))
	if r := readUntilFinal(t, tbr); !strings.HasPrefix(r, "250") {
		t.Fatalf("DATA not accepted: %q", r)
	}
	select {
	case c := <-got:
		if c.from != "bob@test" || len(c.rcpts) != 1 || c.rcpts[0] != "someone@elsewhere.example" {
			t.Fatalf("relay captured wrong envelope: %+v", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay handler was not invoked")
	}
}

// Submission must reject MAIL before authentication.
func TestSubmissionRequiresAuth(t *testing.T) {
	t.Parallel()
	cfg := testEngineConfig()
	srv := NewSubmissionServer(cfg, testTLSConfig(t),
		func(string, string) (bool, error) { return false, nil },
		func(string, []string, []byte) error { return nil })
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, _ := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	defer conn.Close()
	br := bufio.NewReader(conn)
	readUntilFinal(t, br)
	conn.Write([]byte("MAIL FROM:<x@y>\r\n"))
	if r := readUntilFinal(t, br); !strings.HasPrefix(r, "530") {
		t.Fatalf("expected 530 auth-required, got %q", r)
	}
}

// IMAPS must accept an implicit-TLS connection and answer CAPABILITY.
func TestIMAPSImplicitTLS(t *testing.T) {
	t.Parallel()
	cfg := testEngineConfig()
	md := NewMaildir(t.TempDir())
	srv := NewIMAPServer(cfg, stubBridge{}, md, nil).WithImplicitTLS(testTLSConfig(t), "127.0.0.1:0")
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	tconn, err := tls.Dial("tcp", srv.Addr(), &tls.Config{InsecureSkipVerify: true, ServerName: "mail.test"})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer tconn.Close()
	br := bufio.NewReader(tconn)
	if g, _ := br.ReadString('\n'); !strings.Contains(g, "* OK") {
		t.Fatalf("bad greeting: %q", g)
	}
	tconn.Write([]byte("a CAPABILITY\r\n"))
	if l, _ := br.ReadString('\n'); !strings.Contains(l, "IMAP4rev1") {
		t.Fatalf("bad capability: %q", l)
	}
}

// Plaintext IMAP must advertise STARTTLS and upgrade on command.
func TestIMAPStartTLS(t *testing.T) {
	t.Parallel()
	cfg := testEngineConfig()
	md := NewMaildir(t.TempDir())
	srv := NewIMAPServer(cfg, stubBridge{}, md, nil).WithTLS(testTLSConfig(t))
	srv.listenAddr = "127.0.0.1:0"
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, _ := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	defer conn.Close()
	br := bufio.NewReader(conn)
	if g, _ := br.ReadString('\n'); !strings.Contains(g, "STARTTLS") {
		t.Fatalf("greeting should advertise STARTTLS: %q", g)
	}
	conn.Write([]byte("a STARTTLS\r\n"))
	if l, _ := br.ReadString('\n'); !strings.Contains(l, "a OK") {
		t.Fatalf("STARTTLS not accepted: %q", l)
	}
	tconn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "mail.test"})
	if err := tconn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	tbr := bufio.NewReader(tconn)
	tconn.Write([]byte("b CAPABILITY\r\n"))
	if l, _ := tbr.ReadString('\n'); !strings.Contains(l, "IMAP4rev1") {
		t.Fatalf("post-TLS CAPABILITY failed: %q", l)
	}
}
