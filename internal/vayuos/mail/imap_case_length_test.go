// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// A LIST argument holding a character whose upper case is longer ("ɐ" is
// two bytes, "Ɐ" three) made the RETURN group's index fall past the end of
// the argument, and the slice panicked: one command from any mailbox holder
// took the whole process down. It is now read, the session answers, and
// the next command on it is served.
func TestAListArgumentWhoseCaseChangesLengthIsAnswered(t *testing.T) {
	t.Parallel()
	srv, _ := newTestIMAP(t)
	// Ten "ɐ" put the old index ten bytes later, past the end of the short
	// tail after RETURN.
	arg := `"" "` + strings.Repeat("ɐ", 10) + `" RETURN x`
	resp := converse(t, srv.Addr(), "a LOGIN bob pw", "b LIST "+arg, `c LIST "" "*"`, "d LOGOUT")
	mustContain(t, resp, "b OK LIST completed", `"INBOX"`, "c OK LIST completed")
}

// The RETURN group is found in any case, and what precedes it is read as
// the reference and pattern, whatever bytes it holds.
func TestTheReturnGroupIsDroppedInAnyCase(t *testing.T) {
	for arg, pattern := range map[string]string{
		`"" "*" RETURN (SPECIAL-USE)`: "*",
		`"" "*" return (children)`:    "*",
		`"" "%" ReTuRn ()`:            "%",
		"\"\" \"\xff\xfe\" RETURN x":  "\xff\xfe",
	} {
		if _, got := parseListArgs(arg); got != pattern {
			t.Errorf("parseListArgs(%q) pattern %q, want %q", arg, got, pattern)
		}
	}
}

// A panic in one session ends that session, closing its connection, and
// leaves the server running.
func TestAPanickedSessionIsEndedAlone(t *testing.T) {
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer endPanickedSession("imap", server)
		panic("a handler fault")
	}()
	<-done
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("reading the panicked session's connection: %v, want it closed (EOF)", err)
	}
}

// Both mail servers end a panicked session on the connection's own
// goroutine: the line is read from the servers' source, since no command
// is left that panics to show it.
func TestEveryMailSessionEndsItsOwnPanic(t *testing.T) {
	for _, f := range []string{"imapd.go", "smtpd.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "defer endPanickedSession(") {
			t.Errorf("%s: a connection's goroutine does not end its own panic", f)
		}
	}
}
