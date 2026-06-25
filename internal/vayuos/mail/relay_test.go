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

// fakeRelay is a minimal plaintext SMTP sink that advertises AUTH PLAIN and
// captures the envelope + DATA of a single delivered message. It lets us verify
// the relay DeliverFunc end-to-end without a real provider.
type fakeRelay struct {
	addr     string
	mu       sync.Mutex
	authSeen bool
	mailFrom string
	rcpts    []string
	data     string
}

func startFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fr := &fakeRelay{addr: ln.Addr().String()}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer ln.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		br := bufio.NewReader(conn)
		w := func(s string) { _, _ = conn.Write([]byte(s)) }
		w("220 fake ESMTP\r\n")
		inData := false
		var body strings.Builder
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					fr.mu.Lock()
					fr.data = body.String()
					fr.mu.Unlock()
					w("250 2.0.0 queued\r\n")
					continue
				}
				body.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				w("250-fake greets you\r\n250 AUTH PLAIN\r\n")
			case strings.HasPrefix(cmd, "AUTH PLAIN"):
				fr.mu.Lock()
				fr.authSeen = true
				fr.mu.Unlock()
				w("235 2.7.0 accepted\r\n")
			case strings.HasPrefix(cmd, "MAIL FROM"):
				fr.mu.Lock()
				fr.mailFrom = strings.TrimSpace(line[strings.IndexByte(line, ':')+1:])
				fr.mu.Unlock()
				w("250 2.1.0 ok\r\n")
			case strings.HasPrefix(cmd, "RCPT TO"):
				fr.mu.Lock()
				fr.rcpts = append(fr.rcpts, strings.TrimSpace(line[strings.IndexByte(line, ':')+1:]))
				fr.mu.Unlock()
				w("250 2.1.5 ok\r\n")
			case cmd == "DATA":
				inData = true
				w("354 end with <CRLF>.<CRLF>\r\n")
			case cmd == "QUIT":
				w("221 2.0.0 bye\r\n")
				return
			default:
				w("250 2.0.0 ok\r\n")
			}
		}
	}()
	return fr
}

func TestRelayDelivererSendsThroughSmarthost(t *testing.T) {
	t.Parallel()
	fr := startFakeRelay(t)
	host, portStr, _ := net.SplitHostPort(fr.addr)

	cfg := DefaultConfig()
	cfg.RelayHost = host // "127.0.0.1" — net/smtp permits PLAIN auth to localhost without TLS
	cfg.RelayPort = atoiOrZero(portStr)
	cfg.RelayUsername = "apikey"
	cfg.RelayPassword = "s3cret"
	cfg.RelayRequireTLS = false // plaintext sink in-test

	if !cfg.RelayEnabled() {
		t.Fatal("RelayEnabled should be true when RelayHost is set")
	}

	deliver := NewRelayDeliverer(cfg, "mail.example.com", 5*time.Second)
	raw := []byte("From: a@example.com\r\nTo: b@dest.net\r\nSubject: hi\r\n\r\nbody\r\n")
	if err := deliver(context.Background(), "a@example.com", []string{"b@dest.net"}, raw); err != nil {
		t.Fatalf("relay deliver: %v", err)
	}

	fr.mu.Lock()
	defer fr.mu.Unlock()
	if !fr.authSeen {
		t.Error("relay did not authenticate")
	}
	if !strings.Contains(fr.mailFrom, "a@example.com") {
		t.Errorf("MAIL FROM = %q", fr.mailFrom)
	}
	if len(fr.rcpts) != 1 || !strings.Contains(fr.rcpts[0], "b@dest.net") {
		t.Errorf("RCPT = %v", fr.rcpts)
	}
	if !strings.Contains(fr.data, "Subject: hi") {
		t.Errorf("DATA missing message: %q", fr.data)
	}
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
