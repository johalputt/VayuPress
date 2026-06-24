package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func dialLines(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn, bufio.NewReader(conn)
}

func expectPrefix(t *testing.T, br *bufio.Reader, prefix string) string {
	t.Helper()
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read (want %q): %v", prefix, err)
	}
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("got %q, want prefix %q", strings.TrimSpace(line), prefix)
	}
	return line
}

func TestSMTPReceiveDelivers(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"
	cfg.Hostname = "mail.example.com"
	cfg.SMTPListen = "127.0.0.1:0"

	var mu sync.Mutex
	var gotFrom string
	var gotRcpts []string
	var gotRaw []byte
	srv := NewSMTPServer(cfg, func(from string, rcpts []string, raw []byte) error {
		mu.Lock()
		defer mu.Unlock()
		gotFrom, gotRcpts, gotRaw = from, rcpts, raw
		return nil
	})
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop(context.Background())

	conn, br := dialLines(t, srv.Addr())
	defer conn.Close()
	send := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	expectPrefix(t, br, "220")
	send("EHLO client.test")
	// Read EHLO multiline until a line without '-' after code.
	for {
		l := expectPrefix(t, br, "250")
		if len(l) >= 4 && l[3] == ' ' {
			break
		}
	}
	send("MAIL FROM:<alice@partner.test>")
	expectPrefix(t, br, "250")
	// Relay must be denied for a non-local recipient.
	send("RCPT TO:<stranger@elsewhere.org>")
	expectPrefix(t, br, "550")
	send("RCPT TO:<bob@example.com>")
	expectPrefix(t, br, "250")
	send("DATA")
	expectPrefix(t, br, "354")
	send("Subject: Hi")
	send("")
	send("Body line with leading dot stuffing:")
	send("..stuffed")
	send(".")
	expectPrefix(t, br, "250")
	send("QUIT")
	expectPrefix(t, br, "221")

	mu.Lock()
	defer mu.Unlock()
	if gotFrom != "alice@partner.test" {
		t.Errorf("from = %q", gotFrom)
	}
	if len(gotRcpts) != 1 || gotRcpts[0] != "bob@example.com" {
		t.Errorf("rcpts = %v", gotRcpts)
	}
	if !strings.Contains(string(gotRaw), "Subject: Hi") {
		t.Errorf("body missing subject: %q", gotRaw)
	}
	if !strings.Contains(string(gotRaw), "\n.stuffed") && !strings.Contains(string(gotRaw), ".stuffed") {
		t.Errorf("dot-unstuffing failed: %q", gotRaw)
	}
	if strings.Contains(string(gotRaw), "..stuffed") {
		t.Errorf("dot-stuffing not undone: %q", gotRaw)
	}
}
