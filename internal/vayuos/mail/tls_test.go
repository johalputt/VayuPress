package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Hostname = "mail.test"
	p, err := buildTLSProvider(cfg)
	if err != nil {
		t.Fatalf("buildTLSProvider: %v", err)
	}
	return p.config
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

// buildTLSProvider must report the self-signed fallback as untrusted so the
// panel/logs can warn that mail clients will reject the connection.
func TestTLSProviderSelfSignedUntrusted(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Hostname = "mail.test"
	p, err := buildTLSProvider(cfg)
	if err != nil {
		t.Fatalf("buildTLSProvider: %v", err)
	}
	if p.mode != tlsModeSelfSigned {
		t.Fatalf("expected selfsigned mode, got %q", p.mode)
	}
	if p.trusted() {
		t.Fatal("self-signed certificate must not be reported as trusted")
	}
	if p.config == nil || len(p.config.Certificates) != 1 {
		t.Fatal("self-signed provider must carry exactly one certificate")
	}
}

// An operator-supplied keypair must be selected and reported as trusted.
func TestTLSProviderStaticTrusted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	writeTestKeypair(t, certFile, keyFile, "mail.test")

	cfg := DefaultConfig()
	cfg.Hostname = "mail.test"
	cfg.TLSCertFile = certFile
	cfg.TLSKeyFile = keyFile
	p, err := buildTLSProvider(cfg)
	if err != nil {
		t.Fatalf("buildTLSProvider: %v", err)
	}
	if p.mode != tlsModeStatic || !p.trusted() {
		t.Fatalf("expected trusted static mode, got %q trusted=%v", p.mode, p.trusted())
	}
}

// With ACME enabled and a valid hostname, the provider must be in ACME mode,
// expose an HTTP-01 challenge handler, and carry an autocert manager.
func TestTLSProviderACMEMode(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Hostname = "mail.test"
	cfg.StorageDir = t.TempDir()
	cfg.ACMEEnabled = true
	cfg.ACMEDirectoryURL = "https://acme.example.invalid/directory" // never contacted here
	p, err := buildTLSProvider(cfg)
	if err != nil {
		t.Fatalf("buildTLSProvider: %v", err)
	}
	if p.mode != tlsModeACME {
		t.Fatalf("expected acme mode, got %q", p.mode)
	}
	if !p.trusted() {
		t.Fatal("acme mode must be reported as trusted")
	}
	if p.manager == nil || p.httpHandler == nil {
		t.Fatal("acme provider must carry a manager and HTTP-01 handler")
	}
	if p.config == nil || p.config.GetCertificate == nil {
		t.Fatal("acme provider config must set GetCertificate")
	}
	// The wrapped GetCertificate must serve the self-signed fallback (rather
	// than error) for a normal client hello while issuance is unavailable, so
	// the listeners keep answering.
	cert, gerr := p.config.GetCertificate(&tls.ClientHelloInfo{ServerName: "mail.test"})
	if gerr != nil || cert == nil {
		t.Fatalf("expected self-signed fallback, got cert=%v err=%v", cert, gerr)
	}
}

// ACME enabled but no usable hostname must degrade to the self-signed fallback
// rather than fail mail startup.
func TestTLSProviderACMENoHostFallsBack(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Hostname = "" // no hostname to certify
	cfg.StorageDir = t.TempDir()
	cfg.ACMEEnabled = true
	p, err := buildTLSProvider(cfg)
	if err != nil {
		t.Fatalf("buildTLSProvider should not hard-fail: %v", err)
	}
	if p.mode != tlsModeSelfSigned {
		t.Fatalf("expected selfsigned fallback, got %q", p.mode)
	}
}

func writeTestKeypair(t *testing.T, certFile, keyFile, host string) {
	t.Helper()
	cert, err := selfSignedCert(host)
	if err != nil {
		t.Fatalf("selfSignedCert: %v", err)
	}
	cpem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyDER, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	kpem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certFile, cpem, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, kpem, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

// A static (operator-supplied) keypair must be served through GetCertificate so
// it can be hot-reloaded, and must transparently pick up a renewed certificate
// on disk without a restart (the certbot/Let's Encrypt renewal path).
func TestReloadingStaticCert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	writeTestKeypair(t, certFile, keyFile, "mail.test")

	cfg := DefaultConfig()
	cfg.Hostname = "mail.test"
	cfg.TLSCertFile = certFile
	cfg.TLSKeyFile = keyFile
	p, err := buildTLSProvider(cfg)
	if err != nil {
		t.Fatalf("buildTLSProvider: %v", err)
	}
	if p.config.GetCertificate == nil {
		t.Fatal("static provider must serve via GetCertificate for hot-reload")
	}
	first, err := p.config.GetCertificate(&tls.ClientHelloInfo{ServerName: "mail.test"})
	if err != nil || first == nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	rc, err := newReloadingCert(certFile, keyFile)
	if err != nil {
		t.Fatalf("newReloadingCert: %v", err)
	}
	before, _ := rc.getCertificate(nil)
	if before == nil || len(before.Certificate) == 0 {
		t.Fatal("expected an initial certificate")
	}
	beforeDER := string(before.Certificate[0])

	// Simulate a renewal: write a brand-new keypair (fresh key + cert) to the
	// same paths and force an immediate re-check by ageing lastCheck past the
	// throttle window.
	time.Sleep(10 * time.Millisecond)
	writeTestKeypair(t, certFile, keyFile, "mail.test")
	// Guarantee a detectably different modification time regardless of the
	// filesystem's mtime granularity.
	bump := time.Now().Add(5 * time.Second)
	_ = os.Chtimes(certFile, bump, bump)
	_ = os.Chtimes(keyFile, bump, bump)
	rc.mu.Lock()
	rc.lastCheck = time.Now().Add(-time.Hour)
	rc.mu.Unlock()

	after, err := rc.getCertificate(nil)
	if err != nil || after == nil || len(after.Certificate) == 0 {
		t.Fatalf("getCertificate after reload: %v", err)
	}
	// The reloaded certificate must differ from the original on disk, proving
	// the hot-reload fired (a fresh keypair was generated each write).
	if string(after.Certificate[0]) == beforeDER {
		t.Fatal("expected the certificate to be reloaded from disk after renewal")
	}
}
