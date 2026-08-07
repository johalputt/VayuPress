// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

// SECTION 2 AUDIT — STARTTLS command injection. Another CLEAN result, pinned as
// a test because the defence is one line and deleting it is silent.
//
// The attack, in the attacker's voice (CVE-2011-0411 and its many descendants):
//
//	I am on the network between you and your mail server. I cannot read your
//	TLS session, so I will get the server to run my commands INSIDE it.
//
//	I write ONE packet, before the handshake:
//
//	    STARTTLS\r\nVRFY injected\r\n
//
//	The server reads the first line, answers "220 ready", and negotiates TLS.
//	My second line is already sitting in its bufio.Reader's buffer. If the
//	server keeps that reader, it will read my command AFTER the handshake and
//	execute it as though you had typed it — authenticated as you, inside the
//	encrypted session you are trusting.
//
// The defence is to throw the buffered plaintext away by building a NEW reader
// over the TLS connection. All three listeners do it, and this test is here so
// all three keep doing it:
//
//	smtpd.go  setup(conn)                   — new bufio.Reader over tconn
//	imapd.go  setup(conn) + fresh session
//	pop3d.go  br = bufio.NewReader(conn)
//
// Reusing the pre-TLS reader is the natural way to write this code, which is
// exactly why it keeps being written that way.

// pipelineSTARTTLS writes the upgrade command and a smuggled follow-up command
// as a SINGLE write, so the second one lands in the reader's buffer before the
// handshake begins. Returns a TLS client over the same socket.
func pipelineSTARTTLS(t *testing.T, conn net.Conn, upgrade, smuggled string) (*tls.Conn, *bufio.Reader) {
	t.Helper()
	if _, err := conn.Write([]byte(upgrade + "\r\n" + smuggled + "\r\n")); err != nil {
		t.Fatalf("pipelined write: %v", err)
	}
	// Consume the server's "ready to negotiate" line from the PLAINTEXT socket
	// before handing it to crypto/tls, or the handshake reads that line as its
	// first record. Read it a byte at a time: a bufio.Reader here would happily
	// swallow the start of the server's handshake too, and then the client would
	// be the thing that broke the connection.
	readLineRaw(t, conn)
	tconn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, ServerName: "mail.test"})
	if err := tconn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	return tconn, bufio.NewReader(tconn)
}

// readLineRaw reads exactly one CRLF-terminated line straight off the socket,
// buffering nothing beyond it.
func readLineRaw(t *testing.T, conn net.Conn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	var out []byte
	one := make([]byte, 1)
	for {
		n, err := conn.Read(one)
		if err != nil || n == 0 {
			t.Fatalf("reading the pre-TLS ready line: %v (got %q)", err, out)
		}
		out = append(out, one[0])
		if one[0] == '\n' {
			return string(out)
		}
	}
}

// readLineWithin reads one line, or reports that nothing arrived. "Nothing
// arrived" is the expected outcome for the smuggled command, so it must not be
// a test failure by itself.
func readLineWithin(t *testing.T, br *bufio.Reader, conn net.Conn, d time.Duration) (string, bool) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(d))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	s, err := br.ReadString('\n')
	if err != nil && s == "" {
		return "", false
	}
	return s, true
}

func TestSMTPDiscardsPlaintextPipelinedAcrossSTARTTLS(t *testing.T) {
	t.Parallel()
	srv := NewSubmissionServer(testEngineConfig(), testTLSConfig(t),
		func(string, string) (bool, error) { return true, nil },
		func(string, []string, []byte) error { return nil })
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readLineRaw(t, conn) // greeting
	conn.Write([]byte("EHLO client\r\n"))
	for {
		l := readLineRaw(t, conn)
		if len(l) >= 4 && l[3] == ' ' { // final line of the multiline EHLO reply
			break
		}
	}

	// "220 Ready to start TLS" is consumed by the plaintext reader before the
	// handshake; VRFY is the smuggled command, chosen because its reply text
	// ("Cannot VRFY user") appears nowhere else in the protocol.
	tconn, tbr := pipelineSTARTTLS(t, conn, "STARTTLS", "VRFY smuggled")

	if got, ok := readLineWithin(t, tbr, tconn, 400*time.Millisecond); ok {
		t.Errorf("after the TLS handshake the server sent %q before we asked it anything.\n\n"+
			"That is the reply to a command smuggled in the same packet as STARTTLS, "+
			"executed inside the encrypted session. A network attacker who cannot read "+
			"the session can still make the server act on their commands in it.", got)
	}

	// And the session must still be usable — a fix that hangs up on the client is
	// not a fix.
	tconn.Write([]byte("NOOP\r\n"))
	got, ok := readLineWithin(t, tbr, tconn, 3*time.Second)
	if !ok || !strings.HasPrefix(got, "250") {
		t.Fatalf("post-STARTTLS NOOP got %q (ok=%v); the connection is unusable after the "+
			"upgrade", got, ok)
	}
}

func TestPOP3DiscardsPlaintextPipelinedAcrossSTLS(t *testing.T) {
	t.Parallel()
	srv := NewPOP3Server(testEngineConfig(), stubBridge{}, NewMaildir(t.TempDir()), nil).
		WithTLS(testTLSConfig(t))
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readLineRaw(t, conn) // greeting

	tconn, tbr := pipelineSTARTTLS(t, conn, "STLS", "USER smuggled")

	if got, ok := readLineWithin(t, tbr, tconn, 400*time.Millisecond); ok {
		t.Errorf("after the STLS handshake the server sent %q unprompted — the smuggled "+
			"USER command was executed inside the TLS session", got)
	}

	tconn.Write([]byte("CAPA\r\n"))
	if got, ok := readLineWithin(t, tbr, tconn, 3*time.Second); !ok || !strings.HasPrefix(got, "+OK") {
		t.Fatalf("post-STLS CAPA got %q (ok=%v); the connection is unusable after the upgrade", got, ok)
	}
}

func TestIMAPDiscardsPlaintextPipelinedAcrossSTARTTLS(t *testing.T) {
	t.Parallel()
	srv := NewIMAPServer(testEngineConfig(), stubBridge{}, NewMaildir(t.TempDir()), nil).
		WithTLS(testTLSConfig(t))
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readLineRaw(t, conn) // greeting

	// The smuggled command carries its own tag, so its reply is unmistakable.
	tconn, tbr := pipelineSTARTTLS(t, conn, "a1 STARTTLS", "smug NOOP")

	if got, ok := readLineWithin(t, tbr, tconn, 400*time.Millisecond); ok {
		if strings.Contains(got, "smug") {
			t.Errorf("the server answered the smuggled tag inside the TLS session: %q.\n\n"+
				"A command injected before the handshake was executed after it.", got)
		} else {
			t.Errorf("unexpected unprompted line after the handshake: %q", got)
		}
	}

	tconn.Write([]byte("a2 CAPABILITY\r\n"))
	found := false
	for i := 0; i < 4; i++ {
		got, ok := readLineWithin(t, tbr, tconn, 3*time.Second)
		if !ok {
			break
		}
		if strings.Contains(got, "smug") {
			t.Fatalf("the smuggled command's reply arrived late: %q", got)
		}
		if strings.HasPrefix(got, "a2 ") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no reply to our own CAPABILITY after the upgrade; the connection is unusable")
	}
}
