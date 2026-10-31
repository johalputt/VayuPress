// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/signin"
	"github.com/johalputt/vayupress/internal/totp"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// withSignins opens the record of attempts on a's database, as openSignins
// does with the install's key.
func withSignins(t *testing.T, a *App) *signin.Store {
	t.Helper()
	st, err := signin.Open(context.Background(), dbpkg.DB, []byte("a test fingerprint key, 32 bytes"))
	if err != nil {
		t.Fatal(err)
	}
	a.signin = st
	return st
}

// doorRecords is the door each of signInDoors records, and the address its
// i-th try comes from, masked.
var doorRecords = map[string]struct{ door, masked string }{
	"the site's sign-in":            {signin.DoorWeb, "198.51.x.x"},
	"the console's sign-in":         {signin.DoorConsole, "198.51.x.x"},
	"a mail app (IMAP, POP3, SMTP)": {signin.DoorIMAP, "203.0.x.x"},
	"VayuTalk":                      {signin.DoorTalk, "203.0.x.x"},
}

// Each door records a wrong password, with its door, its stage and its
// source's masked address.
func TestEachDoorRecordsAWrongPassword(t *testing.T) {
	for _, d := range signInDoors() {
		t.Run(d.name, func(t *testing.T) {
			a := credentialFloodApp(t)
			st := withSignins(t, a)
			d.try(t, a, 0, "wrong")
			if err := st.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			rows, err := st.Attempts(context.Background(), "", lockedMailbox, 10)
			want := doorRecords[d.name]
			if err != nil || len(rows) != 1 || rows[0].Door != want.door || rows[0].Stage != signin.StagePassword || rows[0].Masked != want.masked {
				t.Errorf("%s recorded %+v (%v); want one wrong password at %s from %s", d.name, rows, err, want.door, want.masked)
			}
		})
	}
}

// Each door refuses a blocked source its right password, before checking
// it, and lets in the same password from another address.
func TestEachDoorRefusesABlockedSource(t *testing.T) {
	for _, d := range signInDoors() {
		t.Run(d.name, func(t *testing.T) {
			a := credentialFloodApp(t)
			st := withSignins(t, a)
			ctx := context.Background()
			if err := a.vayuMail.Accounts().SetRequireDeviceApproval(ctx, lockedMailbox, false); err != nil {
				t.Fatal(err)
			}
			d.try(t, a, 2, "wrong")
			_ = st.Flush(ctx)
			rows, _ := st.Attempts(ctx, "", lockedMailbox, 1)
			if len(rows) != 1 {
				t.Fatalf("no attempt recorded at %s", d.name)
			}
			if _, err := st.BlockSource(ctx, rows[0].FP, false, "", time.Hour, "admin", "192.0.2.200"); err != nil {
				t.Fatal(err)
			}
			if d.try(t, a, 2, lockedRight) {
				t.Errorf("%s let a blocked source in with the right password", d.name)
			}
			if !d.try(t, a, 3, lockedRight) {
				t.Errorf("%s refused the right password from an address that is not blocked", d.name)
			}
		})
	}
}

// A right password with a wrong code is recorded as such at the site's
// sign-in and the console's, and raises the alarm.
func TestARightPasswordWithAWrongCodeIsRecordedAndRaisesTheAlarm(t *testing.T) {
	const pass = "owner-password-123"
	for _, c := range []struct {
		name, door string
		try        func(a *App)
	}{
		{"the site's sign-in", signin.DoorWeb, func(a *App) {
			postVayuMailLoginCode(t, a, "198.51.100.90", "owner@example.com", pass, "000000")
		}},
		{"the console's sign-in", signin.DoorConsole, func(a *App) {
			form := url.Values{"email": {"owner@example.com"}, "password": {pass}, "totp": {"000000"}}
			req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.RemoteAddr = "198.51.100.90:5000"
			a.handleOSLoginSubmit(httptest.NewRecorder(), req)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := credentialFloodApp(t)
			st := withSignins(t, a)
			alarms := make(chan signin.Alarm, 4)
			st.OnAlarm = func(al signin.Alarm) { alarms <- al }
			ctx := context.Background()
			u, err := a.userStore.Create(ctx, "owner@example.com", "Owner", pass, "admin")
			if err != nil {
				t.Fatal(err)
			}
			secret, _ := totp.GenerateSecret()
			_ = a.userStore.SetTOTPSecret(ctx, u.ID, secret)
			_ = a.userStore.EnableTOTP(ctx, u.ID)
			if code, _ := totp.GenerateAt(secret, time.Now()); code == "000000" {
				t.Skip("the current code is the wrong one this test sends")
			}
			c.try(a)
			_ = st.Flush(ctx)
			rows, _ := st.Attempts(ctx, "", "owner@example.com", 10)
			if len(rows) != 1 || rows[0].Stage != signin.StageCode || rows[0].Door != c.door {
				t.Errorf("recorded %+v; want one right password with a wrong code at %s", rows, c.door)
			}
			select {
			case al := <-alarms:
				if al.Account != "owner@example.com" || al.Masked != "198.51.x.x" {
					t.Errorf("alarm = %+v", al)
				}
			case <-time.After(2 * time.Second):
				t.Error("no alarm for a right password with a wrong code")
			}
		})
	}
}

