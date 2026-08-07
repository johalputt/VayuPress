// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// SECTION 2 AUDIT FINDING — unauthenticated memory exhaustion on the mail
// listeners, in the attacker's voice:
//
//	I do not need an account. I do not need to guess a password. I open a TCP
//	connection to 143 or 110 and send bytes forever, never once sending a
//	newline.
//
//	bufio.Reader.ReadString('\n') has no ceiling. It keeps growing its buffer
//	until it finds that byte, and I am never going to send it. Every megabyte
//	I write is a megabyte you allocate, and I can open as many connections as
//	your accept loop will take.
//
//	You are ONE process. The blog, the website, the admin console, SMTP
//	receive, submission and the database writer are in it with you. When the
//	allocator gives up, all of it goes down together, and I never authenticated.
//
// Both loops that read this way are reached BEFORE any credential is checked:
// the IMAP command loop and its AUTHENTICATE continuation, and the POP3 command
// loop. Submission is not affected — smtpd wraps its connection in an
// io.LimitReader, which is the same idea applied one level up.
//
// This is the half connlimit.go assumed was already in place. That file caps
// concurrency (256 global, 16 per source) and describes a connection's cost as
// bounded — "each accepted connection holds a bufio.Reader sized against
// MaxMessageBytes" — which was true of smtpd and of neither listener here. A
// bounded count times an unbounded cost is unbounded, so the two controls only
// compose once a line has a ceiling of its own.

// floodOneEndlessLine opens a connection, reads the greeting, then writes
// `budget` bytes containing no newline at all. It reports how many bytes the
// server accepted before the connection died.
//
// A server that bounds its line length stops reading and closes, so the write
// fails partway once the socket buffers fill. A server that does not will take
// every byte offered, which is the whole finding.
func floodOneEndlessLine(t *testing.T, addr string, budget int) int {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readLineRaw(t, conn) // greeting

	// A real command prefix, so this is an over-long COMMAND rather than
	// something a parser could reject as garbage before the length matters.
	if _, err := conn.Write([]byte("a1 LOGIN ")); err != nil {
		t.Fatalf("write prefix: %v", err)
	}

	chunk := []byte(strings.Repeat("A", 32<<10))
	written := 0
	for written < budget {
		// Generous, but finite: without it a bounded server that simply stops
		// reading (rather than closing) would hang this test instead of failing it.
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		n, err := conn.Write(chunk)
		written += n
		if err != nil {
			break
		}
	}
	return written
}

// acceptedCeiling is the most a single endless line may cost before the server
// gives up on it. Well above the real bound (64 KiB) because the kernel's send
// and receive buffers absorb a few hundred KiB on their own, and well below
// what an unbounded reader would swallow.
const acceptedCeiling = 4 << 20

// floodBudget is what the attacker offers. An unbounded server takes all of it.
const floodBudget = 24 << 20

func TestIMAPRefusesAnEndlessUnauthenticatedLine(t *testing.T) {
	t.Parallel()
	srv := NewIMAPServer(testEngineConfig(), stubBridge{}, NewMaildir(t.TempDir()), nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	if got := floodOneEndlessLine(t, srv.Addr(), floodBudget); got > acceptedCeiling {
		t.Errorf("IMAP accepted %d bytes of a single line with no newline in it (offered %d).\n\n"+
			"Every one of those bytes is heap in a process that is also the website, the "+
			"admin console, SMTP and the database writer. No credential was presented.",
			got, floodBudget)
	}
}

func TestPOP3RefusesAnEndlessUnauthenticatedLine(t *testing.T) {
	t.Parallel()
	srv := NewPOP3Server(testEngineConfig(), stubBridge{}, NewMaildir(t.TempDir()), nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	if got := floodOneEndlessLine(t, srv.Addr(), floodBudget); got > acceptedCeiling {
		t.Errorf("POP3 accepted %d bytes of a single line with no newline in it (offered %d).\n\n"+
			"Pre-authentication, on a process that serves everything else too.",
			got, floodBudget)
	}
}

// The command loop is not the only unauthenticated read. AUTHENTICATE PLAIN
// answers "+" and then reads a continuation line — still before any credential
// has been checked, and reached by sending eleven bytes.
//
// This test exists because unbounding that one read on its own survived the
// entire suite. A guard nothing exercises is a guard that comes back out during
// the next refactor, and this one sits on the pre-auth path.
func TestIMAPRefusesAnEndlessAuthenticateContinuation(t *testing.T) {
	t.Parallel()
	srv := NewIMAPServer(testEngineConfig(), stubBridge{}, NewMaildir(t.TempDir()), nil)
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

	if _, err := conn.Write([]byte("a1 AUTHENTICATE PLAIN\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	readLineRaw(t, conn) // the "+" continuation prompt

	chunk := []byte(strings.Repeat("A", 32<<10))
	written := 0
	for written < floodBudget {
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		n, err := conn.Write(chunk)
		written += n
		if err != nil {
			break
		}
	}
	if written > acceptedCeiling {
		t.Errorf("the AUTHENTICATE continuation accepted %d bytes on one line (offered %d).\n\n"+
			"Eleven bytes of command reach this read, and it is still pre-authentication.",
			written, floodBudget)
	}
}

// THE CONTROL. A bound that costs a real client its session is an outage wearing
// a security label. Ordinary commands must keep working, and a command that is
// merely LONG — a UID set naming a few hundred messages is routine on a big
// mailbox — must be answered rather than dropped.
func TestOrdinaryAndLongCommandsStillWork(t *testing.T) {
	t.Parallel()
	srv := NewIMAPServer(testEngineConfig(), stubBridge{}, NewMaildir(t.TempDir()), nil)
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { srv.Stop(context.Background()) })

	conn, err := net.DialTimeout("tcp", srv.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	readLineRaw(t, conn)

	if _, err := conn.Write([]byte("a1 CAPABILITY\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := readLineRaw(t, conn); !strings.Contains(got, "CAPABILITY") {
		t.Fatalf("a plain CAPABILITY got %q", got)
	}
	readLineRaw(t, conn) // the tagged OK

	// 8 KiB of UID set: long, entirely legitimate, and comfortably under the bound.
	long := "a2 UID FETCH " + strings.TrimSuffix(strings.Repeat("1,", 4<<10), ",") + " (FLAGS)\r\n"
	if _, err := conn.Write([]byte(long)); err != nil {
		t.Fatalf("write long command: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	got := readLineRaw(t, conn)
	if !strings.HasPrefix(got, "a2 ") {
		t.Errorf("a long but legitimate command was answered with %q, not a reply to its tag.\n\n"+
			"A client with a large mailbox sends command lines like this every sync. "+
			"Cutting the session is a worse outcome than the flood the bound exists to stop.", got)
	}
}
