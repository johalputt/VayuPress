// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/email"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

const lockedMailbox, lockedRight = "boss@example.com", "the-password-the-attacker-stole"

// signInDoor is one way into a mailbox: try sends one sign-in with password
// from the i-th address, and reports whether it was let in.
type signInDoor struct {
	name string
	try  func(t *testing.T, a *App, i int, password string) bool
}

func signInDoors() []signInDoor {
	return []signInDoor{
		{"the site's sign-in", func(t *testing.T, a *App, i int, pw string) bool {
			return postVayuMailLogin(t, a, "198.51.100."+strconv.Itoa(10+i), lockedMailbox, pw).Code == http.StatusOK
		}},
		{"the console's sign-in", func(t *testing.T, a *App, i int, pw string) bool {
			form := url.Values{"email": {lockedMailbox}, "password": {pw}}
			req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.RemoteAddr = "198.51.100." + strconv.Itoa(40+i) + ":5000"
			rec := httptest.NewRecorder()
			a.handleOSLoginSubmit(rec, req)
			return rec.Code == http.StatusSeeOther
		}},
		{"a mail app (IMAP, POP3, SMTP)", func(t *testing.T, a *App, i int, pw string) bool {
			ok, err := (&vayuMailBridge{app: a}).AuthUser(lockedMailbox, pw)
			if !ok && pw == lockedRight && !errors.Is(err, vmail.ErrAccountLocked) {
				t.Errorf("a mail app's refused sign-in did not say the account is locked (err %v)", err)
			}
			return ok
		}},
		{"VayuTalk", func(t *testing.T, a *App, i int, pw string) bool {
			return a.talkVerify(context.Background(), lockedMailbox, pw)
		}},
	}
}

// Each door counts its failures toward the account's one counter: five
// wrong passwords at any one door, from five addresses, lock the account.
func TestEachDoorCountsTowardTheAccount(t *testing.T) {
	for _, d := range signInDoors() {
		t.Run(d.name, func(t *testing.T) {
			a := credentialFloodApp(t)
			for i := 0; i < 5; i++ {
				d.try(t, a, i, "wrong-"+strconv.Itoa(i))
			}
			if locked, _ := auth.CheckAccountLockout(lockedMailbox); !locked {
				t.Errorf("five wrong passwords at %s from five addresses did not lock the account", d.name)
			}
		})
	}
}

// Each door refuses a locked account its right password, and a correct
// sign-in at a door that lets it in resets the counter.
func TestEachDoorRefusesALockedAccount(t *testing.T) {
	for _, d := range signInDoors() {
		t.Run(d.name, func(t *testing.T) {
			a := credentialFloodApp(t)
			// The mail servers and VayuTalk take the raw password only from a
			// mailbox that does not require approved devices; the device rule
			// is not what this test is about.
			if err := a.vayuMail.Accounts().SetRequireDeviceApproval(context.Background(), lockedMailbox, false); err != nil {
				t.Fatal(err)
			}
			if !d.try(t, a, 0, lockedRight) {
				t.Fatalf("the right password did not sign in at %s before any lockout", d.name)
			}
			for i := 0; i < 5; i++ {
				auth.RecordAccountFailure(lockedMailbox, "203.0.113."+strconv.Itoa(i))
			}
			if d.try(t, a, 1, lockedRight) {
				t.Errorf("%s let a locked account in", d.name)
			}
		})
	}
}

// A guessed recovery code counts with the passwords, and a locked account's
// recovery page says when to try again.
func TestARecoveryCodeGuessCountsTowardTheAccount(t *testing.T) {
	a := credentialFloodApp(t)
	post := func(ip string) *httptest.ResponseRecorder {
		form := url.Values{"email": {lockedMailbox}, "code": {"AAAA-BBBB"}, "password": {"a-new-password-1"}, "confirm": {"a-new-password-1"}}
		req := httptest.NewRequest(http.MethodPost, "/mail/recover/code", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip + ":5000"
		rec := httptest.NewRecorder()
		a.handleMailRecoverCode(rec, req)
		return rec
	}
	for i := 0; i < 3; i++ {
		post("192.0.2." + strconv.Itoa(i))
	}
	for i := 0; i < 2; i++ {
		postVayuMailLogin(t, a, "198.51.100."+strconv.Itoa(70+i), lockedMailbox, "wrong")
	}
	if locked, _ := auth.CheckAccountLockout(lockedMailbox); !locked {
		t.Fatal("three wrong recovery codes and two wrong passwords did not lock the account")
	}
	if body := post("192.0.2.99").Body.String(); !strings.Contains(body, "Too many failed sign-in attempts") {
		t.Errorf("a locked account's recovery page does not say when to try again:\n%s", body)
	}
}

// A lockout tells its owner, at the account's address, with when it ends
// and where the attempts came from; an address with no account here is
// never mailed, or a lockout would make the install mail strangers.
func TestALockedAccountsOwnerIsTold(t *testing.T) {
	a := credentialFloodApp(t)
	var mu sync.Mutex
	var sent []email.Message
	a.mailer = email.New(email.Config{})
	a.mailer.SetFallback(func(m email.Message) error {
		mu.Lock()
		sent = append(sent, m)
		mu.Unlock()
		return nil
	})
	until := time.Now().Add(15 * time.Minute)
	a.notifyAccountLocked("stranger@elsewhere.test", until, []string{"203.0.113.5"})
	a.notifyAccountLocked(lockedMailbox, until, []string{"203.0.113.5", "a mail app (IMAP, POP3 or SMTP)"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(sent)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // the stranger's notice, were it sent, has had its turn
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0].To != lockedMailbox {
		t.Fatalf("notices sent %+v, want one, to %s", sent, lockedMailbox)
	}
	for _, want := range []string{"paused until", "203.0.113.5", "a mail app (IMAP, POP3 or SMTP)", "still signed in"} {
		if !strings.Contains(sent[0].Text, want) {
			t.Errorf("the notice does not say %q:\n%s", want, sent[0].Text)
		}
	}
}