// A wrong recovery code is recorded at its door, and a blocked source is
// refused the recovery page's sign-in. Its own address: the recovery budget
// per address is the package's, and another test spends lockedMailbox's.
func TestARecoveryCodeIsRecordedAndABlockedSourceRefused(t *testing.T) {
	a := credentialFloodApp(t)
	st := withSignins(t, a)
	ctx := context.Background()
	post := func(ip string) *httptest.ResponseRecorder {
		form := url.Values{"email": {"recover@example.com"}, "code": {"AAAA-BBBB"}, "password": {"a-new-password-1"}, "confirm": {"a-new-password-1"}}
		req := httptest.NewRequest(http.MethodPost, "/mail/recover/code", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip + ":5000"
		rec := httptest.NewRecorder()
		a.handleMailRecoverCode(rec, req)
		return rec
	}
	post("192.0.2.31")
	_ = st.Flush(ctx)
	rows, _ := st.Attempts(ctx, "", "recover@example.com", 10)
	if len(rows) != 1 || rows[0].Door != signin.DoorRecovery || rows[0].Stage != signin.StagePassword {
		t.Fatalf("recorded %+v; want one wrong recovery code", rows)
	}
	if _, err := st.BlockSource(ctx, rows[0].FP, false, "", time.Hour, "admin", "198.51.100.200"); err != nil {
		t.Fatal(err)
	}
	if rec := post("192.0.2.31"); !strings.Contains(rec.Body.String(), "Sign-in from here is not allowed") {
		t.Error("a blocked source was not refused the recovery code")
	}
}

// VayuTalk's connect hands its check the client's address, so its failures
// are recorded with their source and a block on it holds.
func TestVayuTalkConnectHandsOnTheClientsAddress(t *testing.T) {
	got := make(chan string, 1)
	a := &App{}
	e := vtalk.NewEngine(vtalk.Config{Enabled: true, Verify: func(ctx context.Context, _, _ string) bool {
		got <- signinIPFrom(ctx)
		return false
	}, PubKey: func(string) (string, string, error) { return "", "", vpgp.ErrNotFound }})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	a.vayuTalk = e
	req := httptest.NewRequest(http.MethodPost, "/api/v1/talk/connect", strings.NewReader(`{"email":"dana@example.com","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.77:4000"
	a.handleTalkConnect(httptest.NewRecorder(), req)
	select {
	case ip := <-got:
		if ip != "203.0.113.77" {
			t.Errorf("VayuTalk's check was handed %q, want the client's address", ip)
		}
	default:
		t.Fatal("VayuTalk's check was not called")
	}
}

// At the console, a mailbox's right password stopped by the second factor of
// the VayuOS account at its address is a right password with a wrong code.
func TestTheConsolesMailboxPasswordWithAWrongCodeIsRecordedAsSuch(t *testing.T) {
	a := credentialFloodApp(t)
	st := withSignins(t, a)
	ctx := context.Background()
	u, err := a.userStore.Create(ctx, lockedMailbox, "Boss", "the-console-password-1", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := totp.GenerateSecret()
	_ = a.userStore.SetTOTPSecret(ctx, u.ID, secret)
	_ = a.userStore.EnableTOTP(ctx, u.ID)
	if code, _ := totp.GenerateAt(secret, time.Now()); code == "000000" {
		t.Skip("the current code is the wrong one this test sends")
	}
	form := url.Values{"email": {lockedMailbox}, "password": {lockedRight}, "totp": {"000000"}}
	req := httptest.NewRequest(http.MethodPost, "/os/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "198.51.100.91:5000"
	a.handleOSLoginSubmit(httptest.NewRecorder(), req)
	_ = st.Flush(ctx)
	rows, _ := st.Attempts(ctx, "", lockedMailbox, 10)
	if len(rows) != 1 || rows[0].Stage != signin.StageCode || rows[0].Door != signin.DoorConsole {
		t.Errorf("recorded %+v; want the mailbox's right password with a wrong code at the console", rows)
	}
}
