// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

// lockedBridge answers every sign-in as the console does for a locked
// account.
type lockedBridge struct{ stubBridge }

func (lockedBridge) AuthUser(string, string) (bool, error) { return false, ErrAccountLocked }

// A locked account is told to try again later in each protocol's words, not
// that its password is wrong: a mail app that hears "invalid credentials"
// asks its owner for a new password, which is the wrong thing to do.
func TestALockedAccountIsToldToTryLater(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Domain = "example.com"

	cfg.IMAPListen = "127.0.0.1:0"
	imap := NewIMAPServer(cfg, lockedBridge{}, NewMaildir(t.TempDir()), nil)
	if err := imap.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { imap.Stop(context.Background()) })
	resp := converse(t, imap.Addr(), "a LOGIN bob pw", "b AUTHENTICATE PLAIN "+base64.StdEncoding.EncodeToString([]byte("\x00bob\x00pw")), "c LOGOUT")
	mustContain(t, resp, "a NO [UNAVAILABLE]", "b NO [UNAVAILABLE]")
	if strings.Contains(resp, "AUTHENTICATIONFAILED") {
		t.Errorf("IMAP called a locked account's sign-in a wrong password:\n%s", resp)
	}

	cfg.POP3Listen = "127.0.0.1:0"
	pop := NewPOP3Server(cfg, lockedBridge{}, NewMaildir(t.TempDir()), nil)
	if err := pop.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pop.Stop(context.Background()) })
	resp = converse(t, pop.Addr(), "USER bob", "PASS pw", "QUIT")
	mustContain(t, resp, "-ERR [SYS/TEMP]")

	smtp := NewSubmissionServer(cfg, nil, lockedBridge{}.AuthUser, func(string, []string, []byte) error { return nil })
	ok, _, locked := smtp.runAuth(bufio.NewReader(strings.NewReader("")), func(string) {},
		"PLAIN "+base64.StdEncoding.EncodeToString([]byte("\x00bob\x00pw")))
	if ok || !locked {
		t.Errorf("SMTP AUTH of a locked account: ok=%v locked=%v, want a refusal read as the lockout", ok, locked)
	}
}
